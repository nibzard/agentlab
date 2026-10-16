package daemon

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	tb "github.com/agentlab/agentlab/internal/templatebuild"
	"gopkg.in/yaml.v3"
)

// ProfileStore publishes immutable snapshots. Readers capture a revision once;
// activation serializes file persistence and replaces the snapshot atomically.
type ProfileStore struct {
	mu      sync.RWMutex
	dir     string
	current atomic.Pointer[map[string]models.Profile]
	hashes  map[string][32]byte
}

func NewProfileStore(dir string, profiles map[string]models.Profile) (*ProfileStore, error) {
	s := &ProfileStore{dir: dir}
	snapshot := make(map[string]models.Profile, len(profiles))
	for name, profile := range profiles {
		snapshot[name] = profile
	}
	// An empty directory is an explicitly in-memory service (used by embedded
	// consumers and tests); it cannot activate profiles on disk.
	if dir == "" {
		s.current.Store(&snapshot)
		return s, nil
	}
	before, err := profileHashes(dir)
	if err != nil {
		return nil, fmt.Errorf("initialize profile store: %w", err)
	}
	disk, err := LoadProfiles(dir)
	if err != nil {
		return nil, err
	}
	after, err := profileHashes(dir)
	if err != nil {
		return nil, err
	}
	if !sameProfileHashes(before, after) || len(disk) != len(snapshot) {
		return nil, fmt.Errorf("template_profile_conflict: profiles changed while initializing; reload profiles")
	}
	for name, loaded := range snapshot {
		current, ok := disk[name]
		// UpdatedAt is observational metadata; identity and the complete per-document
		// YAML must match the revision that callers already loaded.
		loadedType := loaded.Type
		if loadedType == "" {
			loadedType = models.SandboxTypeVM
		}
		canonical, parseErr := canonicalProfileDocument(loaded.RawYAML)
		if !ok || parseErr != nil || loaded.Name != current.Name || loaded.TemplateVM != current.TemplateVM || loadedType != current.Type || loaded.Image != current.Image || canonical != current.RawYAML {
			return nil, fmt.Errorf("template_profile_conflict: profile %q changed since loading; reload profiles", name)
		}
	}
	s.hashes = after
	s.current.Store(&snapshot)
	return s, nil
}

// Constructors also accept callers that provide a raw document and omit the
// implicit VM type. Normalize representation without ignoring content changes.
func canonicalProfileDocument(raw string) (string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(raw)))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return "", err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("profile snapshot must contain one YAML document")
	}
	return renderProfileYAML(&node)
}
func sameProfileHashes(a, b map[string][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for name, hash := range a {
		if other, ok := b[name]; !ok || other != hash {
			return false
		}
	}
	return true
}
func profileHashes(dir string) (map[string][32]byte, error) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, e
	}
	out := map[string][32]byte{}
	for _, ent := range entries {
		if !isYAML(ent.Name()) || ent.IsDir() {
			continue
		}
		p := filepath.Join(dir, ent.Name())
		info, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("profile must be a regular file")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		out[ent.Name()] = sha256.Sum256(b)
	}
	return out, nil
}
func (s *ProfileStore) Snapshot() map[string]models.Profile {
	if s == nil {
		return nil
	}
	return *s.current.Load()
}
func (api *ControlAPI) profileSnapshot() map[string]models.Profile {
	if api.profileStore != nil {
		return api.profileStore.Snapshot()
	}
	return api.profiles
}
func (o *JobOrchestrator) profileSnapshot() map[string]models.Profile {
	if o.profileStore != nil {
		return o.profileStore.Snapshot()
	}
	return o.profiles
}
func (s *ProfileStore) Activate(name string, expected, vmid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" || s.hashes == nil {
		return fmt.Errorf("template_profile_conflict: no writable profile directory configured")
	}
	if expected <= 0 || vmid <= 0 {
		return fmt.Errorf("template_profile_conflict: positive template VMIDs are required")
	}
	old, ok := s.Snapshot()[name]
	if !ok || old.TemplateVM != expected || old.Type == models.SandboxTypeLXC {
		return fmt.Errorf("template_profile_conflict: profile missing, changed or not VM type")
	}
	hashes, e := profileHashes(s.dir)
	if e != nil {
		return e
	}
	if len(hashes) != len(s.hashes) {
		return fmt.Errorf("template_profile_conflict: external profile edits; reload daemon first")
	}
	for p, h := range hashes {
		if s.hashes[p] != h {
			return fmt.Errorf("template_profile_conflict: external profile edits; reload daemon first")
		}
	}
	target := ""
	var result []byte
	for file := range hashes {
		data, e := os.ReadFile(filepath.Join(s.dir, file))
		if e != nil {
			return e
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var docs []*yaml.Node
		changed := false
		for {
			var doc yaml.Node
			e = decoder.Decode(&doc)
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			var spec profileSpec
			if e = doc.Decode(&spec); e != nil {
				return e
			}
			if spec.Name == name {
				if spec.TemplateVM != expected {
					return fmt.Errorf("template_profile_conflict: source profile template changed")
				}
				if changed || target != "" {
					return fmt.Errorf("duplicate profile")
				}
				changed = true
				node := doc.Content[0]
				found := false
				for i := 0; i < len(node.Content); i += 2 {
					if node.Content[i].Value == "template_vmid" {
						node.Content[i+1].Value = strconv.Itoa(vmid)
						node.Content[i+1].Tag = "!!int"
						found = true
					}
				}
				if !found {
					return fmt.Errorf("profile lacks template_vmid")
				}
			}
			docs = append(docs, &doc)
		}
		if changed {
			var b bytes.Buffer
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(2)
			for _, d := range docs {
				if e = enc.Encode(d); e != nil {
					return e
				}
			}
			if e = enc.Close(); e != nil {
				return e
			}
			target = file
			result = b.Bytes()
		}
	}
	if target == "" {
		return fmt.Errorf("template_profile_conflict: profile source missing")
	}
	// Parse the entire resulting profile set in a private sibling staging directory.
	stage, e := os.MkdirTemp(s.dir, ".activate-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	for file := range hashes {
		data, e := os.ReadFile(filepath.Join(s.dir, file))
		if e != nil {
			return e
		}
		if file == target {
			data = result
		}
		if e = os.WriteFile(filepath.Join(stage, file), data, 0600); e != nil {
			return e
		}
	}
	profiles, e := LoadProfiles(stage)
	if e != nil {
		return e
	}
	for _, p := range profiles {
		if e = validateProfileForProvisioning(p); e != nil {
			return e
		}
	}
	latest, e := profileHashes(s.dir)
	if e != nil {
		return e
	}
	if len(latest) != len(hashes) {
		return fmt.Errorf("template_profile_conflict: external edit during activation")
	}
	for p, h := range hashes {
		if latest[p] != h {
			return fmt.Errorf("template_profile_conflict: external edit during activation")
		}
	}
	path := filepath.Join(s.dir, target)
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	tmp, e := os.CreateTemp(s.dir, ".profile-")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	// Atomic replacement must retain the existing operator/group access policy.
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		tmp.Close()
		return fmt.Errorf("cannot preserve profile ownership")
	}
	if e = tmp.Chown(int(owner.Uid), int(owner.Gid)); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Chmod(info.Mode().Perm()); e != nil {
		tmp.Close()
		return e
	}
	if _, e = tmp.Write(result); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	latest, e = profileHashes(s.dir)
	if e != nil {
		return e
	}
	if !sameProfileHashes(hashes, latest) {
		return fmt.Errorf("template_profile_conflict: external edit before atomic replacement")
	}
	if e = os.Rename(tmp.Name(), path); e != nil {
		return e
	}
	s.hashes[target] = sha256.Sum256(result)
	s.current.Store(&profiles)
	return nil
}
func (api *ControlAPI) activateTemplate(w http.ResponseWriter, r *http.Request, m tb.Manifest) {
	var req struct {
		Profile  string `json:"profile"`
		Expected *int   `json:"expected_template_vmid"`
	}
	if e := decodeJSON(w, r, &req); e != nil {
		writeJSONDecodeError(w, e)
		return
	}
	if req.Profile == "" || req.Expected == nil || *req.Expected <= 0 {
		writeError(w, 400, "profile and expected_template_vmid are required")
		return
	}
	if api.profileStore == nil || api.backend == nil {
		templateError(w, 503, "template_build_unsupported", "profile activation unavailable on this daemon")
		return
	}
	node, e := os.Hostname()
	if e != nil || m.Node != node {
		templateError(w, 409, "template_capability_missing", "template belongs to a different node")
		return
	}
	cfg, e := api.backend.VMConfig(r.Context(), proxmox.VMID(m.VMID))
	if e != nil || tb.VerifyDrift(cfg, m) != nil {
		templateError(w, 409, "template_capability_missing", "template missing or configuration drift detected")
		return
	}
	if !m.BootVerified || m.BootVerifiedAt == nil {
		templateError(w, 409, "template_capability_missing", "template has no automated boot verification; build a new managed template")
		return
	}
	for _, key := range []string{"tailscale", "agent_runner", "ssh", "qemu_guest_agent"} {
		if m.Capabilities[key] == "" {
			templateError(w, 409, "template_capability_missing", "required guest capability missing")
			return
		}
	}
	profile, ok := api.profileStore.Snapshot()[req.Profile]
	if !ok {
		templateError(w, 409, "template_profile_conflict", "profile missing")
		return
	}
	spec, err := parseProfileProvisionSpec(profile.RawYAML)
	build, buildErr := api.store.TemplateBuild(r.Context(), m.BuildID)
	if err != nil || buildErr != nil || build.State != "SUCCEEDED" {
		templateError(w, 409, "template_capability_missing", "template build or profile is invalid")
		return
	}
	if (spec.Storage.SCSIHW != "" && spec.Storage.SCSIHW != "virtio-scsi-pci") || (spec.Storage.RootSizeGB > 0 && spec.Storage.RootSizeGB < build.Plan.DiskSizeGB) || spec.Resources.Cores < 0 || spec.Resources.MemoryMB < 0 {
		templateError(w, 409, "template_capability_missing", "profile disk or resource settings are incompatible with this template")
		return
	}
	if e = api.profileStore.Activate(req.Profile, *req.Expected, m.VMID); e != nil {
		templateError(w, 409, "template_profile_conflict", e.Error())
		return
	}
	_ = api.store.RecordEvent(r.Context(), "template.activated", nil, nil, req.Profile, templateAuditJSON(EventKindTemplateActivated, map[string]any{"build_id": m.BuildID, "profile": req.Profile, "old_vmid": *req.Expected, "new_vmid": m.VMID}))
	writeJSON(w, 200, map[string]any{"profile": req.Profile, "old_template_vmid": *req.Expected, "template_vmid": m.VMID})
}
