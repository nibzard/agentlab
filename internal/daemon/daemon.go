// Package daemon implements the core AgentLab daemon service.
//
// The daemon manages the lifecycle of sandboxes (Proxmox VMs), jobs, workspaces,
// and artifacts. It exposes HTTP APIs over a Unix socket for local control
// and over TCP for guest VM bootstrap and artifact upload.
//
// Main components:
//   - Service: Main daemon structure that wires together all components
//   - SandboxManager: Manages sandbox state transitions and lifecycle
//   - JobOrchestrator: Handles job provisioning and execution
//   - WorkspaceManager: Manages persistent workspace volumes
//   - ControlAPI: HTTP API for local control over Unix socket
//   - BootstrapAPI: HTTP API for guest VM bootstrap
//   - ArtifactAPI: HTTP API for artifact upload and retrieval
//
// The daemon supports two Proxmox backends: API (preferred) and Shell (fallback).
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
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/secrets"
)

const (
	shutdownTimeout  = 5 * time.Second // Graceful shutdown timeout
	socketPerms      = 0o660           // Unix socket permissions (owner+group read/write)
	runDirPerms      = 0o750           // Run directory permissions (owner full, group read+exec)
	artifactDirPerms = 0o750           // Artifact directory permissions
)

// Service wires listeners for the local control socket and guest bootstrap.
//
// It manages multiple HTTP servers:
//   - Unix socket server for local control API
//   - TCP server for guest VM bootstrap
//   - TCP server for artifact upload/download
//   - Optional TCP server for Prometheus metrics
//
// The Service coordinates the lifecycle of all daemon components and ensures
// graceful shutdown on context cancellation.
type Service struct {
	cfg               config.Config
	profiles          map[string]models.Profile
	store             *db.Store
	unixListener      net.Listener
	bootstrapListener net.Listener
	artifactListener  net.Listener
	metricsListener   net.Listener
	unixServer        *http.Server
	bootstrapServer   *http.Server
	artifactServer    *http.Server
	metricsServer     *http.Server
	sandboxManager    *SandboxManager
	workspaceManager  *WorkspaceManager
	artifactGC        *ArtifactGC
	idleStopper       *IdleStopper
	metrics           *Metrics
}

// Run loads profiles, binds listeners, and serves until ctx is canceled.
//
// This is the main entry point for starting the daemon. It performs the following:
// 1. Validates the configuration
// 2. Loads profile definitions from the profiles directory
// 3. Opens the database
// 4. Creates and wires the service with all listeners
// 5. Serves until the context is canceled
//
// Returns any error that occurs during startup or serving.
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
//
// It creates all necessary HTTP servers and binds their listeners:
//   - Unix socket listener for local control API
//   - TCP listener for guest VM bootstrap API
//   - TCP listener for artifact upload/download
//   - Optional TCP listener for Prometheus metrics
//
// The function also initializes all manager components (SandboxManager,
// WorkspaceManager, JobOrchestrator, ArtifactGC) with their dependencies.
//
// Returns an error if any listener fails to bind or if required directories
// cannot be created.
func NewService(cfg config.Config, profiles map[string]models.Profile, store *db.Store) (*Service, error) {
	if err := ensureDir(cfg.RunDir, runDirPerms); err != nil {
		return nil, err
	}
	if err := ensureDir(cfg.ArtifactDir, artifactDirPerms); err != nil {
		return nil, err
	}
	agentSubnet, err := resolveAgentSubnet(cfg.AgentSubnet, cfg.BootstrapListen)
	if err != nil {
		return nil, err
	}
	agentCIDR := ""
	if agentSubnet != nil {
		agentCIDR = agentSubnet.String()
	}
	var metrics *Metrics
	if strings.TrimSpace(cfg.MetricsListen) != "" {
		metrics = NewMetrics()
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
	artifactListener, err := net.Listen("tcp", cfg.ArtifactListen)
	if err != nil {
		_ = bootstrapListener.Close()
		_ = unixListener.Close()
		return nil, fmt.Errorf("listen artifact %s: %w", cfg.ArtifactListen, err)
	}
	var metricsListener net.Listener
	if metrics != nil {
		metricsListener, err = net.Listen("tcp", cfg.MetricsListen)
		if err != nil {
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("listen metrics %s: %w", cfg.MetricsListen, err)
		}
	}

	// Create Proxmox backend based on configuration
	var backend proxmox.Backend
	switch strings.ToLower(strings.TrimSpace(cfg.ProxmoxBackend)) {
	case "api":
		// Use API backend
		apiBackend, err := proxmox.NewAPIBackend(
			cfg.ProxmoxAPIURL,
			cfg.ProxmoxAPIToken,
			cfg.ProxmoxNode,
			agentCIDR,
			cfg.ProxmoxCommandTimeout,
			cfg.ProxmoxTLSInsecure,
			cfg.ProxmoxTLSCAPath,
		)
		if err != nil {
			_ = metricsListener.Close()
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("create Proxmox API backend: %w", err)
		}
		backend = apiBackend
		log.Printf("using Proxmox API backend (url=%s)", cfg.ProxmoxAPIURL)
	case "shell", "", "default":
		// Use shell backend (backward compatible)
		backend = &proxmox.ShellBackend{
			CommandTimeout: cfg.ProxmoxCommandTimeout,
			AgentCIDR:      agentCIDR,
			Runner:         &proxmox.BashRunner{},
		}
		log.Printf("using Proxmox shell backend")
	default:
		_ = metricsListener.Close()
		_ = artifactListener.Close()
		_ = bootstrapListener.Close()
		_ = unixListener.Close()
		return nil, fmt.Errorf("unknown proxmox_backend: %s (must be 'api' or 'shell')", cfg.ProxmoxBackend)
	}
	workspaceManager := NewWorkspaceManager(store, backend, log.Default())
	sandboxManager := NewSandboxManager(store, backend, log.Default()).WithWorkspaceManager(workspaceManager).WithMetrics(metrics)
	exposurePublisher := &TailscaleServePublisher{Runner: proxmox.ExecRunner{}}
	sandboxManager.WithExposureCleaner(NewExposureCleaner(store, exposurePublisher, log.Default()))
	redactor := NewRedactor(nil)
	snippetStore := proxmox.SnippetStore{
		Storage: cfg.SnippetStorage,
		Dir:     cfg.SnippetsDir,
	}
	controllerURL := strings.TrimSpace(cfg.ControllerURL)
	if controllerURL == "" {
		controllerURL = buildControllerURL(cfg.BootstrapListen)
	}
	jobOrchestrator := NewJobOrchestrator(store, profiles, backend, sandboxManager, workspaceManager, snippetStore, cfg.SSHPublicKey, controllerURL, log.Default(), redactor, metrics)
	if jobOrchestrator != nil {
		sandboxManager.WithSnippetCleaner(jobOrchestrator.CleanupSnippet)
	}

	localMux := http.NewServeMux()
	localMux.HandleFunc("/healthz", healthHandler)
	NewControlAPI(store, profiles, sandboxManager, workspaceManager, jobOrchestrator, cfg.ArtifactDir, log.Default()).
		WithMetricsEnabled(metrics != nil).
		WithExposurePublisher(exposurePublisher).
		Register(localMux)

	bootstrapMux := http.NewServeMux()
	bootstrapMux.HandleFunc("/healthz", healthHandler)
	secretsStore := secrets.Store{
		Dir:        cfg.SecretsDir,
		AgeKeyPath: cfg.SecretsAgeKeyPath,
		SopsPath:   cfg.SecretsSopsPath,
	}
	artifactEndpoint := strings.TrimSpace(cfg.ArtifactUploadURL)
	if artifactEndpoint == "" {
		artifactEndpoint = buildArtifactUploadURL(cfg.ArtifactListen)
	}
	NewBootstrapAPI(store, profiles, secretsStore, cfg.SecretsBundle, agentSubnet, artifactEndpoint, time.Duration(cfg.ArtifactTokenTTLMinutes)*time.Minute, redactor).Register(bootstrapMux)
	NewRunnerAPI(jobOrchestrator, agentSubnet).Register(bootstrapMux)

	artifactMux := http.NewServeMux()
	artifactMux.HandleFunc("/healthz", healthHandler)
	NewArtifactAPI(store, cfg.ArtifactDir, cfg.ArtifactMaxBytes, agentSubnet).Register(artifactMux)

	artifactGC := NewArtifactGC(store, profiles, cfg.ArtifactDir, log.Default(), redactor)
	idleStopper := NewIdleStopper(store, backend, profiles, sandboxManager, &ConntrackSessionDetector{}, log.Default(), metrics, IdleStopConfig{
		Enabled:        cfg.IdleStopEnabled,
		Interval:       cfg.IdleStopInterval,
		DefaultMinutes: cfg.IdleStopMinutesDefault,
		CPUThreshold:   cfg.IdleStopCPUThreshold,
	})

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
	artifactServer := &http.Server{
		Handler:           artifactMux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	var metricsServer *http.Server
	if metrics != nil {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", metrics.Handler())
		metricsMux.HandleFunc("/healthz", healthHandler)
		metricsServer = &http.Server{
			Handler:           metricsMux,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
	}

	return &Service{
		cfg:               cfg,
		profiles:          profiles,
		store:             store,
		unixListener:      unixListener,
		bootstrapListener: bootstrapListener,
		artifactListener:  artifactListener,
		metricsListener:   metricsListener,
		unixServer:        unixServer,
		bootstrapServer:   bootstrapServer,
		artifactServer:    artifactServer,
		metricsServer:     metricsServer,
		sandboxManager:    sandboxManager,
		workspaceManager:  workspaceManager,
		artifactGC:        artifactGC,
		idleStopper:       idleStopper,
		metrics:           metrics,
	}, nil
}

// Serve blocks until shutdown or a listener error occurs.
//
// It starts all HTTP servers in goroutines and waits for either:
//   - Context cancellation (graceful shutdown)
//   - A listener error (immediate shutdown)
//
// On shutdown, it gracefully closes all servers with a timeout, closes the
// database, and removes the Unix socket file.
//
// Returns any error that occurred during serving (excluding http.ErrServerClosed).
func (s *Service) Serve(ctx context.Context) error {
	serverCount := 3
	if s.metricsServer != nil {
		serverCount++
	}
	log.Printf("agentlabd: listening on unix=%s", s.cfg.SocketPath)
	log.Printf("agentlabd: listening on bootstrap=%s", s.cfg.BootstrapListen)
	log.Printf("agentlabd: listening on artifacts=%s", s.cfg.ArtifactListen)
	if s.metricsServer != nil {
		log.Printf("agentlabd: listening on metrics=%s", s.cfg.MetricsListen)
	}
	if s.sandboxManager != nil {
		s.sandboxManager.StartLeaseGC(ctx)
		s.sandboxManager.StartReconciler(ctx)
	}
	if s.idleStopper != nil {
		s.idleStopper.Start(ctx)
	}
	if s.artifactGC != nil {
		s.artifactGC.Start(ctx)
	}

	errCh := make(chan error, serverCount)
	go func() { errCh <- s.unixServer.Serve(s.unixListener) }()
	go func() { errCh <- s.bootstrapServer.Serve(s.bootstrapListener) }()
	go func() { errCh <- s.artifactServer.Serve(s.artifactListener) }()
	if s.metricsServer != nil {
		go func() { errCh <- s.metricsServer.Serve(s.metricsListener) }()
	}

	remaining := serverCount
	var serveErr error

	select {
	case <-ctx.Done():
		// graceful shutdown
	case err := <-errCh:
		remaining = serverCount - 1
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
	_ = s.artifactServer.Shutdown(ctx)
	if s.metricsServer != nil {
		_ = s.metricsServer.Shutdown(ctx)
	}
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

func buildControllerURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

func buildArtifactUploadURL(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/upload"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port + "/upload"
}

func resolveAgentSubnet(agentSubnet, listen string) (*net.IPNet, error) {
	agentSubnet = strings.TrimSpace(agentSubnet)
	if agentSubnet == "" {
		return deriveAgentSubnet(listen), nil
	}
	_, subnet, err := net.ParseCIDR(agentSubnet)
	if err != nil {
		return nil, fmt.Errorf("agent_subnet must be CIDR: %w", err)
	}
	return subnet, nil
}
