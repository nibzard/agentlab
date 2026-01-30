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
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	defaultLogTail      = 50
	eventPollInterval   = 2 * time.Second
	defaultEventLimit   = 200
	maxEventLimit       = 1000
	ttlFlagDescription  = "lease ttl in minutes or duration (e.g. 120 or 2h)"
	jsonFlagDescription = "output json"
)

var errHelp = errors.New("help requested")

type commonFlags struct {
	socketPath string
	jsonOutput bool
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.socketPath, "socket", c.socketPath, "path to agentlabd socket")
	fs.BoolVar(&c.jsonOutput, "json", c.jsonOutput, jsonFlagDescription)
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string, usage func(), help *bool) error {
	fs.Usage = usage
	if err := fs.Parse(args); err != nil {
		usage()
		return err
	}
	if help != nil && *help {
		usage()
		return errHelp
	}
	return nil
}

func runJobCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printJobUsage()
		return nil
	}
	switch args[0] {
	case "run":
		return runJobRun(ctx, args[1:], base)
	default:
		printJobUsage()
		return fmt.Errorf("unknown job command %q", args[0])
	}
}

func runJobRun(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job run")
	opts := base
	opts.bind(fs)
	var repo string
	var ref string
	var profile string
	var task string
	var mode string
	var ttl string
	var keepalive bool
	var help bool
	fs.StringVar(&repo, "repo", "", "git repository url")
	fs.StringVar(&ref, "ref", "", "git ref (default main)")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&task, "task", "", "task description")
	fs.StringVar(&mode, "mode", "", "mode (default dangerous)")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.BoolVar(&keepalive, "keepalive", false, "keep sandbox after job completion")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printJobRunUsage, &help); err != nil {
		return err
	}
	if repo == "" || profile == "" || task == "" {
		printJobRunUsage()
		return fmt.Errorf("repo, profile, and task are required")
	}
	ttlMinutes, err := parseTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
	req := jobCreateRequest{
		RepoURL:    repo,
		Ref:        ref,
		Profile:    profile,
		Task:       task,
		Mode:       mode,
		TTLMinutes: ttlMinutes,
		Keepalive:  keepalive,
	}
	payload, err := client.doJSON(ctx, http.MethodPost, "/v1/jobs", req)
	if err != nil {
		return err
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

func runSandboxCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printSandboxUsage()
		return nil
	}
	switch args[0] {
	case "new":
		return runSandboxNew(ctx, args[1:], base)
	case "list":
		return runSandboxList(ctx, args[1:], base)
	case "show":
		return runSandboxShow(ctx, args[1:], base)
	case "destroy":
		return runSandboxDestroy(ctx, args[1:], base)
	case "lease":
		return runSandboxLease(ctx, args[1:], base)
	default:
		printSandboxUsage()
		return fmt.Errorf("unknown sandbox command %q", args[0])
	}
}

func runWorkspaceCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printWorkspaceUsage()
		return nil
	}
	switch args[0] {
	case "create":
		return runWorkspaceCreate(ctx, args[1:], base)
	case "list":
		return runWorkspaceList(ctx, args[1:], base)
	case "attach":
		return runWorkspaceAttach(ctx, args[1:], base)
	case "detach":
		return runWorkspaceDetach(ctx, args[1:], base)
	default:
		printWorkspaceUsage()
		return fmt.Errorf("unknown workspace command %q", args[0])
	}
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
	var keepalive bool
	var help bool
	fs.StringVar(&name, "name", "", "sandbox name")
	fs.StringVar(&profile, "profile", "", "profile name")
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.StringVar(&workspace, "workspace", "", "workspace id or name")
	fs.IntVar(&vmid, "vmid", 0, "vmid override")
	fs.StringVar(&jobID, "job", "", "attach to existing job id")
	fs.BoolVar(&keepalive, "keepalive", false, "keep sandbox after job completion")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxNewUsage, &help); err != nil {
		return err
	}
	if profile == "" {
		printSandboxNewUsage()
		return fmt.Errorf("profile is required")
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

	client := newAPIClient(opts.socketPath)
	req := sandboxCreateRequest{
		Name:       name,
		Profile:    profile,
		Keepalive:  keepalive,
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
	printSandbox(resp)
	return nil
}

func runSandboxList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox list")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxListUsage, &help); err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
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
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxShowUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxShowUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
	payload, err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%d", vmid), nil)
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
	printSandbox(resp)
	return nil
}

func runSandboxDestroy(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox destroy")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxDestroyUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxDestroyUsage()
		return fmt.Errorf("vmid is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/destroy", vmid), nil)
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
	fmt.Printf("sandbox %d destroyed (state=%s)\n", resp.VMID, resp.State)
	return nil
}

func runSandboxLease(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 {
		printSandboxLeaseUsage()
		return nil
	}
	switch args[0] {
	case "renew":
		return runSandboxLeaseRenew(ctx, args[1:], base)
	default:
		printSandboxLeaseUsage()
		return fmt.Errorf("unknown sandbox lease command %q", args[0])
	}
}

func runSandboxLeaseRenew(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("sandbox lease renew")
	opts := base
	opts.bind(fs)
	var ttl string
	var help bool
	fs.StringVar(&ttl, "ttl", "", ttlFlagDescription)
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printSandboxLeaseRenewUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printSandboxLeaseRenewUsage()
		return fmt.Errorf("vmid is required")
	}
	if ttl == "" {
		printSandboxLeaseRenewUsage()
		return fmt.Errorf("ttl is required")
	}
	vmid, err := parseVMID(fs.Arg(0))
	if err != nil {
		return err
	}
	minutes, err := parseRequiredTTLMinutes(ttl)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
	req := leaseRenewRequest{TTLMinutes: minutes}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%d/lease/renew", vmid), req)
	if err != nil {
		return err
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

func runWorkspaceCreate(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace create")
	opts := base
	opts.bind(fs)
	var name string
	var size string
	var storage string
	var help bool
	fs.StringVar(&name, "name", "", "workspace name")
	fs.StringVar(&size, "size", "", "workspace size (e.g. 80G)")
	fs.StringVar(&storage, "storage", "", "Proxmox storage (default local-zfs)")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceCreateUsage, &help); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	size = strings.TrimSpace(size)
	storage = strings.TrimSpace(storage)
	if name == "" || size == "" {
		printWorkspaceCreateUsage()
		return fmt.Errorf("name and size are required")
	}
	sizeGB, err := parseSizeGB(size)
	if err != nil {
		return err
	}

	client := newAPIClient(opts.socketPath)
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
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceListUsage, &help); err != nil {
		return err
	}
	client := newAPIClient(opts.socketPath)
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

func runWorkspaceAttach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace attach")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceAttachUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		printWorkspaceAttachUsage()
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
	client := newAPIClient(opts.socketPath)
	req := workspaceAttachRequest{VMID: vmid}
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/attach", workspace), req)
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

func runWorkspaceDetach(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("workspace detach")
	opts := base
	opts.bind(fs)
	var help bool
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printWorkspaceDetachUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printWorkspaceDetachUsage()
		return fmt.Errorf("workspace is required")
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	client := newAPIClient(opts.socketPath)
	payload, err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/v1/workspaces/%s/detach", workspace), nil)
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

func runLogsCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("logs")
	opts := base
	opts.bind(fs)
	var follow bool
	var tail int
	var help bool
	fs.BoolVar(&follow, "follow", false, "follow new events")
	fs.IntVar(&tail, "tail", defaultLogTail, "show the last N events")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")
	if err := parseFlags(fs, args, printLogsUsage, &help); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		printLogsUsage()
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

	client := newAPIClient(opts.socketPath)
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
		return eventsResponse{}, err
	}
	var resp eventsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return eventsResponse{}, err
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
	fmt.Printf("Keepalive: %t\n", sb.Keepalive)
	fmt.Printf("Lease Expires: %s\n", orDashPtr(sb.LeaseExpires))
	fmt.Printf("Created At: %s\n", sb.CreatedAt)
	fmt.Printf("Updated At: %s\n", sb.LastUpdatedAt)
}

func printSandboxList(sandboxes []sandboxResponse) {
	w := tabwriter.NewWriter(os.Stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(w, "VMID\tNAME\tPROFILE\tSTATE\tIP\tLEASE")
	for _, sb := range sandboxes {
		lease := orDashPtr(sb.LeaseExpires)
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", sb.VMID, sb.Name, sb.Profile, sb.State, orDash(sb.IP), lease)
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

func parseVMID(value string) (int, error) {
	vmid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("invalid vmid %q", value)
	}
	return vmid, nil
}

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
