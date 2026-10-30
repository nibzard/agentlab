package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
)

func repositoryClient(ctx context.Context, base commonFlags) (*apiClient, error) {
	client, err := apiClientFromFlags(base)
	if err != nil {
		return nil, err
	}
	capabilities, err := fetchCapabilities(ctx, client)
	if err != nil {
		return nil, err
	}
	if !capabilities.Features["repository_upload"] {
		return nil, &blockedRequirement{Code: "unsupported_feature", Requirement: "repository_upload", Action: "use a daemon with repository upload support and a configured artifact store"}
	}
	return client, nil
}

func runRepoUpload(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("repo upload")
	base.bind(fs)
	help := bindHelpFlag(fs)
	manifestPath := fs.String("manifest", "", "snapshot manifest path; the matching source.bundle must be in the same directory")
	usage := func() { fmt.Fprintln(os.Stdout, "Usage: agentlab repo upload --manifest PATH [--json]") }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("repo upload requires --manifest PATH and accepts no positional arguments")
	}
	client, err := repositoryClient(ctx, base)
	if err != nil {
		return err
	}
	file, err := os.Open(*manifestPath)
	if err != nil {
		return err
	}
	manifest, err := reposnapshot.ReadManifest(file)
	file.Close()
	if err != nil {
		return repositoryRequirement(err)
	}
	bundlePath := filepath.Join(filepath.Dir(*manifestPath), "source.bundle")
	if err := reposnapshot.Validate(ctx, bundlePath, manifest); err != nil {
		return repositoryRequirement(err)
	}
	if !base.jsonOutput {
		fmt.Fprintf(os.Stderr, "Selected server: %s\n", client.target())
	}
	record, err := uploadRepositorySnapshot(ctx, client, bundlePath, manifest)
	if err != nil {
		return err
	}
	return printRepositoryRecord(record, base.jsonOutput)
}

func uploadRepositorySnapshot(ctx context.Context, client *apiClient, bundlePath string, manifest reposnapshot.Manifest) (protocol.RepositorySnapshot, error) {
	var record protocol.RepositorySnapshot
	file, err := os.Open(bundlePath)
	if err != nil {
		return record, err
	}
	defer file.Close()
	requestCtx, cancel := client.withTimeout(ctx)
	defer cancel()
	read, write := io.Pipe()
	defer read.Close()
	writer := multipart.NewWriter(write)
	contentType := writer.FormDataContentType()
	finished := make(chan error, 1)
	go func() {
		err := func() error {
			part, err := writer.CreateFormFile("manifest", "manifest.json")
			if err != nil {
				return err
			}
			if err := json.NewEncoder(part).Encode(manifest); err != nil {
				return err
			}
			part, err = writer.CreateFormFile("bundle", "source.bundle")
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, io.LimitReader(file, (256<<20)+1)); err != nil {
				return err
			}
			return writer.Close()
		}()
		write.CloseWithError(err)
		finished <- err
	}()
	// Closing the reader releases the writer after an early server rejection.
	defer func() { read.Close(); <-finished }()
	request, err := client.newRequest(requestCtx, http.MethodPost, "/v1/repositories", read)
	if err != nil {
		return record, err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return record, fmt.Errorf("repository upload failed via %s: %w", client.target(), err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return record, endpointRedirectError()
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil {
		return record, err
	}
	if len(data) > 16<<20 {
		return record, fmt.Errorf("repository response exceeds the metadata limit")
	}
	if response.StatusCode >= 400 {
		return record, parseAPIError(response.StatusCode, data)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("invalid repository upload response")
	}
	if record.ID == "" || record.Manifest.BundleSHA256 != manifest.BundleSHA256 || record.Manifest.Revision != manifest.Revision {
		return record, fmt.Errorf("repository upload response does not identify the submitted snapshot")
	}
	return record, nil
}

func runRepoRemote(ctx context.Context, command string, args []string, base commonFlags) error {
	fs := newFlagSet("repo " + command)
	base.bind(fs)
	help := bindHelpFlag(fs)
	usage := func() { fmt.Fprintf(os.Stdout, "Usage: agentlab repo %s ID [--json]\n", command) }
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("repo %s requires one repository ID", command)
	}
	client, err := repositoryClient(ctx, base)
	if err != nil {
		return err
	}
	path, err := endpointPath("/v1/repositories", fs.Arg(0))
	if err != nil {
		return err
	}
	method := http.MethodGet
	if command == "remove" {
		method = http.MethodDelete
		if !base.jsonOutput {
			fmt.Fprintf(os.Stderr, "Selected server: %s\n", client.target())
		}
	}
	data, err := client.doJSON(ctx, method, path, nil)
	if err != nil {
		return err
	}
	if command == "remove" {
		if base.jsonOutput {
			fmt.Fprintln(os.Stdout, string(data))
		} else {
			fmt.Fprintf(os.Stdout, "Removed repository upload: %s\n", fs.Arg(0))
		}
		return nil
	}
	var record protocol.RepositorySnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	return printRepositoryRecord(record, base.jsonOutput)
}

func printRepositoryRecord(record protocol.RepositorySnapshot, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(record)
	}
	fmt.Fprintf(os.Stdout, "Repository upload: %s\nSource: %s\nSnapshot: %s\nExpires: %s\n", record.ID, record.RepoURL, record.Manifest.Revision, record.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"))
	return nil
}

func snapshotJobsUnavailable() error {
	return &blockedRequirement{Code: "unsupported_feature", Requirement: "repository_snapshot_jobs", Action: "use a daemon with snapshot job support and rebuild its VM template with the updated guest runner"}
}
