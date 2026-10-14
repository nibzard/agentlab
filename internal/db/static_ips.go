package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
)

// StaticIPConflictError identifies the sandbox that owns a requested address.
type StaticIPConflictError struct {
	IP   string
	VMID int
}

func (e *StaticIPConflictError) Error() string {
	return fmt.Sprintf("static IP %s is already reserved by sandbox %d", e.IP, e.VMID)
}

// ReserveSandboxStaticIPs claims all configured addresses atomically. Claims
// survive daemon restarts and stop/start cycles, and expire only on destruction.
// This is separate from the guest-discovered IP used for SSH and metadata.
func (s *Store) ReserveSandboxStaticIPs(ctx context.Context, vmid int, ips []string) error {
	if s == nil || s.DB == nil {
		return errors.New("store is nil")
	}
	if vmid <= 0 {
		return errors.New("vmid must be positive")
	}
	if len(ips) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sandbox_static_ips WHERE vmid IN (SELECT vmid FROM sandboxes WHERE state = 'DESTROYED')`); err != nil {
		return err
	}
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return fmt.Errorf("invalid static IP %q: %w", ip, err)
		}
		ip = addr.Unmap().String()
		var owner int
		err = tx.QueryRowContext(ctx, `SELECT vmid FROM sandbox_static_ips WHERE ip = ? AND vmid != ? UNION ALL SELECT vmid FROM sandboxes WHERE ip = ? AND vmid != ? AND state != 'DESTROYED' LIMIT 1`, ip, vmid, ip, vmid).Scan(&owner)
		if err == nil {
			return &StaticIPConflictError{IP: ip, VMID: owner}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_static_ips (ip, vmid) VALUES (?, ?) ON CONFLICT(ip) DO NOTHING`, ip, vmid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReleaseSandboxStaticIPs releases claims when cloning never created a VM.
func (s *Store) ReleaseSandboxStaticIPs(ctx context.Context, vmid int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sandbox_static_ips WHERE vmid = ?`, vmid)
	return err
}
