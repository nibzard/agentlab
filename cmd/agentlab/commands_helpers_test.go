package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func intPtr(value int) *int {
	return &value
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()
	_ = w.Close()
	os.Stdout = oldStdout
	out, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

func TestParseVMID(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"valid", "42", 42, false},
		{"trim", " 7 ", 7, false},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, true},
		{"nonnumeric", "abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVMID(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVMID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseVMID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseTTLMinutes(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    *int
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"minutes", "15", intPtr(15), false},
		{"duration", "1h", intPtr(60), false},
		{"duration ceil", "1m30s", intPtr(2), false},
		{"seconds ceil", "90s", intPtr(2), false},
		{"zero", "0", nil, true},
		{"negative", "-1", nil, true},
		{"bad", "nonsense", nil, true},
		{"negative duration", "-1m", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTTLMinutes(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTTLMinutes() error = %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("parseTTLMinutes() = %v, want %v", got, *tt.want)
			}
		})
	}
}

func TestParseRequiredTTLMinutes(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"empty", "", 0, true},
		{"zero", "0", 0, true},
		{"duration", "30s", 1, false},
		{"minutes", "2", 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequiredTTLMinutes(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRequiredTTLMinutes() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseRequiredTTLMinutes() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseSizeGB(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"plain", "10", 10, false},
		{"suffix g", "10g", 10, false},
		{"suffix gb", "10GB", 10, false},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, true},
		{"bad", "abc", 0, true},
		{"unsupported suffix", "10tb", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSizeGB(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSizeGB() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseSizeGB() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOrDashHelpers(t *testing.T) {
	if orDash(" ") != "-" {
		t.Fatalf("orDash should return - for empty")
	}
	if orDash("ok") != "ok" {
		t.Fatalf("orDash should return value")
	}

	if orDashPtr(nil) != "-" {
		t.Fatalf("orDashPtr should return - for nil")
	}
	empty := " "
	if orDashPtr(&empty) != "-" {
		t.Fatalf("orDashPtr should return - for empty")
	}
	value := "yes"
	if orDashPtr(&value) != "yes" {
		t.Fatalf("orDashPtr should return value")
	}

	if ttlMinutesString(nil) != "-" {
		t.Fatalf("ttlMinutesString should return - for nil")
	}
	zero := 0
	if ttlMinutesString(&zero) != "-" {
		t.Fatalf("ttlMinutesString should return - for zero")
	}
	five := 5
	if ttlMinutesString(&five) != "5" {
		t.Fatalf("ttlMinutesString should return number")
	}

	if vmidString(nil) != "-" {
		t.Fatalf("vmidString should return - for nil")
	}
	if vmidString(&zero) != "-" {
		t.Fatalf("vmidString should return - for zero")
	}
	vmid := 42
	if vmidString(&vmid) != "42" {
		t.Fatalf("vmidString should return number")
	}
}

func TestSelectArtifact(t *testing.T) {
	artifacts := []artifactInfo{
		{Name: "first", Path: "first.txt"},
		{Name: defaultArtifactBundleName, Path: "bundle.tar.gz"},
		{Name: "last", Path: "last.txt"},
	}

	if _, err := selectArtifact(nil, "", "", false, false); err == nil {
		t.Fatalf("expected error for empty artifacts")
	}

	got, err := selectArtifact(artifacts, "first.txt", "", false, false)
	if err != nil {
		t.Fatalf("selectArtifact(path) error = %v", err)
	}
	if got.Name != "first" {
		t.Fatalf("expected first artifact, got %s", got.Name)
	}

	if _, err := selectArtifact(artifacts, "missing.txt", "", false, false); err == nil {
		t.Fatalf("expected error for missing path")
	}

	if _, err := selectArtifact(artifacts, "", "bad/name", false, false); err == nil {
		t.Fatalf("expected error for path separators in name")
	}

	got, err = selectArtifact(artifacts, "", "last", false, false)
	if err != nil {
		t.Fatalf("selectArtifact(name) error = %v", err)
	}
	if got.Name != "last" {
		t.Fatalf("expected last artifact, got %s", got.Name)
	}

	got, err = selectArtifact(artifacts, "", "", false, true)
	if err != nil {
		t.Fatalf("selectArtifact(bundle) error = %v", err)
	}
	if got.Name != defaultArtifactBundleName {
		t.Fatalf("expected bundle artifact, got %s", got.Name)
	}

	got, err = selectArtifact(artifacts, "", "", true, false)
	if err != nil {
		t.Fatalf("selectArtifact(latest) error = %v", err)
	}
	if got.Name != "last" {
		t.Fatalf("expected latest artifact, got %s", got.Name)
	}

	noBundle := []artifactInfo{{Name: "one", Path: "one"}, {Name: "two", Path: "two"}}
	got, err = selectArtifact(noBundle, "", "", false, true)
	if err != nil {
		t.Fatalf("selectArtifact(bundle fallback) error = %v", err)
	}
	if got.Name != "two" {
		t.Fatalf("expected fallback to latest, got %s", got.Name)
	}
}

func TestResolveArtifactOutPath(t *testing.T) {
	tmp := t.TempDir()

	path, err := resolveArtifactOutPath("", "")
	if err != nil {
		t.Fatalf("resolveArtifactOutPath() error = %v", err)
	}
	if path != "artifact" {
		t.Fatalf("expected default name, got %s", path)
	}

	path, err = resolveArtifactOutPath("", "bundle")
	if err != nil {
		t.Fatalf("resolveArtifactOutPath() error = %v", err)
	}
	if path != "bundle" {
		t.Fatalf("expected bundle name, got %s", path)
	}

	outDir := filepath.Join(tmp, "outdir") + string(os.PathSeparator)
	path, err = resolveArtifactOutPath(outDir, "file.txt")
	if err != nil {
		t.Fatalf("resolveArtifactOutPath() error = %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(tmp, "outdir")) {
		t.Fatalf("expected output under outdir, got %s", path)
	}

	existingDir := filepath.Join(tmp, "existing")
	if err := os.MkdirAll(existingDir, 0o750); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	path, err = resolveArtifactOutPath(existingDir, "name.bin")
	if err != nil {
		t.Fatalf("resolveArtifactOutPath() error = %v", err)
	}
	if path != filepath.Join(existingDir, "name.bin") {
		t.Fatalf("unexpected output path %s", path)
	}

	nested := filepath.Join(tmp, "nested", "out.txt")
	path, err = resolveArtifactOutPath(nested, "ignored")
	if err != nil {
		t.Fatalf("resolveArtifactOutPath() error = %v", err)
	}
	if path != nested {
		t.Fatalf("unexpected output path %s", path)
	}
	if _, err := os.Stat(filepath.Dir(nested)); err != nil {
		t.Fatalf("expected nested dir to exist: %v", err)
	}
}

func TestPrintEvents(t *testing.T) {
	events := []eventResponse{
		{ID: 1, Timestamp: "2026-01-30T00:00:00Z", Kind: "sandbox.created", Message: ""},
		{ID: 3, Timestamp: "2026-01-30T00:01:00Z", Kind: "job.started", JobID: "job-1", Message: "started"},
	}

	out := captureStdout(t, func() {
		last := printEvents(events, false)
		if last != 3 {
			t.Fatalf("lastID = %d, want 3", last)
		}
	})
	if !strings.Contains(out, "job=-") {
		t.Fatalf("expected placeholder job, got %q", out)
	}
	if !strings.Contains(out, "job=job-1") {
		t.Fatalf("expected job id output, got %q", out)
	}

	jsonOut := captureStdout(t, func() {
		last := printEvents(events, true)
		if last != 3 {
			t.Fatalf("lastID = %d, want 3", last)
		}
	})
	if !strings.Contains(jsonOut, "\"id\":1") || !strings.Contains(jsonOut, "\"id\":3") {
		t.Fatalf("expected json output, got %q", jsonOut)
	}
}
