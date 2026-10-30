// Package executionarchive validates guest execution evidence without extraction.
package executionarchive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/agentlab/agentlab/internal/contentpolicy"
)

const maxExpandedBytes = 64 << 20

// Evidence describes guest-reported execution. It does not prove tests passed.
type Evidence struct {
	State           string    `json:"state"`
	AgentExitCode   int       `json:"agent_exit_code"`
	DurationSeconds int64     `json:"duration_seconds"`
	Tests           string    `json:"tests"`
	TestRuns        []TestRun `json:"test_runs,omitempty"`
}

// Expected binds an archive to the admitted job and the result namespace.
type Expected struct {
	JobID, SourceID, SourceRevision, ManifestSHA256, Status string
	TestCommands                                            []string
}

type report struct {
	AgentExitCode    *int      `json:"agent_exit_code"`
	TestRuns         []TestRun `json:"test_runs"`
	SchemaVersion    *int      `json:"schema_version"`
	JobID            string    `json:"job_id"`
	SourceID         string    `json:"source_id"`
	SourceRevision   string    `json:"source_revision"`
	Status           string    `json:"status"`
	ExitCode         *int      `json:"exit_code"`
	DurationSeconds  *int64    `json:"duration_seconds"`
	RepositoryResult struct {
		ManifestSHA256 string `json:"manifest_sha256"`
	} `json:"repository_result"`
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// Validate checks bounded, regular archive contents and a job-bound report.
// It never extracts files, executes code, or infers a test verdict.
func Validate(ctx context.Context, filename string, expected Expected) (Evidence, error) {
	return ValidateWithSecrets(ctx, filename, expected, nil)
}

// ValidateWithSecrets also rejects known credential values before guest upload.
// Credential values never appear in returned errors.
func ValidateWithSecrets(ctx context.Context, filename string, expected Expected, secrets [][]byte) (Evidence, error) {
	var evidence Evidence
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return evidence, fmt.Errorf("execution archive is not a bounded regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return evidence, err
	}
	defer file.Close()
	return validateReader(ctx, file, expected, secrets, nil)
}

func validateReader(ctx context.Context, input io.Reader, expected Expected, secrets [][]byte, visit func(string, []byte)) (Evidence, error) {
	var evidence Evidence
	compressed := bufio.NewReader(contextReader{ctx, input})
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return evidence, fmt.Errorf("invalid execution archive")
	}
	defer reader.Close()
	reader.Multistream(false)
	bounded := &io.LimitedReader{R: reader, N: maxExpandedBytes + 1}
	archive := tar.NewReader(bounded)
	seen := map[string]bool{}
	directories := map[string]bool{}
	var reportBytes []byte
	logFound := false
	logDigests := map[string]string{}
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return evidence, err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 1000 || bounded.N <= 0 {
			return evidence, fmt.Errorf("invalid or oversized execution archive")
		}
		name := strings.TrimPrefix(header.Name, "./")
		for key := range header.PAXRecords {
			switch key {
			case "path", "size", "mtime", "atime", "ctime", "uid", "gid", "uname", "gname":
			default:
				return evidence, fmt.Errorf("unsupported execution archive metadata")
			}
		}
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "." || name == "" {
			if header.Typeflag != tar.TypeDir || seen["."] {
				return evidence, fmt.Errorf("invalid archive root")
			}
			seen["."] = true
			continue
		}
		if !utf8.ValidString(name) || len(name) > 4096 || strings.ContainsAny(name, ":\\\x00\r\n") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] || header.Linkname != "" || header.Mode&07000 != 0 {
			return evidence, fmt.Errorf("unsafe or duplicate execution archive entry")
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] && !directories[parent] {
				return evidence, fmt.Errorf("archive parent is not a directory")
			}
		}
		if header.Typeflag != tar.TypeDir {
			for previous := range seen {
				if strings.HasPrefix(previous, name+"/") {
					return evidence, fmt.Errorf("archive file replaces a directory")
				}
			}
		}
		seen[name] = true
		if contentpolicy.SensitivePath(name) || contentpolicy.SensitiveContent(name, []byte(name)) || contentpolicy.ContainsSecret([]byte(name), secrets) {
			return evidence, fmt.Errorf("execution archive contains a credential path")
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return evidence, fmt.Errorf("invalid archive directory")
			}
			directories[name] = true
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 8<<20 || header.Mode&0111 != 0 {
			return evidence, fmt.Errorf("unsupported execution archive entry")
		}
		if name == "report.json" && header.Size > 64<<10 {
			return evidence, fmt.Errorf("execution report is too large")
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			return evidence, fmt.Errorf("truncated execution archive")
		}
		if contentpolicy.SensitiveContent(name, data) {
			return evidence, fmt.Errorf("execution archive contains recognized credentials")
		}
		if contentpolicy.ContainsSecret(data, secrets) {
			return evidence, fmt.Errorf("execution archive contains a known credential")
		}
		if visit != nil {
			visit(name, data)
		}
		if strings.HasPrefix(name, "tests/") {
			digest := sha256.Sum256(data)
			logDigests[name] = hex.EncodeToString(digest[:])
		}
		if name == "report.json" {
			reportBytes = data
		}
		if name == "agent-runner.log" {
			logFound = true
		}
	}
	// Drain the gzip trailer for its checksum. Only tar padding can follow EOF.
	buffer := make([]byte, 32<<10)
	for {
		n, err := bounded.Read(buffer)
		for _, value := range buffer[:n] {
			if value != 0 {
				return evidence, fmt.Errorf("unexpected data after execution archive")
			}
		}
		if bounded.N <= 0 {
			return evidence, fmt.Errorf("execution archive exceeds expanded limit")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return evidence, fmt.Errorf("invalid execution archive checksum")
		}
	}
	if _, err := compressed.ReadByte(); err != io.EOF {
		return evidence, fmt.Errorf("unexpected compressed data after execution archive")
	}
	if !logFound {
		return evidence, fmt.Errorf("execution archive has no runner log")
	}
	evidence, err = ValidateReport(reportBytes, expected)
	if err != nil {
		return Evidence{}, err
	}
	expectedLogs := 0
	for _, run := range evidence.TestRuns {
		if run.State == "skipped" {
			continue
		}
		expectedLogs++
		if logDigests[run.LogPath] != run.LogSHA256 {
			return Evidence{}, fmt.Errorf("test log does not match its report")
		}
	}
	if expectedLogs != len(logDigests) {
		return Evidence{}, fmt.Errorf("execution archive contains unrequested test logs")
	}
	return evidence, nil
}

// ValidateReport verifies the same fields in the archive and terminal report.
func ValidateReport(data []byte, expected Expected) (Evidence, error) {
	return validateReport(data, expected, true)
}

// ValidateTestReport checks command reports for URL-based jobs. It does not
// verify their artifact archive or grant a verified repository receipt.
func ValidateTestReport(data []byte, expected Expected) (Evidence, error) {
	if len(expected.TestCommands) == 0 {
		return Evidence{}, fmt.Errorf("requested test plan is empty")
	}
	return validateReport(data, expected, false)
}

func validateReport(data []byte, expected Expected, snapshot bool) (Evidence, error) {
	var evidence Evidence
	var body report
	decoder := json.NewDecoder(bytes.NewReader(data))
	if len(data) == 0 || len(data) > 64<<10 || uniqueJSON(decoder, 0) != nil {
		return evidence, fmt.Errorf("invalid execution report JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return evidence, fmt.Errorf("unexpected data after execution report")
	}
	if json.Unmarshal(data, &body) != nil || body.SchemaVersion == nil || *body.SchemaVersion != 1 || body.ExitCode == nil || body.DurationSeconds == nil || *body.ExitCode < 0 || *body.ExitCode > 255 || *body.DurationSeconds < 0 {
		return evidence, fmt.Errorf("execution report fields are missing or invalid")
	}
	if expected.JobID == "" || body.JobID != expected.JobID || body.Status != expected.Status || (snapshot && (expected.SourceID == "" || expected.SourceRevision == "" || expected.ManifestSHA256 == "" || body.SourceID != expected.SourceID || body.SourceRevision != expected.SourceRevision || body.RepositoryResult.ManifestSHA256 != expected.ManifestSHA256)) {
		return evidence, fmt.Errorf("execution report does not match the admitted job")
	}
	if (body.Status == "COMPLETED" && *body.ExitCode != 0) || (body.Status == "FAILED" && (*body.ExitCode == 0 || *body.ExitCode == 124)) || (body.Status == "TIMEOUT" && *body.ExitCode != 124) || (body.Status != "COMPLETED" && body.Status != "FAILED" && body.Status != "TIMEOUT") {
		return evidence, fmt.Errorf("execution status conflicts with its exit code")
	}
	agentExit := *body.ExitCode
	if body.AgentExitCode != nil {
		agentExit = *body.AgentExitCode
		if agentExit < 0 || agentExit > 255 {
			return evidence, fmt.Errorf("invalid agent exit code")
		}
	}
	if body.Status == "COMPLETED" && agentExit != 0 {
		return evidence, fmt.Errorf("completed job reports agent failure")
	}
	tests, err := validateTests(body, expected, agentExit)
	if err != nil {
		return evidence, err
	}
	if len(body.TestRuns) == 0 {
		body.TestRuns = nil
	}
	return Evidence{State: "guest_reported", AgentExitCode: agentExit, DurationSeconds: *body.DurationSeconds, Tests: tests, TestRuns: body.TestRuns}, nil
}

// Reject duplicate keys and deep nesting before normal struct decoding.
func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("execution report is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return fmt.Errorf("unexpected JSON delimiter")
	}
	keys := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate execution report key")
			}
			for previous := range keys {
				if strings.EqualFold(previous, name) {
					return fmt.Errorf("ambiguous execution report key")
				}
			}
			keys[name] = true
		}
		if err := uniqueJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
