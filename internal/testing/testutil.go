// Package testing provides shared test utilities for agentlab.
package testing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AssertJSONEqual asserts that two JSON values are semantically equal.
func AssertJSONEqual(t *testing.T, want, got any, msgAndArgs ...interface{}) {
	t.Helper()
	wantBytes, err := json.Marshal(want)
	require.NoError(t, err, "failed to marshal 'want' to JSON")
	gotBytes, err := json.Marshal(got)
	require.NoError(t, err, "failed to marshal 'got' to JSON")

	var wantAny, gotAny any
	require.NoError(t, json.Unmarshal(wantBytes, &wantAny), "failed to unmarshal 'want'")
	require.NoError(t, json.Unmarshal(gotBytes, &gotAny), "failed to unmarshal 'got'")

	assert.Equal(t, wantAny, gotAny, msgAndArgs...)
}

// TempFile creates a temporary file with the given content and returns its path.
// The file is automatically cleaned up when the test completes.
func TempFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "testfile")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err, "failed to write temp file")
	return path
}

// MkdirTempInDir creates a temporary directory under the given parent directory.
// Unlike t.TempDir(), this allows specifying the parent.
func MkdirTempInDir(t *testing.T, parentDir string) string {
	t.Helper()
	path, err := os.MkdirTemp(parentDir, "testdir*")
	require.NoError(t, err, "failed to create temp dir")
	t.Cleanup(func() {
		_ = os.RemoveAll(path)
	})
	return path
}

// ParseTime parses an RFC3339 timestamp.
func ParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err, "failed to parse time %q", s)
	return ts
}

// RequireNoError asserts that err is nil, with a more descriptive message.
func RequireNoError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	require.NoError(t, err, msgAndArgs...)
}

// RequireEqual asserts that two values are equal, with a more descriptive message.
func RequireEqual(t *testing.T, expected, actual any, msgAndArgs ...interface{}) {
	t.Helper()
	require.Equal(t, expected, actual, msgAndArgs...)
}
