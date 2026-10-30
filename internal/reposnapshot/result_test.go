package reposnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResultPatchUsesInitialRevisionAndPreservesTaskCommits(t *testing.T) {
	root := testRepo(t)
	initial := testGit(t, root, "rev-parse", "HEAD")
	writeTestFile(t, root, "code.txt", "agent committed change\n")
	testGit(t, root, "add", "code.txt")
	testGit(t, root, "commit", "-m", "agent change")
	head := testGit(t, root, "rev-parse", "HEAD")
	writeTestFile(t, root, "new.txt", "untracked task result\n")
	writeTestFile(t, root, "binary.dat", string([]byte{0, 1, 2, 3, 4}))
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir(), BaseRevision: initial})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
	if result.Manifest.SourceRevision != initial || testGit(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("task source revision was not preserved")
	}
	patch, err := os.ReadFile(result.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "agent committed change") || !strings.Contains(string(patch), "untracked task result") {
		t.Fatal("patch omitted task changes")
	}
	clone := filepath.Join(t.TempDir(), "review")
	testGit(t, filepath.Dir(clone), "clone", root, clone)
	testGit(t, clone, "checkout", "--detach", initial)
	testGit(t, clone, "apply", "--check", result.PatchPath)
	testGit(t, clone, "apply", result.PatchPath)
	for _, name := range []string{"code.txt", "new.txt", "binary.dat"} {
		actual, err := os.ReadFile(filepath.Join(clone, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(actual) != string(want) {
			t.Fatalf("patch did not reproduce %s", name)
		}
	}
}

func TestResultExclusionDoesNotDeleteOriginalFile(t *testing.T) {
	root := testRepo(t)
	initial := testGit(t, root, "rev-parse", "HEAD")
	writeTestFile(t, root, "code.txt", "PASSWORD=do-not-transfer-this-value\n")
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir(), BaseRevision: initial})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile(result.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 || len(result.Manifest.Changes) != 0 {
		t.Fatal("excluded task content became a deletion")
	}
	if len(result.Manifest.Excluded) != 1 {
		t.Fatal("exclusion was not reported")
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
}

func TestResultExcludesKnownRuntimeCredentialsFromBothTrees(t *testing.T) {
	root := testRepo(t)
	initial := testGit(t, root, "rev-parse", "HEAD")
	secret := []byte("opaque-runtime-value\nsecond-line")
	writeTestFile(t, root, "code.txt", string(secret))
	writeTestFile(t, root, "binary.dat", string(append([]byte{0, 1, 2}, secret...)))
	secretFile := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(secretFile, append(append([]byte{}, secret...), 0), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := RunResultCommand(context.Background(), []string{"--repo", root, "--base-revision", initial, "--out", t.TempDir(), "--secret-file", secretFile}, &output)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile(result.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 || len(result.Manifest.Changes) != 0 || len(result.Manifest.Excluded) != 2 {
		t.Fatal("known credentials must be excluded without deleting original source")
	}
	if bytes.Contains(output.Bytes(), secret) {
		t.Fatal("credential appeared in exporter output")
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
	if testGit(t, root, "rev-parse", "HEAD") != initial {
		t.Fatal("source revision changed")
	}
	data, err := os.ReadFile(filepath.Join(root, "code.txt"))
	if err != nil || !bytes.Equal(data, secret) {
		t.Fatal("source content changed")
	}
}

func TestCredentialInPathDoesNotAppearInExclusionManifest(t *testing.T) {
	for _, recognized := range []bool{false, true} {
		root := testRepo(t)
		secret := "opaque-filename-credential"
		if recognized {
			secret = "ghp_" + strings.Repeat("X", 40)
		}
		writeTestFile(t, root, secret+".txt", "ordinary text")
		output := t.TempDir()
		_, err := Create(context.Background(), Options{Repository: root, OutputDir: output, Secrets: [][]byte{[]byte(secret)}})
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("credential path must fail without exposing its value")
		}
		entries, err := os.ReadDir(output)
		if err != nil || len(entries) != 0 {
			t.Fatal("credential path published result metadata")
		}
	}
}

func TestSourceTransportPolicyOverridesLocalConfiguration(t *testing.T) {
	root := testRepo(t)
	command := exec.Command("git", "-c", "protocol.file.allow=always", "ls-remote", root)
	command.Env = cleanGitEnv(false)
	if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "not allowed") {
		t.Fatalf("source transport was allowed: %s", data)
	}
}

func TestValidateResultBindsInitialSourceContent(t *testing.T) {
	root := testRepo(t)
	initial, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "task")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", initial.BundlePath, clone)
	writeTestFile(t, clone, "code.txt", "task changes\n")
	result, err := Create(context.Background(), Options{Repository: clone, OutputDir: t.TempDir(), BaseRevision: initial.Manifest.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResult(context.Background(), result.BundlePath, result.Manifest, initial.Manifest); err != nil {
		t.Fatal(err)
	}
	changed := initial.Manifest
	changed.Files = append([]File{}, changed.Files...)
	changed.Files[0].SHA256 = strings.Repeat("0", 64)
	if err := ValidateResult(context.Background(), result.BundlePath, result.Manifest, changed); err == nil {
		t.Fatal("a revision label hid different original source bytes")
	}
	changed = initial.Manifest
	changed.Files = nil
	if err := ValidateResult(context.Background(), result.BundlePath, result.Manifest, changed); err == nil {
		t.Fatal("an extra base file was accepted")
	}
}

func TestValidateResultAllowsOnlySymmetricExplicitExclusions(t *testing.T) {
	root := testRepo(t)
	initial, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "task")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", initial.BundlePath, clone)
	writeTestFile(t, clone, "code.txt", "PASSWORD=private-task-value\n")
	result, err := Create(context.Background(), Options{Repository: clone, OutputDir: t.TempDir(), BaseRevision: initial.Manifest.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResult(context.Background(), result.BundlePath, result.Manifest, initial.Manifest); err != nil {
		t.Fatal(err)
	}
	result.Manifest.Excluded = nil
	if err := ValidateResult(context.Background(), result.BundlePath, result.Manifest, initial.Manifest); err == nil {
		t.Fatal("an omitted source file had no exclusion record")
	}
}

func TestValidateResultRejectsForgedBaseWithCorrectRevisionLabel(t *testing.T) {
	ctx := context.Background()
	root := testRepo(t)
	initial, err := Create(ctx, Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	if _, err := git(ctx, private, true, nil, 1<<20, "init", "--bare", "--template=", "."); err != nil {
		t.Fatal(err)
	}
	base := tree{"code.txt": {mode: "100644", data: []byte("forged prior source\n")}}
	current := tree{"code.txt": {mode: "100644", data: []byte("forged new source\n")}}
	var stream bytes.Buffer
	if err := writeCommit(&stream, "refs/heads/agentlab-base", "", "AgentLab sanitized source "+initial.Manifest.Revision, 1, base); err != nil {
		t.Fatal(err)
	}
	if err := writeCommit(&stream, "refs/heads/agentlab-snapshot", "refs/heads/agentlab-base", "AgentLab working tree snapshot", 1, current); err != nil {
		t.Fatal(err)
	}
	stream.WriteString("done\n")
	if _, err := git(ctx, private, true, &stream, 1<<20, "fast-import", "--quiet"); err != nil {
		t.Fatal(err)
	}
	baseID := testGit(t, private, "rev-parse", "refs/heads/agentlab-base")
	revision := testGit(t, private, "rev-parse", "refs/heads/agentlab-snapshot")
	bundle := filepath.Join(t.TempDir(), "forged.bundle")
	testGit(t, private, "-c", "pack.window=0", "bundle", "create", bundle, "refs/heads/agentlab-base", "refs/heads/agentlab-snapshot")
	digest, size, err := hashFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := patchBetween(ctx, private, baseID, revision)
	if err != nil {
		t.Fatal(err)
	}
	patchHash := sha256.Sum256(patch)
	fileHash := sha256.Sum256(current["code.txt"].data)
	manifest := Manifest{PolicyVersion: PolicyVersion, SourceRevision: initial.Manifest.Revision, BaseRevision: baseID, Revision: revision, BundleSHA256: digest, BundleBytes: size, PatchSHA256: hex.EncodeToString(patchHash[:]), PatchBytes: int64(len(patch)), ContentBytes: int64(len(current["code.txt"].data)), Files: []File{{Path: "code.txt", Mode: "100644", SHA256: hex.EncodeToString(fileHash[:]), Bytes: int64(len(current["code.txt"].data))}}, Changes: []Change{{Path: "code.txt", Status: "M"}}, Excluded: []Exclusion{}}
	if err := Validate(ctx, bundle, manifest); err != nil {
		t.Fatalf("fixture must satisfy structural validation: %v", err)
	}
	if err := ValidateResult(ctx, bundle, manifest, initial.Manifest); err == nil {
		t.Fatal("a forged base passed with the correct source revision label")
	}
}

func TestSourceReplacementRefsDoNotChangeSnapshotBase(t *testing.T) {
	root := testRepo(t)
	original := testGit(t, root, "rev-parse", "HEAD:code.txt")
	writeTestFile(t, root, "replacement.txt", "replacement content\n")
	replacement := testGit(t, root, "hash-object", "-w", "replacement.txt")
	if err := os.Remove(filepath.Join(root, "replacement.txt")); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "replace", original, replacement)
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Manifest.Changes) != 0 {
		t.Fatal("replacement refs changed the original source tree")
	}
}
