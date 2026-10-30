package reposnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSnapshot(t *testing.T) {
	root := testRepo(t)
	writeTestFile(t, root, "code.txt", "local change\n")
	writeTestFile(t, root, "new.txt", "new file\n")
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePack(context.Background(), result.BundlePath); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.BundleSHA256 = "incorrect" },
		func(m *Manifest) { m.PolicyVersion++ },
		func(m *Manifest) { m.Files[0].Bytes++ },
		func(m *Manifest) { m.Changes = nil },
		func(m *Manifest) { m.SourceRevision = m.Revision },
	} {
		m, err := ReadManifest(bytes.NewReader(mustJSON(result.Manifest)))
		if err != nil {
			t.Fatal(err)
		}
		change(&m)
		if err := Validate(context.Background(), result.BundlePath, m); err == nil {
			t.Fatal("changed manifest was accepted")
		}
	}
}

func TestValidateRejectsExtraHistoryAndRefs(t *testing.T) {
	root := testRepo(t)
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", result.BundlePath, clone)
	testGit(t, clone, "commit", "--allow-empty", "-m", "unexpected history")
	bundle := filepath.Join(t.TempDir(), "extra.bundle")
	testGit(t, clone, "bundle", "create", bundle, "--all")
	data, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	result.Manifest.BundleSHA256 = hex.EncodeToString(sum[:])
	result.Manifest.BundleBytes = int64(len(data))
	if err := Validate(context.Background(), bundle, result.Manifest); err == nil {
		t.Fatal("bundle with extra refs and history was accepted")
	}
}

func TestReadManifestRejectsTrailingAndUnknownData(t *testing.T) {
	for _, data := range []string{`{} {}`, `{} broken`, `{"unknown":true}`} {
		if _, err := ReadManifest(bytes.NewBufferString(data)); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}

func TestValidateRejectsHistoryWithMatchingRefs(t *testing.T) {
	root := testRepo(t)
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, filepath.Dir(clone), "clone", "-b", "agentlab-snapshot", result.BundlePath, clone)
	testGit(t, clone, "branch", "agentlab-base", result.Manifest.BaseRevision)
	testGit(t, clone, "commit", "--allow-empty", "-m", "unexpected history")
	result.Manifest.Revision = testGit(t, clone, "rev-parse", "HEAD")
	bundle := filepath.Join(t.TempDir(), "history.bundle")
	testGit(t, clone, "bundle", "create", bundle, "refs/heads/agentlab-base", "refs/heads/agentlab-snapshot")
	data, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	result.Manifest.BundleSHA256 = hex.EncodeToString(sum[:])
	result.Manifest.BundleBytes = int64(len(data))
	if err := Validate(context.Background(), bundle, result.Manifest); err == nil {
		t.Fatal("matching refs hid additional history")
	}
}

func TestValidateEmptySnapshot(t *testing.T) {
	root := testRepo(t)
	if err := os.Remove(filepath.Join(root, "code.txt")); err != nil {
		t.Fatal(err)
	}
	result, err := Create(context.Background(), Options{Repository: root, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), result.BundlePath, result.Manifest); err != nil {
		t.Fatal(err)
	}
}
