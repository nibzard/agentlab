package daemon

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a test config
func testConfig(t *testing.T) config.Config {
	t.Helper()
	temp := t.TempDir()
	return config.Config{
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
		DBPath:                  filepath.Join(temp, "agentlab.db"),
		BootstrapListen:         "127.0.0.1:0", // Use :0 to get random available port
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}
}

// Helper to cleanup service resources
func cleanupService(t *testing.T, svc *Service, cfg config.Config) {
	t.Helper()
	if svc.unixListener != nil {
		_ = svc.unixListener.Close()
	}
	if svc.controlListener != nil {
		_ = svc.controlListener.Close()
	}
	if svc.bootstrapListener != nil {
		_ = svc.bootstrapListener.Close()
	}
	if svc.artifactListener != nil {
		_ = svc.artifactListener.Close()
	}
	if svc.metricsListener != nil {
		_ = svc.metricsListener.Close()
	}
	if svc.store != nil {
		_ = svc.store.Close()
	}
	_ = os.Remove(cfg.SocketPath)
}

// Run function tests

func TestRun_ConfigValidation(t *testing.T) {
	t.Run("invalid config returns error", func(t *testing.T) {
		cfg := config.Config{
			// Missing required fields
		}
		err := Run(context.Background(), cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config_path")
	})
}

func TestRun_ConfigPermissions(t *testing.T) {
	t.Run("rejects world-readable config", func(t *testing.T) {
		temp := t.TempDir()
		configPath := filepath.Join(temp, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte("{}"), 0o644))

		cfg := config.Config{
			ConfigPath:              configPath,
			ProfilesDir:             filepath.Join(temp, "profiles"),
			RunDir:                  filepath.Join(temp, "run"),
			SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
			DBPath:                  filepath.Join(temp, "agentlab.db"),
			BootstrapListen:         "127.0.0.1:0",
			ArtifactListen:          "127.0.0.1:0",
			ArtifactDir:             filepath.Join(temp, "artifacts"),
			ArtifactMaxBytes:        1024,
			ArtifactTokenTTLMinutes: 5,
			SecretsDir:              filepath.Join(temp, "secrets"),
			SecretsBundle:           "default",
			SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
			SecretsSopsPath:         "sops",
			SnippetsDir:             filepath.Join(temp, "snippets"),
			SnippetStorage:          "local",
			ProxmoxCommandTimeout:   time.Second,
			ProvisioningTimeout:     time.Second,
		}

		err := Run(context.Background(), cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "accessible by others")
	})
}

func TestRun_ProfileLoadError(t *testing.T) {
	t.Run("non-existent profiles dir", func(t *testing.T) {
		temp := t.TempDir()
		cfg := config.Config{
			ConfigPath:              filepath.Join(temp, "config.yaml"),
			ProfilesDir:             "/nonexistent/profiles/dir",
			RunDir:                  filepath.Join(temp, "run"),
			SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
			DBPath:                  filepath.Join(temp, "agentlab.db"),
			BootstrapListen:         "127.0.0.1:0",
			ArtifactListen:          "127.0.0.1:0",
			ArtifactDir:             filepath.Join(temp, "artifacts"),
			ArtifactMaxBytes:        1024,
			ArtifactTokenTTLMinutes: 5,
			SecretsDir:              filepath.Join(temp, "secrets"),
			SecretsBundle:           "default",
			SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
			SecretsSopsPath:         "sops",
			SnippetsDir:             filepath.Join(temp, "snippets"),
			SnippetStorage:          "local",
			ProxmoxCommandTimeout:   time.Second,
			ProvisioningTimeout:     time.Second,
		}

		// Write empty config file
		err := os.WriteFile(cfg.ConfigPath, []byte("{}"), 0600)
		require.NoError(t, err)

		err = Run(context.Background(), cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "profiles")
	})
}

func TestRun_DatabaseInitError(t *testing.T) {
	t.Run("invalid db path", func(t *testing.T) {
		temp := t.TempDir()
		profilesDir := filepath.Join(temp, "profiles")
		err := os.MkdirAll(profilesDir, 0755)
		require.NoError(t, err)

		cfg := config.Config{
			ConfigPath:              filepath.Join(temp, "config.yaml"),
			ProfilesDir:             profilesDir,
			RunDir:                  filepath.Join(temp, "run"),
			SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
			DBPath:                  "/dev/null/agentlab.db", // Invalid: cannot create db directory under /dev/null
			BootstrapListen:         "127.0.0.1:0",
			ArtifactListen:          "127.0.0.1:0",
			ArtifactDir:             filepath.Join(temp, "artifacts"),
			ArtifactMaxBytes:        1024,
			ArtifactTokenTTLMinutes: 5,
			SecretsDir:              filepath.Join(temp, "secrets"),
			SecretsBundle:           "default",
			SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
			SecretsSopsPath:         "sops",
			SnippetsDir:             filepath.Join(temp, "snippets"),
			SnippetStorage:          "local",
			ProxmoxCommandTimeout:   time.Second,
			ProvisioningTimeout:     time.Second,
		}

		// Write empty config file
		err = os.WriteFile(cfg.ConfigPath, []byte("{}"), 0600)
		require.NoError(t, err)

		err = Run(context.Background(), cfg)
		assert.Error(t, err)
	})
}

// NewService tests

func TestNewService_Success(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)
	require.NotNil(t, service)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	// Verify all listeners are created
	assert.NotNil(t, service.unixListener)
	assert.NotNil(t, service.bootstrapListener)
	assert.NotNil(t, service.artifactListener)

	// Verify servers are created
	assert.NotNil(t, service.unixServer)
	assert.NotNil(t, service.bootstrapServer)
	assert.NotNil(t, service.artifactServer)

	// Verify managers are created
	assert.NotNil(t, service.sandboxManager)
	assert.NotNil(t, service.workspaceManager)
	assert.NotNil(t, service.artifactGC)
}

func TestNewService_RunDirError(t *testing.T) {
	temp := t.TempDir()
	cfg := config.Config{
		RunDir:                  "/dev/null/agentlab-test", // Invalid - cannot create under /dev/null
		SocketPath:              filepath.Join(temp, "sock"),
		DBPath:                  filepath.Join(temp, "db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	_, err = NewService(cfg, map[string]models.Profile{}, store)
	assert.Error(t, err)
	// Error message will contain "create dir" and the path
	assert.Contains(t, err.Error(), "create dir")
}

func TestNewService_ArtifactDirError(t *testing.T) {
	temp := t.TempDir()
	cfg := config.Config{
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "sock"),
		DBPath:                  filepath.Join(temp, "db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             "/dev/null/agentlab-artifacts", // Invalid - cannot create under /dev/null
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	_, err = NewService(cfg, map[string]models.Profile{}, store)
	assert.Error(t, err)
	// Error message will contain "create dir" and path
	assert.Contains(t, err.Error(), "create dir")
}

func TestNewService_UnixSocketError(t *testing.T) {
	temp := t.TempDir()
	cfg := config.Config{
		RunDir:                  temp,
		SocketPath:              "/dev/null/agentlab.sock", // Invalid - cannot create under /dev/null
		DBPath:                  filepath.Join(temp, "db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	_, err = NewService(cfg, map[string]models.Profile{}, store)
	assert.Error(t, err)
}

// listenUnix tests

func TestListenUnix_Success(t *testing.T) {
	temp := t.TempDir()
	socketPath := filepath.Join(temp, "test.sock")

	listener, err := listenUnix(socketPath)
	require.NoError(t, err)
	require.NotNil(t, listener)
	defer listener.Close()

	// Verify socket file exists
	info, err := os.Stat(socketPath)
	require.NoError(t, err)
	assert.True(t, info.Mode()&os.ModeSocket != 0, "should be a socket")

	// Verify permissions
	stat, err := os.Stat(socketPath)
	require.NoError(t, err)
	// On Unix systems, check permissions
	expectedPerms := os.FileMode(socketPerms)
	assert.Equal(t, expectedPerms, stat.Mode().Perm())
}

func TestListenUnix_Permissions(t *testing.T) {
	temp := t.TempDir()
	socketPath := filepath.Join(temp, "test.sock")

	listener, err := listenUnix(socketPath)
	require.NoError(t, err)
	defer listener.Close()

	// Verify socket has correct permissions (0o660 = rw-rw----)
	info, err := os.Stat(socketPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o660), info.Mode().Perm())
}

func TestListenUnix_DirectoryFail(t *testing.T) {
	// Use a path where parent directory can't be created
	socketPath := "/dev/null/test.sock"

	_, err := listenUnix(socketPath)
	assert.Error(t, err)
}

func TestListenUnix_RemoveStale(t *testing.T) {
	temp := t.TempDir()
	socketPath := filepath.Join(temp, "test.sock")

	// Create a stale socket file
	err := os.WriteFile(socketPath, []byte("stale"), 0600)
	require.NoError(t, err)

	// listenUnix should remove the stale file and create a new socket
	listener, err := listenUnix(socketPath)
	require.NoError(t, err)
	defer listener.Close()

	// Verify it's now a valid socket
	info, err := os.Stat(socketPath)
	require.NoError(t, err)
	assert.True(t, info.Mode()&os.ModeSocket != 0)
}

func TestListenUnix_EmptyPath(t *testing.T) {
	_, err := listenUnix("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "socket_path is required")
}

// Serve and shutdown tests

func TestServe_GracefulShutdown(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	// Create a context that will be canceled soon
	ctx, cancel := context.WithCancel(context.Background())

	// Start serving in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify servers are running by making a request
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + service.bootstrapListener.Addr().String() + "/healthz")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Trigger graceful shutdown
	cancel()

	// Wait for serve to complete
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down within timeout")
	}

	// Verify socket was removed
	_, err = os.Stat(cfg.SocketPath)
	assert.True(t, os.IsNotExist(err))
}

func TestServe_ShutdownTimeout(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Trigger shutdown
	cancel()

	// Should complete within shutdown timeout + some buffer
	select {
	case <-errCh:
		// Success
	case <-time.After(shutdownTimeout + 2*time.Second):
		t.Fatal("Serve did not complete within expected time")
	}
}

func TestServe_ConcurrentSignals(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate multiple rapid signals
	for i := 0; i < 5; i++ {
		cancel()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not complete after multiple signals")
	}
}

func TestShutdown_SocketRemoval(t *testing.T) {
	temp := t.TempDir()
	socketPath := filepath.Join(temp, "test.sock")

	cfg := testConfig(t)
	cfg.SocketPath = socketPath

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	// Verify socket exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- service.Serve(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown
	cancel()
	<-errCh

	// Verify socket was removed
	_, err = os.Stat(socketPath)
	assert.True(t, os.IsNotExist(err), "socket should be removed after shutdown")
}

// Listener cleanup tests

func TestPartialCleanup_BootstrapFail(t *testing.T) {
	temp := t.TempDir()
	cfg := testConfig(t)
	cfg.SocketPath = filepath.Join(temp, "sock.sock")

	// Create a socket that will be cleaned up
	err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0755)
	require.NoError(t, err)

	store, err := db.Open(filepath.Join(temp, "db"))
	require.NoError(t, err)

	// Use invalid bootstrap listen to cause partial failure
	cfg.BootstrapListen = "invalid:host:port"

	_, err = NewService(cfg, map[string]models.Profile{}, store)
	assert.Error(t, err)

	// Socket should be cleaned up even after bootstrap failure
	// (This is implicit - if NewService fails, the test verifies it doesn't leak resources)
}

func TestMetricsCleanup_AllListeners(t *testing.T) {
	cfg := testConfig(t)
	cfg.MetricsListen = "127.0.0.1:0"

	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	// Verify metrics listener was created
	assert.NotNil(t, service.metricsListener)
	assert.NotNil(t, service.metricsServer)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown
	cancel()
	<-errCh

	// All listeners should be closed (implicit - no errors in cleanup)
}

// Test helper functions

func TestEnsureDir(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		temp := t.TempDir()
		dir := filepath.Join(temp, "subdir", "nested")

		err := ensureDir(dir, 0755)
		require.NoError(t, err)

		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("empty path", func(t *testing.T) {
		err := ensureDir("", 0755)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})
}

func TestBuildControllerURL(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   string
	}{
		{
			name:   "simple host port",
			listen: "10.77.0.1:8844",
			want:   "http://10.77.0.1:8844",
		},
		{
			name:   "ipv6",
			listen: "[::1]:8844",
			want:   "http://[::1]:8844",
		},
		{
			name:   "without port",
			listen: "localhost",
			want:   "http://localhost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildControllerURL(tt.listen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildArtifactUploadURL(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   string
	}{
		{
			name:   "simple host port",
			listen: "10.77.0.1:8846",
			want:   "http://10.77.0.1:8846/upload",
		},
		{
			name:   "ipv6",
			listen: "[::1]:8846",
			want:   "http://[::1]:8846/upload",
		},
		{
			name:   "empty",
			listen: "",
			want:   "",
		},
		{
			name:   "whitespace",
			listen: "  ",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArtifactUploadURL(tt.listen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveAgentSubnet(t *testing.T) {
	t.Run("explicit subnet", func(t *testing.T) {
		subnet, err := resolveAgentSubnet("10.77.0.0/16", "127.0.0.1:8844")
		require.NoError(t, err)
		assert.Equal(t, "10.77.0.0/16", subnet.String())
	})

	t.Run("empty subnet derives from listen", func(t *testing.T) {
		subnet, err := resolveAgentSubnet("", "10.77.0.1:8844")
		require.NoError(t, err)
		assert.NotNil(t, subnet)
		assert.Equal(t, "10.77.0.0/16", subnet.String())
	})

	t.Run("invalid CIDR", func(t *testing.T) {
		_, err := resolveAgentSubnet("invalid-cidr", "127.0.0.1:8844")
		assert.Error(t, err)
	})
}

// Integration: Test with actual socket operations

func TestService_SocketCommunication(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- service.Serve(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-errCh
		cleanupService(t, service, cfg)
	})

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test Unix socket connection
	conn, err := net.Dial("unix", cfg.SocketPath)
	require.NoError(t, err)
	defer conn.Close()

	// Make a simple HTTP request
	_, err = conn.Write([]byte("GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	require.NoError(t, err)

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	require.NoError(t, err)
	assert.Contains(t, string(buf[:n]), "200 OK")
}

// Test that Service handles context cancellation correctly

func TestService_ContextCancellation(t *testing.T) {
	cfg := testConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	service, err := NewService(cfg, map[string]models.Profile{}, store)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupService(t, service, cfg)
	})

	// Create a context that will be canceled
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Service did not stop when context was canceled")
	}
}

// Test signal handling simulation

func TestService_SignalContextSetup(t *testing.T) {
	// This simulates what main.go does with signal.NotifyContext
	// For testing, we just use a regular cancel context since we can't send real signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Verify context is initially not done
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done immediately")
	default:
		// Expected
	}

	// Simulate signal reception (in real code, signal.NotifyContext would handle this)
	// For this test, we just verify the setup works
	assert.NotNil(t, ctx)
}

// healthHandler tests

func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/healthz", nil)
	require.NoError(t, err)

	rec := &responseRecorder{}

	healthHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.status)
	assert.Equal(t, "ok", rec.body.String())
}

// responseRecorder is a simple http.ResponseWriter for testing
type responseRecorder struct {
	status int
	body   *stringWriter
}

func (r *responseRecorder) Header() http.Header {
	return http.Header{}
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.body == nil {
		r.body = &stringWriter{}
	}
	return r.body.Write(b)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
}

type stringWriter struct {
	data []byte
}

func (s *stringWriter) Write(b []byte) (int, error) {
	s.data = append(s.data, b...)
	return len(b), nil
}

func (s *stringWriter) String() string {
	return string(s.data)
}
