package connection

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func privateCredentialBus(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Secret Service adapter requires Linux")
	}
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("private D-Bus integration requires dbus-daemon")
	}
	// Keep the Unix socket path below the platform limit, even for long tests.
	dir, err := os.MkdirTemp("", "al-credential-bus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, "dbus-daemon", "--session", "--nofork", "--nopidfile", "--print-address=1", "--address=unix:path="+filepath.Join(dir, "bus"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal("private bus did not return its address")
	}
	return strings.TrimSpace(address)
}

type fakeSecretService struct {
	mu                                sync.Mutex
	conn                              *dbus.Conn
	items                             map[dbus.ObjectPath]fakeSecretItemData
	locked, prompt, corrupt, failRead bool
	promptCalls, unlockCalls, next    int
}
type fakeSecretItemData struct {
	attributes map[string]string
	value      []byte
}
type fakeSecretProperties struct{ service *fakeSecretService }

func (p fakeSecretProperties) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	p.service.mu.Lock()
	defer p.service.mu.Unlock()
	if key != "Locked" {
		return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("unsupported property"))
	}
	return dbus.MakeVariant(p.service.locked), nil
}
func (f *fakeSecretService) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/secrets/collection/test", nil
}
func (f *fakeSecretService) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.Variant{}, "/", dbus.MakeFailedError(fmt.Errorf("unsupported algorithm"))
	}
	return dbus.MakeVariant(""), "/org/freedesktop/secrets/session/test", nil
}
func (f *fakeSecretService) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlockCalls++
	return nil, "/org/freedesktop/secrets/prompt/test", nil
}
func (f *fakeSecretService) Prompt(window string) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptCalls++
	return nil
}
func (f *fakeSecretService) Dismiss() *dbus.Error { return nil }
func (f *fakeSecretService) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	unlocked, locked := []dbus.ObjectPath{}, []dbus.ObjectPath{}
	for path, item := range f.items {
		matches := true
		for key, value := range attrs {
			if item.attributes[key] != value {
				matches = false
			}
		}
		if matches {
			if f.locked {
				locked = append(locked, path)
			} else {
				unlocked = append(unlocked, path)
			}
		}
	}
	return unlocked, locked, nil
}
func (f *fakeSecretService) CreateItem(props map[string]dbus.Variant, secret serviceSecret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if replace {
		return "/", "/", dbus.MakeFailedError(fmt.Errorf("replacement is forbidden in this fixture"))
	}
	if f.prompt {
		return "/", "/org/freedesktop/secrets/prompt/test", nil
	}
	attrs, ok := props[secretItemInterface+".Attributes"].Value().(map[string]string)
	if !ok {
		return "/", "/", dbus.MakeFailedError(fmt.Errorf("invalid attributes"))
	}
	f.next++
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/item/i%d", f.next))
	f.items[path] = fakeSecretItemData{attributes: attrs, value: bytes.Clone(secret.Value)}
	if err := f.conn.Export(fakeSecretItem{service: f, path: path}, path, secretItemInterface); err != nil {
		return "/", "/", dbus.MakeFailedError(err)
	}
	if err := f.conn.Export(fakeSecretProperties{service: f}, path, "org.freedesktop.DBus.Properties"); err != nil {
		return "/", "/", dbus.MakeFailedError(err)
	}
	return path, "/", nil
}

type fakeSecretItem struct {
	service *fakeSecretService
	path    dbus.ObjectPath
}

func (i fakeSecretItem) GetSecret(session dbus.ObjectPath) (serviceSecret, *dbus.Error) {
	f := i.service
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRead {
		return serviceSecret{}, dbus.MakeFailedError(fmt.Errorf("must-not-echo-fixture-credential"))
	}
	value := bytes.Clone(f.items[i.path].value)
	if f.corrupt {
		value = []byte(`{"token":"corrupted"}`)
	}
	return serviceSecret{Session: session, Parameters: []byte{}, Value: value, ContentType: "text/plain"}, nil
}
func (i fakeSecretItem) Delete() (dbus.ObjectPath, *dbus.Error) {
	i.service.mu.Lock()
	defer i.service.mu.Unlock()
	delete(i.service.items, i.path)
	return "/", nil
}

func startFakeSecretService(t *testing.T) *fakeSecretService {
	t.Helper()
	address := privateCredentialBus(t)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeSecretService{conn: conn, items: map[dbus.ObjectPath]fakeSecretItemData{}}
	for _, entry := range []struct {
		path     dbus.ObjectPath
		iface    string
		receiver any
	}{
		{secretServicePath, secretServiceInterface, f},
		{"/org/freedesktop/secrets/collection/test", secretCollectionInterface, f},
		{"/org/freedesktop/secrets/collection/test", "org.freedesktop.DBus.Properties", fakeSecretProperties{service: f}},
		{"/org/freedesktop/secrets/prompt/test", "org.freedesktop.Secret.Prompt", f},
	} {
		if err := conn.Export(entry.receiver, entry.path, entry.iface); err != nil {
			t.Fatal(err)
		}
	}
	if reply, err := conn.RequestName(secretServiceName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal("private service could not own its name")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	return f
}

func TestSecretServiceContextRoundTripAndScopeIsolation(t *testing.T) {
	service := startFakeSecretService(t)
	first := ContextStore{Root: t.TempDir(), CredentialMode: "os"}
	cfg := Settings{Endpoint: "https://server.example", Token: strings.Repeat("private-credential-", 900)}
	if err := first.Put("server", cfg); err != nil {
		t.Fatal(err)
	}
	actual, _, _, err := first.Resolve("server")
	if err != nil || actual != cfg {
		t.Fatalf("native round trip failed: %v", err)
	}
	items, _, _ := first.List()
	second, err := openSecretServiceVault(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Load(context.Background(), items[0].CredentialRef); err == nil {
		t.Fatal("a different config directory read the credential")
	}
	if _, err := os.Stat(filepath.Join(first.Root, "credentials")); !os.IsNotExist(err) {
		t.Fatal("native storage created a fallback credential directory")
	}
	if err := first.Remove("server"); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.items) != 0 || service.promptCalls != 0 || service.unlockCalls != 0 {
		t.Fatal("native lifecycle left a credential or opened a prompt")
	}
}

func TestSecretServiceLockedAndPromptStatesNeverDowngrade(t *testing.T) {
	for _, mode := range []string{"locked", "prompt", "corrupt", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			service := startFakeSecretService(t)
			service.mu.Lock()
			service.locked = mode == "locked"
			service.prompt = mode == "prompt"
			service.corrupt = mode == "corrupt"
			service.failRead = mode == "read-error"
			service.mu.Unlock()
			store := ContextStore{Root: t.TempDir(), CredentialMode: "auto"}
			err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "must-not-echo-fixture-credential"})
			if err == nil || strings.Contains(err.Error(), "must-not-echo-fixture-credential") {
				t.Fatalf("invalid state was accepted or exposed a credential: %v", err)
			}
			if _, err := os.Stat(filepath.Join(store.Root, "contexts.json")); !os.IsNotExist(err) {
				t.Fatal("failed native storage published context metadata")
			}
			if _, err := os.Stat(filepath.Join(store.Root, "credentials")); !os.IsNotExist(err) {
				t.Fatal("native failure downgraded to file storage")
			}
			service.mu.Lock()
			if service.promptCalls != 0 || service.unlockCalls != 0 {
				service.mu.Unlock()
				t.Fatal("failed write opened a prompt")
			}
			expected := 0
			if mode == "corrupt" || mode == "read-error" {
				expected = 1
			}
			if len(service.items) != expected {
				service.mu.Unlock()
				t.Fatal("unverified credential ownership was not preserved")
			}
			service.corrupt = false
			service.failRead = false
			service.mu.Unlock()
			if err := store.CleanupCredentials(); err != nil && mode != "locked" {
				t.Fatal(err)
			}
			service.mu.Lock()
			defer service.mu.Unlock()
			if len(service.items) != 0 {
				t.Fatal("restored service retained an unreferenced credential")
			}
		})
	}
}

func TestSecretServiceAbsentSelectsFileWithoutStartingAService(t *testing.T) {
	address := privateCredentialBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	store := ContextStore{Root: t.TempDir(), CredentialMode: "auto"}
	if err := store.Put("server", Settings{Endpoint: "https://server.example", Token: "file-fallback"}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := store.List()
	if items[0].CredentialStorage != "file" {
		t.Fatal("missing service did not select the documented fallback")
	}
}

func TestSecretServiceHandshakeUsesCallerDeadline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Secret Service adapter requires Linux")
	}
	dir, err := os.MkdirTemp("", "al-keyring-stall-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bus")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		peer, err := listener.Accept()
		if err != nil {
			return
		}
		defer peer.Close()
		_, _ = peer.Read(make([]byte, 1))
		// Leave authentication unanswered until the client closes its socket.
		for {
			if _, err := peer.Read(make([]byte, 128)); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if conn, err := connectCredentialBus(ctx, "unix:path="+path); err == nil {
		conn.Close()
		t.Fatal("unanswered authentication succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("credential handshake exceeded its deadline")
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("cancelled credential connection remained open")
	}
}

func TestSecretServiceRejectsNetworkBusAndAddressFallback(t *testing.T) {
	for _, address := range []string{"tcp:host=127.0.0.1,port=1", "unix:path=/missing;tcp:host=127.0.0.1,port=1"} {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		if _, err := openSecretServiceVault(context.Background(), t.TempDir()); err == nil {
			t.Fatal("nonlocal or fallback bus accepted")
		}
	}
}

// This optional trial uses a real daemon, a private bus, and private data and
// runtime directories. It never connects to the desktop's credential service.
func TestSecretServiceDisposableGnomeKeyring(t *testing.T) {
	if os.Getenv("AGENTLAB_TEST_SECRET_SERVICE") != "1" {
		t.Skip("set AGENTLAB_TEST_SECRET_SERVICE=1 for the disposable GNOME Keyring trial")
	}
	if _, err := exec.LookPath("gnome-keyring-daemon"); err != nil {
		t.Skip("GNOME Keyring is required")
	}
	address := privateCredentialBus(t)
	root, err := os.MkdirTemp("", "al-keyring-trial-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	// Pre-create the XDG keyring directory so the daemon cannot select an old
	// keyring directory under the user's home as a compatibility fallback.
	for _, name := range []string{"data/keyrings", "runtime", "config", "cache", "control"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "DBUS_SESSION_BUS_ADDRESS", "GNOME_KEYRING_CONTROL", "GNOME_KEYRING_PID", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME":
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment, "DBUS_SESSION_BUS_ADDRESS="+address, "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_RUNTIME_DIR="+filepath.Join(root, "runtime"), "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	trialContext, cancelTrial := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancelTrial)
	var current *exec.Cmd
	t.Cleanup(func() {
		if current != nil {
			_ = current.Process.Signal(syscall.SIGTERM)
			_ = current.Wait()
		}
	})
	start := func() {
		t.Helper()
		current = exec.CommandContext(trialContext, "gnome-keyring-daemon", "--foreground", "--components=secrets", "--control-directory="+filepath.Join(root, "control"), "--unlock")
		current.Env = environment
		current.Stdin = strings.NewReader("agentlab-disposable-keyring-password")
		if err := current.Start(); err != nil {
			t.Fatal("could not start the disposable keyring")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			if _, err := openSecretServiceVault(ctx, filepath.Join(root, "agentlab")); err == nil {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("disposable keyring did not become ready")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	start()
	store := ContextStore{Root: filepath.Join(root, "agentlab"), CredentialMode: "os"}
	cfg := Settings{Endpoint: "https://disposable.example", Token: strings.Repeat("private-keyring-fixture-", 600)}
	if err := store.Put("trial", cfg); err != nil {
		t.Fatal(err)
	}
	// An optional snapshot client exercises the released command path against
	// this same disposable service, without loading user settings or tokens.
	client := os.Getenv("AGENTLAB_TEST_CLIENT")
	var clientEnvironment []string
	var clientRun func([]string, []byte) []byte
	if client != "" {
		if !filepath.IsAbs(client) {
			t.Fatal("AGENTLAB_TEST_CLIENT must name an absolute binary path")
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"server_identity":"disposable-keyring-fixture","sandboxes":{},"jobs":{}}`))
		}))
		defer server.Close()
		clientEnvironment = []string{"PATH=" + os.Getenv("PATH"), "DBUS_SESSION_BUS_ADDRESS=" + address, "AGENTLAB_CONFIG_DIR=" + filepath.Join(root, "client"), "AGENTLAB_CREDENTIAL_STORE=os"}
		clientRun = func(args []string, input []byte) []byte {
			t.Helper()
			command := exec.CommandContext(trialContext, client, args...)
			command.Dir = root
			command.Env = clientEnvironment
			command.Stdin = bytes.NewReader(input)
			output, err := command.Output()
			if err != nil {
				if failure, ok := err.(*exec.ExitError); ok && !bytes.Contains(failure.Stderr, []byte(cfg.Token)) && !bytes.Contains(output, []byte(cfg.Token)) {
					t.Fatalf("snapshot client %s failed: %s %s", args[0], output, failure.Stderr)
				}
				t.Fatal("snapshot client failed against disposable credential storage")
			}
			if bytes.Contains(output, []byte(cfg.Token)) {
				t.Fatal("snapshot client exposed a credential")
			}
			return output
		}
		output := clientRun([]string{"connect", "--endpoint", server.URL, "--credential-store", "os", "--token-stdin", "--json"}, []byte(cfg.Token))
		if !bytes.Contains(output, []byte(`"credential_storage":"secret-service"`)) {
			t.Fatal("snapshot client did not select native storage")
		}
		if _, err := os.Stat(filepath.Join(root, "client", "credentials")); !os.IsNotExist(err) {
			t.Fatal("snapshot client wrote a fallback credential")
		}
	}
	interrupted := interruptedCredential(t, store, "interrupted", Settings{Endpoint: "https://interrupted.example", Token: "uncommitted-native-fixture"}, false)
	if _, err := os.Stat(filepath.Join(root, "data", "keyrings", "login.keyring")); err != nil {
		t.Fatal("disposable keyring did not use its isolated data directory")
	}
	if err := current.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = current.Wait()
	current = nil
	start()
	actual, _, found, err := store.Resolve("trial")
	if err != nil || !found || actual != cfg {
		t.Fatal("credential did not survive disposable keyring restart")
	}
	restarted := ContextStore{Root: store.Root, CredentialMode: "file"}
	if err := restarted.CleanupCredentials(); err != nil {
		t.Fatal(err)
	}
	assertNoPendingCredentials(t, restarted)
	native, err := openSecretServiceVault(context.Background(), store.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = native.Load(context.Background(), interrupted.CredentialRef)
	var missing *CredentialError
	if !errors.As(err, &missing) || missing.Code != "credential_not_found" {
		t.Fatal("keyring restart recovery retained an uncommitted credential")
	}
	if clientRun != nil {
		clientRun([]string{"status", "--json"}, nil)
		clientRun([]string{"disconnect", "--json"}, nil)
	}
	// Lock only the disposable collection. A normal read must stop without
	// invoking Unlock or changing its recorded credential store.
	vault, err := openSecretServiceVault(context.Background(), store.Root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := vault.(secretServiceVault).open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := service.call(context.Background(), secretServicePath, secretServiceInterface+".Lock", []dbus.ObjectPath{service.collection}).Store(&locked, &prompt); err != nil || prompt != "/" {
		t.Fatal("could not lock the disposable collection")
	}
	service.conn.Close()
	if _, _, _, err := store.Resolve("trial"); err == nil {
		t.Fatal("locked disposable keyring was accepted")
	}
	if err := current.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = current.Wait()
	current = nil
	start()
	_, err = native.Load(context.Background(), interrupted.CredentialRef)
	if !errors.As(err, &missing) || missing.Code != "credential_not_found" {
		t.Fatal("deleted uncommitted credential returned after another keyring restart")
	}
	if err := store.Remove("trial"); err != nil {
		t.Fatal(err)
	}
}
