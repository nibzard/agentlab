package reposnapshot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", root, "-c", "user.name=Snapshot test", "-c", "user.email=snapshot@example.invalid", "-c", "core.hooksPath=" + os.DevNull}, args...)
	command := exec.Command("git", argv...)
	command.Env = append(cleanGitEnv(true), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	destination := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required")
	}
	root := t.TempDir()
	testGit(t, root, "init", "--initial-branch=main")
	writeTestFile(t, root, "code.txt", "original\n")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "source base")
	return root
}

func TestSnapshotPreservesWorkingTreeAndSanitizesHistory(t *testing.T) {
	root := testRepo(t)
	writeTestFile(t, root, "history.txt", "historic-secret-never-transferred")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "private historical commit")
	historic := testGit(t, root, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(root, "history.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".env.production", "unrecognized-secret-protected-by-path")
	writeTestFile(t, root, "delete.txt", "delete me")
	writeTestFile(t, root, "tracked-ignore.cfg", "ignored-even-when-tracked")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "current source")
	source := testGit(t, root, "rev-parse", "HEAD")
	writeTestFile(t, root, ".gitignore", "*.cache\ntracked-ignore.cfg\n")
	writeTestFile(t, root, "ignored.cache", "ignored-private-data")
	writeTestFile(t, root, "token.txt", "ghp_"+strings.Repeat("A", 40))
	writeTestFile(t, root, "new file.txt", "new safe file")
	writeTestFile(t, root, "code.txt", "staged value\n")
	testGit(t, root, "add", "code.txt")
	writeTestFile(t, root, "code.txt", "effective unstaged value\n")
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	indexBefore, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	indexAfter, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("source index changed")
	}
	if testGit(t, root, "rev-parse", "HEAD") != source {
		t.Fatal("source HEAD changed")
	}
	contents, err := os.ReadFile(filepath.Join(root, "code.txt"))
	if err != nil || string(contents) != "effective unstaged value\n" {
		t.Fatal("source changes were overwritten")
	}
	if result.Manifest.SourceRevision != source {
		t.Fatal("source revision was not identified")
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", result.BundlePath, clone)
	imported, err := os.ReadFile(filepath.Join(clone, "code.txt"))
	if err != nil || string(imported) != "effective unstaged value\n" {
		t.Fatal("effective local changes were not transferred")
	}
	for _, name := range []string{".env.production", "ignored.cache", "tracked-ignore.cfg", "token.txt", "history.txt", "delete.txt"} {
		if _, err := os.Stat(filepath.Join(clone, name)); !os.IsNotExist(err) {
			t.Fatalf("excluded or deleted file transferred: %s", name)
		}
	}
	for _, revision := range []string{source, historic} {
		command := exec.Command("git", "-C", clone, "cat-file", "-e", revision)
		if command.Run() == nil {
			t.Fatal("original history was transferred")
		}
	}
	objects := testGit(t, clone, "rev-list", "--objects", "--all")
	for _, line := range strings.Split(objects, "\n") {
		id := strings.Fields(line)[0]
		if testGit(t, clone, "cat-file", "-t", id) != "blob" {
			continue
		}
		data := testGit(t, clone, "cat-file", "blob", id)
		for _, secret := range []string{"historic-secret-never-transferred", "unrecognized-secret-protected-by-path", "ignored-private-data", "ignored-even-when-tracked", "ghp_" + strings.Repeat("A", 40)} {
			if strings.Contains(data, secret) {
				t.Fatal("credential data in transferred Git objects")
			}
		}
	}
	if len(result.Manifest.Changes) < 3 {
		t.Fatal("transferred changes were not identified")
	}
}

func TestSnapshotRespectsConfiguredGlobalIgnores(t *testing.T) {
	root := testRepo(t)
	ignore := filepath.Join(t.TempDir(), "ignore")
	if err := os.WriteFile(ignore, []byte("private.local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[core]\nexcludesFile = "+ignore+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	writeTestFile(t, root, "private.local", "custom-global-private-value")
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Manifest.Files {
		if file.Path == "private.local" {
			t.Fatal("global ignore setting was lost")
		}
	}
}

func TestSnapshotSymlinksAndUnusualPaths(t *testing.T) {
	root := testRepo(t)
	writeTestFile(t, root, "name with \"quotes\" and \nnewline.txt", "safe text")
	writeTestFile(t, root, "café.txt", "Unicode path")
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("outside-secret-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("code.txt", filepath.Join(root, "safe-link")); err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", result.BundlePath, clone)
	if _, err := os.Lstat(filepath.Join(clone, "outside")); !os.IsNotExist(err) {
		t.Fatal("outside symlink transferred")
	}
	target, err := os.Readlink(filepath.Join(clone, "safe-link"))
	if err != nil || target != "code.txt" {
		t.Fatal("safe symlink changed")
	}
	for _, name := range []string{"name with \"quotes\" and \nnewline.txt", "café.txt"} {
		if _, err := os.Stat(filepath.Join(clone, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotLimitsCacheAndOutputLocation(t *testing.T) {
	root := testRepo(t)
	cache := t.TempDir()
	first, err := Create(context.Background(), Options{Repository: root, OutputDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Create(context.Background(), Options{Repository: root, OutputDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Revision != again.Manifest.Revision || first.BundlePath != again.BundlePath {
		t.Fatal("snapshot is not deterministic")
	}
	if err := os.WriteFile(first.BundlePath, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), Options{Repository: root, OutputDir: cache}); err == nil {
		t.Fatal("tampered bundle accepted")
	}
	if _, err := Create(context.Background(), Options{Repository: root, OutputDir: filepath.Join(root, "output")}); err == nil {
		t.Fatal("snapshot writes into source repository")
	}
	if _, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir(), MaxFileBytes: 2}); err == nil {
		t.Fatal("file limit ignored")
	}
}

func TestSnapshotDoesNotRunSourceFiltersOrHooks(t *testing.T) {
	root := testRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\ncat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "config", "core.fsmonitor", hook)
	testGit(t, root, "config", "filter.private.clean", hook)
	testGit(t, root, "config", "filter.private.smudge", hook)
	writeTestFile(t, root, ".gitattributes", "code.txt filter=private\n")
	writeTestFile(t, root, "code.txt", "local changes that must be read directly")
	if _, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("source configuration executed a program")
	}
}

func TestSnapshotCredentialPolicyAndExecutableMode(t *testing.T) {
	root := testRepo(t)
	files := map[string]string{
		"settings.json":       `{"password":"test-secret-value"}`,
		"settings.yaml":       "access_token: test-secret-value\n",
		"settings.toml":       "client_secret = 'test-secret-value'\n",
		"settings.ini":        "api_key=test-secret-value\n",
		"plain.txt":           "PASSWORD=test-secret-value\n",
		".docker/config.json": `{"auths":{"host":{"auth":"test-secret-value"}}}`,
		".git-credentials":    "opaque-secret-data",
		"secrets.yml":         "opaque-secret-data",
	}
	for name, content := range files {
		writeTestFile(t, root, name, content)
	}
	writeTestFile(t, root, "run.sh", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range result.Manifest.Files {
		if _, excluded := files[file.Path]; excluded {
			t.Fatalf("credential file included: %s", file.Path)
		}
		if file.Path == "run.sh" {
			found = file.Mode == "100755"
		}
	}
	if !found {
		t.Fatal("executable mode was lost")
	}
}

func TestSnapshotCacheSeparatesExclusionMetadata(t *testing.T) {
	root := testRepo(t)
	cache := t.TempDir()
	first, err := Create(context.Background(), Options{Repository: root, OutputDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".env", "private-value")
	second, err := Create(context.Background(), Options{Repository: root, OutputDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Revision != second.Manifest.Revision || first.Manifest.BundleSHA256 != second.Manifest.BundleSHA256 {
		t.Fatal("excluded data changed the transferred repository")
	}
	if first.ManifestPath == second.ManifestPath || len(second.Manifest.Excluded) != 1 {
		t.Fatal("updated exclusion metadata was not saved separately")
	}
}

func TestSnapshotRejectsSourceOutputThroughSymlink(t *testing.T) {
	root := testRepo(t)
	link := filepath.Join(t.TempDir(), "source-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), Options{Repository: root, OutputDir: filepath.Join(link, "cache", "new")}); err == nil {
		t.Fatal("symlink output reached source repository")
	}
	if _, err := os.Stat(filepath.Join(root, "cache")); !os.IsNotExist(err) {
		t.Fatal("source output was created")
	}
}

func TestSnapshotRejectsIndexOnlySubmodules(t *testing.T) {
	root := testRepo(t)
	revision := testGit(t, root, "rev-parse", "HEAD")
	testGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+revision+",module")
	_, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	typed, ok := err.(*Error)
	if !ok || typed.Code != "repository_submodule_unsupported" {
		t.Fatalf("expected explicit submodule failure, got %v", err)
	}
}

func TestSnapshotBoundsSymlinkChains(t *testing.T) {
	root := testRepo(t)
	for i := 0; i < 40; i++ {
		target := "code.txt"
		if i < 39 {
			target = fmt.Sprintf("link-%02d", i+1)
		}
		if err := os.Symlink(target, filepath.Join(root, fmt.Sprintf("link-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Manifest.Files {
		if item.Path == "link-00" {
			t.Fatal("overlong symlink chain was retained")
		}
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
}
