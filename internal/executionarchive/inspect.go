package executionarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

const DefaultChunkBytes = 16 << 10
const MaxChunkBytes = 64 << 10

var ErrArchiveChanged = errors.New("execution archive changed")
var ErrEntryNotFound = errors.New("execution entry not found")
var ErrEntryNotText = errors.New("execution entry is not UTF-8 text")
var ErrInvalidOffset = errors.New("invalid execution text offset or limit")

type Entry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Text      bool   `json:"text"`
}
type Chunk struct {
	Path       string `json:"path"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	EOF        bool   `json:"eof"`
	Text       string `json:"text"`
}
type Inspection struct {
	ArchiveSHA256 string   `json:"archive_sha256"`
	ArchiveBytes  int64    `json:"archive_bytes"`
	Evidence      Evidence `json:"evidence"`
	Entries       []Entry  `json:"entries"`
	Chunk         *Chunk   `json:"chunk,omitempty"`
}
type InspectOptions struct {
	Path   string
	Offset int64
	Limit  int
}

// Inspect validates the whole archive and its digest before returning text.
// It reads one open descriptor and never extracts or executes archive entries.
func Inspect(ctx context.Context, file *os.File, expected Expected, digest string, size int64, options InspectOptions) (Inspection, error) {
	if options.Limit == 0 {
		options.Limit = DefaultChunkBytes
	}
	if options.Offset < 0 || options.Limit < 4 || options.Limit > MaxChunkBytes || (options.Path == "" && options.Offset != 0) {
		return Inspection{}, ErrInvalidOffset
	}
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" || size < 0 || size > 256<<20 {
		return Inspection{}, ErrArchiveChanged
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return Inspection{}, ErrArchiveChanged
	}
	result := Inspection{ArchiveSHA256: digest, ArchiveBytes: size, Entries: []Entry{}}
	var selected []byte
	var found bool
	visit := func(name string, data []byte) {
		if name != "agent-runner.log" && name != "report.json" && !strings.HasPrefix(name, "tests/") {
			return
		}
		sum := sha256.Sum256(data)
		result.Entries = append(result.Entries, Entry{Path: name, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Text: utf8.Valid(data)})
		if name == options.Path {
			found = true
			selected = append([]byte{}, data...)
		}
	}
	hash := sha256.New()
	// The extra byte detects growth while the descriptor is read. The validator
	// also rejects trailing gzip members and non-padding tar content.
	input := io.TeeReader(io.NewSectionReader(file, 0, size+1), hash)
	evidence, err := validateReader(ctx, input, expected, nil, visit)
	if err != nil {
		return Inspection{}, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return Inspection{}, ErrArchiveChanged
	}
	result.Evidence = evidence
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })
	if options.Path == "" {
		return result, nil
	}
	if !found {
		return Inspection{}, ErrEntryNotFound
	}
	if !utf8.Valid(selected) {
		return Inspection{}, ErrEntryNotText
	}
	offset := options.Offset
	if offset > int64(len(selected)) || (offset < int64(len(selected)) && !utf8.RuneStart(selected[offset])) {
		return Inspection{}, ErrInvalidOffset
	}
	end := min(offset+int64(options.Limit), int64(len(selected)))
	for end > offset && end < int64(len(selected)) && !utf8.RuneStart(selected[end]) {
		end--
	}
	result.Chunk = &Chunk{Path: options.Path, Offset: offset, NextOffset: end, EOF: end == int64(len(selected)), Text: string(selected[offset:end])}
	return result, nil
}
