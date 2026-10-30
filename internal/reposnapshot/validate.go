package reposnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var snapshotRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var sourceRevisionPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var snapshotTimestampPattern = regexp.MustCompile(`^-?[0-9]+ \+0000$`)

func invalidBundle() error {
	return &Error{Code: "repository_bundle_invalid", Action: "recreate the repository snapshot; its bundle and manifest must match the supported snapshot policy"}
}

// Validate verifies a received snapshot without checking out uploaded content.
// The caller must keep bundlePath immutable throughout this call. All Git writes
// use a fresh private repository with no source or user configuration.
func Validate(ctx context.Context, bundlePath string, manifest Manifest) error {
	return validate(ctx, bundlePath, manifest, nil)
}

// ValidateResult binds the returned base tree to the previously verified source.
// Explicit exclusions can omit files, but cannot replace their original bytes.
func ValidateResult(ctx context.Context, bundlePath string, manifest, source Manifest) error {
	if source.PolicyVersion != PolicyVersion || manifest.SourceRevision != source.Revision {
		return invalidResultBase()
	}
	return validate(ctx, bundlePath, manifest, &source)
}

func invalidResultBase() error {
	return &Error{Code: "repository_result_base_mismatch", Action: "preserve the task worktree and recreate its result against the initial uploaded snapshot"}
}

func validate(ctx context.Context, bundlePath string, manifest Manifest, source *Manifest) error {
	if manifest.PolicyVersion != PolicyVersion || !sourceRevisionPattern.MatchString(manifest.SourceRevision) || !snapshotRevisionPattern.MatchString(manifest.BaseRevision) || !snapshotRevisionPattern.MatchString(manifest.Revision) || manifest.BaseRevision == manifest.Revision {
		return invalidBundle()
	}
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded) > 16<<20 || len(manifest.Files) > DefaultMaxFiles || len(manifest.Changes) > 2*DefaultMaxFiles || len(manifest.Excluded) > 2*DefaultMaxFiles {
		return invalidBundle()
	}
	digest, size, err := hashFile(bundlePath)
	if err != nil {
		return err
	}
	if digest != manifest.BundleSHA256 || size != manifest.BundleBytes {
		return invalidBundle()
	}
	if err := validatePack(ctx, bundlePath); err != nil {
		return err
	}
	bundlePath, err = filepath.Abs(bundlePath)
	if err != nil {
		return err
	}
	private, err := os.MkdirTemp("", "agentlab-verify-snapshot-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(private)
	if _, err := git(ctx, private, true, nil, 1<<20, "init", "--bare", "--template=", "."); err != nil {
		return err
	}
	refs, err := git(ctx, private, true, nil, 4096, "bundle", "list-heads", bundlePath)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(refs)), "\n")
	sort.Strings(lines)
	want := []string{manifest.BaseRevision + " refs/heads/agentlab-base", manifest.Revision + " refs/heads/agentlab-snapshot"}
	sort.Strings(want)
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		return invalidBundle()
	}
	if _, err := git(ctx, private, true, nil, 1<<20, "bundle", "verify", bundlePath); err != nil {
		return err
	}
	if _, err := git(ctx, private, true, nil, 1<<20, "-c", "protocol.file.allow=always", "-c", "fetch.fsckObjects=true", "fetch", "--no-tags", "--no-write-fetch-head", "--no-auto-maintenance", bundlePath, "refs/heads/agentlab-base:refs/heads/agentlab-base", "refs/heads/agentlab-snapshot:refs/heads/agentlab-snapshot"); err != nil {
		return err
	}
	commits, err := git(ctx, private, true, nil, 1024, "rev-list", "--topo-order", "--all", "--parents")
	if err != nil {
		return err
	}
	if string(commits) != manifest.Revision+" "+manifest.BaseRevision+"\n"+manifest.BaseRevision+"\n" {
		return invalidBundle()
	}
	for _, revision := range []string{manifest.BaseRevision, manifest.Revision} {
		commit, err := git(ctx, private, true, nil, 4096, "cat-file", "commit", revision)
		if err != nil {
			return err
		}
		if !validSnapshotCommit(commit, revision == manifest.Revision, manifest) {
			return invalidBundle()
		}
	}
	base, err := validateTree(ctx, private, manifest.BaseRevision)
	if err != nil {
		return err
	}
	current, err := validateTree(ctx, private, manifest.Revision)
	if err != nil {
		return err
	}
	if source != nil {
		excluded := map[string]bool{}
		for _, item := range manifest.Excluded {
			switch item.Reason {
			case "credential_content", "credential_path", "ignored", "unsafe_path", "unsupported_file_type", "unsafe_parent", "unsafe_symlink":
			default:
				return invalidResultBase()
			}
			if _, ok := base[item.Path]; ok {
				return invalidResultBase()
			}
			if _, ok := current[item.Path]; ok {
				return invalidResultBase()
			}
			excluded[item.Path] = true
		}
		count := 0
		for _, expected := range source.Files {
			if excluded[expected.Path] {
				continue
			}
			count++
			actual, ok := base[expected.Path]
			if !ok || actual.mode != expected.Mode || int64(len(actual.data)) != expected.Bytes {
				return invalidResultBase()
			}
			hash := sha256.Sum256(actual.data)
			if hex.EncodeToString(hash[:]) != expected.SHA256 {
				return invalidResultBase()
			}
		}
		if count != len(base) {
			return invalidResultBase()
		}
	}
	var contentBytes int64
	files := make([]File, 0, len(current))
	for name, item := range current {
		sum := sha256.Sum256(item.data)
		contentBytes += int64(len(item.data))
		files = append(files, File{Path: name, Mode: item.mode, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(item.data))})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	actualFiles, _ := json.Marshal(files)
	expectedFiles, _ := json.Marshal(manifest.Files)
	if !bytes.Equal(actualFiles, expectedFiles) || contentBytes != manifest.ContentBytes || !bytes.Equal(jsonChanges(base, current), mustJSON(manifest.Changes)) {
		return invalidBundle()
	}
	// Reject hidden unreachable objects as well as reachable source history.
	reachable, err := git(ctx, private, true, nil, 16<<20, "rev-list", "--objects", "--all", "--no-object-names")
	if err != nil {
		return err
	}
	all, err := git(ctx, private, true, nil, 16<<20, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	if err != nil {
		return err
	}
	a, b := strings.Fields(string(reachable)), strings.Fields(string(all))
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		return invalidBundle()
	}
	if manifest.PatchSHA256 != "" || manifest.PatchBytes != 0 {
		patch, err := patchBetween(ctx, private, manifest.BaseRevision, manifest.Revision)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(patch)
		if manifest.PatchSHA256 != hex.EncodeToString(sum[:]) || manifest.PatchBytes != int64(len(patch)) {
			return invalidBundle()
		}
	}
	return nil
}

func patchBetween(ctx context.Context, private, base, revision string) ([]byte, error) {
	return git(ctx, private, true, nil, 256<<20, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--no-renames", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", base, revision, "--")
}

func validSnapshotCommit(data []byte, snapshot bool, manifest Manifest) bool {
	header, message, ok := bytes.Cut(data, []byte("\n\n"))
	if !ok {
		return false
	}
	lines := strings.Split(string(header), "\n")
	count := 3
	if snapshot {
		count++
	}
	if len(lines) != count || !strings.HasPrefix(lines[0], "tree ") || !snapshotRevisionPattern.MatchString(strings.TrimPrefix(lines[0], "tree ")) {
		return false
	}
	if snapshot {
		if lines[1] != "parent "+manifest.BaseRevision {
			return false
		}
		lines = append(lines[:1], lines[2:]...)
	}
	author := strings.TrimPrefix(lines[1], "author AgentLab snapshot <snapshot@agentlab.invalid> ")
	committer := strings.TrimPrefix(lines[2], "committer AgentLab snapshot <snapshot@agentlab.invalid> ")
	if author != committer || !snapshotTimestampPattern.MatchString(author) {
		return false
	}
	want := "AgentLab sanitized source " + manifest.SourceRevision
	if snapshot {
		want = "AgentLab working tree snapshot"
	}
	return string(message) == want
}

func validateTree(ctx context.Context, private, revision string) (tree, error) {
	listing, err := git(ctx, private, true, nil, 16<<20, "ls-tree", "-r", "-l", "-z", revision)
	if err != nil {
		return nil, err
	}
	type item struct{ name, mode string }
	var items []item
	var requests []blobRequest
	seen := map[string]bool{}
	var total int64
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 4 || !validPath(string(name)) || secretPath(string(name)) || (fields[0] != "100644" && fields[0] != "100755" && fields[0] != "120000") || fields[1] != "blob" || len(items) >= DefaultMaxFiles || seen[string(name)] {
			return nil, invalidBundle()
		}
		seen[string(name)] = true
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 || size > DefaultMaxFileBytes {
			return nil, invalidBundle()
		}
		total += size
		if total > DefaultMaxBytes {
			return nil, invalidBundle()
		}
		items = append(items, item{name: string(name), mode: fields[0]})
		requests = append(requests, blobRequest{Hash: fields[2], Size: size})
	}
	blobs, err := readBlobs(ctx, private, true, requests, DefaultMaxBytes)
	if err != nil {
		return nil, err
	}
	result := tree{}
	for i, item := range items {
		if secretContentForPath(item.name, blobs[i]) {
			return nil, invalidBundle()
		}
		result[item.name] = entry{mode: item.mode, data: blobs[i]}
	}
	before := len(result)
	r := repository{exclusions: map[string]string{}}
	r.filterLinks(result)
	if before != len(result) {
		return nil, invalidBundle()
	}
	return result, nil
}

func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }

func jsonChanges(base, current tree) []byte {
	changes := make([]Change, 0)
	for name, item := range base {
		other, exists := current[name]
		if !exists {
			changes = append(changes, Change{Path: name, Status: "D"})
		} else if item.mode != other.mode || !bytes.Equal(item.data, other.data) {
			changes = append(changes, Change{Path: name, Status: "M"})
		}
	}
	for name := range current {
		if _, exists := base[name]; !exists {
			changes = append(changes, Change{Path: name, Status: "A"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return mustJSON(changes)
}

// ReadManifest bounds metadata before decoding it. Unknown fields are rejected.
func ReadManifest(reader io.Reader) (Manifest, error) {
	var result Manifest
	data, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return result, invalidBundle()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, &Error{Code: "repository_manifest_invalid", Action: "recreate the repository snapshot; the manifest is invalid or contains unsupported fields"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return result, invalidBundle()
	}
	return result, nil
}
