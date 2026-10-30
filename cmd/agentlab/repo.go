package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentlab/agentlab/internal/reposnapshot"
)

func runRepoCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: agentlab repo <snapshot|result|upload|show|remove> [options]")
		return errHelp
	}
	switch args[0] {
	case "result":
		return runRepoResultCommand(ctx, args[1:], base)
	case "upload":
		return runRepoUpload(ctx, args[1:], base)
	case "show", "remove":
		return runRepoRemote(ctx, args[0], args[1:], base)
	}
	if args[0] != "snapshot" {
		return fmt.Errorf("unknown repo command %q", args[0])
	}
	fs := newFlagSet("repo snapshot")
	help := bindHelpFlag(fs)
	repo := fs.String("repo", ".", "source Git worktree")
	baseRevision := fs.String("base-revision", "", "initial task snapshot commit; return changes against this revision")
	output := fs.String("out", "", "private artifact directory outside the source worktree")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab repo snapshot [--repo PATH] [--out DIRECTORY] [--json]")
	}
	if err := parseFlags(fs, args[1:], usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("repo snapshot accepts no positional arguments")
	}
	if *output == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		*output = filepath.Join(cache, "agentlab", "repositories")
	}
	result, err := reposnapshot.Create(ctx, reposnapshot.Options{Repository: *repo, OutputDir: *output, BaseRevision: *baseRevision})
	if err != nil {
		return repositoryRequirement(err)
	}
	summary := map[string]any{"patch_path": result.PatchPath, "patch_sha256": result.Manifest.PatchSHA256, "patch_bytes": result.Manifest.PatchBytes, "policy_version": result.Manifest.PolicyVersion, "source_revision": result.Manifest.SourceRevision, "base_revision": result.Manifest.BaseRevision, "revision": result.Manifest.Revision, "bundle_sha256": result.Manifest.BundleSHA256, "bundle_bytes": result.Manifest.BundleBytes, "content_bytes": result.Manifest.ContentBytes, "file_count": len(result.Manifest.Files), "change_count": len(result.Manifest.Changes), "excluded_count": len(result.Manifest.Excluded), "bundle_path": result.BundlePath, "manifest_path": result.ManifestPath}
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(summary)
	}
	fmt.Fprintf(os.Stdout, "Snapshot: %s\nSource revision: %s\nFiles: %d\nTransferred changes: %d\nAdditional exclusions: %d\nBundle: %s\nManifest: %s\n", result.Manifest.Revision, result.Manifest.SourceRevision, len(result.Manifest.Files), len(result.Manifest.Changes), len(result.Manifest.Excluded), result.BundlePath, result.ManifestPath)
	return nil
}

func repositoryRequirement(err error) error {
	var requirement *reposnapshot.Error
	if errors.As(err, &requirement) {
		return &blockedRequirement{Code: requirement.Code, Requirement: "repository_snapshot", Action: requirement.Action}
	}
	return err
}

// Keep syntax validation separate from exporting files on the host.
func runRepoResultCommand(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("repo result")
	help := bindHelpFlag(fs)
	fs.String("repo", ".", "task worktree")
	fs.String("base-revision", "", "initial task snapshot commit")
	fs.String("out", "", "private output directory outside the worktree")
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab repo result --repo PATH --base-revision COMMIT --out DIRECTORY [--json]")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("repo result accepts no positional arguments")
	}
	return repositoryRequirement(reposnapshot.RunResultCommand(ctx, args, os.Stdout))
}
