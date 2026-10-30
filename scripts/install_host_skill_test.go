package assets

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Run the actual installer functions without its privileged host setup. All
// files, config writes, and tool failures stay in this test's temporary tree.
type hostSkillFixture struct {
	root, source, skills, metadata, harness string
}

func newHostSkillFixture(t *testing.T) hostSkillFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the host installer requires Linux")
	}
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("Linux installer tests require flock")
	}
	script, err := os.ReadFile("install_host.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start, end := strings.Index(text, "json_field() {"), strings.Index(text, "\nCONFIG_PATH=")
	if start < 0 || end <= start {
		t.Fatal("installer skill functions not found")
	}
	f := hostSkillFixture{root: t.TempDir()}
	f.source = filepath.Join(f.root, "source bundle")
	f.skills = filepath.Join(f.root, "loader directory", "skills")
	f.metadata = filepath.Join(f.root, "metadata")
	f.harness = filepath.Join(f.root, "install-skill.sh")
	if err := os.CopyFS(f.source, os.DirFS("../skills/agentlab")); err != nil {
		t.Fatal(err)
	}
	harness := `#!/usr/bin/env bash
set -euo pipefail
log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
config_upsert() { printf '%s=%s\n' "$1" "$2" >> "$FIXTURE_METADATA"; }
SKILL_SRC_DIR="$FIXTURE_SOURCE"
SKILL_MANIFEST_PATH="$SKILL_SRC_DIR/bundle/manifest.json"
SKILL_INSTALL_DIR_NAME=agentlab
CLAUDE_SKILLS_DIR="$FIXTURE_SKILLS"
INSTALL_SKILLS=1
CLAUDE_SKILL_FORCE="${FIXTURE_FORCE:-0}"
CLAUDE_SKILL_VERSION="${FIXTURE_REQUIRED_VERSION:-}"
` + text[start:end] + "\ninstall_claude_skill\n"
	if err := os.WriteFile(f.harness, []byte(harness), 0700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f hostSkillFixture) command(ctx context.Context, extra ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", f.harness)
	cmd.Env = append(os.Environ(), "FIXTURE_SOURCE="+f.source, "FIXTURE_SKILLS="+f.skills, "FIXTURE_METADATA="+f.metadata)
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

func (f hostSkillFixture) run(t *testing.T, extra ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := f.command(ctx, extra...).CombinedOutput()
	if err != nil {
		t.Fatalf("install skill: %v\n%s", err, out)
	}
	return string(out)
}

func skillTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[name] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			result[name] = "symlink:" + target
			return err
		}
		if !entry.Type().IsRegular() {
			result[name] = entry.Type().String()
			return nil
		}
		data, err := os.ReadFile(path)
		result[name] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertSkillTree(t *testing.T, want, got map[string]string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("tree sizes differ: %d != %d", len(want), len(got))
	}
	for name, content := range want {
		if actual, ok := got[name]; !ok || actual != content {
			t.Fatalf("skill entry changed: %s", name)
		}
	}
}

func pendingSkill(t *testing.T, f hostSkillFixture) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(f.skills), ".agentlab-skill-updates", "*", "agentlab"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one pending bundle: %v %v", paths, err)
	}
	return paths[0]
}

func TestHostSkillInstallAndIdenticalRetry(t *testing.T) {
	f := newHostSkillFixture(t)
	if err := os.MkdirAll(f.skills, 0700); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	parent, err := os.Stat(f.skills)
	if err != nil || parent.Mode().Perm() != 0700 {
		t.Fatal("installer changed existing loader directory permissions")
	}
	active := filepath.Join(f.skills, "agentlab")
	assertSkillTree(t, skillTree(t, f.source), skillTree(t, active))
	before, err := os.Stat(filepath.Join(active, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if output := f.run(t); !strings.Contains(output, "already up to date") {
		t.Fatal(output)
	}
	after, err := os.Stat(filepath.Join(active, "SKILL.md"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("identical retry replaced instructions")
	}
	metadata, err := os.ReadFile(f.metadata)
	if err != nil || !bytes.Contains(metadata, []byte("claude_skill_bundle_version=2.0.0")) {
		t.Fatalf("missing active version metadata: %s %v", metadata, err)
	}
}

func TestHostSkillUpgradePreservesExistingInstructions(t *testing.T) {
	for _, mode := range []string{"modified", "extra-file", "old-version", "symlink-root", "symlink-file", "dangling-symlink", "regular-file"} {
		t.Run(mode, func(t *testing.T) {
			f := newHostSkillFixture(t)
			active := filepath.Join(f.skills, "agentlab")
			if err := os.MkdirAll(f.skills, 0755); err != nil {
				t.Fatal(err)
			}
			if mode != "regular-file" && mode != "symlink-root" && mode != "dangling-symlink" {
				if err := os.CopyFS(active, os.DirFS(f.source)); err != nil {
					t.Fatal(err)
				}
			}
			var preserve string
			preserveData := []byte("Keep these user instructions.\n")
			var outsideBefore map[string]string
			var outside string
			switch mode {
			case "modified":
				preserve = filepath.Join(active, "SKILL.md")
			case "extra-file":
				preserve = filepath.Join(active, "local.md")
			case "old-version":
				preserve = filepath.Join(active, "bundle", "manifest.json")
				preserveData = []byte("{\n  \"name\": \"agentlab\",\n  \"version\": \"1.0.0\"\n}\n")
			case "regular-file":
				preserve = active
			case "dangling-symlink":
				if err := os.Symlink(filepath.Join(f.root, "missing"), active); err != nil {
					t.Fatal(err)
				}
			case "symlink-root", "symlink-file":
				outside = filepath.Join(f.root, "custom")
				if err := os.CopyFS(outside, os.DirFS(f.source)); err != nil {
					t.Fatal(err)
				}
				link, target := active, outside
				if mode == "symlink-file" {
					link = filepath.Join(active, "SKILL.md")
					target = filepath.Join(outside, "SKILL.md")
					if err := os.Remove(link); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				outsideBefore = skillTree(t, outside)
			}
			if preserve != "" {
				if err := os.WriteFile(preserve, preserveData, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := skillTree(t, active)
			metadataBefore := []byte("claude_skill_bundle_name=custom\nclaude_skill_bundle_version=old\n")
			if err := os.WriteFile(f.metadata, metadataBefore, 0600); err != nil {
				t.Fatal(err)
			}
			output := f.run(t, "FIXTURE_FORCE=1")
			if !strings.Contains(output, "Preserved existing agent skill:") {
				t.Fatal(output)
			}
			assertSkillTree(t, before, skillTree(t, active))
			if outsideBefore != nil {
				assertSkillTree(t, outsideBefore, skillTree(t, outside))
			}
			pending := pendingSkill(t, f)
			assertSkillTree(t, skillTree(t, f.source), skillTree(t, pending))
			if metadata, err := os.ReadFile(f.metadata); err != nil || !bytes.Equal(metadata, metadataBefore) {
				t.Fatal("pending update changed active metadata")
			}
			f.run(t)
			if pendingSkill(t, f) != pending {
				t.Fatal("retry staged a duplicate update")
			}
		})
	}
}

func TestHostSkillInstallConcurrentPublication(t *testing.T) {
	f := newHostSkillFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if out, err := f.command(ctx).CombinedOutput(); err != nil {
				t.Errorf("concurrent install: %v\n%s", err, out)
			}
		})
	}
	wg.Wait()
	assertSkillTree(t, skillTree(t, f.source), skillTree(t, filepath.Join(f.skills, "agentlab")))
	paths, err := filepath.Glob(filepath.Join(f.skills, ".agentlab-skill-????????"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary bundles remain: %v %v", paths, err)
	}
}

func TestHostSkillInstallFailureDoesNotPublishPartialBundle(t *testing.T) {
	f := newHostSkillFixture(t)
	tools := filepath.Join(f.root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	realInstall, err := exec.LookPath("install")
	if err != nil {
		t.Fatal(err)
	}
	// Fail only a file copy, after the staging directory has been created.
	wrapper := fmt.Sprintf("#!/bin/bash\nif [[ $1 == -m && $2 == 0644 ]]; then exit 73; fi\nexec %q \"$@\"\n", realInstall)
	if err := os.WriteFile(filepath.Join(tools, "install"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := f.command(ctx, "PATH="+tools+":"+os.Getenv("PATH")).CombinedOutput(); err == nil {
		t.Fatalf("injected failure succeeded: %s", out)
	}
	if _, err := os.Lstat(filepath.Join(f.skills, "agentlab")); !os.IsNotExist(err) {
		t.Fatal("failed copy published a bundle")
	}
	paths, err := filepath.Glob(filepath.Join(f.skills, ".agentlab-skill-????????"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("failed staging files remain: %v %v", paths, err)
	}
}

func TestHostSkillInstallDoesNotReplaceAnEntryCreatedDuringPublication(t *testing.T) {
	f := newHostSkillFixture(t)
	tools := filepath.Join(f.root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	realMove, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := fmt.Sprintf(`#!/bin/bash
target="${@: -1}"
mkdir -p "$target"
printf 'Preserve concurrent instructions.\n' > "$target/SKILL.md"
exec %q "$@"
`, realMove)
	if err := os.WriteFile(filepath.Join(tools, "mv"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := f.command(ctx, "PATH="+tools+":"+os.Getenv("PATH")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "existing files are preserved") {
		t.Fatalf("publication race did not block: %v %s", err, out)
	}
	active := skillTree(t, filepath.Join(f.skills, "agentlab"))
	assertSkillTree(t, map[string]string{".": "directory", "SKILL.md": "Preserve concurrent instructions.\n"}, active)
	if _, err := os.Stat(f.metadata); !os.IsNotExist(err) {
		t.Fatal("failed publication changed metadata")
	}
}

func TestHostSkillInstallRefusesChangedPendingBundle(t *testing.T) {
	f := newHostSkillFixture(t)
	f.run(t)
	active := filepath.Join(f.skills, "agentlab", "SKILL.md")
	if err := os.WriteFile(active, []byte("Keep active instructions.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	pending := pendingSkill(t, f)
	if err := os.WriteFile(filepath.Join(pending, "SKILL.md"), []byte("Keep pending edits.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := skillTree(t, pending)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := f.command(ctx).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "pending skill bundle has changed") {
		t.Fatalf("modified pending bundle not blocked: %v %s", err, out)
	}
	assertSkillTree(t, before, skillTree(t, pending))
}

func TestHostSkillInstallRefusesUnsafeEntries(t *testing.T) {
	for _, mode := range []string{"lock-symlink", "lock-fifo", "updates-symlink", "source-symlink", "wrong-version"} {
		t.Run(mode, func(t *testing.T) {
			f := newHostSkillFixture(t)
			if err := os.MkdirAll(f.skills, 0700); err != nil {
				t.Fatal(err)
			}
			extra := []string{}
			switch mode {
			case "lock-symlink":
				if err := os.Symlink(filepath.Join(f.root, "missing"), filepath.Join(f.skills, ".agentlab-skill-install.lock")); err != nil {
					t.Fatal(err)
				}
			case "lock-fifo":
				if err := syscall.Mkfifo(filepath.Join(f.skills, ".agentlab-skill-install.lock"), 0600); err != nil {
					t.Fatal(err)
				}
			case "updates-symlink":
				if err := os.WriteFile(filepath.Join(f.skills, "agentlab"), []byte("preserve"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(f.root, "missing"), filepath.Join(filepath.Dir(f.skills), ".agentlab-skill-updates")); err != nil {
					t.Fatal(err)
				}
			case "source-symlink":
				if err := os.Symlink(filepath.Join(f.root, "missing"), filepath.Join(f.source, "local.md")); err != nil {
					t.Fatal(err)
				}
			case "wrong-version":
				extra = append(extra, "FIXTURE_REQUIRED_VERSION=0.0.0")
			}
			before := skillTree(t, f.skills)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := f.command(ctx, extra...).CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "ERROR:") {
				t.Fatalf("unsafe entry was not rejected promptly: %v %s", err, out)
			}
			// A normal lock may be created before the pending-directory check.
			after := skillTree(t, f.skills)
			if mode == "updates-symlink" {
				delete(after, ".agentlab-skill-install.lock")
			}
			assertSkillTree(t, before, after)
		})
	}
}
