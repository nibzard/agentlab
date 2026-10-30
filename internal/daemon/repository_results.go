package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
)

var repositoryResultPathPattern = regexp.MustCompile(`^repository-results/[0-9a-f]{64}/(?:manifest\.json|source\.bundle|changes\.patch|execution\.tar\.gz)$`)

func artifactMetadata(a db.Artifact) V1ArtifactMetadata {
	return V1ArtifactMetadata{Name: a.Name, Path: a.Path, SizeBytes: a.SizeBytes, Sha256: a.Sha256, MIME: a.MIME}
}

func artifactFileMatches(path, digest string, size int64) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size || size > 256<<20 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	return err == nil && count == size && hex.EncodeToString(hash.Sum(nil)) == digest
}

func (o *JobOrchestrator) WithArtifactRoot(root string) *JobOrchestrator {
	o.artifactRoot = strings.TrimSpace(root)
	return o
}

func (o *JobOrchestrator) prepareRepositoryResult(ctx context.Context, job models.Job, report *JobReport) *protocol.RepositoryResult {
	var body map[string]json.RawMessage
	_ = json.Unmarshal(report.Result, &body)
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	var claim struct {
		ManifestSHA256 string `json:"manifest_sha256"`
	}
	_ = json.Unmarshal(body["repository_result"], &claim)
	receipt := &protocol.RepositoryResult{State: "blocked", CleanupStatus: "blocked", ErrorCode: "repository_result_unverified", Action: "inspect the retained sandbox and collect its results before its lease expires", Resources: []protocol.JobResource{}}
	verified, artifacts, err := o.verifyRepositoryResult(ctx, job, claim.ManifestSHA256, string(report.Status))
	if err == nil {
		expected := executionarchive.Expected{TestCommands: job.TestCommands, JobID: job.ID, SourceID: verified.SourceID, SourceRevision: job.Ref, ManifestSHA256: claim.ManifestSHA256, Status: string(report.Status)}
		evidence, reportErr := executionarchive.ValidateReport(report.Result, expected)
		if reportErr != nil || verified.Execution == nil || !executionarchive.Equal(evidence, *verified.Execution) {
			err = &reposnapshot.Error{Code: "execution_report_mismatch", Action: "inspect the retained sandbox; its terminal report differs from the execution archive"}
		}
	}
	if err == nil {
		receipt = verified
		report.Artifacts = artifacts
	} else {
		// Guest claims cannot publish artifact metadata as verified evidence.
		report.Artifacts = nil
		var requirement *reposnapshot.Error
		if errors.As(err, &requirement) {
			receipt.ErrorCode = requirement.Code
		}
		report.Status = models.JobFailed
		report.Message = "repository results are unverified; inspect the retained sandbox"
	}
	receipt.Target = o.repositoryResultTarget(ctx, job)
	body["repository_result"], _ = json.Marshal(receipt)
	report.Result, _ = json.Marshal(body)
	return receipt
}

func (o *JobOrchestrator) verifyRepositoryResult(ctx context.Context, job models.Job, manifestHash, status string) (*protocol.RepositoryResult, []V1ArtifactMetadata, error) {
	if !repositoryIDPattern.MatchString(manifestHash) || o.artifactRoot == "" {
		return nil, nil, fmt.Errorf("repository result identity is unavailable")
	}
	prefix := "repository-results/" + manifestHash + "/"
	all, err := o.store.ListArtifactsByJob(ctx, job.ID)
	if err != nil {
		return nil, nil, err
	}
	indexed := map[string]db.Artifact{}
	for _, artifact := range all {
		if strings.HasPrefix(artifact.Path, prefix) {
			indexed[artifact.Path] = artifact
		}
	}
	verify := func(name string) (db.Artifact, string, error) {
		path := prefix + name
		artifact, ok := indexed[path]
		if !ok || artifact.VMID == nil || job.SandboxVMID == nil || *artifact.VMID != *job.SandboxVMID {
			return db.Artifact{}, "", fmt.Errorf("result artifact is not bound to the job sandbox")
		}
		absolute := filepath.Join(o.artifactRoot, job.ID, filepath.FromSlash(path))
		if !artifactFileMatches(absolute, artifact.Sha256, artifact.SizeBytes) {
			return db.Artifact{}, "", fmt.Errorf("result artifact is missing or changed")
		}
		return artifact, absolute, nil
	}
	metadata, manifestPath, err := verify("manifest.json")
	if err != nil || metadata.Sha256 != manifestHash || metadata.SizeBytes > 16<<20 {
		return nil, nil, fmt.Errorf("result manifest is missing or changed")
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := reposnapshot.ReadManifest(file)
	file.Close()
	if err != nil || manifest.SourceRevision != job.Ref || manifest.PatchSHA256 == "" {
		return nil, nil, fmt.Errorf("result manifest does not identify the initial job source")
	}
	bundle, bundlePath, err := verify("source.bundle")
	if err != nil {
		return nil, nil, err
	}
	if bundle.Sha256 != manifest.BundleSHA256 || bundle.SizeBytes != manifest.BundleBytes {
		return nil, nil, fmt.Errorf("result bundle does not match the manifest")
	}
	source, _, err := o.store.GetRepositorySnapshot(ctx, strings.TrimPrefix(job.RepoURL, db.SnapshotRepositoryPrefix))
	if err != nil || source.Manifest.Revision != job.Ref {
		return nil, nil, fmt.Errorf("initial repository source evidence is unavailable")
	}
	if err := reposnapshot.ValidateResult(ctx, bundlePath, manifest, source.Manifest); err != nil {
		return nil, nil, err
	}
	artifacts := []V1ArtifactMetadata{artifactMetadata(metadata), artifactMetadata(bundle)}
	patchPath := ""
	if manifest.PatchBytes > 0 {
		patch, _, err := verify("changes.patch")
		if err != nil {
			return nil, nil, err
		}
		if patch.Sha256 != manifest.PatchSHA256 || patch.SizeBytes != manifest.PatchBytes {
			return nil, nil, fmt.Errorf("result patch does not match the validated bundle")
		}
		patchPath = patch.Path
		artifacts = append(artifacts, artifactMetadata(patch))
	}
	execution, executionPath, err := verify("execution.tar.gz")
	if err != nil {
		return nil, nil, err
	}
	evidence, err := executionarchive.Validate(ctx, executionPath, executionarchive.Expected{TestCommands: job.TestCommands, JobID: job.ID, SourceID: source.ID, SourceRevision: job.Ref, ManifestSHA256: manifestHash, Status: status})
	if err != nil {
		return nil, nil, &reposnapshot.Error{Code: "execution_archive_invalid", Action: "inspect the retained sandbox and rebuild its execution evidence"}
	}
	artifacts = append(artifacts, artifactMetadata(execution))
	return &protocol.RepositoryResult{Execution: &evidence, SourceID: source.ID, OriginalRevision: source.Manifest.SourceRevision, State: "verified", ManifestSHA256: manifestHash, SourceRevision: job.Ref, Revision: manifest.Revision, ManifestPath: metadata.Path, BundlePath: bundle.Path, PatchPath: patchPath, PatchSHA256: manifest.PatchSHA256, PatchBytes: manifest.PatchBytes, ExcludedCount: len(manifest.Excluded), VerifiedAt: o.now().UTC().Format(time.RFC3339Nano), CleanupStatus: "pending", Resources: []protocol.JobResource{}}, artifacts, nil
}

func (o *JobOrchestrator) finalizeRepositoryResources(ctx context.Context, job models.Job, report *JobReport, receipt *protocol.RepositoryResult) error {
	resource := protocol.JobResource{Kind: "sandbox", ID: strconv.Itoa(report.VMID), State: "unknown"}
	sandbox, loadErr := o.store.GetSandbox(ctx, report.VMID)
	if loadErr == nil && !sandbox.LeaseExpires.IsZero() {
		resource.ExpiresAt = sandbox.LeaseExpires.UTC().Format(time.RFC3339Nano)
	}
	owned := false
	if loadErr == nil && o.backend != nil {
		if inventory, err := o.backend.ListVMs(ctx); err == nil {
			resource.State = "absent"
			for _, vm := range inventory {
				if int(vm.VMID) == report.VMID {
					if vm.Name != sandbox.Name {
						resource.State = "identity_changed"
					} else if o.repositoryTargetMatches(ctx, job, sandbox, receipt.Target, true) {
						resource.State = "retained"
						owned = true
					} else {
						resource.State = "identity_unverified"
					}
				}
			}
		}
	}
	if receipt.State == "verified" {
		var others int
		checkErr := o.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE sandbox_vmid=? AND id!=? AND status IN ('QUEUED','RUNNING')`, report.VMID, job.ID).Scan(&others)
		if loadErr == nil && resource.State == "absent" && checkErr == nil && others == 0 && o.sandboxManager != nil && o.repositoryTargetMatches(ctx, job, sandbox, receipt.Target, false) {
			// The backend VM is already absent. Reconcile local resources only;
			// do not issue a destroy against an ID that could be reused.
			transitionErr := error(nil)
			if sandbox.State != models.SandboxDestroyed {
				transitionErr = o.sandboxManager.Transition(ctx, report.VMID, models.SandboxDestroyed)
			}
			if transitionErr == nil {
				o.cleanupSnippet(report.VMID)
				o.sandboxManager.cleanupExposures(ctx, report.VMID)
				if err := o.store.DeleteSandboxSecret(ctx, report.VMID); err == nil {
					receipt.CleanupStatus = "verified"
					resource.State = "removed"
					resource.ExpiresAt = ""
				}
			}
		}
		if owned && checkErr == nil && others == 0 {
			if job.Keepalive || sandbox.Keepalive {
				receipt.CleanupStatus = "retained"
			} else if o.sandboxManager != nil {
				if err := o.sandboxManager.Destroy(ctx, report.VMID); err == nil {
					o.cleanupSnippet(report.VMID)
					inventory, err := o.backend.ListVMs(ctx)
					if err == nil {
						found := false
						for _, vm := range inventory {
							if int(vm.VMID) == report.VMID {
								found = true
							}
						}
						if !found {
							resource.State = "removed"
							resource.ExpiresAt = ""
							receipt.CleanupStatus = "verified"
						}
					}
				}
			}
		}
		if receipt.CleanupStatus == "pending" {
			receipt.CleanupStatus = "blocked"
			receipt.Action = "inspect the sandbox backend and confirm cleanup before reporting task completion"
			if receipt.Target == nil || resource.State == "identity_changed" || resource.State == "identity_unverified" {
				receipt.ErrorCode = "repository_cleanup_identity_unverified"
				receipt.Action = "preserve the current VM and inspect the saved server and sandbox identities"
			}
		}
	}
	if receipt.CleanupStatus == "verified" || receipt.CleanupStatus == "retained" {
		receipt.ErrorCode = ""
		receipt.Action = ""
	}
	receipt.Resources = []protocol.JobResource{resource}
	id := strings.TrimPrefix(job.RepoURL, db.SnapshotRepositoryPrefix)
	if source, _, err := o.store.GetRepositorySnapshot(ctx, id); err == nil {
		receipt.Resources = append(receipt.Resources, protocol.JobResource{Kind: "repository_upload", ID: id, State: "retained", ExpiresAt: source.ExpiresAt.Format(time.RFC3339Nano)})
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(report.Result, &body)
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	body["repository_result"], _ = json.Marshal(receipt)
	report.Result, _ = json.Marshal(body)
	result, err := buildJobResult(report.Status, report.Message, report.Artifacts, report.Result, o.now().UTC())
	if err != nil {
		return err
	}
	return o.store.UpdateJobResult(ctx, job.ID, report.Status, result)
}
