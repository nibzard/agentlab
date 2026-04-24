//go:build sshgateway
// +build sshgateway

// ABOUTME: SSH gateway that provides remote access to the agentlab daemon API.
// ABOUTME: Supports CLI command execution (ssh host sandbox list --json) and
// ABOUTME: interactive sandbox proxy sessions (ssh new@host).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultListenAddr        = "0.0.0.0:2222"
	defaultSocketPath        = "/run/agentlab/agentlabd.sock"
	defaultProfile           = "yolo-ephemeral"
	defaultSandboxUser       = "agent"
	defaultSandboxPort       = 22
	defaultWaitTimeout       = 4 * time.Minute
	defaultPollInterval      = 2 * time.Second
	defaultIdleTimeout       = 5 * time.Minute
	defaultKeepaliveInterval = 30 * time.Second
)

// gatewayConfig holds all configuration for the SSH gateway.
type gatewayConfig struct {
	listenAddr        string
	socketPath        string
	authorizedKeys    string
	hostKeyPath       string
	sandboxKeyPath    string
	sandboxUser       string
	sandboxPort       int
	defaultProfile    string
	keepalive         bool
	waitTimeout       time.Duration
	pollInterval      time.Duration
	cliPath           string
	idleTimeout       time.Duration
	keepaliveInterval time.Duration
}

// routeTarget describes where to route a proxy-mode SSH session.
type routeTarget struct {
	vmid    int
	profile string
	isNew   bool
}

// server tracks active sessions and shared resources.
type server struct {
	cfg           gatewayConfig
	allowedKeys   map[string]bool
	hostSigner    ssh.Signer
	sandboxSigner ssh.Signer
	client        *apiClient
	logger        *log.Logger

	mu       sync.Mutex
	conns    map[*ssh.ServerConn]struct{}
	sessions atomic.Int64
}

func main() {
	cfg := gatewayConfig{}
	flag.StringVar(&cfg.listenAddr, "listen", defaultListenAddr, "listen address for SSH gateway")
	flag.StringVar(&cfg.socketPath, "socket", defaultSocketPath, "agentlabd unix socket path")
	flag.StringVar(&cfg.authorizedKeys, "authorized-keys", "/etc/agentlab/keys/ssh_gateway_authorized_keys", "authorized_keys file for gateway access")
	flag.StringVar(&cfg.hostKeyPath, "host-key", "/etc/agentlab/keys/ssh_gateway_host_ed25519", "host private key for SSH server")
	flag.StringVar(&cfg.sandboxKeyPath, "sandbox-key", "/etc/agentlab/keys/agentlab_id_ed25519", "SSH private key used to reach sandboxes")
	flag.StringVar(&cfg.sandboxUser, "sandbox-user", defaultSandboxUser, "SSH user for sandbox connections")
	flag.IntVar(&cfg.sandboxPort, "sandbox-port", defaultSandboxPort, "SSH port for sandbox connections")
	flag.StringVar(&cfg.defaultProfile, "profile", defaultProfile, "default profile for new sandboxes")
	flag.BoolVar(&cfg.keepalive, "keepalive", true, "set keepalive=true for newly created sandboxes")
	flag.DurationVar(&cfg.waitTimeout, "wait-timeout", defaultWaitTimeout, "timeout for sandbox provisioning/SSH readiness")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", defaultPollInterval, "poll interval for sandbox readiness")
	flag.StringVar(&cfg.cliPath, "cli-path", "", "path to agentlab CLI binary (auto-detected if empty)")
	flag.DurationVar(&cfg.idleTimeout, "idle-timeout", defaultIdleTimeout, "idle timeout for SSH connections")
	flag.DurationVar(&cfg.keepaliveInterval, "keepalive-interval", defaultKeepaliveInterval, "SSH keepalive interval")
	flag.Parse()

	logger := log.New(os.Stdout, "ssh-gateway: ", log.LstdFlags)

	allowedKeys, err := loadAuthorizedKeys(cfg.authorizedKeys)
	if err != nil {
		logger.Fatalf("load authorized keys: %v", err)
	}
	if len(allowedKeys) == 0 {
		logger.Fatalf("authorized keys file %s contained no usable keys", cfg.authorizedKeys)
	}

	hostSigner, err := loadHostSigner(cfg.hostKeyPath, logger)
	if err != nil {
		logger.Fatalf("load host key: %v", err)
	}

	sandboxSigner, err := loadPrivateKey(cfg.sandboxKeyPath)
	if err != nil {
		logger.Fatalf("load sandbox key: %v", err)
	}

	cliPath, err := resolveCLIPath(cfg.cliPath)
	if err != nil {
		logger.Fatalf("resolve agentlab CLI: %v", err)
	}
	cfg.cliPath = cliPath
	logger.Printf("using agentlab CLI at %s", cliPath)

	client := newAPIClient(cfg.socketPath, cfg.waitTimeout)

	srv := &server{
		cfg:           cfg,
		allowedKeys:   allowedKeys,
		hostSigner:    hostSigner,
		sandboxSigner: sandboxSigner,
		client:        client,
		logger:        logger,
		conns:         make(map[*ssh.ServerConn]struct{}),
	}

	listener, err := net.Listen("tcp", cfg.listenAddr)
	if err != nil {
		logger.Fatalf("listen %s: %v", cfg.listenAddr, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Printf("shutting down...")
		listener.Close()
		srv.closeAll()
	}()

	logger.Printf("listening on %s (idle-timeout=%s, keepalive=%s)", cfg.listenAddr, cfg.idleTimeout, cfg.keepaliveInterval)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			logger.Printf("accept: %v", err)
			continue
		}
		go srv.handleConnection(conn)
	}

	srv.drain()
	logger.Printf("gateway stopped")
}

func (s *server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn := range s.conns {
		conn.Close()
	}
}

func (s *server) drain() {
	for {
		n := s.sessions.Load()
		if n == 0 {
			return
		}
		s.logger.Printf("waiting for %d active sessions...", n)
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *server) trackConn(conn *ssh.ServerConn) func() {
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	s.sessions.Add(1)
	return func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.sessions.Add(-1)
	}
}

// handleConnection handles an incoming TCP connection, performing SSH
// handshake and authentication before dispatching sessions.
func (s *server) handleConnection(conn net.Conn) {
	defer conn.Close()
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fp := ssh.FingerprintSHA256(key)
			if s.allowedKeys[fp] {
				return &ssh.Permissions{Extensions: map[string]string{"fingerprint": fp}}, nil
			}
			return nil, fmt.Errorf("unauthorized key %s", fp)
		},
	}
	serverConfig.AddHostKey(s.hostSigner)

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		s.logger.Printf("handshake failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	untrack := s.trackConn(sshConn)
	defer untrack()
	defer sshConn.Close()

	// Send keepalive requests to detect dead connections.
	go s.sendKeepalives(sshConn, reqs)

	username := sshConn.User()
	s.logger.Printf("connection from %s as %q (fp=%s)", conn.RemoteAddr(), username, sshConn.Permissions.Extensions["fingerprint"])

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		go s.handleSession(newChannel, username)
	}
}

func (s *server) sendKeepalives(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	ticker := time.NewTicker(s.cfg.keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _, err := conn.SendRequest("keepalive@golang.org", true, nil)
			if err != nil {
				return
			}
		case req := <-reqs:
			if req == nil {
				return
			}
			// Discard global requests but respond to keepalive.
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// handleSession dispatches an SSH session channel to the appropriate handler.
//
// Two modes:
//  1. CLI exec mode: When the client sends an exec request with a CLI command
//     (e.g., "sandbox list --json"), execute it via the agentlab binary.
//  2. Proxy mode: When the client requests an interactive shell (or exec with a
//     sandbox routing command like "new" or "sbx-123"), proxy the session to a sandbox.
func (s *server) handleSession(newChannel ssh.NewChannel, username string) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		s.logger.Printf("accept channel: %v", err)
		return
	}
	defer channel.Close()

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.waitTimeout)
	defer cancel()

	// Collect PTY and env requests for proxy mode.
	var (
		ptyReq      *ptyRequest
		envRequests []envRequest
		determined  bool
	)

	for req := range requests {
		switch req.Type {
		case "pty-req":
			var pty ptyRequest
			if err := ssh.Unmarshal(req.Payload, &pty); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			ptyReq = &pty
			_ = req.Reply(true, nil)

		case "env":
			var env envRequest
			if err := ssh.Unmarshal(req.Payload, &env); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			envRequests = append(envRequests, env)
			_ = req.Reply(true, nil)

		case "exec":
			var execReq execRequest
			if err := ssh.Unmarshal(req.Payload, &execReq); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			determined = true
			cmd := strings.TrimSpace(execReq.Command)
			if cmd == "" {
				s.handleProxySession(ctx, channel, username, ptyReq, envRequests)
			} else if isCLICommand(cmd) {
				s.handleCLIExec(ctx, channel, cmd)
			} else {
				// Check if it's a sandbox routing shortcut.
				route, err := parseRoute(cmd, s.cfg.defaultProfile)
				if err != nil {
					// Unknown command; try CLI as fallback.
					s.handleCLIExec(ctx, channel, cmd)
				} else {
					s.handleProxySessionWithRoute(ctx, channel, route, ptyReq, envRequests)
				}
			}
			return

		case "shell":
			_ = req.Reply(true, nil)
			determined = true
			s.handleProxySession(ctx, channel, username, ptyReq, envRequests)
			return

		case "window-change":
			// Forwarded inside proxy session if active.
			_ = req.Reply(true, nil)

		case "subsystem":
			_ = req.Reply(false, nil)

		default:
			_ = req.Reply(false, nil)
		}
	}

	if !determined {
		writeSessionError(channel, "session ended without shell or exec request")
	}
}

// --- CLI exec mode ---

// isCLICommand checks whether a command string looks like an agentlab CLI command
// (as opposed to a sandbox routing shortcut like "new" or "sbx-123").
func isCLICommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}
	first := parts[0]

	// Known agentlab top-level commands.
	cliCommands := map[string]bool{
		"status": true, "schema": true, "init": true, "bootstrap": true,
		"job": true, "sandbox": true, "workspace": true, "session": true,
		"profile": true, "secrets": true, "msg": true, "ssh": true,
		"logs": true, "connect": true, "disconnect": true,
	}

	if cliCommands[first] {
		return true
	}
	// If it starts with --, it's a global flag followed by a command.
	if strings.HasPrefix(first, "-") {
		return true
	}
	return false
}

// handleCLIExec executes an agentlab CLI command and streams output to the SSH channel.
func (s *server) handleCLIExec(ctx context.Context, channel ssh.Channel, cmd string) {
	args := buildCLIArgs(s.cfg.socketPath, cmd)

	cmdCtx, cancel := context.WithTimeout(ctx, s.cfg.waitTimeout)
	defer cancel()

	s.logger.Printf("exec: agentlab %s", strings.Join(args[2:], " "))

	proc := exec.CommandContext(cmdCtx, s.cfg.cliPath, args...)
	proc.Stdin = channel
	proc.Stdout = channel
	proc.Stderr = channel.Stderr()

	exitCode := 0
	if err := proc.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				exitCode = 1
			}
		} else if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(channel.Stderr(), "agentlab: command timed out\n")
			exitCode = 124
		} else {
			fmt.Fprintf(channel.Stderr(), "agentlab: %v\n", err)
			exitCode = 1
		}
	}

	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: uint32(exitCode)}))
}

// buildCLIArgs constructs the CLI arguments for an agentlab command execution.
// It prepends the --socket flag to ensure the CLI talks to the right daemon.
func buildCLIArgs(socketPath, cmd string) []string {
	args := []string{"--socket", socketPath}
	args = append(args, strings.Fields(cmd)...)
	return args
}

// --- Proxy mode (sandbox create/connect) ---

// handleProxySession parses the username to determine a sandbox route and proxies the session.
func (s *server) handleProxySession(ctx context.Context, channel ssh.Channel, username string, ptyReq *ptyRequest, envRequests []envRequest) {
	route, err := parseRoute(username, s.cfg.defaultProfile)
	if err != nil {
		writeSessionError(channel, err.Error())
		return
	}
	s.handleProxySessionWithRoute(ctx, channel, route, ptyReq, envRequests)
}

// handleProxySessionWithRoute proxies an SSH session to a sandbox VM identified by route.
func (s *server) handleProxySessionWithRoute(ctx context.Context, channel ssh.Channel, route routeTarget, ptyReq *ptyRequest, envRequests []envRequest) {
	var (
		remoteClient  *ssh.Client
		remoteSession *ssh.Session
		once          sync.Once
		closed        bool
	)

	closeSession := func(status uint32) {
		once.Do(func() {
			if !closed {
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: status}))
				closed = true
			}
			_ = channel.Close()
		})
	}

	ensureRemote := func() error {
		if remoteSession != nil {
			return nil
		}
		if route.isNew {
			fmt.Fprintf(channel, "agentlab: creating sandbox (profile=%s)\n", route.profile)
		}
		sandbox, err := resolveSandbox(ctx, s.client, route, s.cfg)
		if err != nil {
			return err
		}
		if route.isNew {
			fmt.Fprintf(channel, "agentlab: sandbox %d ready (%s)\n", sandbox.VMID, sandbox.IP)
		}
		remoteClient, err = dialSandbox(ctx, sandbox.IP, s.cfg.sandboxPort, s.cfg.sandboxUser, s.sandboxSigner)
		if err != nil {
			return err
		}
		remoteSession, err = remoteClient.NewSession()
		if err != nil {
			return err
		}
		remoteSession.Stdout = channel
		remoteSession.Stderr = channel.Stderr()
		remoteSession.Stdin = channel
		if ptyReq != nil {
			_ = remoteSession.RequestPty(ptyReq.Term, int(ptyReq.Rows), int(ptyReq.Cols), ssh.TerminalModes{})
		}
		for _, env := range envRequests {
			_ = remoteSession.Setenv(env.Name, env.Value)
		}
		return nil
	}

	waitRemote := func() {
		if remoteSession == nil {
			return
		}
		err := remoteSession.Wait()
		status := exitStatus(err)
		closeSession(status)
		if remoteClient != nil {
			_ = remoteClient.Close()
		}
	}

	if err := ensureRemote(); err != nil {
		writeSessionError(channel, fmt.Sprintf("gateway error: %v", err))
		closeSession(1)
		return
	}
	if err := remoteSession.Shell(); err != nil {
		writeSessionError(channel, fmt.Sprintf("remote shell failed: %v", err))
		closeSession(1)
		return
	}
	go waitRemote()

	// Block until channel closes (EOF from client).
	buf := make([]byte, 1)
	for {
		if _, err := channel.Read(buf); err != nil {
			break
		}
	}
}

// --- SSH request types ---

type ptyRequest struct {
	Term   string
	Cols   uint32
	Rows   uint32
	Width  uint32
	Height uint32
	Modes  []byte
}

type windowChangeRequest struct {
	Cols   uint32
	Rows   uint32
	Width  uint32
	Height uint32
}

type execRequest struct {
	Command string
}

type envRequest struct {
	Name  string
	Value string
}

func writeSessionError(channel ssh.Channel, message string) {
	_, _ = fmt.Fprintf(channel, "agentlab: %s\n", message)
}

// --- Route parsing (sandbox proxy mode) ---

func parseRoute(username, defaultProfile string) (routeTarget, error) {
	user := strings.TrimSpace(username)
	if user == "" {
		return routeTarget{}, fmt.Errorf("empty username")
	}
	if user == "new" {
		return routeTarget{profile: defaultProfile, isNew: true}, nil
	}
	for _, prefix := range []string{"new+", "new:", "new-"} {
		if strings.HasPrefix(user, prefix) {
			profile := strings.TrimSpace(strings.TrimPrefix(user, prefix))
			if profile == "" {
				return routeTarget{}, fmt.Errorf("missing profile after %s", prefix)
			}
			return routeTarget{profile: profile, isNew: true}, nil
		}
	}
	if strings.HasPrefix(user, "sbx-") {
		id := strings.TrimPrefix(user, "sbx-")
		vmid, err := strconv.Atoi(id)
		if err != nil || vmid <= 0 {
			return routeTarget{}, fmt.Errorf("invalid vmid %q", id)
		}
		return routeTarget{vmid: vmid}, nil
	}
	vmid, err := strconv.Atoi(user)
	if err == nil && vmid > 0 {
		return routeTarget{vmid: vmid}, nil
	}
	return routeTarget{}, fmt.Errorf("unsupported username; use new, new+profile, sbx-<id>, <id>, or send a CLI command")
}

// --- Sandbox resolution ---

func resolveSandbox(ctx context.Context, client *apiClient, route routeTarget, cfg gatewayConfig) (sandboxResponse, error) {
	var sandbox sandboxResponse
	var err error
	if route.isNew {
		sandbox, err = createSandbox(ctx, client, route.profile, cfg.keepalive)
		if err != nil {
			return sandboxResponse{}, err
		}
	} else {
		sandbox, err = fetchSandbox(ctx, client, route.vmid)
		if err != nil {
			return sandboxResponse{}, err
		}
	}
	if strings.EqualFold(sandbox.State, "STOPPED") {
		sandbox, err = startSandbox(ctx, client, sandbox.VMID)
		if err != nil {
			return sandboxResponse{}, err
		}
	}
	if strings.TrimSpace(sandbox.IP) == "" {
		sandbox, err = waitForSandboxIP(ctx, client, sandbox.VMID, cfg.pollInterval)
		if err != nil {
			return sandboxResponse{}, err
		}
	}
	return sandbox, nil
}

func waitForSandboxIP(ctx context.Context, client *apiClient, vmid int, interval time.Duration) (sandboxResponse, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		sandbox, err := fetchSandbox(ctx, client, vmid)
		if err != nil {
			return sandboxResponse{}, err
		}
		if strings.TrimSpace(sandbox.IP) != "" {
			return sandbox, nil
		}
		select {
		case <-ctx.Done():
			return sandboxResponse{}, fmt.Errorf("timeout waiting for sandbox %d IP", vmid)
		case <-ticker.C:
		}
	}
}

func dialSandbox(ctx context.Context, ip string, port int, user string, signer ssh.Signer) (*ssh.Client, error) {
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	for {
		client, err := ssh.Dial("tcp", address, config)
		if err == nil {
			return client, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("dial sandbox %s: %w", address, err)
		case <-time.After(2 * time.Second):
		}
	}
}

func exitStatus(err error) uint32 {
	if err == nil {
		return 0
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return uint32(exitErr.ExitStatus())
	}
	return 1
}

// --- Key management ---

func loadAuthorizedKeys(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("parse authorized key: %w", err)
		}
		allowed[ssh.FingerprintSHA256(pub)] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return allowed, nil
}

func loadHostSigner(path string, logger *log.Logger) (ssh.Signer, error) {
	if path == "" {
		return generateHostSigner(logger), nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	logger.Printf("host key %s not found; generating ephemeral key", path)
	return generateHostSigner(logger), nil
}

func generateHostSigner(logger *log.Logger) ssh.Signer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		logger.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		logger.Fatalf("create host signer: %v", err)
	}
	return signer
}

func loadPrivateKey(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, fmt.Errorf("sandbox key path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// resolveCLIPath finds the agentlab CLI binary.
func resolveCLIPath(explicit string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("cli-path %s: %w", explicit, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("cli-path %s is a directory", explicit)
		}
		return explicit, nil
	}
	path, err := exec.LookPath("agentlab")
	if err != nil {
		return "", fmt.Errorf("agentlab binary not found in PATH; set --cli-path explicitly")
	}
	return path, nil
}

// --- Minimal agentlabd API client (shared, pooled) ---

type apiClient struct {
	socketPath string
	httpClient *http.Client
	timeout    time.Duration
}

type apiError struct {
	Error string `json:"error"`
}

type sandboxCreateRequest struct {
	Name      string `json:"name,omitempty"`
	Profile   string `json:"profile"`
	Keepalive *bool  `json:"keepalive,omitempty"`
}

type sandboxResponse struct {
	VMID    int    `json:"vmid"`
	Name    string `json:"name"`
	Profile string `json:"profile"`
	State   string `json:"state"`
	IP      string `json:"ip,omitempty"`
}

func newAPIClient(socketPath string, timeout time.Duration) *apiClient {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	// Use connection pooling via shared transport.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	return &apiClient{
		socketPath: socketPath,
		httpClient: &http.Client{Transport: transport, Timeout: timeout},
		timeout:    timeout,
	}
}

func (c *apiClient) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 400 {
		return data, nil
	}
	var apiErr apiError
	if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Error != "" {
		return nil, fmt.Errorf("api error: %s", apiErr.Error)
	}
	return nil, fmt.Errorf("api error: status %d", resp.StatusCode)
}

func createSandbox(ctx context.Context, client *apiClient, profile string, keepalive bool) (sandboxResponse, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return sandboxResponse{}, fmt.Errorf("profile is required")
	}
	req := sandboxCreateRequest{Profile: profile, Keepalive: &keepalive}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes", req)
	if err != nil {
		return sandboxResponse{}, err
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sandboxResponse{}, err
	}
	return resp, nil
}

func fetchSandbox(ctx context.Context, client *apiClient, vmid int) (sandboxResponse, error) {
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
	if err != nil {
		return sandboxResponse{}, err
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sandboxResponse{}, err
	}
	return resp, nil
}

func startSandbox(ctx context.Context, client *apiClient, vmid int) (sandboxResponse, error) {
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/start", vmid), nil)
	if err != nil {
		return sandboxResponse{}, err
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sandboxResponse{}, err
	}
	return resp, nil
}
