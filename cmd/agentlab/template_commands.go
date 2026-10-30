package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	tb "github.com/agentlab/agentlab/internal/templatebuild"
)

const templateUsage = `Usage:
  agentlab template capabilities
  agentlab template validate --name NAME --storage STORAGE [build flags]
  agentlab template create --name NAME --storage STORAGE [--wait] [--wait-timeout 45m]
  agentlab template build list [--state STATE] [--offset N] [--limit N]
  agentlab template build show ID
  agentlab template build logs ID [--follow] [--after N]
  agentlab template build wait ID [--timeout 45m]
  agentlab template build cancel ID
  agentlab template list [--offset N] [--limit N]
  agentlab template show VMID
  agentlab template activate VMID --profile NAME --expected-template-vmid VMID

Build flags: --vmid, --recipe, --cloudinit-storage, --bridge, --memory-mb,
--cores, --disk-size-gb, --image-url, --image-sha256, --agent-tools=false,
--timeout-seconds, --request-id (UUID; preserve to retry a lost response).
Global --endpoint, --token, --socket, --json and --timeout apply.
JSON logs --follow emits NDJSON: sequence, timestamp (UTC), stage, message.
Client wait timeout and Ctrl-C leave the durable server build running.
`

func templateUUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
func runTemplateCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprint(os.Stdout, templateUsage)
		return errHelp
	}
	command := args[0]
	args = args[1:]
	build := false
	if command == "build" {
		if len(args) == 0 || isHelpToken(args[0]) {
			fmt.Fprint(os.Stdout, templateUsage)
			return errHelp
		}
		build = true
		command = args[0]
		args = args[1:]
		if command != "list" && command != "show" && command != "logs" && command != "wait" && command != "cancel" {
			return newUsageError(fmt.Errorf("unknown template build command %q", command), true)
		}
	}
	fs := newFlagSet("template " + command)
	opts := base
	// Positional IDs precede flags in the documented command syntax.
	id := ""
	if command == "show" || command == "activate" || command == "logs" || command == "wait" || command == "cancel" {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			id = args[0]
			args = args[1:]
		}
	}
	opts.bind(fs)
	help := bindHelpFlag(fs)
	var p tb.Plan
	var wait, follow bool
	var waitTimeout time.Duration
	var offset, limit int
	var after int64
	var state, profile string
	var expected int
	switch command {
	case "create", "validate":
		if build {
			return newUsageError(fmt.Errorf("unknown template build command"), true)
		}
		fs.StringVar(&p.Name, "name", "", "template name")
		fs.StringVar(&p.Storage, "storage", "", "local disk storage")
		fs.IntVar(&p.VMID, "vmid", 0, "unused template VMID")
		fs.StringVar(&p.Recipe, "recipe", tb.Recipe, "daemon recipe")
		fs.StringVar(&p.CloudInitStorage, "cloudinit-storage", "", "cloud-init storage")
		fs.StringVar(&p.Bridge, "bridge", "vmbr1", "local bridge")
		fs.IntVar(&p.MemoryMB, "memory-mb", 4096, "guest memory MiB")
		fs.IntVar(&p.Cores, "cores", 2, "guest cores")
		fs.IntVar(&p.DiskSizeGB, "disk-size-gb", 40, "disk size GiB")
		fs.StringVar(&p.ImageURL, "image-url", "", "HTTPS image override")
		fs.StringVar(&p.ImageSHA256, "image-sha256", "", "override SHA-256")
		p.AgentTools = fs.Bool("agent-tools", true, "install pinned agent CLIs")
		fs.IntVar(&p.TimeoutSeconds, "timeout-seconds", 3600, "server build timeout")
		fs.StringVar(&p.RequestID, "request-id", "", "idempotency UUID")
		fs.BoolVar(&wait, "wait", false, "wait for build")
		fs.DurationVar(&waitTimeout, "wait-timeout", 45*time.Minute, "client wait limit")
	case "list":
		fs.IntVar(&offset, "offset", 0, "page offset")
		fs.IntVar(&limit, "limit", 100, "page limit")
		if build {
			fs.StringVar(&state, "state", "", "state filter")
		}
	case "logs":
		if !build {
			return newUsageError(fmt.Errorf("use template build logs"), true)
		}
		fs.BoolVar(&follow, "follow", false, "follow build logs")
		fs.Int64Var(&after, "after", 0, "exclusive sequence cursor")
	case "wait":
		if !build {
			return newUsageError(fmt.Errorf("use template build wait"), true)
		}
		waitTimeout = 45 * time.Minute
		// For this subcommand --timeout is the client wait bound, as documented.
		fs.DurationVar(&waitTimeout, "wait-timeout", 45*time.Minute, "client wait limit")
	case "activate":
		fs.StringVar(&profile, "profile", "", "existing profile")
		fs.IntVar(&expected, "expected-template-vmid", 0, "expected current template")
	case "cancel":
		if !build {
			return newUsageError(fmt.Errorf("use template build cancel"), true)
		}
	case "capabilities", "show":
	default:
		return newUsageError(fmt.Errorf("unknown template command %q", command), true)
	}
	if e := parseFlags(fs, args, func() { fmt.Fprint(os.Stdout, templateUsage) }, help, opts.jsonOutput); e != nil {
		return e
	}
	if fs.NArg() != 0 {
		return newUsageError(fmt.Errorf("unexpected arguments"), true)
	}
	if command == "wait" {
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "timeout" {
				waitTimeout = opts.timeout
				opts.timeout = base.timeout
			}
		})
	}
	if (command == "show" || command == "activate" || command == "logs" || command == "wait" || command == "cancel") && id == "" {
		return newUsageError(fmt.Errorf("ID is required"), true)
	}
	if (command == "wait" || (command == "create" && wait)) && waitTimeout <= 0 {
		return newUsageError(fmt.Errorf("wait timeout must be positive"), true)
	}
	if command == "list" && limit == 0 {
		return newUsageError(fmt.Errorf("page limit must be between 1 and 500"), true)
	}
	if state != "" && state != "QUEUED" && state != "RUNNING" && state != "SUCCEEDED" && state != "FAILED" && state != "CANCELLED" {
		return newUsageError(fmt.Errorf("invalid build state filter %q", state), true)
	}
	if after < 0 || offset < 0 || limit < 0 || limit > 500 || waitTimeout < 0 {
		return newUsageError(fmt.Errorf("invalid cursor, pagination or timeout"), true)
	}
	if !build && (command == "show" || command == "activate") {
		n, e := strconv.Atoi(id)
		if e != nil || n <= 0 {
			return newUsageError(fmt.Errorf("VMID must be positive"), true)
		}
	}
	client, e := apiClientFromFlags(opts)
	if e != nil {
		return e
	}
	request := func(method, path string, payload any) ([]byte, error) {
		data, e := client.doJSON(ctx, method, path, payload)
		return data, templateFeatureError(e)
	}
	path := "/v1/templates"
	if build {
		path = "/v1/template-builds"
	}
	var data []byte
	switch command {
	case "create", "validate":
		clientLimits := tb.Limits{VMIDMin: 1, VMIDMax: 999999999, MemoryMB: 1 << 30, Cores: 1 << 20, DiskSizeGB: 1 << 30, TimeoutSeconds: 7200}
		if p.ImageURL != "" {
			u, _ := url.Parse(p.ImageURL)
			if u != nil {
				clientLimits.ImageHosts = []string{u.Hostname()}
			}
		}
		v := tb.Normalize(p, clientLimits)
		if !v.OK {
			return newUsageError(fmt.Errorf("invalid template input: %s: %s", v.Errors[0].Field, v.Errors[0].Message), true)
		}
		if command == "validate" {
			data, e = templateValidate(ctx, client, p)
			if e == nil {
				var validation tb.Validation
				if e = json.Unmarshal(data, &validation); e != nil {
					return e
				}
				if !validation.OK {
					fmt.Fprintln(os.Stdout, string(data))
					err := fmt.Errorf("template validation failed; inspect errors and host prerequisites")
					if opts.jsonOutput {
						return newPrintedJSONOnlyError(err)
					}
					return err
				}
			}
		} else {
			if p.RequestID == "" {
				p.RequestID, e = templateUUID()
				if e != nil {
					return e
				}
			}
			fmt.Fprintf(os.Stderr, "Request ID: %s (reuse --request-id to retry submission)\n", p.RequestID)
			data, e = request("POST", "/v1/template-builds", p)
		}
		if e != nil {
			return e
		}
		if wait && command == "create" {
			var b tb.Build
			if e = json.Unmarshal(data, &b); e != nil {
				return e
			}
			fmt.Fprintf(os.Stderr, "Build %s, VMID %d; reconnect: agentlab template build wait %s\n", b.ID, b.VMID, b.ID)
			return templateWait(ctx, client, b.ID, waitTimeout, opts.jsonOutput)
		}
	case "capabilities":
		if build {
			return newUsageError(fmt.Errorf("use template capabilities"), true)
		}
		data, e = request("GET", path+"/capabilities", nil)
	case "list":
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
		if state != "" {
			q.Set("state", state)
		}
		data, e = request("GET", path+"?"+q.Encode(), nil)
	case "show":
		data, e = request("GET", path+"/"+url.PathEscape(id), nil)
	case "cancel":
		data, e = request("POST", path+"/"+url.PathEscape(id)+"/cancel", map[string]any{})
	case "activate":
		if profile == "" || expected <= 0 {
			return newUsageError(fmt.Errorf("profile and expected-template-vmid required"), true)
		}
		data, e = request("POST", path+"/"+id+"/activate", map[string]any{"profile": profile, "expected_template_vmid": expected})
	case "wait":
		return templateWait(ctx, client, id, waitTimeout, opts.jsonOutput)
	case "logs":
		return templateLogs(ctx, client, id, after, follow, opts.jsonOutput)
	}
	if e != nil {
		return e
	}
	if opts.jsonOutput {
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}
	if command == "create" {
		var b tb.Build
		if e = json.Unmarshal(data, &b); e != nil {
			return e
		}
		fmt.Fprintf(os.Stdout, "Build %s: %s; VMID %d\nReconnect: agentlab template build wait %s\n", b.ID, b.State, b.VMID, b.ID)
		return nil
	}
	var pretty any
	if e = json.Unmarshal(data, &pretty); e != nil {
		return e
	}
	b, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}
func templateWait(ctx context.Context, c *apiClient, id string, timeout time.Duration, jsonOutput bool) error {
	if timeout <= 0 {
		return newUsageError(fmt.Errorf("wait timeout must be positive"), true)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	previous := ""
	for {
		raw, e := c.doJSON(ctx, "GET", "/v1/template-builds/"+url.PathEscape(id), nil)
		if e != nil {
			fmt.Fprintf(os.Stderr, "Reconnect: agentlab template build wait %s\n", id)
			return templateFeatureError(e)
		}
		var b tb.Build
		if e = json.Unmarshal(raw, &b); e != nil {
			return e
		}
		if b.Terminal() {
			if jsonOutput {
				fmt.Fprintln(os.Stdout, string(raw))
			} else {
				fmt.Fprintf(os.Stdout, "Build %s: %s (VMID %d)\n", id, b.State, b.VMID)
			}
			if b.State != "SUCCEEDED" {
				e := fmt.Errorf("build %s: %s %s", b.State, b.ErrorCode, b.Error)
				if jsonOutput {
					return newPrintedJSONOnlyError(e)
				}
				return e
			}
			return nil
		}
		stage := b.State + " " + b.Stage
		if stage != previous {
			fmt.Fprintln(os.Stderr, stage)
			previous = stage
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "Reconnect: agentlab template build wait %s\n", id)
			return fmt.Errorf("stopped waiting: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}
func templateLogs(ctx context.Context, c *apiClient, id string, after int64, follow, jsonOutput bool) (err error) {
	defer func() {
		if err != nil && follow {
			fmt.Fprintf(os.Stderr, "Reconnect: agentlab template build logs %s --follow --after %d\n", id, after)
			if jsonOutput {
				fmt.Fprintln(os.Stderr, err)
				err = newPrintedJSONOnlyError(err)
			}
		}
	}()
	for {
		raw, e := c.doJSON(ctx, "GET", fmt.Sprintf("/v1/template-builds/%s/logs?after=%d&limit=500", url.PathEscape(id), after), nil)
		if e != nil {
			return templateFeatureError(e)
		}
		var logs tb.Logs
		if e = json.Unmarshal(raw, &logs); e != nil {
			return templateFeatureError(e)
		}
		for _, l := range logs.Records {
			if jsonOutput {
				b, _ := json.Marshal(l)
				fmt.Fprintln(os.Stdout, string(b))
			} else {
				fmt.Fprintf(os.Stdout, "%d %s [%s] %s\n", l.Sequence, l.Timestamp.Format(time.RFC3339), l.Stage, l.Message)
			}
		}
		after = logs.Next
		if !follow {
			return nil
		}
		if len(logs.Records) > 0 {
			continue
		}
		raw, e = c.doJSON(ctx, "GET", "/v1/template-builds/"+url.PathEscape(id), nil)
		if e != nil {
			return templateFeatureError(e)
		}
		var b tb.Build
		if e = json.Unmarshal(raw, &b); e != nil {
			return templateFeatureError(e)
		}
		if b.Terminal() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func templateValidate(ctx context.Context, c *apiClient, p tb.Plan) ([]byte, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	req, e := c.newRequest(ctx, "POST", "/v1/templates/validate-plan", bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.httpClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, endpointRedirectError()
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxJSONOutputBytes))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode == 422 || resp.StatusCode == 200 {
		return data, nil
	}
	return nil, templateFeatureError(parseAPIError(resp.StatusCode, data))
}

func templateFeatureError(err error) error {
	var response apiResponseError
	if errors.As(err, &response) && response.Status == 404 {
		return fmt.Errorf("template feature or resource unavailable; check ID and upgrade agentlabd if template routes are unsupported: %w", err)
	}
	return err
}
