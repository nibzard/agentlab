// Package reposnapshot creates reviewable Git bundles without modifying source worktrees.
package reposnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/agentlab/agentlab/internal/contentpolicy"
)

const PolicyVersion = 1
const DefaultMaxBytes int64 = 100 << 20
const DefaultMaxFileBytes int64 = 8 << 20
const DefaultMaxFiles = 10000

type Options struct {
	Repository   string
	BaseRevision string
	OutputDir    string
	MaxBytes     int64
	MaxFileBytes int64
	MaxFiles     int
	Secrets      [][]byte
}
type Exclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
type File struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Manifest struct {
	PatchSHA256    string      `json:"patch_sha256,omitempty"`
	PatchBytes     int64       `json:"patch_bytes,omitempty"`
	PolicyVersion  int         `json:"policy_version"`
	SourceRevision string      `json:"source_revision,omitempty"`
	BaseRevision   string      `json:"base_revision"`
	Revision       string      `json:"revision"`
	BundleSHA256   string      `json:"bundle_sha256"`
	BundleBytes    int64       `json:"bundle_bytes"`
	ContentBytes   int64       `json:"content_bytes"`
	Files          []File      `json:"files"`
	Changes        []Change    `json:"changes"`
	Excluded       []Exclusion `json:"excluded"`
}
type Result struct {
	PatchPath    string   `json:"patch_path,omitempty"`
	BundlePath   string   `json:"bundle_path"`
	ManifestPath string   `json:"manifest_path"`
	Manifest     Manifest `json:"manifest"`
}
type Error struct {
	Code   string
	Action string
}

func (e *Error) Error() string { return e.Action }

type entry struct {
	mode string
	data []byte
}
type tree map[string]entry

type repository struct {
	root       string
	options    Options
	exclusions map[string]string
}

func cleanGitEnv(private bool) []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") || (!private && strings.HasPrefix(value, "GIT_CONFIG_")) {
			env = append(env, value)
		}
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	if !private {
		env = append(env, "GIT_ALLOW_PROTOCOL=")
	}
	if private {
		env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_ATTR_NOSYSTEM=1")
	}
	return env
}

type limitedOutput struct {
	exceeded bool
	bytes.Buffer
	limit int64
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	if int64(w.Len()+len(data)) > w.limit {
		w.exceeded = true
		return 0, &Error{Code: "repository_limit_exceeded", Action: "repository data exceeds snapshot limits; reduce the transfer size and retry"}
	}
	return w.Buffer.Write(data)
}

// git never returns diagnostic output, which can contain source credentials.
func git(ctx context.Context, dir string, private bool, input io.Reader, limit int64, args ...string) ([]byte, error) {
	argv := []string{"--no-pager", "--no-optional-locks"}
	argv = append(argv, "-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-c", "core.hooksPath="+os.DevNull, "-C", dir)
	argv = append(argv, args...)
	command := exec.CommandContext(ctx, "git", argv...)
	command.Env = cleanGitEnv(private)
	command.Stdin = input
	output := &limitedOutput{limit: limit}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if output.exceeded {
			return nil, &Error{Code: "repository_limit_exceeded", Action: "repository metadata exceeds snapshot limits; reduce the transfer scope"}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exceeded *Error
		if errors.As(err, &exceeded) {
			return nil, err
		}
		return nil, &Error{Code: "repository_git_failed", Action: "Git could not read or build the snapshot; verify Git is installed and all required objects exist locally"}
	}
	return output.Bytes(), nil
}

func validPath(name string) bool {
	return name != "" && name != "." && utf8.ValidString(name) && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

func (r *repository) exclude(name, reason string) {
	if _, exists := r.exclusions[name]; !exists {
		r.exclusions[name] = reason
	}
}

func (r *repository) ignored(ctx context.Context, names []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(names) == 0 {
		return ignored, nil
	}
	command := exec.CommandContext(ctx, "git", "--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-c", "core.hooksPath="+os.DevNull, "-C", r.root, "check-ignore", "--no-index", "-z", "--stdin")
	command.Env = cleanGitEnv(false)
	command.Stdin = strings.NewReader(strings.Join(names, "\x00") + "\x00")
	command.Stderr = io.Discard
	output := &limitedOutput{limit: 16 << 20}
	command.Stdout = output
	err := command.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return nil, &Error{Code: "repository_ignore_failed", Action: "Git ignore rules could not be checked; repair the repository configuration and retry"}
	}
	for _, name := range strings.Split(output.String(), "\x00") {
		if name != "" {
			ignored[name] = true
		}
	}
	return ignored, nil
}

func (r *repository) allowed(name string, ignored map[string]bool) bool {
	if !validPath(name) {
		r.exclude(name, "unsafe_path")
		return false
	}
	if secretPath(name) {
		r.exclude(name, "credential_path")
		return false
	}
	if ignored[name] {
		r.exclude(name, "ignored")
		return false
	}
	return true
}

func (r *repository) base(ctx context.Context, revision string) (tree, error) {
	result := tree{}
	if revision == "" {
		return result, nil
	}
	listing, err := git(ctx, r.root, false, nil, 16<<20, "ls-tree", "-r", "-z", "-l", revision)
	if err != nil {
		return nil, err
	}
	type item struct {
		name, mode, hash string
		size             int64
	}
	var items []item
	var names []string
	for _, line := range bytes.Split(listing, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(line, []byte{'\t'})
		if !ok {
			return nil, &Error{Code: "repository_tree_invalid", Action: "repair the repository tree before retrying"}
		}
		fields := strings.Fields(string(header))
		if len(fields) != 4 {
			return nil, fmt.Errorf("invalid Git tree listing")
		}
		if fields[0] == "160000" {
			return nil, &Error{Code: "repository_submodule_unsupported", Action: "submodule transfer is not supported yet; use a repository source that includes the required files"}
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("invalid Git object size")
		}
		items = append(items, item{name: string(name), mode: fields[0], hash: fields[2], size: size})
		names = append(names, string(name))
		if len(names) > r.options.MaxFiles {
			return nil, &Error{Code: "repository_limit_exceeded", Action: "repository metadata exceeds the snapshot file limit; reduce the transfer scope"}
		}
	}
	ignored, err := r.ignored(ctx, names)
	if err != nil {
		return nil, err
	}
	var total int64
	var selected []item
	var requests []blobRequest
	for _, item := range items {
		if !r.allowed(item.name, ignored) {
			continue
		}
		if item.mode != "100644" && item.mode != "100755" && item.mode != "120000" {
			r.exclude(item.name, "unsupported_file_type")
			continue
		}
		if item.size > r.options.MaxFileBytes {
			return nil, &Error{Code: "repository_file_too_large", Action: "a repository file exceeds the per-file snapshot limit; reduce the transfer size"}
		}
		total += item.size
		if total > r.options.MaxBytes || len(selected) >= r.options.MaxFiles {
			return nil, &Error{Code: "repository_limit_exceeded", Action: "repository data exceeds snapshot limits; reduce the transfer size"}
		}
		selected = append(selected, item)
		requests = append(requests, blobRequest{Hash: item.hash, Size: item.size})
	}
	blobs, err := readBlobs(ctx, r.root, false, requests, r.options.MaxBytes)
	if err != nil {
		return nil, err
	}
	for i, item := range selected {
		if secretContentForPath(item.name, blobs[i]) || contentpolicy.ContainsSecret(blobs[i], r.options.Secrets) {
			r.exclude(item.name, "credential_content")
			continue
		}
		result[item.name] = entry{mode: item.mode, data: blobs[i]}
	}
	r.filterLinks(result)
	return result, nil
}

func (r *repository) current(ctx context.Context) (tree, error) {
	index, err := git(ctx, r.root, false, nil, 16<<20, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	for _, item := range bytes.Split(index, []byte{0}) {
		if bytes.HasPrefix(item, []byte("160000 ")) {
			return nil, &Error{Code: "repository_submodule_unsupported", Action: "submodule transfer is not supported yet; use a repository source that includes the required files"}
		}
	}
	listing, err := git(ctx, r.root, false, nil, 16<<20, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, name := range strings.Split(string(listing), "\x00") {
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	if len(names) > r.options.MaxFiles {
		return nil, &Error{Code: "repository_limit_exceeded", Action: "repository metadata exceeds the snapshot file limit; reduce the transfer scope"}
	}
	sort.Strings(names)
	ignored, err := r.ignored(ctx, names)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(r.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result := tree{}
	var total int64
	for _, name := range names {
		if !r.allowed(name, ignored) {
			continue
		}
		parent := path.Dir(name)
		safe := true
		for parent != "." {
			info, err := root.Lstat(parent)
			if err != nil || !info.IsDir() {
				safe = false
				break
			}
			parent = path.Dir(parent)
		}
		if !safe {
			r.exclude(name, "unsafe_parent")
			continue
		}
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var data []byte
		mode := "100644"
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := root.Readlink(name)
			if err != nil {
				return nil, err
			}
			data = []byte(target)
			mode = "120000"
		case info.Mode().IsRegular():
			if info.Size() > r.options.MaxFileBytes {
				return nil, &Error{Code: "repository_file_too_large", Action: "a repository file exceeds the per-file snapshot limit; reduce the transfer size"}
			}
			file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return nil, &Error{Code: "repository_changed", Action: "a source file changed during snapshot creation; retry after edits finish"}
			}
			opened, err := file.Stat()
			if err != nil {
				file.Close()
				return nil, err
			}
			if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
				file.Close()
				return nil, &Error{Code: "repository_changed", Action: "a source file changed during snapshot creation; retry after edits finish"}
			}
			data, err = io.ReadAll(io.LimitReader(file, r.options.MaxFileBytes+1))
			after, statErr := file.Stat()
			file.Close()
			if err != nil {
				return nil, err
			}
			if statErr != nil {
				return nil, statErr
			}
			if int64(len(data)) > r.options.MaxFileBytes || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
				return nil, &Error{Code: "repository_changed", Action: "a source file changed during snapshot creation; retry after edits finish"}
			}
			if info.Mode().Perm()&0111 != 0 {
				mode = "100755"
			}
		default:
			r.exclude(name, "unsupported_file_type")
			continue
		}
		if secretContentForPath(name, data) || contentpolicy.ContainsSecret(data, r.options.Secrets) {
			r.exclude(name, "credential_content")
			continue
		}
		total += int64(len(data))
		if total > r.options.MaxBytes || len(result) >= r.options.MaxFiles {
			return nil, &Error{Code: "repository_limit_exceeded", Action: "repository data exceeds snapshot limits; reduce the transfer size"}
		}
		result[name] = entry{mode: mode, data: data}
	}
	r.filterLinks(result)
	return result, nil
}

func (r *repository) filterLinks(files tree) {
	var safe func(string, map[string]bool) bool
	safe = func(name string, visited map[string]bool) bool {
		item, ok := files[name]
		if !ok {
			return false
		}
		if item.mode != "120000" {
			return true
		}
		if visited[name] || len(visited) >= 32 {
			return false
		}
		visited[name] = true
		target := string(item.data)
		if path.IsAbs(target) || strings.ContainsRune(target, 0) {
			return false
		}
		resolved := path.Clean(path.Join(path.Dir(name), target))
		if !validPath(resolved) || secretPath(resolved) {
			return false
		}
		return safe(resolved, visited)
	}
	for name, item := range files {
		if item.mode == "120000" && !safe(name, map[string]bool{}) {
			delete(files, name)
			r.exclude(name, "unsafe_symlink")
		}
	}
}

func sameTree(first, second tree) bool {
	if len(first) != len(second) {
		return false
	}
	for name, item := range first {
		other, ok := second[name]
		if !ok || item.mode != other.mode || !bytes.Equal(item.data, other.data) {
			return false
		}
	}
	return true
}

// Create captures the effective allowed working tree. It creates two historyless
// commits so the resulting changes are reviewable without transferring old history.
func Create(ctx context.Context, options Options) (Result, error) {
	var result Result
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = DefaultMaxFileBytes
	}
	if options.MaxFiles <= 0 {
		options.MaxFiles = DefaultMaxFiles
	}
	root, err := filepath.Abs(options.Repository)
	if err != nil {
		return result, err
	}
	location, err := git(ctx, root, false, nil, 1<<20, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	root = strings.TrimSuffix(string(location), "\n")
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	output, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return result, err
	}
	ancestor := output
	var missing []string
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return result, err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return result, err
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return result, err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return result, err
	}
	if relative == "." || (!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != "..") {
		return result, &Error{Code: "snapshot_output_inside_source", Action: "choose a snapshot output directory outside the source repository"}
	}
	options.OutputDir = resolved
	r := repository{root: root, options: options, exclusions: map[string]string{}}
	revisionBytes, err := git(ctx, root, false, nil, 1024, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, &Error{Code: "repository_revision_required", Action: "create an initial repository commit before making a snapshot"}
	}
	head := strings.TrimSpace(string(revisionBytes))
	revision := head
	if options.BaseRevision != "" {
		if !sourceRevisionPattern.MatchString(options.BaseRevision) {
			return result, &Error{Code: "repository_base_invalid", Action: "provide the full commit ID of the initial task snapshot"}
		}
		revision = options.BaseRevision
	}
	timestamp, err := git(ctx, root, false, nil, 1024, "show", "-s", "--format=%ct", revision)
	if err != nil {
		return result, err
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(timestamp)), 10, 64)
	if err != nil {
		return result, fmt.Errorf("invalid source commit timestamp")
	}
	base, err := r.base(ctx, revision)
	if err != nil {
		return result, err
	}
	current, err := r.current(ctx)
	if err != nil {
		return result, err
	}
	verify, err := r.current(ctx)
	if err != nil {
		return result, err
	}
	verifyBase, err := r.base(ctx, revision)
	if err != nil {
		return result, err
	}
	if !sameTree(base, verifyBase) {
		return result, &Error{Code: "repository_changed", Action: "repository ignore rules changed during snapshot creation; retry after edits finish"}
	}
	finalRevision, err := git(ctx, root, false, nil, 1024, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	if !sameTree(current, verify) || strings.TrimSpace(string(finalRevision)) != head {
		return result, &Error{Code: "repository_changed", Action: "repository files changed during snapshot creation; retry after edits finish"}
	}
	if options.BaseRevision != "" {
		// An excluded result file must not become a deletion in the returned
		// patch. Apply the same exclusions to both sides, including links.
		for {
			before := len(base) + len(current)
			for name := range r.exclusions {
				delete(base, name)
				delete(current, name)
			}
			r.filterLinks(base)
			r.filterLinks(current)
			if before == len(base)+len(current) {
				break
			}
		}
	}
	// Paths appear in manifests, including excluded paths. Block credential
	// values in names rather than publishing those values as exclusions.
	privateName := func(name string) bool {
		return contentpolicy.ContainsSecret([]byte(name), options.Secrets) || contentpolicy.SensitiveContent(name, []byte(name))
	}
	for name := range base {
		if privateName(name) {
			return result, &Error{Code: "repository_credential_path", Action: "rename a source path containing a credential before exporting results"}
		}
	}
	for name := range current {
		if privateName(name) {
			return result, &Error{Code: "repository_credential_path", Action: "rename a source path containing a credential before exporting results"}
		}
	}
	for name := range r.exclusions {
		if privateName(name) {
			return result, &Error{Code: "repository_credential_path", Action: "rename a source path containing a credential before exporting results"}
		}
	}
	if err := os.MkdirAll(options.OutputDir, 0700); err != nil {
		return result, err
	}
	temp, err := os.MkdirTemp(options.OutputDir, ".snapshot-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temp)
	private := filepath.Join(temp, "repository.git")
	if _, err := git(ctx, temp, true, nil, 1<<20, "init", "--bare", "--template=", private); err != nil {
		return result, err
	}
	stream, err := os.CreateTemp(temp, "import-*")
	if err != nil {
		return result, err
	}
	defer stream.Close()
	if err := writeCommit(stream, "refs/heads/agentlab-base", "", "AgentLab sanitized source "+revision, seconds, base); err != nil {
		return result, err
	}
	if err := writeCommit(stream, "refs/heads/agentlab-snapshot", "refs/heads/agentlab-base", "AgentLab working tree snapshot", seconds, current); err != nil {
		return result, err
	}
	if _, err := io.WriteString(stream, "done\n"); err != nil {
		return result, err
	}
	if _, err := stream.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if _, err := git(ctx, private, true, stream, 1<<20, "fast-import", "--quiet"); err != nil {
		return result, err
	}
	baseID, err := git(ctx, private, true, nil, 1024, "rev-parse", "refs/heads/agentlab-base")
	if err != nil {
		return result, err
	}
	snapshotID, err := git(ctx, private, true, nil, 1024, "rev-parse", "refs/heads/agentlab-snapshot")
	if err != nil {
		return result, err
	}
	bundle := filepath.Join(temp, "source.bundle")
	if _, err := git(ctx, private, true, nil, 1<<20, "-c", "pack.threads=1", "-c", "pack.window=0", "-c", "pack.compression=9", "bundle", "create", bundle, "refs/heads/agentlab-snapshot", "refs/heads/agentlab-base"); err != nil {
		return result, err
	}
	patchData, err := patchBetween(ctx, private, strings.TrimSpace(string(baseID)), strings.TrimSpace(string(snapshotID)))
	if err != nil {
		return result, err
	}
	patch := filepath.Join(temp, "changes.patch")
	if err := os.WriteFile(patch, patchData, 0600); err != nil {
		return result, err
	}
	patchSum := sha256.Sum256(patchData)
	digest, size, err := hashFile(bundle)
	if err != nil {
		return result, err
	}
	if size > 256<<20 {
		return result, &Error{Code: "repository_bundle_too_large", Action: "the repository bundle exceeds 256 MiB; reduce the transfer size"}
	}
	manifest := Manifest{PatchSHA256: hex.EncodeToString(patchSum[:]), PatchBytes: int64(len(patchData)), PolicyVersion: PolicyVersion, SourceRevision: revision, BaseRevision: strings.TrimSpace(string(baseID)), Revision: strings.TrimSpace(string(snapshotID)), BundleSHA256: digest, BundleBytes: size, Files: []File{}, Changes: []Change{}, Excluded: []Exclusion{}}
	for name, item := range current {
		sum := sha256.Sum256(item.data)
		manifest.ContentBytes += int64(len(item.data))
		manifest.Files = append(manifest.Files, File{Path: name, Mode: item.mode, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(item.data))})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	all := map[string]bool{}
	for name := range base {
		all[name] = true
	}
	for name := range current {
		all[name] = true
	}
	for name := range all {
		before, beforeOK := base[name]
		after, afterOK := current[name]
		status := "M"
		if !beforeOK {
			status = "A"
		} else if !afterOK {
			status = "D"
		} else if before.mode == after.mode && bytes.Equal(before.data, after.data) {
			continue
		}
		manifest.Changes = append(manifest.Changes, Change{Path: name, Status: status})
	}
	sort.Slice(manifest.Changes, func(i, j int) bool { return manifest.Changes[i].Path < manifest.Changes[j].Path })
	for name, reason := range r.exclusions {
		manifest.Excluded = append(manifest.Excluded, Exclusion{Path: name, Reason: reason})
	}
	sort.Slice(manifest.Excluded, func(i, j int) bool { return manifest.Excluded[i].Path < manifest.Excluded[j].Path })
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	if len(data)+1 > 16<<20 {
		return result, &Error{Code: "repository_metadata_too_large", Action: "snapshot metadata exceeds 16 MiB; reduce the transfer scope"}
	}
	manifestHash := sha256.Sum256(data)
	destination := filepath.Join(options.OutputDir, manifest.Revision+"-"+hex.EncodeToString(manifestHash[:8]))
	if _, err := os.Lstat(destination); err == nil {
		return verifyResult(destination, manifest)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	publication, err := os.MkdirTemp(options.OutputDir, ".publish-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(publication)
	if err := os.Rename(bundle, filepath.Join(publication, "source.bundle")); err != nil {
		return result, err
	}
	if err := os.Rename(patch, filepath.Join(publication, "changes.patch")); err != nil {
		return result, err
	}
	if err := os.Chmod(filepath.Join(publication, "source.bundle"), 0600); err != nil {
		return result, err
	}

	if err := os.WriteFile(filepath.Join(publication, "manifest.json"), append(data, '\n'), 0600); err != nil {
		return result, err
	}
	if err := os.Rename(publication, destination); err != nil {
		if _, verifyErr := verifyResult(destination, manifest); verifyErr != nil {
			return result, err
		}
	}
	return verifyResult(destination, manifest)
}
