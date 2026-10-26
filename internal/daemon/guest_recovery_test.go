package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/guestidentity"
)

type recoveryFixture struct {
	api     *GuestRecoveryAPI
	mux     *http.ServeMux
	proof   db.GuestProof
	private ed25519.PrivateKey
	dir     string
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	base, token, vmid := mintTestEnv(t, "")
	base.now = time.Now
	if _, e := base.store.DB.Exec(`UPDATE bootstrap_tokens SET expires_at=?`, time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000000000Z07:00")); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	api, e := newGuestRecoveryAPI(base, dir)
	if e != nil {
		t.Fatal(e)
	}
	inc, e := base.store.GetSandboxIncarnation(context.Background(), vmid)
	if e != nil {
		t.Fatal(e)
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	api.Register(mux)
	return &recoveryFixture{api, mux, db.GuestProof{VMID: vmid, Incarnation: inc, Token: token, PublicKey: base64.StdEncoding.EncodeToString(pub), RequestID: randomGuestID(), Operation: "register"}, key, dir}
}
func (f *recoveryFixture) call(path string, p db.GuestProof) *httptest.ResponseRecorder {
	data, _ := json.Marshal(p)
	r := httptest.NewRequest("POST", "https://controller/v1/bootstrap/"+path, bytes.NewReader(data))
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "10.77.0.2:12345"
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}
func (f *recoveryFixture) signed(t *testing.T, request string) db.GuestProof {
	t.Helper()
	p := f.proof
	p.Token = ""
	p.PublicKey = ""
	p.RequestID = request
	p.Operation = "recover"
	w := f.call("challenge", p)
	if w.Code != 200 {
		t.Fatalf("challenge %d: %s", w.Code, w.Body)
	}
	var resp struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &resp); e != nil {
		t.Fatal(e)
	}
	p.ChallengeID = resp.ChallengeID
	p.Nonce = resp.Nonce
	p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, guestidentity.Message(p.VMID, p.Incarnation, p.ChallengeID, p.Nonce, p.Operation, p.RequestID)))
	return p
}
func responseMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("response %d: %s", w.Code, w.Body)
	}
	var m map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &m); e != nil {
		t.Fatal(e)
	}
	return m
}
func TestGuestRecoveryLostResponsesAndRestart(t *testing.T) {
	f := newRecoveryFixture(t)
	initial := responseMap(t, f.call("register", f.proof))
	if initial["job_disposition"] != "execute" {
		t.Fatal(initial)
	}
	// Initial registration committed but response was lost. Re-prove possession.
	p := f.signed(t, f.proof.RequestID)
	retry := responseMap(t, f.call("recover", p))
	if initial["sandbox_secret"] != retry["sandbox_secret"] {
		t.Fatal("lost initial response rotated secret")
	}
	if w := f.call("recover", p); w.Code != 403 {
		t.Fatal("replay accepted")
	}
	if w := f.call("register", f.proof); w.Code != 403 {
		t.Fatal("spent token accepted")
	}
	// Recreate the API with the same encryption key to simulate daemon restart.
	fresh, e := newGuestRecoveryAPI(f.api.bootstrap, f.dir)
	if e != nil {
		t.Fatal(e)
	}
	f.api = fresh
	f.mux = http.NewServeMux()
	fresh.Register(f.mux)
	p = f.signed(t, randomGuestID())
	resp := responseMap(t, f.call("recover", p))
	if resp["job_disposition"] != "interrupted" || resp["git"] != nil || resp["tailscale"] != nil || resp["artifact"] != nil {
		t.Fatal("recovery reissued execution/enrollment material", resp)
	}
	id := p.RequestID
	restarted, e := newGuestRecoveryAPI(f.api.bootstrap, f.dir)
	if e != nil {
		t.Fatal(e)
	}
	f.api = restarted
	f.mux = http.NewServeMux()
	restarted.Register(f.mux)
	p = f.signed(t, id)
	again := responseMap(t, f.call("recover", p))
	if resp["sandbox_secret"] != again["sandbox_secret"] {
		t.Fatal("lost recovery response rotated secret")
	}
	var ciphertext []byte
	if e := f.api.bootstrap.store.DB.QueryRow(`SELECT ciphertext FROM guest_deliveries`).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(ciphertext, []byte(resp["sandbox_secret"].(string))) {
		t.Fatal("plaintext in cache")
	}
}
func TestGuestRecoveryConcurrentClaims(t *testing.T) {
	f := newRecoveryFixture(t)
	responseMap(t, f.call("register", f.proof))
	p1, p2 := f.signed(t, randomGuestID()), f.signed(t, randomGuestID())
	var wg sync.WaitGroup
	codes := make(chan *httptest.ResponseRecorder, 2)
	for _, p := range []db.GuestProof{p1, p2} {
		wg.Add(1)
		go func(p db.GuestProof) { defer wg.Done(); codes <- f.call("recover", p) }(p)
	}
	wg.Wait()
	close(codes)
	wins, loses := 0, 0
	for response := range codes {
		c := response.Code
		if c == 200 {
			wins++
			delivered := responseMap(t, response)
			expected, _ := db.HashSandboxSecret(delivered["sandbox_secret"].(string))
			actual, e := f.api.bootstrap.store.GetSandboxSecretHash(context.Background(), f.proof.VMID)
			if e != nil || actual != expected {
				t.Fatal("losing claim invalidated winner", e)
			}
		} else if c == 403 {
			loses++
		} else {
			t.Fatal(c)
		}
	}
	if wins != 1 || loses != 1 {
		t.Fatal(wins, loses)
	}
}
func TestGuestRecoveryRejectScopeExpiryRevocation(t *testing.T) {
	for _, kind := range []string{"signature", "scope", "expiry", "destroy", "cleanup", "wrong-operation", "http"} {
		t.Run(kind, func(t *testing.T) {
			f := newRecoveryFixture(t)
			first := responseMap(t, f.call("register", f.proof))
			p := f.signed(t, randomGuestID())
			switch kind {
			case "signature":
				p.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
			case "scope":
				p.Incarnation = randomGuestID()
			case "expiry":
				f.api.bootstrap.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
			case "destroy":
				_, e := f.api.bootstrap.store.DB.Exec(`UPDATE sandboxes SET state='DESTROYED' WHERE vmid=?`, p.VMID)
				if e != nil {
					t.Fatal(e)
				}
			case "cleanup":
				if e := f.api.bootstrap.store.DeleteSandboxSecret(context.Background(), p.VMID); e != nil {
					t.Fatal(e)
				}
			case "wrong-operation":
				p.Operation = "register"
			case "http":
				data, _ := json.Marshal(p)
				r := httptest.NewRequest("POST", "http://controller/v1/bootstrap/recover", bytes.NewReader(data))
				w := httptest.NewRecorder()
				f.mux.ServeHTTP(w, r)
				if w.Code != 403 {
					t.Fatal(w.Code)
				}
				return
			}
			w := f.call("recover", p)
			if w.Code != 403 && w.Code != 400 {
				t.Fatalf("accepted invalid proof: %d %s", w.Code, w.Body)
			}
			var hash string
			e := f.api.bootstrap.store.DB.QueryRow(`SELECT secret_hash FROM sandbox_secrets WHERE vmid=?`, p.VMID).Scan(&hash)
			if kind != "destroy" && kind != "cleanup" {
				expected, _ := db.HashSandboxSecret(first["sandbox_secret"].(string))
				if e != nil || hash != expected {
					t.Fatal("rejected proof mutated secret", e)
				}
			}
		})
	}
}
func TestGuestRecoveryExecutionClaimOnce(t *testing.T) {
	f := newRecoveryFixture(t)
	resp := responseMap(t, f.call("register", f.proof))
	p := f.proof
	p.RequestID = resp["attempt_id"].(string)
	call := func() *httptest.ResponseRecorder {
		data, _ := json.Marshal(p)
		r := httptest.NewRequest("POST", "https://controller/v1/bootstrap/execution/start", bytes.NewReader(data))
		r.TLS = &tls.ConnectionState{}
		r.RemoteAddr = "10.77.0.2:1234"
		r.Header.Set("X-AgentLab-Sandbox-Secret", resp["sandbox_secret"].(string))
		w := httptest.NewRecorder()
		f.mux.ServeHTTP(w, r)
		return w
	}
	if w := call(); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	var starts int
	_ = f.api.bootstrap.store.DB.QueryRow(`SELECT started FROM guest_attempts`).Scan(&starts)
	if starts != 1 {
		t.Fatal(starts)
	}
}

// Run this executable fixture in Linux (same Bash/OpenSSL/coreutils as guests).
// It removes tmpfs between invocations: service cleanup and reboot both used to
// strand the consumed token. No private key or decrypted bundle may be in tmpfs.
func TestGuestRecoveryHelperRestartAndLostResponse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("guest executable fixture requires Linux")
	}
	for _, dep := range []string{"openssl", "jq", "curl", "flock", "bash"} {
		if _, e := exec.LookPath(dep); e != nil {
			t.Skip(dep + " unavailable")
		}
	}
	f := newRecoveryFixture(t)
	f.api.bootstrap.now = time.Now
	_, e := f.api.bootstrap.store.DB.Exec(`UPDATE bootstrap_tokens SET expires_at=?`, time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal(e)
	}
	lose := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/register") && lose {
			lose = false
			discard := httptest.NewRecorder()
			f.mux.ServeHTTP(discard, r)
			if discard.Code != 200 {
				t.Errorf("registration %d %s", discard.Code, discard.Body)
			}
			h := w.(http.Hijacker)
			conn, _, _ := h.Hijack()
			conn.Close()
			return
		}
		f.mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	// The subnet protection remains enabled in daemon deployments. Loopback fixture.
	f.api.bootstrap.agentSubnet = nil
	root := t.TempDir()
	identity := filepath.Join(root, "identity")
	run := filepath.Join(root, "run")
	descriptor := filepath.Join(root, "bootstrap.json")
	cert := server.Certificate()
	pemCert := pemCertificate(cert.Raw)
	data, _ := json.Marshal(map[string]any{"token": f.proof.Token, "vmid": f.proof.VMID, "incarnation": f.proof.Incarnation, "recovery_url": server.URL, "recovery_ca": pemCert})
	if e = os.WriteFile(descriptor, data, 0600); e != nil {
		t.Fatal(e)
	}
	repo := os.Getenv("AGENTLAB_TEST_REPO")
	if repo == "" {
		repo = "../.."
	}
	invoke := func() {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(repo, "scripts/guest/agent-bootstrap"))
		cmd.Env = append(os.Environ(), "AGENTLAB_BOOTSTRAP_SOURCE="+descriptor, "AGENTLAB_IDENTITY_DIR="+identity, "AGENTLAB_BOOTSTRAP_RUNTIME="+run, "AGENTLAB_BOOTSTRAP_TEST=1", fmt.Sprintf("AGENTLAB_RUNTIME_OWNER=%d:%d", os.Getuid(), os.Getgid()))
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("helper: %v %s", e, out)
		}
	}
	invoke()
	key, e := os.ReadFile(filepath.Join(identity, "private.pem"))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = os.RemoveAll(run); e != nil {
			t.Fatal(e)
		}
		invoke()
		key2, _ := os.ReadFile(filepath.Join(identity, "private.pem"))
		if !bytes.Equal(key, key2) {
			t.Fatal("identity changed")
		}
		response, _ := os.ReadFile(filepath.Join(run, "secrets/bootstrap.json"))
		if strings.Contains(string(response), `"job_disposition":"execute"`) {
			t.Fatal("restarted job")
		}
	}
	info, _ := os.Stat(filepath.Join(identity, "private.pem"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	entries, _ := os.ReadDir(identity)
	for _, entry := range entries {
		if entry.Name() != "lock" && entry.Name() != "incarnation" && entry.Name() != "private.pem" && entry.Name() != "tailscale-configured" {
			t.Fatal("unexpected durable material", entry.Name())
		}
	}
}

func pemCertificate(raw []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}))
}

func TestGuestRecoveryVMIDReuseAndDuplicateKey(t *testing.T) {
	f := newRecoveryFixture(t)
	responseMap(t, f.call("register", f.proof))
	old := f.signed(t, randomGuestID())
	ctx := context.Background()
	sb, e := f.api.bootstrap.store.GetSandbox(ctx, f.proof.VMID)
	if e != nil {
		t.Fatal(e)
	}
	sb.VMID++
	if e = f.api.bootstrap.store.CreateSandbox(ctx, sb); e != nil {
		t.Fatal(e)
	}
	inc, e := f.api.bootstrap.store.GetSandboxIncarnation(ctx, sb.VMID)
	if e != nil {
		t.Fatal(e)
	}
	hash, _ := db.HashBootstrapToken("other-token")
	if e = f.api.bootstrap.store.CreateBootstrapToken(ctx, hash, sb.VMID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	p := f.proof
	p.VMID = sb.VMID
	p.Incarnation = inc
	p.Token = "other-token"
	p.RequestID = randomGuestID()
	if w := f.call("register", p); w.Code != 403 {
		t.Fatal("copied active key accepted", w.Code)
	}
	// Reusing the old numeric VMID creates a new incarnation and erases old proofs.
	if _, e = f.api.bootstrap.store.DB.Exec(`DELETE FROM sandboxes WHERE vmid=?`, old.VMID); e != nil {
		t.Fatal(e)
	}
	sb.VMID = old.VMID
	if e = f.api.bootstrap.store.CreateSandbox(ctx, sb); e != nil {
		t.Fatal(e)
	}
	replacement, e := f.api.bootstrap.store.GetSandboxIncarnation(ctx, sb.VMID)
	if e != nil || replacement == old.Incarnation {
		t.Fatal("incarnation reused", e)
	}
	if w := f.call("recover", old); w.Code != 403 {
		t.Fatal("old identity recovered reused VMID", w.Code)
	}
}

func TestGuestRecoveryTerminalAndRefresh(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			f := newRecoveryFixture(t)
			if terminal {
				if _, e := f.api.bootstrap.store.DB.Exec(`UPDATE jobs SET status='COMPLETED'`); e != nil {
					t.Fatal(e)
				}
			}
			resp := responseMap(t, f.call("register", f.proof))
			if terminal && (resp["job_disposition"] != "terminal" || resp["env"] != nil || resp["attempt_id"] != nil) {
				t.Fatal(resp)
			}
			p := f.signed(t, randomGuestID())
			p.Operation = "refresh"
			p.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, guestidentity.Message(p.VMID, p.Incarnation, p.ChallengeID, p.Nonce, p.Operation, p.RequestID)))
			responseMap(t, f.call("recover", p))
			var status string
			if e := f.api.bootstrap.store.DB.QueryRow(`SELECT status FROM jobs`).Scan(&status); e != nil {
				t.Fatal(e)
			}
			if !terminal && status != "RUNNING" {
				t.Fatal("refresh interrupted active job", status)
			}
			if terminal && status != "COMPLETED" {
				t.Fatal("terminal status changed", status)
			}
			var code string
			if e := f.api.bootstrap.store.DB.QueryRow(`SELECT json FROM events WHERE kind='sandbox.recovery' ORDER BY id DESC LIMIT 1`).Scan(&code); e != nil || !strings.Contains(code, "guest_credential_refreshed") {
				t.Fatal("recovery outcome missing", e, code)
			}
		})
	}
}

func TestGuestRecoveryCredentialExpiryAndLegacyLateRotation(t *testing.T) {
	f := newRecoveryFixture(t)
	resp := responseMap(t, f.call("register", f.proof))
	if _, e := f.api.bootstrap.store.GetSandboxSecretHash(context.Background(), f.proof.VMID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.api.bootstrap.store.DB.Exec(`UPDATE sandbox_secrets SET expires_at=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	if _, e := f.api.bootstrap.store.GetSandboxSecretHash(context.Background(), f.proof.VMID); e == nil {
		t.Fatal("expired credential accepted")
	}
	if _, e := f.api.bootstrap.store.DB.Exec(`UPDATE sandboxes SET state='DESTROYED' WHERE vmid=?`, f.proof.VMID); e != nil {
		t.Fatal(e)
	}
	hash, _ := db.HashSandboxSecret(resp["sandbox_secret"].(string))
	if e := f.api.bootstrap.store.UpsertSandboxSecretForIncarnation(context.Background(), f.proof.VMID, hash, f.proof.Incarnation); e == nil {
		t.Fatal("late legacy request restored revoked credential")
	}
}

func TestGuestRecoveryFailedRegistrationRollsBack(t *testing.T) {
	f := newRecoveryFixture(t)
	path := filepath.Join(f.api.bootstrap.secretsStore.Dir, "default.yaml")
	if e := os.WriteFile(path, []byte("version: 1\ntailscale:\n  admin_api_key: admin-fixture\n  tailnet: example.com\n"), 0600); e != nil {
		t.Fatal(e)
	}
	f.api.bootstrap.tailscaleMinter = &fakeTailscaleMinter{err: fmt.Errorf("fixture unavailable")}
	w := f.call("register", f.proof)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	hash, _ := db.HashBootstrapToken(f.proof.Token)
	valid, e := f.api.bootstrap.store.ValidateBootstrapToken(context.Background(), hash, f.proof.VMID, time.Now())
	if e != nil || !valid {
		t.Fatal("failed registration spent token", e)
	}
	var bound int
	if e = f.api.bootstrap.store.DB.QueryRow(`SELECT count(*) FROM guest_identities WHERE public_key IS NOT NULL`).Scan(&bound); e != nil || bound != 0 {
		t.Fatal("failed registration bound identity", e)
	}
	f.api.bootstrap.tailscaleMinter = &fakeTailscaleMinter{key: "tskey-auth-fixture-retry"}
	responseMap(t, f.call("register", f.proof))
}

func TestGuestRecoveryDestroyRaceDoesNotRestoreAccess(t *testing.T) {
	f := newRecoveryFixture(t)
	responseMap(t, f.call("register", f.proof))
	p := f.signed(t, randomGuestID())
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- f.call("recover", p) }()
	if _, e := f.api.bootstrap.store.DB.Exec(`UPDATE sandboxes SET state='DESTROYED' WHERE vmid=?`, p.VMID); e != nil {
		t.Fatal(e)
	}
	response := <-result
	if response.Code != 200 && response.Code != 403 {
		t.Fatal(response.Code, response.Body)
	}
	for _, query := range []string{`SELECT count(*) FROM sandbox_secrets WHERE vmid=?`, `SELECT count(*) FROM guest_challenges WHERE incarnation=(SELECT incarnation FROM guest_identities WHERE vmid=?)`, `SELECT count(*) FROM guest_deliveries WHERE incarnation=(SELECT incarnation FROM guest_identities WHERE vmid=?)`} {
		var remaining int
		if e := f.api.bootstrap.store.DB.QueryRow(query, p.VMID).Scan(&remaining); e != nil || remaining != 0 {
			t.Fatal("destroyed access remained", e, remaining)
		}
	}
	if response = f.call("recover", p); response.Code != 403 {
		t.Fatal("destroyed identity accepted", response.Code)
	}
}
