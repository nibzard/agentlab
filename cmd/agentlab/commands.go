// ABOUTME: Command implementations for job, sandbox, workspace, and logs operations.
// ABOUTME: Handles flag parsing, API calls, and output formatting for all CLI commands.

// Package main implements all CLI command handlers for agentlab.
//
// # Command Structure
//
// Commands are organized hierarchically:
//
//	job:       Manage jobs (run, show, artifacts)
//	sandbox:   Manage sandboxes (new, list, show, start, stop, revert, destroy, lease, prune, expose, exposed, unexpose)
//	workspace: Manage workspaces (create, list, check, fsck, attach, detach, rebind, fork, snapshot)
//	profile:   Manage profiles (list)
//	msg:       Manage messagebox (post, tail)
//	ssh:       Generate SSH connection parameters
//	logs:      View sandbox event logs
//
// # Output Formats
//
// By default, commands produce human-readable text output. With --json,
// commands output JSON for programmatic consumption.
//
// # Flag Parsing
//
// This package uses the standard flag package with custom error handling
// to provide consistent error messages and usage output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	defaultLogTail                  = 50
	eventPollInterval               = 2 * time.Second
	defaultEventLimit               = 200
	maxEventLimit                   = 1000
	defaultMessageTail              = 50
	defaultMessageLimit             = 200
	maxMessageLimit                 = 1000
	defaultRequestTimeout           = 10 * time.Minute
	ttlFlagDescription              = "lease ttl in minutes or duration (e.g. 120 or 2h)"
	jsonFlagDescription             = "output json"
	defaultArtifactBundleName       = "agentlab-artifacts.tar.gz"
	defaultDoctorBundlePrefix       = "agentlab-doctor"
	defaultStatefulWorkspaceSizeGB  = 80
	defaultStatefulWorkspaceStorage = "local-zfs"
)

var (
	errHelp            = errors.New("help requested")
	errUsage           = errors.New("invalid usage")
	statusSandboxOrder = []string{
		"REQUESTED",
		"PROVISIONING",
		"BOOTING",
		"READY",
		"RUNNING",
		"SUSPENDED",
		"COMPLETED",
		"FAILED",
		"TIMEOUT",
		"STOPPED",
		"DESTROYED",
	}
	statusJobOrder = []string{
		"QUEUED",
		"RUNNING",
		"COMPLETED",
		"FAILED",
		"TIMEOUT",
	}
	statusNetworkModeOrder = []string{
		"off",
		"nat",
		"allowlist",
	}
)

// usageError wraps an error with a flag indicating whether usage should be shown.
type usageError struct {
	err       error
	showUsage bool
}

func (e usageError) Error() string {
	if e.err == nil {
		return errUsage.Error()
	}
	return fmt.Sprintf("%s: %v", errUsage.Error(), e.err)
}

func (e usageError) Unwrap() error {
	return errUsage
}

func newUsageError(err error, showUsage bool) error {
	return usageError{err: err, showUsage: showUsage}
}

func usageErrorMessage(err error) (string, bool, bool) {
	var ue usageError
	if errors.As(err, &ue) {
		if ue.err != nil {
			return ue.err.Error(), ue.showUsage, true
		}
		return errUsage.Error(), ue.showUsage, true
	}
	return "", false, false
}

// commonFlags contains flags shared by all commands.
type commonFlags struct {
	socketPath string
	endpoint   string
	token      string
	jsonOutput bool
	timeout    time.Duration
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.endpoint, "endpoint", c.endpoint, "control plane endpoint (http(s)://host:port)")
	fs.StringVar(&c.token, "token", c.token, "control plane auth token")
	fs.StringVar(&c.socketPath, "socket", c.socketPath, "path to agentlabd socket")
	fs.BoolVar(&c.jsonOutput, "json", c.jsonOutput, jsonFlagDescription)
	fs.DurationVar(&c.timeout, "timeout", c.timeout, "request timeout (e.g. 30s, 2m)")
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// optionalBool is a bool flag that can track whether it was explicitly set.
type optionalBool struct {
	value bool
	set   bool
}

func (o *optionalBool) String() string {
	if o == nil || !o.set {
		return ""
	}
	if o.value {
		return "true"
	}
	return "false"
}

func (o *optionalBool) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	o.value = parsed
	o.set = true
	return nil
}

func (o *optionalBool) IsBoolFlag() bool {
	return true
}

func (o *optionalBool) Ptr() *bool {
	if o == nil || !o.set {
		return nil
	}
	value := o.value
	return &value
}

func bindHelpFlag(fs *flag.FlagSet) *bool {
	help := new(bool)
	fs.BoolVar(help, "help", false, "show help")
	fs.BoolVar(help, "h", false, "show help")
	return help
}

func parseFlags(fs *flag.FlagSet, args []string, usage func(), help *bool, jsonOutput bool) error {
	fs.Usage = usage
	if err := fs.Parse(args); err != nil {
		if !jsonOutput {
			usage()
		}
		return newUsageError(err, false)
	}
	if help != nil && *help {
		usage()
		return errHelp
	}
	return nil
}

// runStatusCommand displays the control plane status summary.
func runStatusCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("status")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printStatusUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp statusResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printStatus(resp)
	return nil
}

type connectOutput struct {
	Endpoint   string        `json:"endpoint"`
	JumpHost   string        `json:"jump_host,omitempty"`
	JumpUser   string        `json:"jump_user,omitempty"`
	ConfigPath string        `json:"config_path"`
	TokenSet   bool          `json:"token_set"`
	Host       *hostResponse `json:"host,omitempty"`
}

func runConnectCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("connect")
	opts := base
	opts.bind(fs)
	var jumpHost string
	var jumpUser string
	help := bindHelpFlag(fs)
	fs.StringVar(&jumpHost, "jump-host", "", "default SSH jump host (used when subnet route is unavailable)")
	fs.StringVar(&jumpUser, "jump-user", "", "default SSH jump username")
	if err := parseFlags(fs, args, printConnectUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !opts.jsonOutput {
			printConnectUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	endpointRaw := strings.TrimSpace(opts.endpoint)
	if endpointRaw == "" {
		if !opts.jsonOutput {
			printConnectUsage()
		}
		return fmt.Errorf("endpoint is required")
	}
	endpoint, err := normalizeEndpoint(endpointRaw)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(opts.token)
	client := newAPIClient(clientOptions{Endpoint: endpoint, Token: token}, opts.timeout)

	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return withHints(err, "verify the endpoint and token are correct")
	}
	if opts.jsonOutput && payload != nil {
		// Ensure /v1/status returned valid JSON without printing it.
		var status statusResponse
		if err := json.Unmarshal(payload, &status); err != nil {
			return err
		}
	}

	var hostInfo *hostResponse
	if hostPayload, err := client.doJSON(ctx, http.MethodGet, "/v1/host", nil); err == nil {
		var resp hostResponse
		if err := json.Unmarshal(hostPayload, &resp); err == nil {
			hostInfo = &resp
		}
	}

	path, err := clientConfigPath()
	if err != nil {
		return err
	}
	jumpHost = strings.TrimSpace(jumpHost)
	jumpUser = strings.TrimSpace(jumpUser)
	if jumpHost == "" && hostInfo != nil {
		jumpHost = strings.TrimSpace(hostInfo.TailscaleDNS)
	}
	cfg := clientConfig{
		Endpoint: endpoint,
		Token:    token,
		JumpHost: jumpHost,
		JumpUser: jumpUser,
	}
	if err := writeClientConfig(path, cfg); err != nil {
		return err
	}

	if opts.jsonOutput {
		out := connectOutput{
			Endpoint:   endpoint,
			JumpHost:   jumpHost,
			JumpUser:   jumpUser,
			ConfigPath: path,
			TokenSet:   token != "",
			Host:       hostInfo,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(out)
	}

	fmt.Fprintf(os.Stdout, "Connected to %s\n", endpoint)
	fmt.Fprintf(os.Stdout, "Config written to %s\n", path)
	if hostInfo != nil && strings.TrimSpace(hostInfo.TailscaleDNS) != "" {
		fmt.Fprintf(os.Stdout, "Tailscale DNS: %s\n", strings.TrimSpace(hostInfo.TailscaleDNS))
	}
	return nil
}

func runDisconnectCommand(ctx context.Context, args []string, base commonFlags) error {
	_ = ctx
	fs := newFlagSet("disconnect")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printDisconnectUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		if !opts.jsonOutput {
			printDisconnectUsage()
		}
		return fmt.Errorf("unexpected extra arguments")
	}
	path, err := clientConfigPath()
	if err != nil {
		return err
	}
	removed, err := removeClientConfig(path)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		payload := map[string]any{
			"disconnected": true,
			"removed":      removed,
			"config_path":  path,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}
	if removed {
		fmt.Fprintf(os.Stdout, "Disconnected (removed %s)\n", path)
		return nil
	}
	fmt.Fprintf(os.Stdout, "Disconnected (no config found)\n")
	return nil
}

// runJobCommand dispatches job subcommands (run, show, artifacts).
func runJobCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printJobUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("job command is required"), false)
	}
	if isHelpToken(args[0]) {
		printJobUsage()
		return errHelp
	}
	switch args[0] {
	case "run":
		return runJobRun(ctx, args[1:], base)
	case "show":
		return runJobShow(ctx, args[1:], base)
	case "artifacts":
		return runJobArtifacts(ctx, args[1:], base)
	case "doctor":
		return runJobDoctor(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printJobUsage()
		}
		return unknownSubcommandError("job", args[0], []string{"run", "show", "artifacts", "doctor"})
	}
}

// runJobRun creates and starts a new job.
func runJobRun(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job run")
	opts := base
	opts.bind(fs)
	var repo string
	var ref string
	var branch string
	var profile string
	var task string
	var mode string
	var ttl string
	var workspace string
	var workspaceCreate string
	var workspaceSize string
	var workspaceStorage string
	var workspaceWait string
	var stateful bool
	var keepalive optionalBool
	help := bindHelpFlag(fs)
	fs.StringVar(&repo, "repo", "", "git repository url")
	fs.StringVar(&ref, "ref", "", "git ref (default main)")
	fs.StringVar(&branch, "branch", "", "branch name (session-backed)")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&task, "task", "", "task description")
	fs.StringVar(&mode, "mode", "", "mode (default dangerous)")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.StringVar(&workspace, "workspace", "", "workspace id or name (or new:<name>)")
	fs.StringVar(&workspaceCreate, "workspace-create", "", "create workspace with name")
	fs.StringVar(&workspaceSize, "workspace-size", "", "workspace size for creation (e.g. 80G)")
	fs.StringVar(&workspaceStorage, "workspace-storage", "", "workspace storage (default local-zfs)")
	fs.StringVar(&workspaceWait, "workspace-wait", "", "wait for workspace detach (e.g. 2m, 30s)")
	fs.BoolVar(&stateful, "stateful", false, "create a default workspace for a stateful job")
	fs.Var(&keepalive, "keepalive", "keep sandbox after job completion")
	if err := parseFlags(fs, args, printJobRunUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if repo == "" || profile == "" || task == "" {
		if !opts.jsonOutput {
			printJobRunUsage()
		}
		return fmt.Errorf("repo, profile, and task are required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}
	branch = strings.TrimSpace(branch)
	workspace = strings.TrimSpace(workspace)
	workspaceCreate = strings.TrimSpace(workspaceCreate)
	workspaceSize = strings.TrimSpace(workspaceSize)
	workspaceStorage = strings.TrimSpace(workspaceStorage)
	workspaceWait = strings.TrimSpace(workspaceWait)
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	var (
		workspaceID        *string
		workspaceCreateReq *workspaceCreateRequest
		workspaceWaitSecs  *int
		sessionID          *string
	)
	if branch != "" {
		session, err := resolveBranchSession(ctx, client, branch, profile, workspace, workspaceCreate, workspaceSize, workspaceStorage)
		if err != nil {
			return wrapUnknownProfileError(ctx, client, profile, err)
		}
		if strings.TrimSpace(session.WorkspaceID) == "" {
			return fmt.Errorf("session workspace_id is required")
		}
		ws := strings.TrimSpace(session.WorkspaceID)
		workspaceID = &ws
		sessionID = &session.ID
	} else {
		if workspace != "" && workspaceCreate != "" {
			return fmt.Errorf("--workspace and --workspace-create are mutually exclusive")
		}
		if strings.HasPrefix(workspace, "new:") {
			name := strings.TrimSpace(strings.TrimPrefix(workspace, "new:"))
			if name == "" {
				return fmt.Errorf("workspace name is required after new")
			}
			if workspaceCreate != "" {
				return fmt.Errorf("--workspace new:<name> cannot be combined with --workspace-create")
			}
			workspaceCreate = name
			workspace = ""
		}
		if stateful && workspace == "" && workspaceCreate == "" {
			defaultName, err := defaultStatefulWorkspaceName(repo)
			if err != nil {
				return err
			}
			workspaceCreate = defaultName
		}

		if workspace != "" {
			value := strings.TrimSpace(workspace)
			if value != "" {
				workspaceID = &value
			}
		}
		if workspaceCreate != "" {
			sizeGB := 0
			if workspaceSize == "" {
				if stateful {
					sizeGB = defaultStatefulWorkspaceSizeGB
				} else {
					return fmt.Errorf("--workspace-size is required when creating a workspace")
				}
			} else {
				sizeGB, err = parseSizeGB(workspaceSize)
				if err != nil {
					return err
				}
			}
			storage := workspaceStorage
			if storage == "" && stateful {
				storage = defaultStatefulWorkspaceStorage
			}
			workspaceCreateReq = &workspaceCreateRequest{
				Name:    workspaceCreate,
				SizeGB:  sizeGB,
				Storage: storage,
			}
		} else if workspaceSize != "" || workspaceStorage != "" {
			return fmt.Errorf("--workspace-size/--workspace-storage require workspace creation (use --workspace new:<name> or --workspace-create)")
		}
	}
	if workspaceWait != "" {
		workspaceWaitSecs, err = parseWorkspaceWaitSeconds(workspaceWait)
		if err != nil {
			return err
		}
		if workspaceID == nil && workspaceCreateReq == nil {
			return fmt.Errorf("--workspace-wait requires --workspace or --workspace-create")
		}
	}
	req := jobCreateRequest{
		RepoURL:              repo,
		Ref:                  ref,
		Profile:              profile,
		Task:                 task,
		Mode:                 mode,
		TTLMinutes:           ttlMinutes,
		Keepalive:            keepalive.Ptr(),
		WorkspaceID:          workspaceID,
		WorkspaceCreate:      workspaceCreateReq,
		WorkspaceWaitSeconds: workspaceWaitSecs,
		SessionID:            sessionID,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/jobs", req)
	if err != nil {
		err = wrapJobWorkspaceCompatibilityError(err)
		err = wrapJobWorkspaceConflict(workspaceID, workspaceCreateReq, workspaceWaitSecs, err)
		err = wrapWorkspaceNotFound(workspaceTargetName(workspaceID, workspaceCreateReq), err)
		return wrapUnknownProfileError(ctx, client, profile, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp jobResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printJob(resp)
	return nil
}

// runJobShow displays details of a single job.
func runJobShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job show")
	opts := base
	opts.bind(fs)
	var eventsTail int
	help := bindHelpFlag(fs)
	fs.IntVar(&eventsTail, "events-tail", -1, "number of recent events to include (0 to omit)")
	if err := parseFlags(fs, args, printJobShowUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printJobShowUsage()
		}
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	query := ""
	if eventsTail >= 0 {
		query = fmt.Sprintf("?events_tail=%d", eventsTail)
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s%s", jobID, query), nil)
	if err != nil {
		return wrapJobNotFound(jobID, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp jobResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printJob(resp)
	if len(resp.Events) > 0 {
		fmt.Println("Events:")
		printEvents(resp.Events, false)
	}
	return nil
}

// runJobArtifacts handles job artifacts subcommands.
func runJobArtifacts(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printJobArtifactsUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("job artifacts command is required"), false)
	}
	switch args[0] {
	case "download":
		return runJobArtifactsDownload(ctx, args[1:], base)
	default:
		return runJobArtifactsList(ctx, args, base)
	}
}

func runJobArtifactsList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job artifacts")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printJobArtifactsUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printJobArtifactsUsage()
		}
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s/artifacts", jobID), nil)
	if err != nil {
		return wrapJobNotFound(jobID, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp artifactsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printArtifactsList(resp.Artifacts)
	return nil
}

func runJobArtifactsDownload(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job artifacts download")
	opts := base
	opts.bind(fs)
	var out string
	var path string
	var name string
	var latest bool
	var bundle bool
	help := bindHelpFlag(fs)
	fs.StringVar(&out, "out", "", "output file path or directory")
	fs.StringVar(&path, "path", "", "artifact path to download")
	fs.StringVar(&name, "name", "", "artifact name to download")
	fs.BoolVar(&latest, "latest", false, "download latest artifact")
	fs.BoolVar(&bundle, "bundle", false, "download latest bundle (agentlab-artifacts.tar.gz)")
	if err := parseFlags(fs, args, printJobArtifactsDownloadUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printJobArtifactsDownloadUsage()
		}
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}
	path = strings.TrimSpace(path)
	name = strings.TrimSpace(name)
	if path != "" && name != "" {
		return fmt.Errorf("path and name are mutually exclusive")
	}
	if path == "" && name == "" && !latest && !bundle {
		bundle = true
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s/artifacts", jobID), nil)
	if err != nil {
		return wrapJobNotFound(jobID, err)
	}
	var resp artifactsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	if len(resp.Artifacts) == 0 {
		return fmt.Errorf("no artifacts found for job %s", jobID)
	}
	artifact, err := selectArtifact(resp.Artifacts, path, name, latest, bundle)
	if err != nil {
		return err
	}
	if strings.TrimSpace(artifact.Path) == "" {
		artifact.Path = strings.TrimSpace(artifact.Name)
	}
	if strings.TrimSpace(artifact.Path) == "" {
		return fmt.Errorf("selected artifact has no path")
	}

	targetName := strings.TrimSpace(artifact.Name)
	if targetName == "" {
		targetName = filepath.Base(strings.TrimSpace(artifact.Path))
	}
	targetPath, err := resolveArtifactOutPath(out, targetName)
	if err != nil {
		return err
	}

	downloadPath := fmt.Sprintf("/v1/jobs/%s/artifacts/download?path=%s", jobID, url.QueryEscape(artifact.Path))
	respBody, err := client.doRequest(ctx, http.MethodGet, downloadPath, nil, nil)
	if err != nil {
		return err
	}
	defer respBody.Body.Close()

	outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, respBody.Body); err != nil {
		return err
	}
	if err := outFile.Sync(); err != nil {
		return err
	}

	if opts.jsonOutput {
		result := map[string]any{
			"job_id":   jobID,
			"artifact": artifact,
			"out":      targetPath,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, _ = os.Stdout.Write(append(data, '\n'))
		return nil
	}
	fmt.Printf("downloaded %s to %s\n", artifact.Path, targetPath)
	return nil
}

func runJobDoctor(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job doctor")
	opts := base
	opts.bind(fs)
	var out string
	help := bindHelpFlag(fs)
	fs.StringVar(&out, "out", "", "output file path or directory")
	if err := parseFlags(fs, args, printJobDoctorUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printJobDoctorUsage()
		}
		return fmt.Errorf("job_id is required")
	}
	jobID := strings.TrimSpace(fs.Arg(0))
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}
	bundleName := defaultDoctorBundleName("job", jobID)
	path := fmt.Sprintf("/v1/jobs/%s/doctor", jobID)
	return downloadDoctorBundle(ctx, opts, path, out, bundleName, "job", jobID)
}

// runSandboxCommand dispatches sandbox subcommands.
func runSandboxCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printSandboxUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("sandbox command is required"), false)
	}
	if isHelpToken(args[0]) {
		printSandboxUsage()
		return errHelp
	}
	switch args[0] {
	case "new":
		return runSandboxNew(ctx, args[1:], base)
	case "list":
		return runSandboxList(ctx, args[1:], base)
	case "show":
		return runSandboxShow(ctx, args[1:], base)
	case "start":
		return runSandboxStart(ctx, args[1:], base)
	case "stop":
		return runSandboxStop(ctx, args[1:], base)
	case "pause":
		return runSandboxPause(ctx, args[1:], base)
	case "resume":
		return runSandboxResume(ctx, args[1:], base)
	case "revert":
		return runSandboxRevert(ctx, args[1:], base)
	case "destroy":
		return runSandboxDestroy(ctx, args[1:], base)
	case "lease":
		return runSandboxLease(ctx, args[1:], base)
	case "prune":
		return runSandboxPrune(ctx, args[1:], base)
	case "expose":
		return runSandboxExpose(ctx, args[1:], base)
	case "exposed":
		return runSandboxExposed(ctx, args[1:], base)
	case "unexpose":
		return runSandboxUnexpose(ctx, args[1:], base)
	case "doctor":
		return runSandboxDoctor(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printSandboxUsage()
		}
		return unknownSubcommandError("sandbox", args[0], []string{"new", "list", "show", "start", "stop", "pause", "resume", "revert", "destroy", "lease", "prune", "expose", "exposed", "unexpose", "doctor"})
	}
}

// runWorkspaceCommand dispatches workspace subcommands.
func runWorkspaceCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printWorkspaceUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("workspace command is required"), false)
	}
	if isHelpToken(args[0]) {
		printWorkspaceUsage()
		return errHelp
	}
	switch args[0] {
	case "create":
		return runWorkspaceCreate(ctx, args[1:], base)
	case "list":
		return runWorkspaceList(ctx, args[1:], base)
	case "check":
		return runWorkspaceCheck(ctx, args[1:], base)
	case "fsck":
		return runWorkspaceFsck(ctx, args[1:], base)
	case "attach":
		return runWorkspaceAttach(ctx, args[1:], base)
	case "detach":
		return runWorkspaceDetach(ctx, args[1:], base)
	case "rebind":
		return runWorkspaceRebind(ctx, args[1:], base)
	case "fork":
		return runWorkspaceFork(ctx, args[1:], base)
	case "snapshot":
		return runWorkspaceSnapshotCommand(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printWorkspaceUsage()
		}
		return unknownSubcommandError("workspace", args[0], []string{"create", "list", "check", "fsck", "attach", "detach", "rebind", "fork", "snapshot"})
	}
}

// runSessionCommand dispatches session subcommands.
func runSessionCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printSessionUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("session command is required"), false)
	}
	if isHelpToken(args[0]) {
		printSessionUsage()
		return errHelp
	}
	switch args[0] {
	case "create":
		return runSessionCreate(ctx, args[1:], base)
	case "list":
		return runSessionList(ctx, args[1:], base)
	case "show":
		return runSessionShow(ctx, args[1:], base)
	case "resume":
		return runSessionResume(ctx, args[1:], base)
	case "stop":
		return runSessionStop(ctx, args[1:], base)
	case "fork":
		return runSessionFork(ctx, args[1:], base)
	case "branch":
		return runSessionBranch(ctx, args[1:], base)
	case "doctor":
		return runSessionDoctor(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printSessionUsage()
		}
		return unknownSubcommandError("session", args[0], []string{"create", "list", "show", "resume", "stop", "fork", "branch", "doctor"})
	}
}

// runProfileCommand dispatches profile subcommands.
func runProfileCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printProfileUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("profile command is required"), false)
	}
	if isHelpToken(args[0]) {
		printProfileUsage()
		return errHelp
	}
	switch args[0] {
	case "list":
		return runProfileList(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printProfileUsage()
		}
		return unknownSubcommandError("profile", args[0], []string{"list"})
	}
}

// runMsgCommand dispatches messagebox subcommands.
func runMsgCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printMsgUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("msg command is required"), false)
	}
	if isHelpToken(args[0]) {
		printMsgUsage()
		return errHelp
	}
	switch args[0] {
	case "post":
		return runMsgPost(ctx, args[1:], base)
	case "tail":
		return runMsgTail(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printMsgUsage()
		}
		return unknownSubcommandError("msg", args[0], []string{"post", "tail"})
	}
}

func runProfileList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("profile list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printProfileListUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/profiles", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp profilesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printProfileList(resp.Profiles)
	return nil
}

func runSandboxNew(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox new")
	opts := base
	opts.bind(fs)
	var name string
	var profile string
	var ttl string
	var workspace string
	var vmid int
	var jobID string
	var andSSH bool
	var keepalive optionalBool
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "sandbox name")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.StringVar(&workspace, "workspace", "", "workspace id or name")
	fs.IntVar(&vmid, "vmid", 0, "vmid override")
	fs.StringVar(&jobID, "job", "", "attach to existing job id")
	fs.BoolVar(&andSSH, "and-ssh", false, "create sandbox and immediately ssh into it")
	fs.Var(&keepalive, "keepalive", "enable keepalive lease for sandbox")
	if err := parseFlags(fs, args, printSandboxNewUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	modifiers, err := parseSandboxModifiers(fs.Args())
	if err != nil {
		if !opts.jsonOutput {
			printSandboxNewUsage()
		}
		return err
	}
	profile = strings.TrimSpace(profile)
	if profile != "" && len(modifiers) > 0 {
		return fmt.Errorf("cannot combine --profile with modifiers")
	}
	if andSSH {
		if opts.jsonOutput {
			return fmt.Errorf("cannot use --and-ssh with --json")
		}
		if !isInteractive() {
			return fmt.Errorf("--and-ssh requires an interactive terminal")
		}
	}
	if profile == "" && len(modifiers) == 0 {
		if !opts.jsonOutput {
			printSandboxNewUsage()
		}
		return fmt.Errorf("profile is required (or provide +modifiers)")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}
	var workspaceID *string
	if strings.TrimSpace(workspace) != "" {
		value := strings.TrimSpace(workspace)
		workspaceID = &value
	}
	var vmidPtr *int
	if vmid > 0 {
		value := vmid
		vmidPtr = &value
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	if profile != "" || len(modifiers) > 0 {
		profiles, err := fetchProfiles(ctx, client)
		if err != nil {
			return err
		}
		if len(modifiers) > 0 {
			resolvedProfile, resolveErr := resolveProfileFromModifiers(modifiers, profiles)
			if resolveErr != nil {
				return resolveErr
			}
			profile = resolvedProfile
		} else {
			resolvedProfile, resolveErr := validateProfileName(profile, profiles)
			if resolveErr != nil {
				return resolveErr
			}
			profile = resolvedProfile
		}
	}
	req := sandboxCreateRequest{
		Name:       name,
		Profile:    profile,
		Keepalive:  keepalive.Ptr(),
		TTLMinutes: ttlMinutes,
		Workspace:  workspaceID,
		VMID:       vmidPtr,
		JobID:      jobID,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	if andSSH {
		return runSSHCommand(ctx, []string{"--exec", "--wait", strconv.Itoa(resp.VMID)}, base)
	}
	printSandbox(resp)
	return nil
}

func runSandboxList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxListUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sandboxes", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSandboxList(resp.Sandboxes)
	return nil
}

func runSandboxShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox show")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxShowUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxShowUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	touchSandboxBestEffort(ctx, client, vmid)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSandbox(resp)
	return nil
}

func runSandboxStart(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox start")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxStartUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxStartUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/start", vmid), nil)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d started (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxStop(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox stop")
	opts := base
	opts.bind(fs)
	var all bool
	var force bool
	help := bindHelpFlag(fs)
	fs.BoolVar(&all, "all", false, "stop all running sandboxes")
	fs.BoolVar(&force, "force", false, "skip confirmation prompt")
	if err := parseFlags(fs, args, printSandboxStopUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	if all {
		if fs.NArg() > 0 {
			if !opts.jsonOutput {
				printSandboxStopUsage()
			}
			return fmt.Errorf("vmid is not allowed with --all")
		}
		if err := requireConfirmation(confirmOptions{
			action:     "stop all running sandboxes",
			force:      force,
			jsonOutput: opts.jsonOutput,
		}); err != nil {
			return err
		}
		payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes/stop_all", sandboxStopAllRequest{Force: force})
		if err != nil {
			return err
		}
		if opts.jsonOutput {
			return prettyPrintJSON(os.Stdout, payload)
		}
		var resp sandboxStopAllResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return err
		}
		printSandboxStopAll(resp)
		return nil
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxStopUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/stop", vmid), nil)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d stopped (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxPause(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox pause")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxPauseUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxPauseUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/pause", vmid), nil)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d paused (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxResume(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox resume")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxResumeUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxResumeUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/resume", vmid), nil)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d resumed (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func printSandboxStopAll(resp sandboxStopAllResponse) {
	fmt.Printf("stop-all complete: stopped=%d skipped=%d failed=%d (total=%d)\n", resp.Stopped, resp.Skipped, resp.Failed, resp.Total)
	for _, result := range resp.Results {
		switch result.Result {
		case "failed":
			if result.Error != "" {
				fmt.Printf("failed: vmid=%d state=%s error=%s\n", result.VMID, result.State, result.Error)
			} else {
				fmt.Printf("failed: vmid=%d state=%s\n", result.VMID, result.State)
			}
		case "skipped":
			fmt.Printf("skipped: vmid=%d state=%s\n", result.VMID, result.State)
		}
	}
}

func runSandboxRevert(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox revert")
	opts := base
	opts.bind(fs)
	var force bool
	var noRestart bool
	var restart bool
	help := bindHelpFlag(fs)
	fs.BoolVar(&force, "force", false, "force revert even if a job is running")
	fs.BoolVar(&noRestart, "no-restart", false, "do not restart the sandbox after revert")
	fs.BoolVar(&restart, "restart", false, "restart the sandbox after revert")
	if err := parseFlags(fs, args, printSandboxRevertUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxRevertUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	if noRestart && restart {
		return fmt.Errorf("cannot use --restart and --no-restart together")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	var restartPtr *bool
	if noRestart {
		value := false
		restartPtr = &value
	}
	if restart {
		value := true
		restartPtr = &value
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	if !force {
		sb, err := fetchSandbox(ctx, client, vmid)
		if err != nil {
			return err
		}
		state := strings.TrimSpace(sb.State)
		if strings.EqualFold(state, "RUNNING") || strings.EqualFold(state, "READY") {
			if err := requireConfirmation(confirmOptions{
				action:     fmt.Sprintf("revert running sandbox %d", vmid),
				force:      force,
				jsonOutput: opts.jsonOutput,
			}); err != nil {
				return err
			}
		}
	}
	req := sandboxRevertRequest{Force: force, Restart: restartPtr}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/revert", vmid), req)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxRevertResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	state := resp.Sandbox.State
	snapshot := resp.Snapshot
	if snapshot == "" {
		snapshot = "clean"
	}
	if resp.Restarted {
		fmt.Printf("sandbox %d reverted to snapshot %s (state=%s, restarted)\n", resp.Sandbox.VMID, snapshot, state)
	} else {
		fmt.Printf("sandbox %d reverted to snapshot %s (state=%s)\n", resp.Sandbox.VMID, snapshot, state)
	}
	return nil
}

func runSandboxDestroy(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox destroy")
	opts := base
	opts.bind(fs)
	var force bool
	help := bindHelpFlag(fs)
	fs.BoolVar(&force, "force", false, "force destroy even if in invalid state")
	if err := parseFlags(fs, args, printSandboxDestroyUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxDestroyUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := sandboxDestroyRequest{Force: force}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/destroy", vmid), req)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sandboxResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d destroyed (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxLease(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printSandboxLeaseUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("sandbox lease command is required"), false)
	}
	if isHelpToken(args[0]) {
		printSandboxLeaseUsage()
		return errHelp
	}
	switch args[0] {
	case "renew":
		return runSandboxLeaseRenew(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printSandboxLeaseUsage()
		}
		return unknownSubcommandError("sandbox lease", args[0], []string{"renew"})
	}
}

func runSandboxLeaseRenew(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox lease renew")
	opts := base
	opts.bind(fs)
	var ttl string
	help := bindHelpFlag(fs)
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	if err := parseFlags(fs, args, printSandboxLeaseRenewUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxLeaseRenewUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	if ttl == "" {
		if !opts.jsonOutput {
			printSandboxLeaseRenewUsage()
		}
		return fmt.Errorf("ttl is required. Flags must come before vmid (e.g., --ttl 120 1009)")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	minutes, err := parseRequiredTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := leaseRenewRequest{TTLMinutes: minutes}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/lease/renew", vmid), req)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp leaseRenewResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("sandbox %d lease renewed until %s\n", resp.VMID, resp.LeaseExpires)
	return nil
}

func runSandboxPrune(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox prune")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxPruneUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sandboxes/prune", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp pruneResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	fmt.Printf("pruned %d sandbox(es)\n", resp.Count)
	return nil
}

func runSandboxExpose(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox expose")
	opts := base
	opts.bind(fs)
	var force bool
	help := bindHelpFlag(fs)
	fs.BoolVar(&force, "force", false, "skip confirmation prompt")
	if err := parseFlags(fs, args, printSandboxExposeUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		if !opts.jsonOutput {
			printSandboxExposeUsage()
		}
		return fmt.Errorf("vmid and port are required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	port, err := parseExposePort(fs.Arg(1))
	if err != nil {
		return err
	}
	if err := requireConfirmation(confirmOptions{
		action:     fmt.Sprintf("expose sandbox %d port %d", vmid, port),
		force:      force,
		jsonOutput: opts.jsonOutput,
	}); err != nil {
		return err
	}
	req := exposureCreateRequest{
		Name:  exposureName(vmid, port),
		VMID:  vmid,
		Port:  port,
		Force: force,
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/exposures", req)
	if err != nil {
		return wrapSandboxNotFound(ctx, client, vmid, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp exposureResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printExposure(resp)
	return nil
}

func runSandboxExposed(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox exposed")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxExposedUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/exposures", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp exposuresResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printExposureList(resp.Exposures)
	return nil
}

func runSandboxUnexpose(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox unexpose")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSandboxUnexposeUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxUnexposeUsage()
		}
		return fmt.Errorf("name is required")
	}
	name := strings.TrimSpace(fs.Arg(0))
	if name == "" {
		return fmt.Errorf("name is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/v1/exposures/%s", url.PathEscape(name)), nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp exposureResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printExposure(resp)
	return nil
}

func runSandboxDoctor(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox doctor")
	opts := base
	opts.bind(fs)
	var out string
	help := bindHelpFlag(fs)
	fs.StringVar(&out, "out", "", "output file path or directory")
	if err := parseFlags(fs, args, printSandboxDoctorUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSandboxDoctorUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	bundleName := defaultDoctorBundleName("sandbox", strconv.Itoa(vmid))
	path := fmt.Sprintf("/v1/sandboxes/%d/doctor", vmid)
	return downloadDoctorBundle(ctx, opts, path, out, bundleName, "sandbox", strconv.Itoa(vmid))
}

func runWorkspaceCreate(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace create")
	opts := base
	opts.bind(fs)
	var name string
	var size string
	var storage string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "workspace name")
	fs.StringVar(&size, "size", "", "workspace size (e.g. 80G)")
	fs.StringVar(&storage, "storage", "", "Proxmox storage (default local-zfs)")
	if err := parseFlags(fs, args, printWorkspaceCreateUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	size = strings.TrimSpace(size)
	storage = strings.TrimSpace(storage)
	if name == "" || size == "" {
		if !opts.jsonOutput {
			printWorkspaceCreateUsage()
		}
		return fmt.Errorf("name and size are required")
	}
	sizeGB, err := parseSizeGB(size)
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceCreateRequest{
		Name:    name,
		SizeGB:  sizeGB,
		Storage: storage,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/workspaces", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceListUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/workspaces", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspacesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceList(resp.Workspaces)
	return nil
}

func runWorkspaceCheck(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace check")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceCheckUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceCheckUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/workspaces/%s/check", workspace), nil)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceCheckResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceCheck(resp)
	return nil
}

func runWorkspaceFsck(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace fsck")
	opts := base
	opts.bind(fs)
	var repair bool
	help := bindHelpFlag(fs)
	fs.BoolVar(&repair, "repair", false, "repair filesystem (requires detached workspace)")
	if err := parseFlags(fs, args, printWorkspaceFsckUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceFsckUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceFsckRequest{Repair: repair}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/fsck", workspace), req)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceFsckResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceFsck(resp)
	return nil
}

func runWorkspaceAttach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace attach")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceAttachUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		if !opts.jsonOutput {
			printWorkspaceAttachUsage()
		}
		return fmt.Errorf("workspace and vmid are required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	vmid, err := parseVMID(fs.Arg(1))
	if err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceAttachRequest{VMID: vmid}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/attach", workspace), req)
	if err != nil {
		err = wrapSandboxNotFound(ctx, client, vmid, err)
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceDetach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace detach")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceDetachUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceDetachUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/detach", workspace), nil)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

func runWorkspaceRebind(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace rebind")
	opts := base
	opts.bind(fs)
	var profile string
	var ttl string
	var keepOld bool
	help := bindHelpFlag(fs)
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.BoolVar(&keepOld, "keep-old", false, "keep old sandbox running")
	if err := parseFlags(fs, args, printWorkspaceRebindUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceRebindUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		if !opts.jsonOutput {
			printWorkspaceRebindUsage()
		}
		return fmt.Errorf("profile is required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceRebindRequest{
		Profile:    profile,
		TTLMinutes: ttlMinutes,
		KeepOld:    keepOld,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/rebind", workspace), req)
	if err != nil {
		err = wrapUnknownProfileError(ctx, client, profile, err)
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceRebindResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceRebind(resp, keepOld)
	return nil
}

func runWorkspaceFork(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace fork")
	opts := base
	opts.bind(fs)
	var name string
	var fromSnapshot string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "new workspace name")
	fs.StringVar(&fromSnapshot, "from-snapshot", "", "snapshot name to fork from")
	if err := parseFlags(fs, args, printWorkspaceForkUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceForkUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	source := strings.TrimSpace(fs.Arg(0))
	if source == "" {
		return fmt.Errorf("workspace is required")
	}
	name = strings.TrimSpace(name)
	fromSnapshot = strings.TrimSpace(fromSnapshot)
	if name == "" {
		if !opts.jsonOutput {
			printWorkspaceForkUsage()
		}
		return fmt.Errorf("name is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceForkRequest{
		Name:         name,
		FromSnapshot: fromSnapshot,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/fork", url.PathEscape(source)), req)
	if err != nil {
		return wrapWorkspaceNotFound(source, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspace(resp)
	return nil
}

// runWorkspaceSnapshotCommand dispatches workspace snapshot subcommands.
func runWorkspaceSnapshotCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		if !base.jsonOutput {
			printWorkspaceSnapshotUsage()
			return nil
		}
		return newUsageError(fmt.Errorf("workspace snapshot command is required"), false)
	}
	if isHelpToken(args[0]) {
		printWorkspaceSnapshotUsage()
		return errHelp
	}
	switch args[0] {
	case "create":
		return runWorkspaceSnapshotCreate(ctx, args[1:], base)
	case "list":
		return runWorkspaceSnapshotList(ctx, args[1:], base)
	case "restore":
		return runWorkspaceSnapshotRestore(ctx, args[1:], base)
	default:
		if !base.jsonOutput {
			printWorkspaceSnapshotUsage()
		}
		return unknownSubcommandError("workspace snapshot", args[0], []string{"create", "list", "restore"})
	}
}

func runWorkspaceSnapshotCreate(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace snapshot create")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceSnapshotCreateUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		if !opts.jsonOutput {
			printWorkspaceSnapshotCreateUsage()
		}
		return fmt.Errorf("workspace and snapshot name are required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	name := strings.TrimSpace(fs.Arg(1))
	if workspace == "" || name == "" {
		return fmt.Errorf("workspace and snapshot name are required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := workspaceSnapshotCreateRequest{Name: name}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/snapshots", workspace), req)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceSnapshotResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceSnapshot(resp)
	return nil
}

func runWorkspaceSnapshotList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace snapshot list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceSnapshotListUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printWorkspaceSnapshotListUsage()
		}
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/workspaces/%s/snapshots", workspace), nil)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceSnapshotsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceSnapshotList(resp.Snapshots)
	return nil
}

func runWorkspaceSnapshotRestore(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace snapshot restore")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printWorkspaceSnapshotRestoreUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		if !opts.jsonOutput {
			printWorkspaceSnapshotRestoreUsage()
		}
		return fmt.Errorf("workspace and snapshot name are required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	name := strings.TrimSpace(fs.Arg(1))
	if workspace == "" || name == "" {
		return fmt.Errorf("workspace and snapshot name are required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/snapshots/%s/restore", workspace, name), nil)
	if err != nil {
		return wrapWorkspaceNotFound(workspace, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp workspaceSnapshotResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printWorkspaceSnapshot(resp)
	return nil
}

func runSessionCreate(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session create")
	opts := base
	opts.bind(fs)
	var name string
	var profile string
	var branch string
	var workspace string
	var workspaceCreate string
	var workspaceSize string
	var workspaceStorage string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "session name")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&branch, "branch", "", "branch label")
	fs.StringVar(&workspace, "workspace", "", "workspace id or name (or new:<name>)")
	fs.StringVar(&workspaceCreate, "workspace-create", "", "create workspace with name")
	fs.StringVar(&workspaceSize, "workspace-size", "", "workspace size for creation (e.g. 80G)")
	fs.StringVar(&workspaceStorage, "workspace-storage", "", "workspace storage (default local-zfs)")
	if err := parseFlags(fs, args, printSessionCreateUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	profile = strings.TrimSpace(profile)
	branch = strings.TrimSpace(branch)
	if name == "" {
		if !opts.jsonOutput {
			printSessionCreateUsage()
		}
		return fmt.Errorf("name is required")
	}
	if profile == "" {
		if !opts.jsonOutput {
			printSessionCreateUsage()
		}
		return fmt.Errorf("profile is required")
	}
	workspaceID, workspaceCreateReq, err := parseWorkspaceSelection(workspace, workspaceCreate, workspaceSize, workspaceStorage)
	if err != nil {
		if !opts.jsonOutput {
			printSessionCreateUsage()
		}
		return err
	}
	if workspaceID == nil && workspaceCreateReq == nil {
		if !opts.jsonOutput {
			printSessionCreateUsage()
		}
		return fmt.Errorf("workspace is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := sessionCreateRequest{
		Name:            name,
		Profile:         profile,
		WorkspaceID:     workspaceID,
		WorkspaceCreate: workspaceCreateReq,
		Branch:          branch,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sessions", req)
	if err != nil {
		err = wrapUnknownProfileError(ctx, client, profile, err)
		return wrapWorkspaceNotFound(workspaceTargetName(workspaceID, workspaceCreateReq), err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSession(resp)
	return nil
}

func runSessionList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSessionListUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sessions", nil)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSessionList(resp.Sessions)
	return nil
}

func runSessionShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session show")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSessionShowUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionShowUsage()
		}
		return fmt.Errorf("session is required")
	}
	sessionID := strings.TrimSpace(fs.Arg(0))
	if sessionID == "" {
		return fmt.Errorf("session is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return wrapSessionNotFound(sessionID, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSession(resp)
	return nil
}

func runSessionResume(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session resume")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSessionResumeUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionResumeUsage()
		}
		return fmt.Errorf("session is required")
	}
	sessionID := strings.TrimSpace(fs.Arg(0))
	if sessionID == "" {
		return fmt.Errorf("session is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/resume", nil)
	if err != nil {
		return wrapSessionNotFound(sessionID, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionResumeResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSessionResume(resp)
	return nil
}

func runSessionStop(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session stop")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, printSessionStopUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionStopUsage()
		}
		return fmt.Errorf("session is required")
	}
	sessionID := strings.TrimSpace(fs.Arg(0))
	if sessionID == "" {
		return fmt.Errorf("session is required")
	}
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/stop", nil)
	if err != nil {
		return wrapSessionNotFound(sessionID, err)
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSession(resp)
	return nil
}

func runSessionFork(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session fork")
	opts := base
	opts.bind(fs)
	var name string
	var profile string
	var branch string
	var workspace string
	var workspaceCreate string
	var workspaceSize string
	var workspaceStorage string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "new session name")
	fs.StringVar(&profile, "profile", "", "profile name (defaults to source session)")
	fs.StringVar(&branch, "branch", "", "branch label")
	fs.StringVar(&workspace, "workspace", "", "workspace id or name (or new:<name>)")
	fs.StringVar(&workspaceCreate, "workspace-create", "", "create workspace with name")
	fs.StringVar(&workspaceSize, "workspace-size", "", "workspace size for creation (e.g. 80G)")
	fs.StringVar(&workspaceStorage, "workspace-storage", "", "workspace storage (default local-zfs)")
	if err := parseFlags(fs, args, printSessionForkUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionForkUsage()
		}
		return fmt.Errorf("session is required")
	}
	sourceID := strings.TrimSpace(fs.Arg(0))
	if sourceID == "" {
		return fmt.Errorf("session is required")
	}
	name = strings.TrimSpace(name)
	profile = strings.TrimSpace(profile)
	branch = strings.TrimSpace(branch)
	if name == "" {
		if !opts.jsonOutput {
			printSessionForkUsage()
		}
		return fmt.Errorf("name is required")
	}
	workspaceID, workspaceCreateReq, err := parseWorkspaceSelection(workspace, workspaceCreate, workspaceSize, workspaceStorage)
	if err != nil {
		if !opts.jsonOutput {
			printSessionForkUsage()
		}
		return err
	}
	if workspaceID == nil && workspaceCreateReq == nil {
		if !opts.jsonOutput {
			printSessionForkUsage()
		}
		return fmt.Errorf("workspace is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := sessionForkRequest{
		Name:            name,
		Profile:         profile,
		WorkspaceID:     workspaceID,
		WorkspaceCreate: workspaceCreateReq,
		Branch:          branch,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sourceID)+"/fork", req)
	if err != nil {
		err = wrapUnknownProfileError(ctx, client, profile, err)
		return wrapSessionNotFound(sourceID, wrapWorkspaceNotFound(workspaceTargetName(workspaceID, workspaceCreateReq), err))
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, payload)
	}
	var resp sessionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	printSession(resp)
	return nil
}

func runSessionBranch(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session branch")
	opts := base
	opts.bind(fs)
	var profile string
	var workspace string
	var workspaceCreate string
	var workspaceSize string
	var workspaceStorage string
	help := bindHelpFlag(fs)
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&workspace, "workspace", "", "workspace id or name (or new:<name>)")
	fs.StringVar(&workspaceCreate, "workspace-create", "", "create workspace with name")
	fs.StringVar(&workspaceSize, "workspace-size", "", "workspace size for creation (e.g. 80G)")
	fs.StringVar(&workspaceStorage, "workspace-storage", "", "workspace storage (default local-zfs)")
	if err := parseFlags(fs, args, printSessionBranchUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionBranchUsage()
		}
		return fmt.Errorf("branch is required")
	}
	branch := strings.TrimSpace(fs.Arg(0))
	if branch == "" {
		return fmt.Errorf("branch is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	session, err := resolveBranchSession(ctx, client, branch, profile, workspace, workspaceCreate, workspaceSize, workspaceStorage)
	if err != nil {
		if !opts.jsonOutput {
			printSessionBranchUsage()
		}
		return wrapUnknownProfileError(ctx, client, profile, err)
	}
	if opts.jsonOutput {
		payload, err := json.Marshal(session)
		if err != nil {
			return err
		}
		return prettyPrintJSON(os.Stdout, payload)
	}
	printSession(session)
	return nil
}

func runSessionDoctor(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("session doctor")
	opts := base
	opts.bind(fs)
	var out string
	help := bindHelpFlag(fs)
	fs.StringVar(&out, "out", "", "output file path or directory")
	if err := parseFlags(fs, args, printSessionDoctorUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printSessionDoctorUsage()
		}
		return fmt.Errorf("session id or name is required")
	}
	sessionID := strings.TrimSpace(fs.Arg(0))
	if sessionID == "" {
		return fmt.Errorf("session id or name is required")
	}
	bundleName := defaultDoctorBundleName("session", sessionID)
	path := fmt.Sprintf("/v1/sessions/%s/doctor", url.PathEscape(sessionID))
	return downloadDoctorBundle(ctx, opts, path, out, bundleName, "session", sessionID)
}

func resolveBranchSession(ctx context.Context, client *apiClient, branch, profile, workspace, workspaceCreate, workspaceSize, workspaceStorage string) (sessionResponse, error) {
	if client == nil {
		return sessionResponse{}, fmt.Errorf("client is required")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return sessionResponse{}, fmt.Errorf("branch is required")
	}
	sessionName, err := sessionNameFromBranch(branch)
	if err != nil {
		return sessionResponse{}, err
	}
	workspace = strings.TrimSpace(workspace)
	workspaceCreate = strings.TrimSpace(workspaceCreate)
	workspaceSize = strings.TrimSpace(workspaceSize)
	workspaceStorage = strings.TrimSpace(workspaceStorage)
	selectionProvided := workspace != "" || workspaceCreate != "" || workspaceSize != "" || workspaceStorage != ""

	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionName), nil)
	if err == nil {
		var resp sessionResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return sessionResponse{}, err
		}
		if resp.Branch != "" && resp.Branch != branch {
			return sessionResponse{}, fmt.Errorf("branch %q maps to session %q labeled %q", branch, resp.Name, resp.Branch)
		}
		if selectionProvided {
			return sessionResponse{}, fmt.Errorf("session %q already exists; omit workspace flags or use session fork", resp.Name)
		}
		return resp, nil
	}
	if !isNotFoundError(err, "session") {
		return sessionResponse{}, err
	}

	profile = strings.TrimSpace(profile)
	if profile == "" {
		return sessionResponse{}, fmt.Errorf("profile is required when creating a session")
	}
	var workspaceID *string
	var workspaceCreateReq *workspaceCreateRequest
	if workspace != "" || workspaceCreate != "" {
		workspaceID, workspaceCreateReq, err = parseWorkspaceSelection(workspace, workspaceCreate, workspaceSize, workspaceStorage)
		if err != nil {
			return sessionResponse{}, err
		}
	} else {
		sizeGB := defaultStatefulWorkspaceSizeGB
		if workspaceSize != "" {
			sizeGB, err = parseSizeGB(workspaceSize)
			if err != nil {
				return sessionResponse{}, err
			}
		}
		storage := workspaceStorage
		if storage == "" {
			storage = defaultStatefulWorkspaceStorage
		}
		workspaceCreateReq = &workspaceCreateRequest{
			Name:    sessionName,
			SizeGB:  sizeGB,
			Storage: storage,
		}
	}
	if workspaceID == nil && workspaceCreateReq == nil {
		return sessionResponse{}, fmt.Errorf("workspace is required")
	}
	req := sessionCreateRequest{
		Name:            sessionName,
		Profile:         profile,
		WorkspaceID:     workspaceID,
		WorkspaceCreate: workspaceCreateReq,
		Branch:          branch,
	}
	payload, err = client.doJSON(ctx, http.MethodPost, "/v1/sessions", req)
	if err != nil {
		return sessionResponse{}, err
	}
	var resp sessionResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return sessionResponse{}, err
	}
	return resp, nil
}

func runMsgPost(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("msg post")
	opts := base
	opts.bind(fs)
	var jobID string
	var workspaceID string
	var sessionID string
	var author string
	var kind string
	var text string
	var payload string
	help := bindHelpFlag(fs)
	fs.StringVar(&jobID, "job", "", "message scope job id")
	fs.StringVar(&workspaceID, "workspace", "", "message scope workspace id")
	fs.StringVar(&sessionID, "session", "", "message scope session id")
	fs.StringVar(&author, "author", "", "message author")
	fs.StringVar(&kind, "kind", "", "message kind")
	fs.StringVar(&text, "text", "", "message text")
	fs.StringVar(&payload, "payload", "", "message json payload")
	if err := parseFlags(fs, args, printMsgPostUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	scopeType, scopeID, err := resolveMessageScope(jobID, workspaceID, sessionID)
	if err != nil {
		if !opts.jsonOutput {
			printMsgPostUsage()
		}
		return err
	}
	if strings.TrimSpace(text) == "" && fs.NArg() > 0 {
		text = strings.Join(fs.Args(), " ")
	}
	text = strings.TrimSpace(text)
	payload = strings.TrimSpace(payload)
	var rawPayload json.RawMessage
	if payload != "" {
		if !json.Valid([]byte(payload)) {
			return fmt.Errorf("payload must be valid JSON")
		}
		rawPayload = json.RawMessage(payload)
	}
	if text == "" && len(rawPayload) == 0 {
		return fmt.Errorf("text or payload is required")
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	req := messageCreateRequest{
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Author:    strings.TrimSpace(author),
		Kind:      strings.TrimSpace(kind),
		Text:      text,
		Payload:   rawPayload,
	}
	respPayload, err := client.doJSON(ctx, http.MethodPost, "/v1/messages", req)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return prettyPrintJSON(os.Stdout, respPayload)
	}
	var resp messageResponse
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		return err
	}
	printMessages([]messageResponse{resp}, false)
	return nil
}

func runMsgTail(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("msg tail")
	opts := base
	opts.bind(fs)
	var jobID string
	var workspaceID string
	var sessionID string
	var follow bool
	var tail int
	help := bindHelpFlag(fs)
	fs.StringVar(&jobID, "job", "", "message scope job id")
	fs.StringVar(&workspaceID, "workspace", "", "message scope workspace id")
	fs.StringVar(&sessionID, "session", "", "message scope session id")
	fs.BoolVar(&follow, "follow", false, "follow new messages")
	fs.IntVar(&tail, "tail", defaultMessageTail, "show the last N messages")
	if err := parseFlags(fs, args, printMsgTailUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	scopeType, scopeID, err := resolveMessageScope(jobID, workspaceID, sessionID)
	if err != nil {
		if !opts.jsonOutput {
			printMsgTailUsage()
		}
		return err
	}
	if tail <= 0 {
		tail = defaultMessageTail
	}
	if tail > maxMessageLimit {
		tail = maxMessageLimit
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	resp, err := fetchMessages(ctx, client, scopeType, scopeID, tail, 0)
	if err != nil {
		return err
	}
	lastID := printMessages(resp.Messages, opts.jsonOutput)
	if resp.LastID > lastID {
		lastID = resp.LastID
	}
	if !follow {
		return nil
	}
	if lastID == 0 {
		lastID = resp.LastID
	}

	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			resp, err := fetchMessages(ctx, client, scopeType, scopeID, 0, lastID)
			if err != nil {
				return err
			}
			latest := printMessages(resp.Messages, opts.jsonOutput)
			if latest > lastID {
				lastID = latest
			}
			if resp.LastID > lastID {
				lastID = resp.LastID
			}
		}
	}
}

// runLogsCommand displays sandbox event logs, with optional follow mode.
func runLogsCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("logs")
	opts := base
	opts.bind(fs)
	var follow bool
	var tail int
	help := bindHelpFlag(fs)
	fs.BoolVar(&follow, "follow", false, "follow new events")
	fs.IntVar(&tail, "tail", defaultLogTail, "show the last N events")
	if err := parseFlags(fs, args, printLogsUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		if !opts.jsonOutput {
			printLogsUsage()
		}
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	if tail <= 0 {
		tail = defaultLogTail
	}
	if tail > maxEventLimit {
		tail = maxEventLimit
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	touchSandboxBestEffort(ctx, client, vmid)
	resp, err := fetchEvents(ctx, client, vmid, tail, 0)
	if err != nil {
		return err
	}
	lastID := printEvents(resp.Events, opts.jsonOutput)
	if resp.LastID > lastID {
		lastID = resp.LastID
	}
	if !follow {
		return nil
	}
	if lastID == 0 {
		lastID = resp.LastID
	}

	ticker := time.NewTicker(eventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			resp, err := fetchEvents(ctx, client, vmid, 0, lastID)
			if err != nil {
				return err
			}
			latest := printEvents(resp.Events, opts.jsonOutput)
			if latest > lastID {
				lastID = latest
			}
			if resp.LastID > lastID {
				lastID = resp.LastID
			}
		}
	}
}

// fetchEvents retrieves sandbox events from the daemon.
// If tail > 0, fetches the last N events. If after > 0, fetches events after the given ID.
func fetchEvents(ctx context.Context, client *apiClient, vmid int, tail int, after int64) (eventsResponse, error) {
	query := ""
	limit := defaultEventLimit
	if limit > maxEventLimit {
		limit = maxEventLimit
	}
	if tail > 0 {
		query = fmt.Sprintf("?tail=%d", tail)
	} else if after > 0 {
		query = fmt.Sprintf("?after=%d&limit=%d", after, limit)
	} else {
		query = fmt.Sprintf("?limit=%d", limit)
	}
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d/events%s", vmid, query), nil)
	if err != nil {
		return eventsResponse{}, wrapSandboxNotFound(ctx, client, vmid, err)
	}
	var resp eventsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return eventsResponse{}, err
	}
	return resp, nil
}

// fetchMessages retrieves messagebox entries for a scope.
// If tail > 0, fetches the last N messages. If after > 0, fetches messages after the given ID.
func fetchMessages(ctx context.Context, client *apiClient, scopeType, scopeID string, tail int, after int64) (messagesResponse, error) {
	values := url.Values{}
	values.Set("scope_type", scopeType)
	values.Set("scope_id", scopeID)
	limit := defaultMessageLimit
	if limit > maxMessageLimit {
		limit = maxMessageLimit
	}
	if tail > 0 {
		values.Set("limit", strconv.Itoa(tail))
	} else if after > 0 {
		values.Set("after_id", fmt.Sprintf("%d", after))
		values.Set("limit", strconv.Itoa(limit))
	} else {
		values.Set("limit", strconv.Itoa(limit))
	}
	payload, err := client.doJSON(ctx, http.MethodGet, "/v1/messages?"+values.Encode(), nil)
	if err != nil {
		return messagesResponse{}, err
	}
	var resp messagesResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return messagesResponse{}, err
	}
	return resp, nil
}

func printSandbox(sb sandboxResponse) {
	fmt.Printf("VMID: %d\n", sb.VMID)
	fmt.Printf("Name: %s\n", sb.Name)
	fmt.Printf("Profile: %s\n", sb.Profile)
	fmt.Printf("State: %s\n", sb.State)
	fmt.Printf("IP: %s\n", orDash(sb.IP))
	fmt.Printf("Workspace: %s\n", orDashPtr(sb.WorkspaceID))
	mode := "-"
	firewall := "-"
	firewallGroup := "-"
	if sb.Network != nil {
		mode = orDash(sb.Network.Mode)
		firewall = orDashBoolPtr(sb.Network.Firewall)
		firewallGroup = orDash(sb.Network.FirewallGroup)
	}
	fmt.Printf("Network Mode: %s\n", mode)
	fmt.Printf("Firewall: %s\n", firewall)
	fmt.Printf("Firewall Group: %s\n", firewallGroup)
	fmt.Printf("Keepalive: %t\n", sb.Keepalive)
	fmt.Printf("Lease Expires: %s\n", orDashPtr(sb.LeaseExpires))
	fmt.Printf("Last Used At: %s\n", orDashPtr(sb.LastUsedAt))
	fmt.Printf("Created At: %s\n", sb.CreatedAt)
	fmt.Printf("Updated At: %s\n", sb.LastUpdatedAt)
}

func printStatus(resp statusResponse) {
	fmt.Println("Sandboxes:")
	printCountTable("STATE", resp.Sandboxes, statusSandboxOrder)
	if len(resp.NetworkModes) > 0 {
		fmt.Println("Network Modes:")
		printCountTable("MODE", resp.NetworkModes, statusNetworkModeOrder)
	}
	fmt.Println("Jobs:")
	printCountTable("STATUS", resp.Jobs, statusJobOrder)
	fmt.Println("Artifacts:")
	fmt.Printf("Root: %s\n", orDash(resp.Artifacts.Root))
	fmt.Printf("Total Bytes: %d\n", resp.Artifacts.TotalBytes)
	fmt.Printf("Free Bytes: %d\n", resp.Artifacts.FreeBytes)
	fmt.Printf("Used Bytes: %d\n", resp.Artifacts.UsedBytes)
	fmt.Printf("Error: %s\n", orDash(resp.Artifacts.Error))
	fmt.Println("Metrics:")
	fmt.Printf("Enabled: %t\n", resp.Metrics.Enabled)
	fmt.Println("Recent Failures:")
	if len(resp.RecentFailures) == 0 {
		fmt.Println("-")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tKIND\tJOB\tSANDBOX\tMESSAGE")
	for _, ev := range resp.RecentFailures {
		job := strings.TrimSpace(ev.JobID)
		if job == "" {
			job = "-"
		}
		msg := strings.TrimSpace(ev.Message)
		if msg == "" {
			msg = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			orDash(ev.Timestamp),
			orDash(ev.Kind),
			job,
			vmidString(ev.SandboxVMID),
			msg,
		)
	}
	_ = w.Flush()
}

func printSandboxList(sandboxes []sandboxResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "VMID\tNAME\tPROFILE\tSTATE\tIP\tMODE\tFWGROUP\tLEASE\tLAST USED")
	for _, sb := range sandboxes {
		lease := orDashPtr(sb.LeaseExpires)
		lastUsed := orDashPtr(sb.LastUsedAt)
		mode := "-"
		firewallGroup := "-"
		if sb.Network != nil {
			mode = orDash(sb.Network.Mode)
			firewallGroup = orDash(sb.Network.FirewallGroup)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", sb.VMID, sb.Name, sb.Profile, sb.State, orDash(sb.IP), mode, firewallGroup, lease, lastUsed)
	}
	_ = w.Flush()
}

func printExposure(exposure exposureResponse) {
	fmt.Printf("Name: %s\n", exposure.Name)
	fmt.Printf("VMID: %d\n", exposure.VMID)
	fmt.Printf("Port: %d\n", exposure.Port)
	fmt.Printf("Target IP: %s\n", orDash(exposure.TargetIP))
	fmt.Printf("URL: %s\n", orDash(exposure.URL))
	fmt.Printf("State: %s\n", orDash(exposure.State))
	fmt.Printf("Created At: %s\n", orDash(exposure.CreatedAt))
	fmt.Printf("Updated At: %s\n", orDash(exposure.UpdatedAt))
}

func printExposureList(exposures []exposureResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVMID\tPORT\tTARGET\tURL\tSTATE\tUPDATED")
	for _, exposure := range exposures {
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\t%s\n",
			orDash(exposure.Name),
			exposure.VMID,
			exposure.Port,
			orDash(exposure.TargetIP),
			orDash(exposure.URL),
			orDash(exposure.State),
			orDash(exposure.UpdatedAt),
		)
	}
	_ = w.Flush()
}

func printWorkspace(ws workspaceResponse) {
	fmt.Printf("ID: %s\n", ws.ID)
	fmt.Printf("Name: %s\n", ws.Name)
	fmt.Printf("Storage: %s\n", ws.Storage)
	fmt.Printf("Volume ID: %s\n", ws.VolumeID)
	fmt.Printf("Size GB: %d\n", ws.SizeGB)
	fmt.Printf("Attached VMID: %s\n", vmidString(ws.AttachedVMID))
	fmt.Printf("Created At: %s\n", ws.CreatedAt)
	fmt.Printf("Updated At: %s\n", ws.UpdatedAt)
}

func printWorkspaceList(workspaces []workspaceResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSIZE(GB)\tSTORAGE\tATTACHED")
	for _, ws := range workspaces {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", ws.ID, ws.Name, ws.SizeGB, ws.Storage, vmidString(ws.AttachedVMID))
	}
	_ = w.Flush()
}

func printWorkspaceSnapshot(snapshot workspaceSnapshotResponse) {
	fmt.Printf("Workspace: %s\n", snapshot.WorkspaceID)
	fmt.Printf("Snapshot: %s\n", snapshot.Name)
	fmt.Printf("Backend Ref: %s\n", snapshot.BackendRef)
	fmt.Printf("Created At: %s\n", snapshot.CreatedAt)
}

func printWorkspaceSnapshotList(snapshots []workspaceSnapshotResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCREATED\tBACKEND_REF")
	for _, snapshot := range snapshots {
		fmt.Fprintf(w, "%s\t%s\t%s\n", snapshot.Name, snapshot.CreatedAt, snapshot.BackendRef)
	}
	_ = w.Flush()
}

func printSession(session sessionResponse) {
	fmt.Printf("ID: %s\n", session.ID)
	fmt.Printf("Name: %s\n", session.Name)
	fmt.Printf("Workspace: %s\n", session.WorkspaceID)
	fmt.Printf("Current VMID: %s\n", vmidString(session.CurrentVMID))
	fmt.Printf("Profile: %s\n", session.Profile)
	fmt.Printf("Branch: %s\n", orDash(session.Branch))
	fmt.Printf("Created At: %s\n", orDash(session.CreatedAt))
	fmt.Printf("Updated At: %s\n", orDash(session.UpdatedAt))
}

func printSessionList(sessions []sessionResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tWORKSPACE\tVMID\tPROFILE\tBRANCH\tUPDATED")
	for _, session := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			orDash(session.ID),
			orDash(session.Name),
			orDash(session.WorkspaceID),
			vmidString(session.CurrentVMID),
			orDash(session.Profile),
			orDash(session.Branch),
			orDash(session.UpdatedAt),
		)
	}
	_ = w.Flush()
}

func printSessionResume(resp sessionResumeResponse) {
	fmt.Printf("Session: %s\n", resp.Session.Name)
	fmt.Printf("Workspace: %s\n", resp.Workspace.Name)
	fmt.Printf("New VMID: %d\n", resp.Sandbox.VMID)
	fmt.Printf("New IP: %s\n", orDash(resp.Sandbox.IP))
	if resp.OldVMID != nil {
		fmt.Printf("Old VMID: %d (destroyed)\n", *resp.OldVMID)
	}
}

func printWorkspaceCheck(resp workspaceCheckResponse) {
	ws := resp.Workspace
	fmt.Printf("Workspace: %s\n", ws.Name)
	fmt.Printf("ID: %s\n", ws.ID)
	fmt.Printf("Storage: %s\n", ws.Storage)
	fmt.Printf("Volume ID: %s\n", ws.VolumeID)
	fmt.Printf("Volume Exists: %t\n", resp.Volume.Exists)
	fmt.Printf("Volume Path: %s\n", orDash(resp.Volume.Path))
	fmt.Printf("Attached VMID: %s\n", vmidString(ws.AttachedVMID))
	fmt.Printf("Checked At: %s\n", resp.CheckedAt)
	if len(resp.Findings) == 0 {
		fmt.Println("Findings: none")
		return
	}
	fmt.Println("Findings:")
	for i, finding := range resp.Findings {
		sev := strings.ToUpper(strings.TrimSpace(finding.Severity))
		if sev == "" {
			sev = "INFO"
		}
		fmt.Printf("%d. %s: %s\n", i+1, sev, finding.Message)
		if details := formatWorkspaceCheckDetails(finding.Details); details != "" {
			fmt.Printf("Details: %s\n", details)
		}
		for _, fix := range finding.Remediation {
			switch {
			case strings.TrimSpace(fix.Command) != "":
				fmt.Printf("Suggested fix: %s\n", fix.Command)
			case strings.TrimSpace(fix.Note) != "":
				fmt.Printf("Suggested fix: %s\n", fix.Note)
			case strings.TrimSpace(fix.Action) != "":
				fmt.Printf("Suggested fix: %s\n", fix.Action)
			}
		}
	}
}

func printWorkspaceFsck(resp workspaceFsckResponse) {
	ws := resp.Workspace
	fmt.Printf("Workspace: %s\n", ws.Name)
	fmt.Printf("ID: %s\n", ws.ID)
	fmt.Printf("Method: %s\n", orDash(resp.Method))
	fmt.Printf("Mode: %s\n", strings.ToUpper(resp.Mode))
	fmt.Printf("Status: %s\n", strings.ToUpper(resp.Status))
	fmt.Printf("Exit Code: %d\n", resp.ExitCode)
	if strings.TrimSpace(resp.ExitSummary) != "" {
		fmt.Printf("Exit Summary: %s\n", resp.ExitSummary)
	}
	fmt.Printf("Volume Path: %s\n", orDash(resp.Volume.Path))
	fmt.Printf("Attached VMID: %s\n", vmidString(ws.AttachedVMID))
	if strings.TrimSpace(resp.StartedAt) != "" {
		fmt.Printf("Started At: %s\n", resp.StartedAt)
	}
	if strings.TrimSpace(resp.CompletedAt) != "" {
		fmt.Printf("Completed At: %s\n", resp.CompletedAt)
	}
	if resp.RebootRequired {
		fmt.Println("Note: Filesystem recommends a reboot.")
	}
	if resp.NeedsRepair && strings.EqualFold(resp.Mode, "read-only") {
		fmt.Printf("Remediation: agentlab workspace fsck %s --repair\n", workspaceReferenceForCLI(resp.Workspace))
	}
	if strings.TrimSpace(resp.Output) != "" {
		fmt.Println("Output:")
		fmt.Println(resp.Output)
	}
}

func printProfileList(profiles []profileResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTEMPLATE\tUPDATED")
	for _, profile := range profiles {
		fmt.Fprintf(w, "%s\t%d\t%s\n", orDash(profile.Name), profile.TemplateVMID, orDash(profile.UpdatedAt))
	}
	_ = w.Flush()
}

func printWorkspaceRebind(resp workspaceRebindResponse, keepOld bool) {
	fmt.Printf("Workspace: %s\n", resp.Workspace.Name)
	fmt.Printf("New VMID: %d\n", resp.Sandbox.VMID)
	fmt.Printf("New IP: %s\n", orDash(resp.Sandbox.IP))
	if resp.OldVMID != nil {
		status := "destroyed"
		if keepOld {
			status = "kept"
		}
		fmt.Printf("Old VMID: %d (%s)\n", *resp.OldVMID, status)
	}
}

func printJob(job jobResponse) {
	fmt.Printf("Job ID: %s\n", job.ID)
	fmt.Printf("Repo: %s\n", job.RepoURL)
	fmt.Printf("Ref: %s\n", job.Ref)
	fmt.Printf("Profile: %s\n", job.Profile)
	fmt.Printf("Task: %s\n", job.Task)
	fmt.Printf("Mode: %s\n", job.Mode)
	fmt.Printf("Status: %s\n", job.Status)
	fmt.Printf("Keepalive: %t\n", job.Keepalive)
	fmt.Printf("TTL Minutes: %s\n", ttlMinutesString(job.TTLMinutes))
	fmt.Printf("Sandbox VMID: %s\n", vmidString(job.SandboxVMID))
	fmt.Printf("Created At: %s\n", job.CreatedAt)
	fmt.Printf("Updated At: %s\n", job.UpdatedAt)
}

func printArtifactsList(artifacts []artifactInfo) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tSIZE(B)\tMIME\tCREATED\tSHA256")
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(artifact.Path))
		}
		sha := strings.TrimSpace(artifact.Sha256)
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			orDash(name),
			orDash(artifact.Path),
			artifact.SizeBytes,
			orDash(artifact.MIME),
			orDash(artifact.CreatedAt),
			orDash(sha),
		)
	}
	_ = w.Flush()
}

func printMessages(messages []messageResponse, jsonOutput bool) int64 {
	var lastID int64
	for _, msg := range messages {
		if msg.ID > lastID {
			lastID = msg.ID
		}
		if jsonOutput {
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			_, _ = os.Stdout.Write(append(data, '\n'))
			continue
		}
		author := strings.TrimSpace(msg.Author)
		if author == "" {
			author = "-"
		}
		kind := strings.TrimSpace(msg.Kind)
		if kind == "" {
			kind = "-"
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			text = "-"
		}
		fmt.Printf("%s\t%d\t%s\t%s\t%s\n", orDash(msg.Timestamp), msg.ID, author, kind, text)
	}
	return lastID
}

func printEvents(events []eventResponse, jsonOutput bool) int64 {
	var lastID int64
	for _, ev := range events {
		if ev.ID > lastID {
			lastID = ev.ID
		}
		if jsonOutput {
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = os.Stdout.Write(append(data, '\n'))
			continue
		}
		job := "-"
		if strings.TrimSpace(ev.JobID) != "" {
			job = ev.JobID
		}
		msg := strings.TrimSpace(ev.Message)
		if msg == "" {
			msg = "-"
		}
		fmt.Printf("%s\t%s\tjob=%s\t%s\n", ev.Timestamp, ev.Kind, job, msg)
	}
	return lastID
}

func resolveMessageScope(jobID, workspaceID, sessionID string) (string, string, error) {
	jobID = strings.TrimSpace(jobID)
	workspaceID = strings.TrimSpace(workspaceID)
	sessionID = strings.TrimSpace(sessionID)

	var scopeType string
	var scopeID string
	setScope := func(candidateType, candidateID string) error {
		if candidateID == "" {
			return nil
		}
		if scopeType != "" {
			return fmt.Errorf("only one of --job, --workspace, or --session may be set")
		}
		scopeType = candidateType
		scopeID = candidateID
		return nil
	}
	if err := setScope("job", jobID); err != nil {
		return "", "", err
	}
	if err := setScope("workspace", workspaceID); err != nil {
		return "", "", err
	}
	if err := setScope("session", sessionID); err != nil {
		return "", "", err
	}
	if scopeType == "" {
		return "", "", fmt.Errorf("scope is required (use --job, --workspace, or --session)")
	}
	return scopeType, scopeID, nil
}

func printCountTable(label string, counts map[string]int, order []string) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintf(w, "%s\tCOUNT\n", label)
	seen := make(map[string]struct{}, len(counts))
	for _, key := range order {
		if count, ok := counts[key]; ok {
			fmt.Fprintf(w, "%s\t%d\n", key, count)
			seen[key] = struct{}{}
		}
	}
	var extra []string
	for key := range counts {
		if _, ok := seen[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		fmt.Fprintf(w, "%s\t%d\n", key, counts[key])
	}
	_ = w.Flush()
}

// selectArtifact selects an artifact from a list based on the provided criteria.
// Checks path, name, bundle, and latest in that order.
func selectArtifact(artifacts []artifactInfo, path, name string, latest, bundle bool) (artifactInfo, error) {
	if len(artifacts) == 0 {
		return artifactInfo{}, fmt.Errorf("no artifacts found")
	}
	if path != "" {
		for _, artifact := range artifacts {
			if strings.TrimSpace(artifact.Path) == path {
				return artifact, nil
			}
		}
		return artifactInfo{}, fmt.Errorf("artifact path %q not found", path)
	}
	if name != "" {
		if strings.ContainsAny(name, "/\\") {
			return artifactInfo{}, fmt.Errorf("artifact name must not contain path separators")
		}
		var match *artifactInfo
		for i := range artifacts {
			if artifacts[i].Name == name {
				match = &artifacts[i]
			}
		}
		if match == nil {
			return artifactInfo{}, fmt.Errorf("artifact name %q not found", name)
		}
		return *match, nil
	}
	if bundle {
		var match *artifactInfo
		for i := range artifacts {
			if artifacts[i].Name == defaultArtifactBundleName {
				match = &artifacts[i]
			}
		}
		if match != nil {
			return *match, nil
		}
	}
	if latest || bundle {
		return artifacts[len(artifacts)-1], nil
	}
	return artifacts[len(artifacts)-1], nil
}

func resolveArtifactOutPath(out, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "artifact"
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return name, nil
	}
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		if err := os.MkdirAll(out, 0o750); err != nil {
			return "", err
		}
		return filepath.Join(out, name), nil
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		return filepath.Join(out, name), nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(out)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", err
		}
	}
	return out, nil
}

func defaultDoctorBundleName(kind, id string) string {
	kind = strings.TrimSpace(kind)
	safeID := slugifyWorkspaceName(id)
	if safeID == "" {
		safeID = "bundle"
	}
	if kind == "" {
		return fmt.Sprintf("%s-%s.tar.gz", defaultDoctorBundlePrefix, safeID)
	}
	return fmt.Sprintf("%s-%s-%s.tar.gz", defaultDoctorBundlePrefix, kind, safeID)
}

func downloadDoctorBundle(ctx context.Context, opts commonFlags, path, out, bundleName, kind, id string) error {
	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	targetPath, err := resolveArtifactOutPath(out, bundleName)
	if err != nil {
		return err
	}
	respBody, err := client.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return err
	}
	defer respBody.Body.Close()

	outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, respBody.Body); err != nil {
		return err
	}
	if err := outFile.Sync(); err != nil {
		return err
	}

	if opts.jsonOutput {
		result := map[string]any{
			"kind": kind,
			"id":   id,
			"out":  targetPath,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, _ = os.Stdout.Write(append(data, '\n'))
		return nil
	}
	fmt.Printf("doctor bundle saved to %s\n", targetPath)
	return nil
}

// parseVMID parses and validates a VM ID from a string.
func parseVMID(value string) (int, error) {
	vmid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("invalid vmid %q", value)
	}
	return vmid, nil
}

func parseExposePort(value string) (int, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("port is required")
	}
	raw = strings.TrimPrefix(raw, ":")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("port is required")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

func exposureName(vmid, port int) string {
	return fmt.Sprintf("sbx-%d-%d", vmid, port)
}

// parseTTLMinutes parses a TTL value, accepting either minutes (int) or a duration string.
// Returns nil if the value is empty.
func parseTTLMinutes(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if minutes, err := strconv.Atoi(value); err == nil {
		if minutes <= 0 {
			return nil, fmt.Errorf("ttl must be positive")
		}
		return &minutes, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl %q", value)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	minutes := int(math.Ceil(dur.Minutes()))
	if minutes <= 0 {
		minutes = 1
	}
	return &minutes, nil
}

// parseRequiredTTLMinutes parses a TTL value that must be provided and positive.
func parseRequiredTTLMinutes(value string) (int, error) {
	minutes, err := parseTTLMinutes(value)
	if err != nil {
		return 0, err
	}
	if minutes == nil || *minutes <= 0 {
		return 0, fmt.Errorf("ttl must be positive")
	}
	return *minutes, nil
}

// parseSizeGB parses a disk size string (e.g., "80G", "80GB") into gigabytes.
func parseSizeGB(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("size is required")
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasSuffix(lower, "gb"):
		lower = strings.TrimSuffix(lower, "gb")
	case strings.HasSuffix(lower, "g"):
		lower = strings.TrimSuffix(lower, "g")
	}
	lower = strings.TrimSpace(lower)
	size, err := strconv.Atoi(lower)
	if err != nil || size <= 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return size, nil
}

// parseWorkspaceWaitSeconds parses a wait duration or seconds into seconds.
// Returns nil if the value is empty.
func parseWorkspaceWaitSeconds(value string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return nil, fmt.Errorf("workspace-wait must be positive")
		}
		return &seconds, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace-wait %q", value)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("workspace-wait must be positive")
	}
	seconds := int(math.Ceil(dur.Seconds()))
	if seconds <= 0 {
		seconds = 1
	}
	return &seconds, nil
}

func parseWorkspaceSelection(workspace, workspaceCreate, workspaceSize, workspaceStorage string) (*string, *workspaceCreateRequest, error) {
	workspace = strings.TrimSpace(workspace)
	workspaceCreate = strings.TrimSpace(workspaceCreate)
	workspaceSize = strings.TrimSpace(workspaceSize)
	workspaceStorage = strings.TrimSpace(workspaceStorage)
	if workspace != "" && workspaceCreate != "" {
		return nil, nil, fmt.Errorf("--workspace and --workspace-create are mutually exclusive")
	}
	if strings.HasPrefix(workspace, "new:") {
		name := strings.TrimSpace(strings.TrimPrefix(workspace, "new:"))
		if name == "" {
			return nil, nil, fmt.Errorf("workspace name is required after new")
		}
		if workspaceCreate != "" {
			return nil, nil, fmt.Errorf("--workspace new:<name> cannot be combined with --workspace-create")
		}
		workspaceCreate = name
		workspace = ""
	}
	var workspaceID *string
	if workspace != "" {
		value := workspace
		workspaceID = &value
	}
	var workspaceCreateReq *workspaceCreateRequest
	if workspaceCreate != "" {
		if workspaceSize == "" {
			return nil, nil, fmt.Errorf("--workspace-size is required when creating a workspace")
		}
		sizeGB, err := parseSizeGB(workspaceSize)
		if err != nil {
			return nil, nil, err
		}
		storage := workspaceStorage
		workspaceCreateReq = &workspaceCreateRequest{
			Name:    workspaceCreate,
			SizeGB:  sizeGB,
			Storage: storage,
		}
	} else if workspaceSize != "" || workspaceStorage != "" {
		return nil, nil, fmt.Errorf("--workspace-size/--workspace-storage require workspace creation (use --workspace new:<name> or --workspace-create)")
	}
	return workspaceID, workspaceCreateReq, nil
}

func defaultStatefulWorkspaceName(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("repo is required to derive workspace name")
	}
	base := strings.TrimRight(repo, "/")
	lastSlash := strings.LastIndex(base, "/")
	lastColon := strings.LastIndex(base, ":")
	index := lastSlash
	if lastColon > index {
		index = lastColon
	}
	if index >= 0 && index+1 < len(base) {
		base = base[index+1:]
	}
	base = strings.TrimSuffix(base, ".git")
	slug := slugifyWorkspaceName(base)
	if slug == "" {
		return "stateful-workspace", nil
	}
	return "stateful-" + slug, nil
}

func slugifyWorkspaceName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastDash := false
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func sessionNameFromBranch(branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", fmt.Errorf("branch is required")
	}
	slug := slugifyWorkspaceName(branch)
	if slug == "" {
		return "", fmt.Errorf("invalid branch %q", branch)
	}
	return "branch-" + slug, nil
}

func workspaceTargetName(workspaceID *string, create *workspaceCreateRequest) string {
	if create != nil && strings.TrimSpace(create.Name) != "" {
		return create.Name
	}
	if workspaceID != nil {
		return *workspaceID
	}
	return ""
}

func workspaceReferenceForCLI(ws workspaceResponse) string {
	if strings.TrimSpace(ws.Name) != "" {
		return ws.Name
	}
	return ws.ID
}

func formatWorkspaceCheckDetails(details map[string]string) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(details[key])
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func orDashPtr(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}

func orDashBoolPtr(value *bool) string {
	if value == nil {
		return "-"
	}
	if *value {
		return "true"
	}
	return "false"
}

func ttlMinutesString(value *int) string {
	if value == nil || *value == 0 {
		return "-"
	}
	return strconv.Itoa(*value)
}

func vmidString(value *int) string {
	if value == nil || *value <= 0 {
		return "-"
	}
	return strconv.Itoa(*value)
}
