package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/connection"
	"github.com/agentlab/agentlab/internal/operations"
)

type setupInput struct {
	Architecture string `json:"architecture,omitempty"`
	BundleSHA256 string `json:"bundle_sha256,omitempty"`
	WaitSeconds  int64  `json:"readiness_wait_seconds,omitempty"`
	ClientSHA256 string `json:"client_sha256,omitempty"`
	DaemonSHA256 string `json:"daemon_sha256,omitempty"`
	Host         string `json:"host"`
	User         string `json:"ssh_user"`
	Port         int    `json:"ssh_port"`
	Identity     string `json:"ssh_identity,omitempty"`
	Repository   string `json:"repository"`
	Revision     string `json:"revision"`
	Assets       string `json:"assets"`
	ClientBinary string `json:"client_binary,omitempty"`
	DaemonBinary string `json:"daemon_binary,omitempty"`
	Profile      string `json:"profile"`
	Context      string `json:"context"`
}

func operationStore() (operations.Store, error) {
	root, err := clientConfigBaseDir()
	return operations.Store{Root: filepath.Join(root, "operations")}, err
}

func runSetupCommand(ctx context.Context, args []string, base commonFlags) (resultErr error) {
	fs := newFlagSet("setup")
	help := bindHelpFlag(fs)
	input := setupInput{User: "root", Port: 22, Profile: "", Context: base.contextName}
	var key string
	var plan bool
	var noWait bool
	var wait time.Duration
	fs.StringVar(&input.Host, "host", "", "existing SSH server")
	fs.StringVar(&input.User, "ssh-user", input.User, "SSH user")
	fs.IntVar(&input.Port, "ssh-port", 22, "SSH port")
	fs.StringVar(&input.Identity, "identity", "", "SSH identity file")
	fs.StringVar(&input.Repository, "repo", ".", "local repository to prepare")
	fs.StringVar(&input.Assets, "assets", "", "AgentLab installation assets")
	fs.StringVar(&input.ClientBinary, "agentlab-bin", "", "Linux CLI binary for the server")
	fs.StringVar(&input.DaemonBinary, "agentlabd-bin", "", "Linux daemon binary for the server")
	fs.StringVar(&input.Profile, "profile", input.Profile, "readiness sandbox profile")
	fs.StringVar(&input.Context, "context", input.Context, "destination connection context")
	fs.StringVar(&key, "request-id", "", "idempotency key; defaults to the normalized request hash")
	fs.BoolVar(&plan, "plan", false, "inspect the host and persist a plan without installing")
	fs.BoolVar(&noWait, "no-wait", false, "start server setup and return its operation without waiting")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	fs.DurationVar(&wait, "wait-timeout", 20*time.Minute, "readiness wait limit")
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab setup --host HOST --repo PATH [--plan] [--no-wait] [--request-id KEY]")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 || input.Host == "" {
		return fmt.Errorf("setup requires --host and accepts no positional arguments")
	}
	if input.Port < 1 || input.Port > 65535 || wait < time.Second || wait > 24*time.Hour {
		return fmt.Errorf("SSH port and wait timeout must be positive and valid")
	}
	if input.Context != "" {
		if err := connection.ValidateContextName(input.Context); err != nil {
			return err
		}
	}
	if key != "" && !setupRequestKey.MatchString(key) {
		return fmt.Errorf("invalid setup request key; use 1 to 128 letters, digits, dots, colons, underscores, or hyphens")
	}
	repo, err := filepath.Abs(input.Repository)
	if err != nil {
		return err
	}
	input.Repository = repo
	if input.Identity != "" {
		input.Identity, err = filepath.Abs(input.Identity)
		if err != nil {
			return err
		}
	}
	input.WaitSeconds = int64(wait / time.Second)
	git := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--verify", "HEAD")
	revision, err := git.Output()
	if err != nil {
		return fmt.Errorf("--repo must identify a local Git repository with a commit")
	}
	input.Revision = strings.TrimSpace(string(revision))
	assets, err := resolveBootstrapAssets(input.Assets)
	if err != nil {
		return err
	}
	input.Assets = assets
	bootstrap := bootstrapOptions{host: input.Host, sshUser: input.User, sshPort: input.Port, identity: input.Identity, assetsDir: assets, agentlabBin: input.ClientBinary, agentlabdBin: input.DaemonBinary, controlPort: defaultControlPort, requestTimeout: base.timeout, jsonOutput: true, keepTemp: true}
	if bootstrap.requestTimeout <= 0 {
		bootstrap.requestTimeout = defaultRequestTimeout
	}
	ssh, err := newBootstrapSSHClient(bootstrap)
	if err != nil {
		return err
	}
	host, err := inspectBootstrapHost(ctx, ssh)
	if err != nil {
		return err
	}
	if input.Context == "" {
		input.Context = "setup-" + host.Identity[len(host.Identity)-12:]
	}
	bootstrap.contextName = input.Context
	bootstrap.targetArch = host.Architecture
	input.Architecture = host.Architecture
	binaries, err := resolveBootstrapBinariesForHost(ctx, assets, bootstrap)
	if err != nil {
		return err
	}
	if binaries.mode == "upload" {
		for _, path := range []string{binaries.agentlab, binaries.agentlabd} {
			if err := validateBootstrapBinary(path, host.Architecture); err != nil {
				return err
			}
		}
	}
	if binaries.mode == "upload" {
		bootstrap.agentlabBin = binaries.agentlab
		bootstrap.agentlabdBin = binaries.agentlabd
		input.ClientBinary = binaries.agentlab
		input.DaemonBinary = binaries.agentlabd
		input.ClientSHA256, err = setupFileHash(binaries.agentlab)
		if err != nil {
			return err
		}
		input.DaemonSHA256, err = setupFileHash(binaries.agentlabd)
		if err != nil {
			return err
		}
	}
	if binaries.mode != "upload" {
		return fmt.Errorf("setup requires pinned local server artifacts")
	}
	bundle, err := createBootstrapBundle(assets, binaries)
	if err != nil {
		return err
	}
	defer os.Remove(bundle)
	input.BundleSHA256, err = setupFileHash(bundle)
	if err != nil {
		return err
	}
	store, err := operationStore()
	if err != nil {
		return err
	}
	normalized, _ := json.Marshal(input)
	hash := sha256.Sum256(normalized)
	if key == "" {
		key = hex.EncodeToString(hash[:])
	}
	record, err := store.Create(operations.Request{Owner: strconv.Itoa(os.Getuid()), Context: input.Context, ServerIdentity: host.Identity, Key: key, Input: input})
	if err != nil {
		if record.ID != "" {
			return &setupOperationError{Cause: err, Record: record}
		}
		return err
	}
	defer func() {
		if resultErr != nil {
			latest, readErr := store.Read(record.ID)
			if readErr != nil {
				latest = record
			}
			resultErr = &setupOperationError{Cause: resultErr, Record: latest}
		}
	}()
	// Only the input hash enters the record. Retry with the same arguments.
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s (identity=%s, context=%s)\nOperation: %s\n", input.Host, host.Identity, input.Context, record.ID)
	}
	if plan {
		return printSetupRecord(record, base.jsonOutput)
	}
	request := serverSetupRequest{Version: 1, OperationID: record.ID, Owner: record.Owner, Key: key, ServerIdentity: host.Identity, Input: input}
	if err := saveSetupReference(store, request); err != nil {
		return err
	}
	result, err := executeRemoteSetup(ctx, store, request, bundle, noWait, wait)
	if err != nil {
		return err
	}
	return printSetupRecord(result, base.jsonOutput)
}

func printSetupRecord(record operations.Record, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(record)
	}
	fmt.Fprintf(os.Stdout, "Setup %s: %s\nCleanup: %s\n", record.ID, record.State, record.Cleanup)
	if record.Worker != nil {
		fmt.Fprintf(os.Stdout, "Server worker: %s\n", record.Worker.State)
	}
	if record.ClientConnection != "" {
		fmt.Fprintf(os.Stdout, "Client connection: %s\n", record.ClientConnection)
	}
	if record.Action != "" {
		fmt.Fprintln(os.Stdout, record.Action)
	}
	return nil
}

func setupClient(contextName string) (*apiClient, error) {
	store, err := contextStore()
	if err != nil {
		return nil, err
	}
	cfg, _, found, err := store.Resolve(contextName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("setup connection is unavailable")
	}
	opts, err := (commonFlags{endpoint: cfg.Endpoint, token: cfg.Token, allowInsecureHTTP: cfg.AllowInsecureHTTP}).clientOptions()
	if err != nil {
		return nil, err
	}
	return newAPIClient(opts, defaultRequestTimeout), nil
}

func appendSetupResource(resources []operations.Resource, resource operations.Resource) []operations.Resource {
	for _, existing := range resources {
		if existing.Kind == resource.Kind && existing.ID == resource.ID {
			return resources
		}
	}
	return append(resources, resource)
}

func setupSandbox(client *apiClient, ctx context.Context, id string) (sandboxResponse, bool, error) {
	var sb sandboxResponse
	payload, err := client.doJSON(ctx, "GET", "/v1/sandboxes/"+id, nil)
	if err != nil {
		var apiErr apiResponseError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return sb, false, nil
		}
		return sb, false, err
	}
	if err := json.Unmarshal(payload, &sb); err != nil {
		return sb, false, err
	}
	return sb, true, nil
}

func testSetupSandbox(ctx context.Context, r *operations.Record, save func() error, input setupInput, bootstrap bootstrapOptions, ssh *bootstrapSSHClient, operationID string, wait time.Duration) error {
	client, err := setupClient(input.Context)
	if err != nil {
		return err
	}
	guest := func(ctx context.Context, vmid string) (string, error) {
		return ssh.runArgs(ctx, withSudoArgs(bootstrap, []string{"qm", "guest", "exec", vmid, "--", "/bin/true"}), nil)
	}
	return testSetupSandboxClient(ctx, r, save, input, client, guest, operationID, wait)
}

func testSetupSandboxClient(ctx context.Context, r *operations.Record, save func() error, input setupInput, client *apiClient, guest func(context.Context, string) (string, error), operationID string, wait time.Duration) error {
	if input.Profile == "" {
		profiles, err := fetchProfiles(ctx, client)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			if profile.TemplateVMID > 0 {
				input.Profile = profile.Name
				break
			}
		}
		for _, profile := range profiles {
			if profile.Name == "yolo-ephemeral" && profile.TemplateVMID > 0 {
				input.Profile = profile.Name
				break
			}
		}
		if input.Profile == "" {
			return &operations.Requirement{Code: "profile_required", Action: "install a profile with a valid template, then resume setup"}
		}
	}
	if r.ServerIdentity != "" {
		if err := verifySetupIdentity(ctx, client, r.ServerIdentity); err != nil {
			return err
		}
	}
	name := "agentlab-setup-" + operationID
	index := -1
	for i := range r.Resources {
		if r.Resources[i].Kind == "readiness_sandbox" && r.Resources[i].State != "cleaned" {
			index = i
			break
		}
	}
	if index < 0 {
		payload, err := client.doJSON(ctx, "GET", "/v1/sandboxes/inventory", nil)
		if err != nil {
			return err
		}
		var inventory sandboxInventoryResponse
		if err := json.Unmarshal(payload, &inventory); err != nil {
			return err
		}
		vmid := 1000
		for _, entry := range inventory.Sandboxes {
			if entry.VMID >= vmid {
				vmid = entry.VMID + 1
			}
		}
		if vmid <= 0 || vmid > 999999999 {
			return fmt.Errorf("no available VM ID for readiness")
		}

		expiry := time.Now().UTC().Add(30 * time.Minute)
		r.Resources = append(r.Resources, operations.Resource{Kind: "readiness_sandbox", ID: strconv.Itoa(vmid), State: "planned", ExpiresAt: &expiry})
		index = len(r.Resources) - 1
		r.Cleanup = "pending"
		if err := save(); err != nil {
			return err
		}
	}
	resource := &r.Resources[index]
	sb, exists, err := setupSandbox(client, ctx, resource.ID)
	if err != nil {
		return err
	}
	if exists && sb.Name != name {
		return fmt.Errorf("readiness VM ID belongs to another sandbox; preserve it and inspect the operation")
	}
	if exists && sb.State == "DESTROYED" {
		resource.State = "cleaned"
		if err := save(); err != nil {
			return err
		}
		return testSetupSandboxClient(ctx, r, save, input, client, guest, operationID, wait)
	}
	if !exists {
		vmid, _ := strconv.Atoi(resource.ID)
		ttl := 30
		keepalive := false
		payload, err := client.doJSON(ctx, "POST", "/v1/sandboxes/validate-plan", sandboxValidatePlanRequest{Name: name, Profile: input.Profile, VMID: &vmid, TTLMinutes: &ttl, Keepalive: &keepalive})
		if err != nil {
			return err
		}
		var validation sandboxValidatePlanResponse
		if err := json.Unmarshal(payload, &validation); err != nil {
			return err
		}
		if !validation.OK {
			return &operations.Requirement{Code: "readiness_configuration_invalid", Action: "select a valid profile with available sandbox capacity, then retry setup"}
		}
		_, err = client.doJSON(ctx, "POST", "/v1/sandboxes", sandboxCreateRequest{Name: name, Profile: input.Profile, VMID: &vmid, TTLMinutes: &ttl, Keepalive: &keepalive})
		if err != nil {
			return err
		}
		resource.State = "created"
		if err := save(); err != nil {
			return err
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		sb, exists, err = setupSandbox(client, waitCtx, resource.ID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("readiness sandbox disappeared before verification")
		}
		if sb.Name != name {
			return fmt.Errorf("readiness sandbox identity changed")
		}
		if sb.State == "FAILED" || sb.State == "DESTROYED" || sb.State == "TIMEOUT" {
			return fmt.Errorf("readiness sandbox entered %s", sb.State)
		}
		if sb.Health != nil && sb.Health.Guest != nil && sb.Health.Guest.Ready && (sb.State == "READY" || sb.State == "RUNNING") {
			present, err := setupBackendTarget(waitCtx, client, resource.ID, name)
			if err != nil {
				return err
			}
			if !present {
				return fmt.Errorf("readiness sandbox is absent from backend inventory")
			}
			output, err := guest(waitCtx, resource.ID)
			if err != nil {
				return fmt.Errorf("readiness guest command failed")
			}
			var execution struct {
				ExitCode int `json:"exitcode"`
				Exited   int `json:"exited"`
			}
			if err := json.Unmarshal([]byte(output), &execution); err != nil || execution.Exited != 1 || execution.ExitCode != 0 {
				return fmt.Errorf("readiness guest command did not verify success")
			}
			resource.State = "verified"
			return save()
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return waitCtx.Err()
		case <-timer.C:
		}
	}
}

func cleanupSetupSandbox(ctx context.Context, r *operations.Record, contextName, operationID string) error {
	return cleanupSetupSandboxClient(ctx, r, func() (*apiClient, error) { return setupClient(contextName) }, operationID)
}
func cleanupSetupSandboxClient(ctx context.Context, r *operations.Record, lookup func() (*apiClient, error), operationID string) error {
	var client *apiClient
	for i := range r.Resources {
		resource := &r.Resources[i]
		if resource.Kind != "readiness_sandbox" || resource.State == "cleaned" || resource.State == "verified_cleaned" {
			continue
		}
		if client == nil {
			var err error
			client, err = lookup()
			if err != nil {
				return err
			}
		}
		if r.ServerIdentity != "" {
			if err := verifySetupIdentity(ctx, client, r.ServerIdentity); err != nil {
				return err
			}
		}
		sb, exists, err := setupSandbox(client, ctx, resource.ID)
		if err != nil {
			return err
		}
		if exists && sb.Name != "agentlab-setup-"+operationID {
			return fmt.Errorf("refusing cleanup: sandbox is not owned by this setup")
		}
		if exists && sb.State != "DESTROYED" {
			present, err := setupBackendTarget(ctx, client, resource.ID, "agentlab-setup-"+operationID)
			if err != nil {
				return err
			}
			if !present {
				return fmt.Errorf("sandbox API and backend inventory disagree; inspect before cleanup")
			}
			if _, err := client.doJSON(ctx, "POST", "/v1/sandboxes/"+resource.ID+"/destroy", sandboxDestroyRequest{Force: true}); err != nil {
				return err
			}
			sb, exists, err = setupSandbox(client, ctx, resource.ID)
			if err != nil {
				return err
			}
			if exists && sb.State != "DESTROYED" {
				return fmt.Errorf("readiness sandbox cleanup is not complete")
			}
		}
		present, err := setupBackendTarget(ctx, client, resource.ID, "agentlab-setup-"+operationID)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("readiness sandbox remains in backend inventory")
		}
		if resource.State == "verified" {
			resource.State = "verified_cleaned"
		} else {
			resource.State = "cleaned"
		}
	}
	return nil
}

func setupFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifySetupIdentity(ctx context.Context, client *apiClient, expected string) error {
	host, err := fetchHostInfo(ctx, client)
	if err != nil {
		return err
	}
	if expected == "" || host == nil || host.ServerIdentity != expected {
		return &operations.Requirement{Code: "server_identity_mismatch", Action: "check the selected endpoint and server version; do not retry mutations on a different server"}
	}
	return nil
}

// Inventory comes from a successful backend read. A database tombstone alone
// cannot establish removal of the temporary VM.
func setupBackendTarget(ctx context.Context, client *apiClient, id, name string) (bool, error) {
	vmid, err := strconv.Atoi(id)
	if err != nil || vmid <= 0 || vmid > 999999999 {
		return false, fmt.Errorf("invalid setup sandbox identity")
	}
	payload, err := client.doJSON(ctx, "GET", "/v1/sandboxes/inventory", nil)
	if err != nil {
		return false, err
	}
	var inventory sandboxInventoryResponse
	if json.Unmarshal(payload, &inventory) != nil || inventory.Sandboxes == nil {
		return false, fmt.Errorf("invalid backend sandbox inventory")
	}
	found := false
	present := false
	for _, entry := range inventory.Sandboxes {
		if entry.VMID != vmid {
			continue
		}
		if found {
			return false, fmt.Errorf("duplicate backend sandbox identity")
		}
		found = true
		if entry.Name != name || !entry.Managed {
			return false, fmt.Errorf("backend sandbox identity changed; preserve it for inspection")
		}
		if entry.ProxmoxStatus == "" {
			missing := false
			for _, drift := range entry.Drift {
				if drift == "missing_in_proxmox" {
					missing = true
				}
			}
			if !missing {
				return false, fmt.Errorf("backend sandbox presence is unverified")
			}
		} else {
			present = true
		}
	}
	return present, nil
}
