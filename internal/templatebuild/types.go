// Package templatebuild defines the remote template contract and trusted recipes.
package templatebuild

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Recipe = "ubuntu-24.04-agentlab-v1"
const DefaultImage = "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"

type Plan struct {
	Name             string `json:"name"`
	VMID             int    `json:"vmid,omitempty"`
	Recipe           string `json:"recipe"`
	Storage          string `json:"storage"`
	CloudInitStorage string `json:"cloudinit_storage"`
	Bridge           string `json:"bridge"`
	MemoryMB         int    `json:"memory_mb"`
	Cores            int    `json:"cores"`
	DiskSizeGB       int    `json:"disk_size_gb"`
	ImageURL         string `json:"image_url,omitempty"`
	ImageSHA256      string `json:"image_sha256,omitempty"`
	AgentTools       *bool  `json:"agent_tools,omitempty"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	RequestID        string `json:"request_id,omitempty"`
}
type Limits struct {
	VMIDMin        int      `json:"vmid_min" yaml:"vmid_min"`
	VMIDMax        int      `json:"vmid_max" yaml:"vmid_max"`
	MemoryMB       int      `json:"memory_mb" yaml:"memory_mb"`
	Cores          int      `json:"cores" yaml:"cores"`
	DiskSizeGB     int      `json:"disk_size_gb" yaml:"disk_size_gb"`
	TimeoutSeconds int      `json:"timeout_seconds" yaml:"timeout_seconds"`
	Pending        int      `json:"pending" yaml:"pending"`
	ImageHosts     []string `json:"image_hosts" yaml:"image_hosts"`
}

func (l Limits) Defaults() Limits {
	if l.VMIDMin == 0 {
		l.VMIDMin = 9000
	}
	if l.VMIDMax == 0 {
		l.VMIDMax = 9999
	}
	if l.MemoryMB == 0 {
		l.MemoryMB = 65536
	}
	if l.Cores == 0 {
		l.Cores = 32
	}
	if l.DiskSizeGB == 0 {
		l.DiskSizeGB = 1024
	}
	if l.TimeoutSeconds == 0 {
		l.TimeoutSeconds = 7200
	}
	if l.Pending == 0 {
		l.Pending = 10
	}
	if len(l.ImageHosts) == 0 {
		l.ImageHosts = []string{"cloud-images.ubuntu.com"}
	}
	return l
}

type Issue struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Validation struct {
	OK       bool    `json:"ok"`
	Plan     Plan    `json:"plan"`
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}
type Capabilities struct {
	Supported bool     `json:"supported"`
	Recipes   []string `json:"recipes"`
	Defaults  Plan     `json:"defaults"`
	Limits    Limits   `json:"limits"`
	Errors    []Issue  `json:"errors"`
}
type Manifest struct {
	BootVerified   bool              `json:"boot_verified"`
	BootVerifiedAt *time.Time        `json:"boot_verified_at,omitempty"`
	BuildID        string            `json:"build_id"`
	VMID           int               `json:"vmid"`
	Name           string            `json:"name"`
	Node           string            `json:"node"`
	Recipe         string            `json:"recipe"`
	AssetDigest    string            `json:"asset_digest"`
	ImageURL       string            `json:"image_url"`
	ImageSHA256    string            `json:"image_sha256"`
	Capabilities   map[string]string `json:"capabilities"`
	Enrolled       bool              `json:"enrolled"`
	Config         map[string]string `json:"config"`
	CreatedAt      time.Time         `json:"created_at"`
}
type Build struct {
	SmokeVMID           int        `json:"smoke_vmid,omitempty"`
	SmokeResources      []string   `json:"smoke_resources,omitempty"`
	WorkerLaunched      bool       `json:"worker_launched"`
	ResolvedImageSHA256 string     `json:"resolved_image_sha256,omitempty"`
	ID                  string     `json:"id"`
	VMID                int        `json:"vmid"`
	State               string     `json:"state"`
	Stage               string     `json:"stage"`
	Plan                Plan       `json:"plan"`
	Caller              string     `json:"caller"`
	AssetDigest         string     `json:"asset_digest"`
	Unit                string     `json:"worker_unit"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	CancelRequested     bool       `json:"cancel_requested"`
	ErrorCode           string     `json:"error_code,omitempty"`
	Error               string     `json:"error,omitempty"`
	ExitStatus          *int       `json:"exit_status,omitempty"`
	CleanupRequired     bool       `json:"cleanup_required"`
	CleanupErrorCode    string     `json:"cleanup_error_code,omitempty"`
	Resources           []string   `json:"resources,omitempty"`
	Result              *Manifest  `json:"result,omitempty"`
}

func (b Build) Terminal() bool {
	return b.State == "SUCCEEDED" || b.State == "FAILED" || b.State == "CANCELLED"
}

type Log struct {
	Sequence  int64     `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
}
type Logs struct {
	Records []Log `json:"records"`
	Next    int64 `json:"next_cursor"`
}

var nameRE = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
var resourceRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
var shaRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var uuidRE = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func Normalize(p Plan, limits Limits) Validation {
	l := limits.Defaults()
	v := Validation{Plan: p, Errors: []Issue{}, Warnings: []Issue{}}
	p2 := &v.Plan
	if p2.Recipe == "" {
		p2.Recipe = Recipe
	}
	if p2.Bridge == "" {
		p2.Bridge = "vmbr1"
	}
	if p2.CloudInitStorage == "" {
		p2.CloudInitStorage = p2.Storage
	}
	if p2.MemoryMB == 0 {
		p2.MemoryMB = 4096
	}
	if p2.Cores == 0 {
		p2.Cores = 2
	}
	if p2.DiskSizeGB == 0 {
		p2.DiskSizeGB = 40
	}
	if p2.TimeoutSeconds == 0 {
		p2.TimeoutSeconds = 3600
	}
	if p2.AgentTools == nil {
		b := true
		p2.AgentTools = &b
	}
	add := func(field, msg string) { v.Errors = append(v.Errors, Issue{"template_invalid_plan", field, msg}) }
	if !nameRE.MatchString(p2.Name) {
		add("name", "use 1–63 lowercase letters, digits or hyphens")
	}
	if p2.Recipe != Recipe {
		add("recipe", "unsupported recipe")
	}
	for field, s := range map[string]string{"storage": p2.Storage, "cloudinit_storage": p2.CloudInitStorage, "bridge": p2.Bridge} {
		if !resourceRE.MatchString(s) {
			add(field, "a valid local resource name is required")
		}
	}
	if p2.VMID != 0 && (p2.VMID < l.VMIDMin || p2.VMID > l.VMIDMax) {
		add("vmid", "outside configured template range")
	}
	for _, n := range []struct {
		field      string
		value, max int
	}{{"memory_mb", p2.MemoryMB, l.MemoryMB}, {"cores", p2.Cores, l.Cores}, {"disk_size_gb", p2.DiskSizeGB, l.DiskSizeGB}, {"timeout_seconds", p2.TimeoutSeconds, l.TimeoutSeconds}} {
		if n.value <= 0 || n.value > n.max || (n.field == "timeout_seconds" && n.value > 7200) {
			add(n.field, fmt.Sprintf("must be positive and at most %d", n.max))
		}
	}
	if p2.RequestID != "" && !uuidRE.MatchString(p2.RequestID) {
		add("request_id", "must be a UUID")
	}
	if p2.ImageURL != "" || p2.ImageSHA256 != "" {
		p2.ImageSHA256 = strings.ToLower(p2.ImageSHA256)
		if !shaRE.MatchString(p2.ImageSHA256) {
			add("image_sha256", "image override requires a SHA-256 digest")
		}
		if err := CheckURL(p2.ImageURL, l.ImageHosts); err != nil {
			add("image_url", err.Error())
		}
	}
	v.OK = len(v.Errors) == 0
	return v
}
func CheckURL(raw string, hosts []string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("an HTTPS URL without credentials, fragment or custom port is required")
	}
	for _, h := range hosts {
		if strings.EqualFold(h, u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("image hostname is not permitted by host policy")
}
