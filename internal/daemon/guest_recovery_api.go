package daemon

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/guestidentity"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
	"gopkg.in/yaml.v3"
)

type GuestRecoveryAPI struct {
	bootstrap *BootstrapAPI
	cipher    cipher.AEAD
}

func randomGuestID() string {
	b := make([]byte, 32)
	if _, e := io.ReadFull(rand.Reader, b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func newGuestRecoveryAPI(api *BootstrapAPI, dir string) (*GuestRecoveryAPI, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "delivery.key")
	key, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		e = os.WriteFile(path, key, 0600)
	}
	if e != nil {
		return nil, e
	}
	if info, e := os.Lstat(path); e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("guest delivery key must be a regular private file (0600)")
	}
	if len(key) != 32 {
		return nil, errors.New("invalid guest delivery encryption key")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	return &GuestRecoveryAPI{api, aead}, e
}
func (api *GuestRecoveryAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/bootstrap/register", api.register)
	mux.HandleFunc("/v1/bootstrap/challenge", api.challenge)
	mux.HandleFunc("/v1/bootstrap/recover", api.recover)
	mux.HandleFunc("/v1/bootstrap/execution/start", api.startExecution)
}
func (api *GuestRecoveryAPI) request(w http.ResponseWriter, r *http.Request) (db.GuestProof, bool) {
	var p db.GuestProof
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return p, false
	}
	if r.TLS == nil {
		writeError(w, 403, "guest_recovery_requires_tls")
		return p, false
	}
	if !api.bootstrap.remoteAllowed(r.RemoteAddr) {
		writeError(w, 403, "guest_recovery_subnet_denied")
		return p, false
	}
	if api.bootstrap.rateLimiter != nil && !api.bootstrap.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
		return p, false
	}
	if e := decodeJSON(w, r, &p); e != nil {
		writeError(w, 400, "guest_recovery_invalid_request")
		return p, false
	}
	if p.VMID <= 0 || !guestidentity.ValidID(p.Incarnation) || !guestidentity.ValidID(p.RequestID) {
		writeError(w, 400, "guest_recovery_invalid_scope")
		return p, false
	}
	return p, true
}
func (api *GuestRecoveryAPI) challenge(w http.ResponseWriter, r *http.Request) {
	p, ok := api.request(w, r)
	if !ok {
		return
	}
	id, nonce := randomGuestID(), randomGuestID()
	if e := api.bootstrap.store.CreateGuestChallenge(r.Context(), p, id, nonce, api.bootstrap.now().UTC()); e != nil {
		status, code := 503, "guest_recovery_unavailable"
		if errors.Is(e, db.ErrGuestProof) || errors.Is(e, db.ErrGuestChallenge) {
			status = 403
			code = e.Error()
		}
		writeError(w, status, code)
		return
	}
	writeJSON(w, 200, map[string]string{"challenge_id": id, "nonce": nonce, "expires_in": "60"})
}
func (api *GuestRecoveryAPI) register(w http.ResponseWriter, r *http.Request) {
	p, ok := api.request(w, r)
	if !ok {
		return
	}
	key, e := base64.StdEncoding.DecodeString(p.PublicKey)
	if p.Operation != "register" || e != nil || len(key) != ed25519.PublicKeySize || len(p.Token) > 256 || p.Token == "" {
		writeError(w, 400, "guest_registration_invalid")
		return
	}
	api.exchange(w, r, p)
}
func (api *GuestRecoveryAPI) recover(w http.ResponseWriter, r *http.Request) {
	p, ok := api.request(w, r)
	if !ok {
		return
	}
	if (p.Operation != "recover" && p.Operation != "refresh") || !guestidentity.ValidID(p.ChallengeID) || !guestidentity.ValidID(p.Nonce) || len(p.Signature) > 128 {
		writeError(w, 400, "guest_recovery_invalid_proof")
		return
	}
	api.exchange(w, r, p)
}
func (api *GuestRecoveryAPI) exchange(w http.ResponseWriter, r *http.Request, p db.GuestProof) {
	now := api.bootstrap.now().UTC()
	encrypted, e := api.bootstrap.store.GuestExchange(r.Context(), p, now, func(tx *sql.Tx, initial bool) ([]byte, string, error) {
		resp := map[string]any{"incarnation": p.Incarnation, "job_disposition": "none", "credential_expires_at": now.Add(time.Hour).Format(time.RFC3339)}
		var name, profile string
		if e := tx.QueryRowContext(r.Context(), `SELECT name,profile FROM sandboxes WHERE vmid=?`, p.VMID).Scan(&name, &profile); e != nil {
			return nil, "", e
		}
		resp["sandbox"] = V1BootstrapSandbox{p.VMID, name, profile}
		var jobID, status string
		e := tx.QueryRowContext(r.Context(), `SELECT id,status FROM jobs WHERE sandbox_vmid=? ORDER BY created_at DESC LIMIT 1`, p.VMID).Scan(&jobID, &status)
		hasJob := e == nil
		if e != nil && e != sql.ErrNoRows {
			return nil, "", e
		}
		execute := false
		jobMode := ""
		if hasJob {
			resp["job"] = map[string]string{"id": jobID}
			disposition := "terminal"
			if status == "RUNNING" || status == "QUEUED" || status == "PROVISIONING" {
				disposition = "interrupted"
			}
			if initial && disposition == "interrupted" {
				attempt := randomGuestID()
				res, e := tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO guest_attempts VALUES(?,?,?,0)`, p.Incarnation, jobID, attempt)
				if e != nil {
					return nil, "", e
				}
				n, _ := res.RowsAffected()
				if n == 1 {
					execute = true
					disposition = "execute"
					resp["attempt_id"] = attempt
					var job V1BootstrapJob
					var ttl int
					e = tx.QueryRowContext(r.Context(), `SELECT id,repo_url,ref,COALESCE(task,''),COALESCE(mode,''),profile,keepalive,COALESCE(ttl_minutes,0) FROM jobs WHERE id=?`, jobID).Scan(&job.ID, &job.RepoURL, &job.Ref, &job.Task, &job.Mode, &job.Profile, &job.Keepalive, &ttl)
					if e != nil {
						return nil, "", e
					}
					jobMode = job.Mode
					if ttl > 0 {
						job.TTLMinutes = &ttl
					}
					resp["job"] = job
				}
			}
			if !initial && p.Operation != "refresh" && disposition == "interrupted" {
				_, e := tx.ExecContext(r.Context(), `UPDATE jobs SET status='FAILED',result_json='{"error":"guest_job_interrupted","retry_required":true}',updated_at=? WHERE id=? AND status IN ('RUNNING','QUEUED')`, now.Format("2006-01-02T15:04:05.000000000Z07:00"), jobID)
				if e != nil {
					return nil, "", e
				}
			}
			if status == "FAILED" {
				var result sql.NullString
				_ = tx.QueryRowContext(r.Context(), `SELECT result_json FROM jobs WHERE id=?`, jobID).Scan(&result)
				if result.String == `{"error":"guest_job_interrupted","retry_required":true}` {
					disposition = "interrupted"
				}
			}
			resp["job_disposition"] = disposition
		}
		// Only initial delivery carries enrollment keys. Recovery never re-enrolls.
		if initial {
			bundleName := api.bootstrap.secretsBundle
			profiles := api.bootstrap.profiles
			if api.bootstrap.profileStore != nil {
				profiles = api.bootstrap.profileStore.Snapshot()
			}
			if prof, ok := profiles[profile]; ok {
				var cfg struct {
					SecretsBundle string `yaml:"secrets_bundle"`
				}
				if e := yaml.Unmarshal([]byte(prof.RawYAML), &cfg); e != nil {
					return nil, "", e
				}
				if cfg.SecretsBundle != "" {
					bundleName = cfg.SecretsBundle
				}
			}
			bundle, e := api.bootstrap.secretsStore.Load(r.Context(), bundleName)
			if errors.Is(e, secrets.ErrBundleNotFound) && !hasJob && bundleName == api.bootstrap.secretsBundle {
				bundle = secrets.Bundle{}
				e = nil
			}
			if e != nil {
				return nil, "", errors.New("bootstrap_secrets_unavailable")
			}
			ts, e := api.bootstrap.tailscaleForBootstrap(r.Context(), bundle, p.VMID)
			if e != nil {
				return nil, "", errors.New("tailscale_key_mint_failed")
			}
			if ts != nil {
				resp["tailscale"] = ts
			}
			if execute {
				resp["git"] = bootstrapGitFromBundle(bundle)
				resp["env"] = bundle.Env
				settings, e := bundle.ClaudeSettingsJSON()
				if e != nil {
					return nil, "", e
				}
				resp["claude_settings_json"] = settings
				if prof, ok := profiles[profile]; ok {
					policy, e := bootstrapPolicyFromJobAndProfile(models.Job{Profile: profile, Mode: jobMode}, &prof)
					if e != nil {
						return nil, "", e
					}
					resp["policy"] = policy
				}
				if api.bootstrap.artifactEndpoint != "" {
					token, hash, expires, e := api.bootstrap.newArtifactToken()
					if e != nil {
						return nil, "", e
					}
					_, e = tx.ExecContext(r.Context(), `INSERT INTO artifact_tokens(token,job_id,vmid,expires_at,created_at) VALUES(?,?,?,?,?)`, hash, jobID, p.VMID, expires.Format("2006-01-02T15:04:05.000000000Z07:00"), now.Format("2006-01-02T15:04:05.000000000Z07:00"))
					if e != nil {
						return nil, "", e
					}
					resp["artifact"] = &V1BootstrapArtifact{Endpoint: api.bootstrap.artifactEndpoint, Token: token}
					api.bootstrap.redactor.AddValues(token)
				} else {
					resp["artifact"] = bootstrapArtifactFromBundle(bundle)
				}
			}
		}
		secret := randomGuestID()
		resp["sandbox_secret"] = secret
		api.bootstrap.redactor.AddValues(secret)
		plain, e := json.Marshal(resp)
		if e != nil {
			return nil, "", e
		}
		nonce := make([]byte, api.cipher.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return nil, "", e
		}
		cipher := api.cipher.Seal(nonce, nonce, plain, []byte(p.Incarnation+"/"+p.RequestID))
		hash, _ := db.HashSandboxSecret(secret)
		return cipher, hash, nil
	})
	if e != nil {
		code := "guest_recovery_unavailable"
		if e.Error() == "bootstrap_secrets_unavailable" || e.Error() == "tailscale_key_mint_failed" {
			code = e.Error()
		}
		status := 503
		if errors.Is(e, db.ErrGuestProof) || errors.Is(e, db.ErrGuestChallenge) {
			code = e.Error()
			status = 403
		}
		api.record(r.Context(), p.VMID, code)
		writeError(w, status, code)
		return
	}
	if len(encrypted) < api.cipher.NonceSize() {
		writeError(w, 503, "guest_delivery_invalid")
		return
	}
	plain, e := api.cipher.Open(nil, encrypted[:api.cipher.NonceSize()], encrypted[api.cipher.NonceSize():], []byte(p.Incarnation+"/"+p.RequestID))
	if e != nil {
		writeError(w, 503, "guest_delivery_invalid")
		return
	}
	var sensitive map[string]any
	_ = json.Unmarshal(plain, &sensitive)
	api.bootstrap.redactor.AddValues(recoverySecretValues(sensitive)...)
	code := "guest_recovery_ready"
	if sensitive["job_disposition"] == "interrupted" && p.Operation != "refresh" {
		code = "guest_job_interrupted"
	}
	if p.Operation == "refresh" {
		code = "guest_credential_refreshed"
	}
	var jobID *string
	if code == "guest_job_interrupted" {
		if job, ok := sensitive["job"].(map[string]any); ok {
			if id, ok := job["id"].(string); ok {
				jobID = &id
			}
		}
	}
	_ = emitEvent(r.Context(), NewStoreEventRecorder(api.bootstrap.store), EventKindSandboxRecovery, &p.VMID, jobID, "guest credential recovery", map[string]string{"code": code})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(plain)
}
func recoverySecretValues(m map[string]any) []string {
	var values []string
	for _, field := range []string{"sandbox_secret", "claude_settings_json"} {
		if v, ok := m[field].(string); ok {
			values = append(values, v)
		}
	}
	if env, ok := m["env"].(map[string]any); ok {
		values = append(values, sensitiveValues(env)...)
	}
	for field, keys := range map[string][]string{"git": {"token", "ssh_private_key"}, "artifact": {"token"}, "tailscale": {"authkey"}} {
		if object, ok := m[field].(map[string]any); ok {
			for _, key := range keys {
				if v, ok := object[key].(string); ok {
					values = append(values, v)
				}
			}
		}
	}
	return values
}
func sensitiveValues(m map[string]any) []string {
	var result []string
	for _, v := range m {
		switch x := v.(type) {
		case string:
			result = append(result, x)
		case map[string]any:
			result = append(result, sensitiveValues(x)...)
		}
	}
	return result
}
func (api *GuestRecoveryAPI) record(ctx context.Context, vmid int, code string) {
	_ = emitEvent(ctx, NewStoreEventRecorder(api.bootstrap.store), EventKindSandboxRecovery, &vmid, nil, "guest credential recovery", map[string]string{"code": code})
}
func (api *GuestRecoveryAPI) startExecution(w http.ResponseWriter, r *http.Request) {
	p, ok := api.request(w, r)
	if !ok {
		return
	}
	presented, _ := db.HashSandboxSecret(r.Header.Get("X-AgentLab-Sandbox-Secret"))
	stored, e := api.bootstrap.store.GetSandboxSecretHash(r.Context(), p.VMID)
	if e != nil || subtle.ConstantTimeCompare([]byte(presented), []byte(stored)) != 1 {
		writeError(w, 403, "guest_execution_denied")
		return
	}
	res, e := api.bootstrap.store.DB.ExecContext(r.Context(), `UPDATE guest_attempts SET started=1 WHERE incarnation=? AND attempt_id=? AND started=0 AND EXISTS(SELECT 1 FROM guest_identities g JOIN sandboxes s ON s.vmid=g.vmid WHERE g.vmid=? AND g.incarnation=guest_attempts.incarnation AND g.revoked=0 AND s.state NOT IN ('DESTROYED','DESTROYING')) AND EXISTS(SELECT 1 FROM jobs WHERE id=guest_attempts.job_id AND status IN ('RUNNING','QUEUED')) AND EXISTS(SELECT 1 FROM sandbox_secrets WHERE vmid=? AND secret_hash=? AND (expires_at IS NULL OR expires_at>?))`, p.Incarnation, p.RequestID, p.VMID, p.VMID, presented, time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"))
	if e != nil {
		writeError(w, 503, "guest_execution_unavailable")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		writeError(w, 409, "guest_execution_already_claimed")
		return
	}
	writeJSON(w, 200, map[string]string{"disposition": "execute"})
}
