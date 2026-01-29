package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

const (
	shutdownTimeout = 5 * time.Second
	socketPerms     = 0o660
	runDirPerms     = 0o750
)

// Service wires listeners for the local control socket and guest bootstrap.
type Service struct {
	cfg               config.Config
	profiles          map[string]models.Profile
	store             *db.Store
	unixListener      net.Listener
	bootstrapListener net.Listener
	unixServer        *http.Server
	bootstrapServer   *http.Server
}

// Run loads profiles, binds listeners, and serves until ctx is canceled.
func Run(ctx context.Context, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	profiles, err := LoadProfiles(cfg.ProfilesDir)
	if err != nil {
		return err
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	service, err := NewService(cfg, profiles, store)
	if err != nil {
		_ = store.Close()
		return err
	}
	log.Printf("agentlabd: loaded %d profiles from %s", len(profiles), cfg.ProfilesDir)
	return service.Serve(ctx)
}

// NewService constructs a service with bound listeners.
func NewService(cfg config.Config, profiles map[string]models.Profile, store *db.Store) (*Service, error) {
	if err := ensureDir(cfg.RunDir, runDirPerms); err != nil {
		return nil, err
	}
	unixListener, err := listenUnix(cfg.SocketPath)
	if err != nil {
		return nil, err
	}
	bootstrapListener, err := net.Listen("tcp", cfg.BootstrapListen)
	if err != nil {
		_ = unixListener.Close()
		return nil, fmt.Errorf("listen bootstrap %s: %w", cfg.BootstrapListen, err)
	}

	localMux := http.NewServeMux()
	localMux.HandleFunc("/healthz", healthHandler)

	bootstrapMux := http.NewServeMux()
	bootstrapMux.HandleFunc("/healthz", healthHandler)
	bootstrapMux.HandleFunc("/v1/bootstrap/fetch", bootstrapNotImplementedHandler)

	unixServer := &http.Server{
		Handler:           localMux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	bootstrapServer := &http.Server{
		Handler:           bootstrapMux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	return &Service{
		cfg:               cfg,
		profiles:          profiles,
		store:             store,
		unixListener:      unixListener,
		bootstrapListener: bootstrapListener,
		unixServer:        unixServer,
		bootstrapServer:   bootstrapServer,
	}, nil
}

// Serve blocks until shutdown or a listener error occurs.
func (s *Service) Serve(ctx context.Context) error {
	log.Printf("agentlabd: listening on unix=%s", s.cfg.SocketPath)
	log.Printf("agentlabd: listening on bootstrap=%s", s.cfg.BootstrapListen)

	errCh := make(chan error, 2)
	go func() { errCh <- s.unixServer.Serve(s.unixListener) }()
	go func() { errCh <- s.bootstrapServer.Serve(s.bootstrapListener) }()

	remaining := 2
	var serveErr error

	select {
	case <-ctx.Done():
		// graceful shutdown
	case err := <-errCh:
		remaining = 1
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}

	s.shutdown()
	for i := 0; i < remaining; i++ {
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) && serveErr == nil {
			serveErr = err
		}
	}

	_ = os.Remove(s.cfg.SocketPath)
	return serveErr
}

func (s *Service) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = s.unixServer.Shutdown(ctx)
	_ = s.bootstrapServer.Shutdown(ctx)
	if s.store != nil {
		_ = s.store.Close()
	}
}

func ensureDir(path string, perms os.FileMode) error {
	if path == "" {
		return errors.New("run_dir is required")
	}
	if err := os.MkdirAll(path, perms); err != nil {
		return fmt.Errorf("create dir %s: %w", path, err)
	}
	return nil
}

func listenUnix(socketPath string) (net.Listener, error) {
	if socketPath == "" {
		return nil, errors.New("socket_path is required")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), runDirPerms); err != nil {
		return nil, fmt.Errorf("create socket dir %s: %w", filepath.Dir(socketPath), err)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, socketPerms); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", socketPath, err)
	}
	return listener, nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func bootstrapNotImplementedHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"bootstrap endpoint not implemented"}`))
}
