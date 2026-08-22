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

	"github.com/agentlab/agentlab/internal/api"
	"github.com/agentlab/agentlab/internal/auth"
	backendpkg "github.com/agentlab/agentlab/internal/backend"
	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/pool"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/proxy"
	"github.com/agentlab/agentlab/internal/sandbox"
	"github.com/agentlab/agentlab/internal/secrets"
	"github.com/agentlab/agentlab/internal/user"
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
	controlListener   net.Listener
	bootstrapListener net.Listener
	artifactListener  net.Listener
	metricsListener   net.Listener
	unixServer        *http.Server
	controlServer     *http.Server
	bootstrapServer   *http.Server
	artifactServer    *http.Server
	metricsServer     *http.Server
	sandboxManager    *SandboxManager
	workspaceManager  *WorkspaceManager
	artifactGC        *ArtifactGC
	idleStopper       *IdleStopper
	metrics           *Metrics
	metadataRouting   *MetadataRouting
	lxcBackend        *sandbox.LXCBackend
	sandboxBackend    sandbox.Backend
	integrationStore  *integrations.Store
	userRegistry      *user.Registry
	resourcePool      *pool.Pool

	// Lifecycle: a context cancelled at shutdown and a tracker for in-flight
	// background work, so shutdown waits for (or times out waiting for) detached
	// operations before closing the store (review H2).
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	tasks           *taskTracker
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
	if warning, err := config.CheckConfigPermissions(cfg.ConfigPath); err != nil {
		return err
	} else if warning != "" {
		log.Printf("warning: %s", warning)
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
// NewService constructs a service with backend selection based on config.
func NewService(cfg config.Config, profiles map[string]models.Profile, store *db.Store) (*Service, error) {
	return newService(cfg, profiles, store, nil)
}

// NewServiceWithBackend constructs a service using the provided backend.
// This is primarily intended for tests that want deterministic backends.
func NewServiceWithBackend(cfg config.Config, profiles map[string]models.Profile, store *db.Store, backend proxmox.Backend) (*Service, error) {
	if backend == nil {
		return nil, errors.New("backend is required")
	}
	return newService(cfg, profiles, store, backend)
}

func newService(cfg config.Config, profiles map[string]models.Profile, store *db.Store, backendOverride proxmox.Backend) (*Service, error) {
	if cfg.Offline {
		log.Printf("offline mode enabled: all external network calls are blocked")
	}
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
	cloneMode := strings.ToLower(strings.TrimSpace(cfg.ProxmoxCloneMode))
	if cloneMode == "" {
		cloneMode = "linked"
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
	var controlListener net.Listener
	if strings.TrimSpace(cfg.ControlListen) != "" {
		controlListener, err = net.Listen("tcp", cfg.ControlListen)
		if err != nil {
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("listen control %s: %w", cfg.ControlListen, err)
		}
	}
	var metricsListener net.Listener
	if metrics != nil {
		metricsListener, err = net.Listen("tcp", cfg.MetricsListen)
		if err != nil {
			if controlListener != nil {
				_ = controlListener.Close()
			}
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("listen metrics %s: %w", cfg.MetricsListen, err)
		}
	}

	// Create Proxmox backend based on configuration (unless overridden)
	var backend proxmox.Backend
	var sbBackend sandbox.Backend // tracked for Service.sandboxBackend
	primaryBackend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if primaryBackend == "" {
		primaryBackend = "proxmox" // default for backward compat
	}

	switch primaryBackend {
	case "proxmox":
		if backendOverride != nil {
			backend = backendOverride
			sbBackend = sandbox.NewVMBackend(backendOverride)
			log.Printf("using provided Proxmox backend override")
		} else {
			switch strings.ToLower(strings.TrimSpace(cfg.ProxmoxBackend)) {
			case "api":
				if cfg.ProxmoxTLSInsecure {
					log.Printf("warning: proxmox_tls_insecure is enabled; Proxmox API TLS verification is disabled. This is insecure and should only be used temporarily. See docs/configuration.md for proxmox_tls_ca_path guidance.")
				}
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
				apiBackend.CloneMode = cloneMode
				apiBackend.AllowShellFallback = cfg.ProxmoxAPIShellFallback
				if cfg.ProxmoxAPIShellFallback {
					apiBackend.ShellFallback = &proxmox.ShellBackend{
						CommandTimeout: cfg.ProxmoxCommandTimeout,
						Runner:         &proxmox.BashRunner{},
					}
				}
				backend = apiBackend
				sbBackend = sandbox.NewVMBackend(apiBackend)
				log.Printf("using Proxmox API backend (url=%s)", cfg.ProxmoxAPIURL)
			case "shell", "", "default":
				backend = &proxmox.ShellBackend{
					CommandTimeout: cfg.ProxmoxCommandTimeout,
					AgentCIDR:      agentCIDR,
					Runner:         &proxmox.BashRunner{},
					CloneMode:      cloneMode,
				}
				sbBackend = sandbox.NewVMBackend(backend)
				log.Printf("using Proxmox shell backend")
			default:
				_ = metricsListener.Close()
				_ = artifactListener.Close()
				_ = bootstrapListener.Close()
				_ = unixListener.Close()
				return nil, fmt.Errorf("unknown proxmox_backend: %s (must be 'api' or 'shell')", cfg.ProxmoxBackend)
			}
		}
	case "docker":
		dockerCfg := sandbox.DockerConfig{
			Host:    cfg.DockerHost,
			Timeout: cfg.ProxmoxCommandTimeout,
			Offline: cfg.Offline,
		}
		dockerBackend, err := sandbox.NewDockerBackend(dockerCfg)
		if err != nil {
			_ = metricsListener.Close()
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("create Docker backend: %w", err)
		}
		// Run health check on startup.
		if err := backendpkg.CheckHealth(context.Background(), dockerBackend); err != nil {
			log.Printf("warning: Docker backend health check failed: %v", err)
		} else {
			log.Printf("Docker backend health check passed")
		}
		log.Printf("using Docker backend (host=%s)", dockerCfg.Host)
		sbBackend = dockerBackend
		backend = newSandboxBackendAdapter(dockerBackend)
	case "libvirt":
		libvirtCfg := sandbox.LibvirtConfig{
			URI:     cfg.LibvirtURI,
			Timeout: cfg.ProxmoxCommandTimeout,
		}
		libvirtBackend, err := sandbox.NewLibvirtBackend(libvirtCfg)
		if err != nil {
			_ = metricsListener.Close()
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("create libvirt backend: %w", err)
		}
		// Run health check on startup.
		if err := backendpkg.CheckHealth(context.Background(), libvirtBackend); err != nil {
			log.Printf("warning: libvirt backend health check failed: %v", err)
		} else {
			log.Printf("libvirt backend health check passed")
		}
		log.Printf("using libvirt backend (uri=%s)", libvirtCfg.URI)
		sbBackend = libvirtBackend
		backend = newSandboxBackendAdapter(libvirtBackend)
	default:
		_ = metricsListener.Close()
		_ = artifactListener.Close()
		_ = bootstrapListener.Close()
		_ = unixListener.Close()
		return nil, fmt.Errorf("unknown backend: %s (must be 'proxmox', 'docker', or 'libvirt')", primaryBackend)
	}
	workspaceManager := NewWorkspaceManager(store, backend, log.Default())
	sandboxManager := NewSandboxManager(store, backend, log.Default()).WithWorkspaceManager(workspaceManager).WithMetrics(metrics)

	// Set up LXC backend if enabled and using Proxmox API
	var lxcBackend *sandbox.LXCBackend
	if cfg.LXCEnabled && strings.ToLower(strings.TrimSpace(cfg.ProxmoxBackend)) == "api" {
		lxcCfg := sandbox.ProxmoxAPIConfig{
			URL:         cfg.ProxmoxAPIURL,
			Token:       cfg.ProxmoxAPIToken,
			Node:        cfg.ProxmoxNode,
			Timeout:     cfg.ProxmoxCommandTimeout,
			TLSInsecure: cfg.ProxmoxTLSInsecure,
			TLSCAPath:   cfg.ProxmoxTLSCAPath,
		}
		var lxcErr error
		lxcBackend, lxcErr = sandbox.NewLXCBackend(lxcCfg)
		if lxcErr != nil {
			log.Printf("warning: LXC backend init failed (LXC sandboxes will not be available): %v", lxcErr)
		} else {
			log.Printf("LXC container backend enabled (node=%s)", cfg.ProxmoxNode)
		}
	} else if cfg.LXCEnabled {
		log.Printf("warning: LXC backend requires proxmox_backend=api; LXC sandboxes will not be available")
	}

	// Build exposure publisher: Tailscale is always available,
	// Caddy proxy is added when proxy_enabled is true in config.
	// In offline mode, Tailscale is skipped (requires internet coordination).
	var exposurePublisher ExposurePublisher
	if !cfg.Offline {
		exposurePublisher = &TailscaleServePublisher{Runner: proxmox.ExecRunner{}, AgentSubnet: agentSubnet}
	}

	if cfg.ProxyEnabled {
		tlsMode := proxy.TLSMode(strings.TrimSpace(cfg.ProxyTLSMode))
		if tlsMode == "" {
			tlsMode = proxy.TLSModeSelfSigned
		}
		proxyCfg := proxy.ProxyConfig{
			Enabled:    true,
			Domain:     cfg.ProxyDomain,
			TLSMode:    tlsMode,
			TLSEmail:   cfg.ProxyTLSEmail,
			CaddyAPI:   cfg.ProxyCaddyAPI,
			HostsFile:  cfg.ProxyHostsFile,
			CADir:      cfg.ProxyCADir,
			TLSCertDir: cfg.ProxyTLSCertDir,
			ProxyIP:    cfg.ProxyIP,
		}
		caddyPub, err := proxy.NewCaddyPublisher(proxyCfg, log.Default())
		if err != nil {
			log.Printf("warning: caddy proxy init failed, using tailscale only: %v", err)
		} else {
			caddyAdapter := NewCaddyProxyPublisher(caddyPub)
			if exposurePublisher == nil {
				exposurePublisher = caddyAdapter
			} else {
				exposurePublisher = NewMultiPublisher(log.Default(), exposurePublisher, caddyAdapter)
			}
			log.Printf("using caddy proxy publisher (domain=%s, tls=%s)", cfg.ProxyDomain, tlsMode)
		}
	}

	if exposurePublisher == nil {
		// No publisher available (offline mode without proxy_enabled).
		exposurePublisher = &noopPublisher{}
		log.Printf("warning: no exposure publisher available (offline mode without proxy)")
	}

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
		jobOrchestrator.WithProvisionTimeout(cfg.ProvisioningTimeout)
		sandboxManager.WithSnippetCleaner(jobOrchestrator.CleanupSnippet)
	}

	localMux := http.NewServeMux()
	localMux.HandleFunc("/healthz", healthHandler)

	// Create resource pool for sandbox over-commit tracking.
	resourcePool := pool.New(pool.Config{
		TotalCores:       cfg.PoolTotalCores,
		TotalMemoryMB:    cfg.PoolTotalMemoryMB,
		CPUOverCommit:    cfg.PoolCPUOverCommit,
		MemoryOverCommit: cfg.PoolMemOverCommit,
		BurstDuration:    cfg.PoolBurstDuration,
	})
	if resourcePool.IsEnabled() {
		log.Printf("resource pool enabled: %d cores, %d MB RAM, cpu_over_commit=%.1f, mem_over_commit=%.1f",
			cfg.PoolTotalCores, cfg.PoolTotalMemoryMB, resourcePool.Status().Config.CPUOverCommit, resourcePool.Status().Config.MemoryOverCommit)
	}

	controlAPI := NewControlAPI(store, profiles, sandboxManager, workspaceManager, jobOrchestrator, cfg.ArtifactDir, log.Default()).
		WithBackend(backend).
		WithMetrics(metrics).
		WithMetricsEnabled(metrics != nil).
		WithExposurePublisher(exposurePublisher).
		WithRedactor(redactor).
		WithSkillBundle(cfg.ClaudeSkillBundleName, cfg.ClaudeSkillBundleVersion).
		WithAgentSubnet(agentCIDR).
		WithTailscaleStatus(defaultTailscaleDNSName).
		WithTailscalePeerInventory(defaultTailscalePeerInventory).
		WithResourcePool(resourcePool)
	controlAPI.Register(localMux)

	// Register pool status endpoint.
	NewPoolAPI(resourcePool).Register(localMux)

	// Build the secrets store once and share it between the agent-drivable
	// control-plane SecretsAPI (registered here on localMux) and the
	// bootstrap/metadata APIs further down. AllowPlaintext stays false
	// server-side so staged keys are always written age-encrypted at rest.
	secretsStore := secrets.Store{
		Dir:        cfg.SecretsDir,
		AgeKeyPath: cfg.SecretsAgeKeyPath,
		SopsPath:   cfg.SecretsSopsPath,
	}
	NewSecretsAPI(secretsStore, cfg.SecretsBundle, redactor, log.Default()).Register(localMux)

	// Set up integrations system if enabled.
	var integrationStore *integrations.Store
	if cfg.IntegrationsEnabled {
		var encKey []byte
		if cfg.IntegrationEncKey != "" {
			var keyErr error
			encKey, keyErr = integrations.ParseEncryptionKeyHex(cfg.IntegrationEncKey)
			if keyErr != nil {
				_ = metricsListener.Close()
				_ = artifactListener.Close()
				_ = bootstrapListener.Close()
				_ = unixListener.Close()
				return nil, fmt.Errorf("parse integration_enc_key: %w", keyErr)
			}
		} else {
			var keyErr error
			encKey, keyErr = integrations.GenerateEncryptionKey()
			if keyErr != nil {
				_ = metricsListener.Close()
				_ = artifactListener.Close()
				_ = bootstrapListener.Close()
				_ = unixListener.Close()
				return nil, fmt.Errorf("generate integration encryption key: %w", keyErr)
			}
			log.Printf("warning: integration_enc_key not set; generated ephemeral key (integrations will not survive restart)")
		}
		var storeErr error
		integrationStore, storeErr = integrations.NewStore(store, encKey)
		if storeErr != nil {
			_ = metricsListener.Close()
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("create integration store: %w", storeErr)
		}
		integrationAPI := NewIntegrationAPI(integrationStore, log.Default()).
			WithTargetAllowlist(cfg.IntegrationTargetAllowlist)
		integrationAPI.Register(localMux)
		log.Printf("integrations system enabled")
	}

	// Set up multi-user support via SSH keys.
	userStore := user.NewStore(store)
	userRegistry := user.NewRegistry(userStore)
	userAPI := NewUserAPI(userRegistry)
	userAPI.Register(localMux)
	log.Printf("multi-user support enabled")

	// Register POST /v1/exec and /v1/exec/dry-run endpoints.
	// These mirror the CLI 1:1 over HTTPS (the "SSH API shoved into a POST body").
	cliPath := strings.TrimSpace(cfg.CLIPath)
	if cliPath != "" {
		execAPI := api.NewExecAPI(cliPath, cfg.SocketPath, log.Default())
		execAPI.Register(localMux)
		log.Printf("exec API enabled (cli=%s)", cliPath)
	} else if cliPath, err := api.ResolveCLIPath(""); err == nil {
		execAPI := api.NewExecAPI(cliPath, cfg.SocketPath, log.Default())
		execAPI.Register(localMux)
		log.Printf("exec API enabled (cli=%s, auto-detected)", cliPath)
	}

	bootstrapMux := http.NewServeMux()
	bootstrapMux.HandleFunc("/healthz", healthHandler)
	artifactEndpoint := strings.TrimSpace(cfg.ArtifactUploadURL)
	if artifactEndpoint == "" {
		artifactEndpoint = buildArtifactUploadURL(cfg.ArtifactListen)
	}
	bootstrapLimiter := NewIPRateLimiter(cfg.BootstrapRateLimitQPS, cfg.BootstrapRateLimitBurst)
	artifactLimiter := NewIPRateLimiter(cfg.ArtifactRateLimitQPS, cfg.ArtifactRateLimitBurst)
	NewBootstrapAPI(store, profiles, secretsStore, cfg.SecretsBundle, agentSubnet, artifactEndpoint, time.Duration(cfg.ArtifactTokenTTLMinutes)*time.Minute, redactor, bootstrapLimiter).Register(bootstrapMux)
	NewRunnerAPI(jobOrchestrator, agentSubnet).Register(bootstrapMux)
	NewMetadataAPI(store, secretsStore, cfg.SecretsBundle, agentSubnet, bootstrapLimiter, log.Default()).Register(bootstrapMux)

	// Register integration proxy routes on bootstrap mux so sandboxes can
	// access integrations through http://169.254.169.254/proxy/{name}/...
	if integrationStore != nil {
		NewIntegrationProxyAPI(integrationStore, store, agentSubnet, bootstrapLimiter, log.Default(), cfg.Offline, cfg.TrustAgentSubnet).Register(bootstrapMux)
	}

	artifactMux := http.NewServeMux()
	artifactMux.HandleFunc("/healthz", healthHandler)
	NewArtifactAPI(store, cfg.ArtifactDir, cfg.ArtifactMaxBytes, agentSubnet, artifactLimiter).Register(artifactMux)

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
	var controlServer *http.Server
	if controlListener != nil {
		// Use the new auth middleware that supports SSH key tokens
		// alongside legacy bearer tokens.
		authMw, err := auth.NewMiddleware(auth.MiddlewareConfig{
			AuthorizedKeysPath: cfg.AuthorizedKeysPath,
			LegacyToken:        cfg.ControlAuthToken,
			AllowCIDRs:         cfg.ControlAllowCIDRs,
		})
		if err != nil {
			if metricsListener != nil {
				_ = metricsListener.Close()
			}
			_ = controlListener.Close()
			_ = artifactListener.Close()
			_ = bootstrapListener.Close()
			_ = unixListener.Close()
			return nil, fmt.Errorf("control auth setup: %w", err)
		}
		controlServer = &http.Server{
			// WrapNetwork (not Wrap): the TCP control listener is the network
			// trust boundary. It authenticates only; authorization is
			// per-route. ControlAPI handlers call authorize, the standalone
			// APIs (secrets, integrations, users, pool) call
			// authorizeStandalone, and /v1/exec calls execAllowed. Scoped SSH
			// tokens pass authentication and are then confined per route. The
			// local Unix socket uses localMux directly and remains a trusted
			// full-access path.
			Handler:           authMw.WrapNetwork(localMux),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		if authMw.KeyStore() != nil {
			log.Printf("SSH key authentication enabled (%d keys loaded from %s)", authMw.KeyStore().Count(), cfg.AuthorizedKeysPath)
		}
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

	// Optionally set up metadata routing via iptables DNAT for 169.254.169.254.
	var metadataRouting *MetadataRouting
	if cfg.MetadataRoutingEnabled {
		metadataRouting = NewMetadataRouting(cfg.BootstrapListen)
		if err := metadataRouting.Setup(); err != nil {
			log.Printf("warning: metadata routing setup failed (need root?): %v", err)
			metadataRouting = nil
		}
	}

	s := &Service{
		cfg:               cfg,
		profiles:          profiles,
		store:             store,
		unixListener:      unixListener,
		controlListener:   controlListener,
		bootstrapListener: bootstrapListener,
		artifactListener:  artifactListener,
		metricsListener:   metricsListener,
		unixServer:        unixServer,
		controlServer:     controlServer,
		bootstrapServer:   bootstrapServer,
		artifactServer:    artifactServer,
		metricsServer:     metricsServer,
		sandboxManager:    sandboxManager,
		workspaceManager:  workspaceManager,
		artifactGC:        artifactGC,
		idleStopper:       idleStopper,
		metrics:           metrics,
		metadataRouting:   metadataRouting,
		lxcBackend:        lxcBackend,
		sandboxBackend:    sbBackend,
		integrationStore:  integrationStore,
		userRegistry:      userRegistry,
		resourcePool:      resourcePool,
	}
	// Wire the daemon lifecycle runner into components that spawn detached work
	// or run synchronous provisioning, so that work is cancelled and awaited at
	// shutdown (review H2). At construction time the lifecycle context/tracker
	// are nil; Serve populates them before accepting requests.
	if jobOrchestrator != nil {
		jobOrchestrator.WithBackgroundRunner(s)
		jobOrchestrator.WithResourcePool(resourcePool)
	}
	if controlAPI != nil {
		controlAPI.WithBackgroundRunner(s)
	}
	return s, nil
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
	// Establish a lifecycle context cancelled at shutdown and a task tracker so
	// detached work can be awaited (or timed out) before the store closes. The
	// lifecycle context is a child of the caller's ctx, so external cancellation
	// (SIGTERM) still propagates (review H2).
	lifecycleCtx, lifecycleCancel := context.WithCancel(ctx)
	s.lifecycleCtx = lifecycleCtx
	s.lifecycleCancel = lifecycleCancel
	s.tasks = &taskTracker{}

	serverCount := 3
	if s.controlServer != nil {
		serverCount++
	}
	if s.metricsServer != nil {
		serverCount++
	}
	log.Printf("agentlabd: listening on unix=%s", s.cfg.SocketPath)
	if s.controlServer != nil {
		log.Printf("agentlabd: listening on control=%s", s.cfg.ControlListen)
	}
	log.Printf("agentlabd: listening on bootstrap=%s", s.cfg.BootstrapListen)
	log.Printf("agentlabd: listening on artifacts=%s", s.cfg.ArtifactListen)
	if s.metricsServer != nil {
		log.Printf("agentlabd: listening on metrics=%s", s.cfg.MetricsListen)
	}
	if s.resourcePool != nil && s.resourcePool.IsEnabled() {
		// Rebuild in-memory pool accounting from live sandbox rows so a restart
		// does not silently drop capacity enforcement (review H3).
		if n, err := ReconstructPool(lifecycleCtx, s.resourcePool, s.store, s.profiles); err != nil {
			log.Printf("agentlabd: pool reconstruction failed: %v", err)
		} else if n > 0 {
			log.Printf("agentlabd: reconstructed %d pool allocation(s) from live sandboxes", n)
		}
	}
	if s.sandboxManager != nil {
		// Recover any orphans left by a prior crash before accepting work, so a
		// SIGKILL during provisioning cannot strand a sandbox in a transient
		// state forever (review H2).
		s.sandboxManager.SweepStartupOrphans(lifecycleCtx, s.cfg.OrphanGracePeriodOrDefault())
		s.sandboxManager.StartLeaseGC(lifecycleCtx)
		s.sandboxManager.StartReconciler(lifecycleCtx)
	}
	if s.idleStopper != nil {
		s.idleStopper.Start(lifecycleCtx)
	}
	if s.artifactGC != nil {
		s.artifactGC.Start(lifecycleCtx)
	}
	if s.resourcePool != nil && s.resourcePool.IsEnabled() {
		s.startPoolReclaimer(lifecycleCtx)
	}

	errCh := make(chan error, serverCount)
	go func() { errCh <- s.unixServer.Serve(s.unixListener) }()
	if s.controlServer != nil {
		go func() { errCh <- s.controlServer.Serve(s.controlListener) }()
	}
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

// ControlAddr returns the bound control listener address (host:port).
// Returns empty string if the control listener is not enabled.
func (s *Service) ControlAddr() string {
	if s == nil || s.controlListener == nil {
		return ""
	}
	return s.controlListener.Addr().String()
}

// startPoolReclaimer runs periodic reclamation of expired burst allocations.
func (s *Service) startPoolReclaimer(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Destroy the VM first, then release accounting only on success,
				// so a failed destroy cannot drop capacity accounting while the
				// VM still exists (review H3).
				expired := s.resourcePool.PeekExpiredBurst()
				for _, id := range expired {
					if s.sandboxManager != nil {
						if err := s.sandboxManager.ForceDestroy(ctx, id); err != nil {
							log.Printf("resource pool: failed to destroy expired sandbox %d: %v", id, err)
							continue
						}
					}
					s.resourcePool.Release(id)
					log.Printf("resource pool: reclaimed burst allocation for sandbox %d (expired)", id)
				}
			}
		}
	}()
}

func (s *Service) shutdown() {
	// Ordered shutdown (review H2):
	//   1. Close registration of new background work so Wait cannot race Add.
	//   2. Cancel the lifecycle context, signalling in-flight detached work
	//      (including synchronous provisioning running under the lifecycle
	//      context inside an HTTP handler) to abort.
	//   3. Stop the HTTP servers with a bounded grace period. Handlers blocked
	//      on lifecycle-cancelled work return here.
	//   4. Wait for the remaining tracked workers to finish (bounded), while the
	//      store is still open so their cleanup writes succeed.
	//   5. Close the store last.
	if s.tasks != nil {
		s.tasks.closeRegistration()
	}
	if s.lifecycleCancel != nil {
		s.lifecycleCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = s.unixServer.Shutdown(ctx)
	if s.controlServer != nil {
		_ = s.controlServer.Shutdown(ctx)
	}
	_ = s.bootstrapServer.Shutdown(ctx)
	_ = s.artifactServer.Shutdown(ctx)
	if s.metricsServer != nil {
		_ = s.metricsServer.Shutdown(ctx)
	}
	if s.tasks != nil {
		if !s.tasks.wait(shutdownTimeout) {
			log.Printf("agentlabd: shutdown timed out waiting for %d background task(s)", s.tasks.count())
		}
	}
	if s.metadataRouting != nil {
		s.metadataRouting.Cleanup()
	}
	if s.store != nil {
		_ = s.store.Close()
	}
}

// Go runs fn in a tracked goroutine under the daemon lifecycle context, so the
// work is cancelled and awaited at shutdown. It returns false when the daemon
// is shutting down, in which case fn is not invoked (review H2).
func (s *Service) Go(name string, fn func(ctx context.Context)) bool {
	if s == nil {
		go fn(context.Background())
		return true
	}
	if s.tasks == nil {
		// Lifecycle not initialized (e.g., direct Service use in tests); run
		// detached so behavior matches the pre-H2 code path.
		go fn(s.LifecycleContext())
		return true
	}
	done, ok := s.tasks.register(name)
	if !ok {
		return false
	}
	go func() {
		defer done()
		fn(s.lifecycleCtx)
	}()
	return true
}

// LifecycleContext returns the daemon lifecycle context (cancelled at shutdown,
// outliving any single HTTP request). Returns context.Background() when no
// lifecycle has been established.
func (s *Service) LifecycleContext() context.Context {
	if s == nil || s.lifecycleCtx == nil {
		return context.Background()
	}
	return s.lifecycleCtx
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
