package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/agentlab/agentlab/internal/guestidentity"
)

var ErrGuestProof = errors.New("guest_identity_invalid_or_revoked")
var ErrGuestChallenge = errors.New("guest_challenge_expired_or_replayed")

type GuestProof struct {
	VMID        int    `json:"vmid"`
	Incarnation string `json:"incarnation"`
	RequestID   string `json:"request_id"`
	Token       string `json:"token,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	Operation   string `json:"operation"`
	Signature   string `json:"signature,omitempty"`
}

func (s *Store) GetSandboxIncarnation(ctx context.Context, vmid int) (string, error) {
	var inc string
	err := s.DB.QueryRowContext(ctx, `SELECT incarnation FROM guest_identities WHERE vmid=? AND revoked=0`, vmid).Scan(&inc)
	return inc, err
}
func nonceHash(n string) string { h := sha256.Sum256([]byte(n)); return hex.EncodeToString(h[:]) }
func (s *Store) CreateGuestChallenge(ctx context.Context, p GuestProof, id, nonce string, now time.Time) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// A write reservation serializes lifecycle changes with challenge creation.
	if _, e = tx.ExecContext(ctx, `DELETE FROM guest_challenges WHERE expires_at<=?`, formatTime(now)); e != nil {
		return e
	}
	var key string
	e = tx.QueryRowContext(ctx, `SELECT g.public_key FROM guest_identities g JOIN sandboxes s ON s.vmid=g.vmid WHERE g.vmid=? AND g.incarnation=? AND g.revoked=0 AND g.public_key IS NOT NULL AND s.state NOT IN ('DESTROYED','DESTROYING')`, p.VMID, p.Incarnation).Scan(&key)
	if e != nil {
		return ErrGuestProof
	}
	// Bound outstanding challenges per identity; expired rows are removed above.
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM guest_challenges WHERE incarnation=?`, p.Incarnation).Scan(&count); e != nil {
		return e
	}
	if count >= 32 {
		return ErrGuestChallenge
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO guest_challenges VALUES(?,?,?,COALESCE((SELECT secret_hash FROM sandbox_secrets WHERE vmid=?),''),?,0)`, id, p.Incarnation, nonceHash(nonce), p.VMID, formatTime(now.Add(time.Minute)))
	if e != nil {
		return e
	}
	return tx.Commit()
}

// GuestExchange commits proof consumption, registration, encrypted delivery and
// credential rotation together. Build must use tx, never the Store connection.
func (s *Store) GuestExchange(ctx context.Context, p GuestProof, now time.Time, build func(*sql.Tx, bool) ([]byte, string, error)) ([]byte, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM guest_deliveries WHERE expires_at<=?`, formatTime(now)); e != nil {
		return nil, e
	}
	var key sql.NullString
	var inc string
	e = tx.QueryRowContext(ctx, `SELECT g.incarnation,g.public_key FROM guest_identities g JOIN sandboxes s ON s.vmid=g.vmid WHERE g.vmid=? AND g.revoked=0 AND s.state NOT IN ('DESTROYED','DESTROYING')`, p.VMID).Scan(&inc, &key)
	if e != nil || inc != p.Incarnation {
		return nil, ErrGuestProof
	}
	initial := p.Operation == "register"
	if initial {
		if key.Valid {
			return nil, ErrGuestProof
		}
		hash, _ := HashBootstrapToken(p.Token)
		res, e := tx.ExecContext(ctx, `UPDATE bootstrap_tokens SET consumed_at=? WHERE token=? AND vmid=? AND incarnation=? AND consumed_at IS NULL AND expires_at>?`, formatTime(now), hash, p.VMID, inc, formatTime(now))
		if e != nil {
			return nil, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, ErrGuestProof
		}
		if _, e = tx.ExecContext(ctx, `UPDATE guest_identities SET public_key=? WHERE vmid=?`, p.PublicKey, p.VMID); e != nil {
			return nil, ErrGuestProof
		}
	} else {
		if !key.Valid || !guestidentity.Verify(key.String, guestidentity.Message(p.VMID, inc, p.ChallengeID, p.Nonce, p.Operation, p.RequestID), p.Signature) {
			return nil, ErrGuestProof
		}
		res, e := tx.ExecContext(ctx, `UPDATE guest_challenges SET consumed=1 WHERE id=? AND incarnation=? AND nonce_hash=? AND consumed=0 AND expires_at>?`, p.ChallengeID, inc, nonceHash(p.Nonce), formatTime(now))
		if e != nil {
			return nil, e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil, ErrGuestChallenge
		}
		var cached []byte
		e = tx.QueryRowContext(ctx, `SELECT ciphertext FROM guest_deliveries WHERE incarnation=? AND request_id=?`, inc, p.RequestID).Scan(&cached)
		if e == nil {
			if e = tx.Commit(); e != nil {
				return nil, e
			}
			return cached, nil
		}
		if e != sql.ErrNoRows {
			return nil, e
		}
	}
	if !initial {
		var generation string
		if e = tx.QueryRowContext(ctx, `SELECT generation FROM guest_challenges WHERE id=?`, p.ChallengeID).Scan(&generation); e != nil {
			return nil, e
		}
		var current string
		if e = tx.QueryRowContext(ctx, `SELECT secret_hash FROM sandbox_secrets WHERE vmid=?`, p.VMID).Scan(&current); e != nil || current != generation {
			return nil, ErrGuestChallenge
		}
	}
	payload, hash, e := build(tx, initial)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO sandbox_secrets(vmid,secret_hash,created_at,expires_at) VALUES(?,?,?,?) ON CONFLICT(vmid) DO UPDATE SET secret_hash=excluded.secret_hash,created_at=excluded.created_at,expires_at=excluded.expires_at`, p.VMID, hash, formatTime(now), formatTime(now.Add(time.Hour))); e != nil {
		return nil, e
	}
	// Only the latest response may be retried: older credentials were superseded.
	if _, e = tx.ExecContext(ctx, `DELETE FROM guest_deliveries WHERE incarnation=?`, inc); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO guest_deliveries VALUES(?,?,?,?)`, inc, p.RequestID, payload, formatTime(now.Add(5*time.Minute))); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return payload, nil
}
