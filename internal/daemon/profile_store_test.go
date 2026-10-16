package daemon

import (
	"github.com/agentlab/agentlab/internal/models"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestProfileActivationPreservesDocumentsAndRejectsStaleEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	original := `# host profiles
name: alpha
template_vmid: 9999
resources:
  memory_mb: 4096
custom: keep-this
---
name: beta
template_vmid: 9001
custom: preserve-this-too
`
	if e := os.WriteFile(path, []byte(original), 0640); e != nil {
		t.Fatal(e)
	}
	profiles, e := LoadProfiles(dir)
	if e != nil {
		t.Fatal(e)
	}
	store, e := NewProfileStore(dir, profiles)
	if e != nil {
		t.Fatal(e)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	if e = store.Activate("alpha", 9998, 9010); e == nil {
		t.Fatal("stale expected VMID accepted")
	}
	if e = store.Activate("alpha", 9999, 9010); e != nil {
		t.Fatal(e)
	}
	if before["alpha"].TemplateVM != 9999 {
		t.Fatal("existing revision mutated")
	}
	if store.Snapshot()["alpha"].TemplateVM != 9010 || store.Snapshot()["beta"].TemplateVM != 9001 {
		t.Fatal("snapshot not coherently updated")
	}
	changedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	oldOwner := originalInfo.Sys().(*syscall.Stat_t)
	newOwner := changedInfo.Sys().(*syscall.Stat_t)
	if oldOwner.Uid != newOwner.Uid || oldOwner.Gid != newOwner.Gid || changedInfo.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatal("activation changed profile access policy")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{"# host profiles", "custom: keep-this", "custom: preserve-this-too", "memory_mb: 4096", "---"} {
		if !strings.Contains(string(raw), text) {
			t.Fatalf("lost %q", text)
		}
	}
	external := append(raw, []byte("\n# external edit\n")...)
	if e = os.WriteFile(path, external, 0640); e != nil {
		t.Fatal(e)
	}
	if e = store.Activate("alpha", 9010, 9011); e == nil {
		t.Fatal("external edit overwritten")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(external) || store.Snapshot()["alpha"].TemplateVM != 9010 {
		t.Fatal("failed activation changed disk or snapshot")
	}
}
func TestProfileActivationRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.yaml")
	_ = os.WriteFile(target, []byte("name: outside\ntemplate_vmid: 9000\n"), 0600)
	if e := os.Symlink(target, filepath.Join(dir, "profile.yaml")); e != nil {
		t.Fatal(e)
	}
	if _, e := profileHashes(dir); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestProfileStoreRejectsEditsBetweenLoadAndInitialization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.yaml")
	for _, change := range []struct{ name, content string }{
		{"template", "name: test\ntemplate_vmid: 9001\nresources:\n  memory_mb: 4096\n"},
		{"unrelated-field", "name: test\ntemplate_vmid: 9000\nresources:\n  memory_mb: 8192\n"},
	} {
		t.Run(change.name, func(t *testing.T) {
			initial := []byte("name: test\ntemplate_vmid: 9000\nresources:\n  memory_mb: 4096\n")
			if err := os.WriteFile(path, initial, 0600); err != nil {
				t.Fatal(err)
			}
			profiles, err := LoadProfiles(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte(change.content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = NewProfileStore(dir, profiles); err == nil {
				t.Fatal("bound new hashes to an old profile snapshot")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != change.content {
				t.Fatal("initialization mutated external edit")
			}
		})
	}
}
func TestProfileStoreInitializationDoesNotHideFileErrors(t *testing.T) {
	if _, err := NewProfileStore(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing profile directory silently accepted")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("name: test\ntemplate_vmid: 9000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "test.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProfileStore(dir, nil); err == nil {
		t.Fatal("symlink profile directory entry silently accepted")
	}
}

func TestProfileStoreAcceptsEquivalentRawDocumentAndImplicitVMType(t *testing.T) {
	dir := t.TempDir()
	raw := "\nname: test\ntemplate_vmid: 9000\nresources:\n  cores: 2\n"
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := map[string]models.Profile{"test": {Name: "test", TemplateVM: 9000, RawYAML: raw}}
	store, err := NewProfileStore(dir, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Activate("test", 9000, 9001); err != nil {
		t.Fatal(err)
	}
	if store.Snapshot()["test"].TemplateVM != 9001 {
		t.Fatal("implicit VM profile could not activate")
	}
}
