package daemon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorBundleDeterministic(t *testing.T) {
	input := doctorBundleInput{
		Meta: doctorMeta{
			Version: doctorBundleVersion,
			Kind:    "job",
			ID:      "job-test-1",
		},
		Job: &V1JobResponse{
			ID:        "job-test-1",
			RepoURL:   "https://example.com/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    "RUNNING",
			CreatedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-01-01T00:00:00Z",
		},
		Sandbox: &V1SandboxResponse{
			VMID:          101,
			Name:          "sbx-test",
			Profile:       "default",
			State:         "RUNNING",
			CreatedAt:     "2024-01-01T00:00:00Z",
			LastUpdatedAt: "2024-01-01T00:00:00Z",
		},
		JobEvents: &V1EventsResponse{
			Events: []V1Event{{
				ID:        1,
				Kind:      "job.created",
				Timestamp: "2024-01-01T00:00:00Z",
				Message:   "created",
			}},
			LastID: 1,
		},
		Artifacts: &V1ArtifactsResponse{
			JobID: "job-test-1",
			Artifacts: []V1Artifact{{
				Name:      "bundle.tar.gz",
				Path:      "bundle.tar.gz",
				SizeBytes: 42,
				Sha256:    "abc123",
				CreatedAt: "2024-01-01T00:00:00Z",
			}},
		},
		Proxmox: &doctorProxmoxInfo{
			VMID:   101,
			Status: "running",
			Config: newDoctorOrderedConfig(map[string]string{
				"boot":  "order=scsi0",
				"cores": "4",
			}),
		},
	}

	redactor := NewRedactor(nil)
	bundleA := buildDoctorBundleBytes(t, input, redactor)
	bundleB := buildDoctorBundleBytes(t, input, redactor)

	if !bytes.Equal(bundleA, bundleB) {
		t.Fatalf("doctor bundles should be deterministic")
	}

	names := doctorBundleFileNames(t, bundleA)
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	assert.Equal(t, sorted, names)
	assert.Contains(t, names, "meta.json")
	assert.Contains(t, names, "records/job.json")
	assert.Contains(t, names, "proxmox.json")
}

func TestDoctorBundleRedaction(t *testing.T) {
	secret := "super-secret-value"
	result := json.RawMessage([]byte(`{"token":"` + secret + `"}`))
	input := doctorBundleInput{
		Meta: doctorMeta{
			Version: doctorBundleVersion,
			Kind:    "job",
			ID:      "job-secret",
		},
		Job: &V1JobResponse{
			ID:        "job-secret",
			RepoURL:   "https://example.com/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    "FAILED",
			Result:    result,
			CreatedAt: "2024-01-01T00:00:00Z",
			UpdatedAt: "2024-01-01T00:00:00Z",
		},
		JobEvents: &V1EventsResponse{
			Events: []V1Event{{
				ID:        9,
				Kind:      "job.failed",
				Timestamp: "2024-01-01T00:00:00Z",
				Message:   "token=" + secret,
			}},
			LastID: 9,
		},
		Proxmox: &doctorProxmoxInfo{
			VMID:   101,
			Status: "stopped",
			Config: newDoctorOrderedConfig(map[string]string{
				"token": secret,
			}),
		},
	}

	redactor := NewRedactor(nil)
	bundle := buildDoctorBundleBytes(t, input, redactor)
	contents := doctorBundleContents(t, bundle)

	for name, payload := range contents {
		if strings.Contains(payload, secret) {
			t.Fatalf("bundle %s contains secret value", name)
		}
	}
	var redacted bool
	for _, payload := range contents {
		if strings.Contains(payload, "[REDACTED]") {
			redacted = true
			break
		}
	}
	if !redacted {
		t.Fatalf("expected redacted markers in bundle")
	}
}

func buildDoctorBundleBytes(t *testing.T, input doctorBundleInput, redactor *Redactor) []byte {
	t.Helper()
	files, err := buildDoctorFiles(redactor, input)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, writeDoctorBundle(&buf, files))
	return buf.Bytes()
}

func doctorBundleFileNames(t *testing.T, bundle []byte) []string {
	t.Helper()
	reader := doctorBundleReader(t, bundle)
	var names []string
	for {
		hdr, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	return names
}

func doctorBundleContents(t *testing.T, bundle []byte) map[string]string {
	t.Helper()
	reader := doctorBundleReader(t, bundle)
	contents := make(map[string]string)
	for {
		hdr, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		payload, err := io.ReadAll(reader)
		require.NoError(t, err)
		contents[hdr.Name] = string(payload)
	}
	return contents
}

func doctorBundleReader(t *testing.T, bundle []byte) *tar.Reader {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	require.NoError(t, err)
	return tar.NewReader(gz)
}
