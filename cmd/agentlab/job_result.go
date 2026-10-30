package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
)

var resultHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type resultRequirement struct {
	*blockedRequirement
	JobID     string
	JobStatus string
	Receipt   protocol.RepositoryResult
}

func (e *resultRequirement) Unwrap() error { return e.blockedRequirement }

func (e *resultRequirement) ErrorMetadata() map[string]any {
	return map[string]any{"job_id": e.JobID, "job_status": e.JobStatus, "repository_result": e.Receipt, "resources": e.Receipt.Resources}
}

type collectedJobResult struct {
	JobID            string                    `json:"job_id"`
	ServerIdentity   string                    `json:"server_identity"`
	JobStatus        string                    `json:"job_status"`
	TestCommands     []string                  `json:"test_commands,omitempty"`
	RepositoryResult protocol.RepositoryResult `json:"repository_result"`
	Artifacts        []artifactInfo            `json:"artifacts"`
}

func runJobResult(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("job result")
	base.bind(fs)
	help := bindHelpFlag(fs)
	output := fs.String("out", "", "private directory for downloaded results")
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab job result JOB_ID [--out DIRECTORY] [--json]") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("job result requires one job ID")
	}
	client, err := apiClientFromFlags(base)
	if err != nil {
		return err
	}
	caps, err := fetchCapabilities(ctx, client)
	if err != nil {
		return err
	}
	if !caps.Features["repository_results"] {
		return &blockedRequirement{Code: "unsupported_feature", Requirement: "repository_results", Action: "use a daemon and VM template with verified repository result support"}
	}
	path, err := endpointPath("/v1/jobs", fs.Arg(0))
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, http.MethodGet, path+"?events_tail=0", nil)
	if err != nil {
		return err
	}
	var job jobResponse
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	if job.ID != fs.Arg(0) || !strings.HasPrefix(job.RepoURL, protocol.RepositorySnapshotPrefix) {
		return fmt.Errorf("the requested job does not identify a repository snapshot")
	}
	var envelope struct {
		Artifacts []artifactInfo `json:"artifacts"`
		Result    struct {
			RepositoryResult protocol.RepositoryResult `json:"repository_result"`
		} `json:"result"`
	}
	if len(job.Result) > 0 {
		if err := json.Unmarshal(job.Result, &envelope); err != nil {
			return err
		}
	}
	receipt := envelope.Result.RepositoryResult
	if receipt.Resources == nil {
		receipt.Resources = []protocol.JobResource{}
	}
	if (job.Status != "COMPLETED" && job.Status != "FAILED" && job.Status != "TIMEOUT") || receipt.State != "verified" {
		action := receipt.Action
		if action == "" {
			action = "observe the job until its repository results are verified"
		}
		return &resultRequirement{blockedRequirement: &blockedRequirement{Code: "repository_result_unavailable", Requirement: "verified_repository_result", Action: action}, JobID: job.ID, JobStatus: job.Status, Receipt: receipt}
	}
	if !resultHashPattern.MatchString(receipt.ManifestSHA256) || receipt.SourceRevision != job.Ref || (receipt.SourceID != "" && job.RepoURL != protocol.RepositorySnapshotPrefix+receipt.SourceID) {
		return fmt.Errorf("repository result identity does not match the job")
	}
	if *output == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		*output = filepath.Join(cache, "agentlab", "results")
	}
	record := collectedJobResult{JobID: job.ID, ServerIdentity: caps.ServerIdentity, JobStatus: job.Status, TestCommands: job.TestCommands, RepositoryResult: receipt, Artifacts: envelope.Artifacts}
	destination, err := collectJobResult(ctx, client, *output, record)
	if err != nil {
		return err
	}
	result := map[string]any{"job_id": job.ID, "job_status": job.Status, "out": destination, "repository_result": receipt, "resources": receipt.Resources, "patch_path": filepath.Join(destination, "changes.patch"), "execution_path": filepath.Join(destination, "execution.tar.gz"), "receipt_path": filepath.Join(destination, "receipt.json")}
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Fprintf(os.Stdout, "Job: %s (%s)\nVerified results: %s\nPatch: %s\nCleanup: %s\n", job.ID, job.Status, destination, result["patch_path"], receipt.CleanupStatus)
	if receipt.Execution != nil {
		fmt.Fprintf(os.Stdout, "Test commands: %s (guest reported)\n", receipt.Execution.Tests)
	}
	return nil
}

func collectJobResult(ctx context.Context, client *apiClient, root string, record collectedJobResult) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	metadata, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(metadata)
	destination := filepath.Join(root, "result-"+hex.EncodeToString(sum[:]))
	expected, err := resultArtifacts(record)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := verifyCollectedResult(ctx, destination, metadata, record, expected); err != nil {
			return "", &blockedRequirement{Code: "result_cache_changed", Requirement: "verified_local_results", Action: "preserve the modified cache files and choose another --out directory to collect a fresh result"}
		}
		return destination, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	identity := sha256.Sum256([]byte(record.ServerIdentity + "\x00" + client.target() + "\x00" + record.JobID))
	key := hex.EncodeToString(identity[:])
	release, err := lockResultOutput(ctx, root, key)
	if err != nil {
		return "", err
	}
	defer release()
	prefix := ".collect-" + key + "-"
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return "", err
			}
		}
	}
	temp, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	for name, artifact := range expected {
		if err := downloadResultArtifact(ctx, client, record.JobID, artifact, filepath.Join(temp, name)); err != nil {
			return "", err
		}
	}
	if record.RepositoryResult.PatchBytes == 0 {
		if err := os.WriteFile(filepath.Join(temp, "changes.patch"), nil, 0600); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(temp, "receipt.json"), metadata, 0600); err != nil {
		return "", err
	}
	if err := verifyCollectedResult(ctx, temp, metadata, record, expected); err != nil {
		return "", err
	}
	if err := syncResultFiles(temp); err != nil {
		return "", err
	}
	if err := os.Rename(temp, destination); err != nil {
		if err := verifyCollectedResult(ctx, destination, metadata, record, expected); err != nil {
			return "", err
		}
	}
	if err := syncResultDirectory(root); err != nil {
		return "", err
	}
	return destination, nil
}

func resultArtifacts(record collectedJobResult) (map[string]artifactInfo, error) {
	receipt := record.RepositoryResult
	prefix := "repository-results/" + receipt.ManifestSHA256 + "/"
	paths := map[string]string{"manifest.json": prefix + "manifest.json", "source.bundle": prefix + "source.bundle", "execution.tar.gz": prefix + "execution.tar.gz"}
	if receipt.ManifestPath != paths["manifest.json"] || receipt.BundlePath != paths["source.bundle"] {
		return nil, fmt.Errorf("invalid repository result artifact paths")
	}
	if receipt.PatchBytes > 0 {
		paths["changes.patch"] = prefix + "changes.patch"
		if receipt.PatchPath != paths["changes.patch"] {
			return nil, fmt.Errorf("invalid repository patch path")
		}
	}
	result := map[string]artifactInfo{}
	for name, path := range paths {
		for _, artifact := range record.Artifacts {
			if artifact.Path == path {
				if artifact.SizeBytes <= 0 || artifact.SizeBytes > 256<<20 || !resultHashPattern.MatchString(artifact.Sha256) {
					return nil, fmt.Errorf("invalid result artifact metadata")
				}
				result[name] = artifact
			}
		}
		if _, ok := result[name]; !ok {
			return nil, fmt.Errorf("verified result artifact metadata is missing")
		}
	}
	if result["manifest.json"].Sha256 != receipt.ManifestSHA256 {
		return nil, fmt.Errorf("result manifest hash does not match its receipt")
	}
	return result, nil
}

func downloadResultArtifact(ctx context.Context, client *apiClient, jobID string, artifact artifactInfo, destination string) error {
	path, err := endpointPath("/v1/jobs", jobID, "artifacts", "download")
	if err != nil {
		return err
	}
	response, err := client.doRequest(ctx, http.MethodGet, path+"?path="+url.QueryEscape(artifact.Path), nil, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.SizeBytes+1))
	if err != nil {
		return err
	}
	if size != artifact.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != artifact.Sha256 {
		return &blockedRequirement{Code: "artifact_integrity_failed", Requirement: "verified_artifact", Action: "inspect artifact storage; the downloaded result differs from the saved receipt"}
	}
	return file.Sync()
}

func verifyCollectedResult(ctx context.Context, root string, metadata []byte, record collectedJobResult, expected map[string]artifactInfo) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("result cache is not a directory")
	}
	receiptInfo, err := os.Lstat(filepath.Join(root, "receipt.json"))
	if err != nil || !receiptInfo.Mode().IsRegular() || receiptInfo.Size() != int64(len(metadata)) {
		return fmt.Errorf("result cache receipt changed")
	}
	data, err := os.ReadFile(filepath.Join(root, "receipt.json"))
	if err != nil || !bytes.Equal(data, metadata) {
		return fmt.Errorf("result cache receipt changed")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 5 {
		return fmt.Errorf("result cache contains unexpected files")
	}
	for name, artifact := range expected {
		if err := verifyResultFile(filepath.Join(root, name), artifact.Sha256, artifact.SizeBytes); err != nil {
			return err
		}
	}
	file, err := os.Open(filepath.Join(root, "manifest.json"))
	if err != nil {
		return err
	}
	manifest, err := reposnapshot.ReadManifest(file)
	file.Close()
	if err != nil {
		return err
	}
	receipt := record.RepositoryResult
	if manifest.SourceRevision != receipt.SourceRevision || manifest.Revision != receipt.Revision || manifest.PatchSHA256 != receipt.PatchSHA256 || manifest.PatchBytes != receipt.PatchBytes {
		return fmt.Errorf("downloaded manifest does not match the saved repository result")
	}
	if err := verifyResultFile(filepath.Join(root, "changes.patch"), manifest.PatchSHA256, manifest.PatchBytes); err != nil {
		return err
	}
	if err := reposnapshot.Validate(ctx, filepath.Join(root, "source.bundle"), manifest); err != nil {
		return err
	}
	evidence, err := executionarchive.Validate(ctx, filepath.Join(root, "execution.tar.gz"), executionarchive.Expected{TestCommands: record.TestCommands, JobID: record.JobID, SourceID: receipt.SourceID, SourceRevision: receipt.SourceRevision, ManifestSHA256: receipt.ManifestSHA256, Status: record.JobStatus})
	if err != nil {
		return err
	}
	if receipt.Execution == nil || !executionarchive.Equal(*receipt.Execution, evidence) {
		return fmt.Errorf("execution evidence does not match the saved receipt")
	}
	return nil
}

func verifyResultFile(path, digest string, size int64) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("result cache file is missing or changed")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, size+1))
	if err != nil {
		return err
	}
	if count != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("result cache checksum changed")
	}
	return nil
}

func lockResultOutput(ctx context.Context, root, key string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(root, ".result-"+key+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
		} else if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func syncResultFiles(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		file, err := os.Open(filepath.Join(root, entry.Name()))
		if err != nil {
			return err
		}
		err = file.Sync()
		file.Close()
		if err != nil {
			return err
		}
	}
	return syncResultDirectory(root)
}

func syncResultDirectory(root string) error {
	file, err := os.Open(root)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
