// ABOUTME: Minimal SSH gateway prototype that routes usernames to sandbox create/connect.
// ABOUTME: Authenticates users via authorized_keys and proxies SSH sessions to sandboxes via agentlabd.
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
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultListenAddr   = "0.0.0.0:2222"
	defaultSocketPath   = "/run/agentlab/agentlabd.sock"
	defaultProfile      = "yolo-ephemeral"
	defaultSandboxUser  = "agent"
	defaultSandboxPort  = 22
	defaultWaitTimeout  = 4 * time.Minute
	defaultPollInterval = 2 * time.Second
)

type gatewayConfig struct {
	listenAddr     string
	socketPath     string
	authorizedKeys string
	hostKeyPath    string
	sandboxKeyPath string
	sandboxUser    string
	sandboxPort    int
	defaultProfile string
	keepalive      bool
	waitTimeout    time.Duration
	pollInterval   time.Duration
}

type routeTarget struct {
	vmid    int
	profile string
	isNew   bool
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

	listener, err := net.Listen("tcp", cfg.listenAddr)
	if err != nil {
		logger.Fatalf("listen %s: %v", cfg.listenAddr, err)
	}
	defer listener.Close()

	client := newAPIClient(cfg.socketPath, cfg.waitTimeout)
	logger.Printf("listening on %s", cfg.listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Printf("accept: %v", err)
			continue
		}
		go handleConnection(conn, cfg, allowedKeys, hostSigner, sandboxSigner, client, logger)
	}
}

func handleConnection(conn net.Conn, cfg gatewayConfig, allowedKeys map[string]bool, hostSigner ssh.Signer, sandboxSigner ssh.Signer, client *apiClient, logger *log.Logger) {
	defer conn.Close()
	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fp := ssh.FingerprintSHA256(key)
			if allowedKeys[fp] {
				return &ssh.Permissions{Extensions: map[string]string{"fingerprint": fp}}, nil
			}
			return nil, fmt.Errorf("unauthorized key %s", fp)
		},
	}
	serverConfig.AddHostKey(hostSigner)

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		logger.Printf("handshake failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)
	username := sshConn.User()
	logger.Printf("connection from %s as %q", conn.RemoteAddr(), username)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		go handleSession(newChannel, username, cfg, sandboxSigner, client, logger)
	}
}

func handleSession(newChannel ssh.NewChannel, username string, cfg gatewayConfig, sandboxSigner ssh.Signer, client *apiClient, logger *log.Logger) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		logger.Printf("accept channel: %v", err)
		return
	}
	defer channel.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.waitTimeout)
	defer cancel()

	route, err := parseRoute(username, cfg.defaultProfile)
	if err != nil {
		writeSessionError(channel, fmt.Sprintf("invalid username %q: %v", username, err))
		return
	}

	var (
		remoteClient  *ssh.Client
		remoteSession *ssh.Session
		ptyRequest    *ptyRequest
		envRequests   []envRequest
		once          sync.Once
		closed        bool
	)

	closeSession := func(status uint32) {
		once.Do(func() {
			if !closed {
				_ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: status}))
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
		sandbox, err := resolveSandbox(ctx, client, route, cfg)
		if err != nil {
			return err
		}
		if route.isNew {
			fmt.Fprintf(channel, "agentlab: sandbox %d ready (%s)\n", sandbox.VMID, sandbox.IP)
		}
		remoteClient, err = dialSandbox(ctx, sandbox.IP, cfg.sandboxPort, cfg.sandboxUser, sandboxSigner)
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
		if ptyRequest != nil {
			_ = remoteSession.RequestPty(ptyRequest.Term, int(ptyRequest.Rows), int(ptyRequest.Cols), int(ptyRequest.Height), int(ptyRequest.Width), ssh.TerminalModes{})
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

	for req := range requests {
		switch req.Type {
		case "pty-req":
			var pty ptyRequest
			if err := ssh.Unmarshal(req.Payload, &pty); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			ptyRequest = &pty
			_ = req.Reply(true, nil)
		case "env":
			var env envRequest
			if err := ssh.Unmarshal(req.Payload, &env); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			envRequests = append(envRequests, env)
			_ = req.Reply(true, nil)
		case "window-change":
			var win windowChangeRequest
			if err := ssh.Unmarshal(req.Payload, &win); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			if remoteSession != nil {
				_ = remoteSession.WindowChange(int(win.Rows), int(win.Cols))
			}
			_ = req.Reply(true, nil)
		case "exec":
			var execReq execRequest
			if err := ssh.Unmarshal(req.Payload, &execReq); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			if err := ensureRemote(); err != nil {
				writeSessionError(channel, fmt.Sprintf("gateway error: %v", err))
				_ = req.Reply(false, nil)
				closeSession(1)
				return
			}
			if err := remoteSession.Start(execReq.Command); err != nil {
				writeSessionError(channel, fmt.Sprintf("remote exec failed: %v", err))
				_ = req.Reply(false, nil)
				closeSession(1)
				return
			}
			_ = req.Reply(true, nil)
			go waitRemote()
		case "shell":
			if err := ensureRemote(); err != nil {
				writeSessionError(channel, fmt.Sprintf("gateway error: %v", err))
				_ = req.Reply(false, nil)
				closeSession(1)
				return
			}
			if err := remoteSession.Shell(); err != nil {
				writeSessionError(channel, fmt.Sprintf("remote shell failed: %v", err))
				_ = req.Reply(false, nil)
				closeSession(1)
				return
			}
			_ = req.Reply(true, nil)
			go waitRemote()
		case "subsystem":
			_ = req.Reply(false, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

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
	if err != nil || vmid <= 0 {
		return routeTarget{}, fmt.Errorf("unsupported username; use new, new+profile, sbx-<id>, or <id>")
	}
	return routeTarget{vmid: vmid}, nil
}

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

// --- Minimal agentlabd API client ---

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
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
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
