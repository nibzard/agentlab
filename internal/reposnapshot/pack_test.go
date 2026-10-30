package reposnapshot

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func packFixture(t *testing.T, count uint32, kind byte, size uint64, data []byte) string {
	t.Helper()
	var content bytes.Buffer
	content.WriteString("# v2 git bundle\n\nPACK")
	binary.Write(&content, binary.BigEndian, uint32(2))
	binary.Write(&content, binary.BigEndian, count)
	first := byte(size&15) | (kind << 4)
	size >>= 4
	if size != 0 {
		first |= 128
	}
	content.WriteByte(first)
	for size != 0 {
		next := byte(size & 127)
		size >>= 7
		if size != 0 {
			next |= 128
		}
		content.WriteByte(next)
	}
	compressor := zlib.NewWriter(&content)
	compressor.Write(data)
	compressor.Close()
	content.Write(make([]byte, 20))
	filename := filepath.Join(t.TempDir(), "input.bundle")
	if err := os.WriteFile(filename, content.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestPackLimitsBeforeGitImport(t *testing.T) {
	for _, item := range []struct {
		name  string
		count uint32
		kind  byte
		size  uint64
		data  []byte
	}{
		{name: "delta", count: 1, kind: 6},
		{name: "tag", count: 1, kind: 4},
		{name: "object count", count: 4*DefaultMaxFiles + 3, kind: 3},
		{name: "declared blob size", count: 1, kind: 3, size: uint64(DefaultMaxFileBytes) + 1},
		{name: "declared commit size", count: 1, kind: 1, size: 4097},
		{name: "inflation", count: 1, kind: 3, size: 1, data: bytes.Repeat([]byte("x"), 1<<20)},
		{name: "truncation", count: 1, kind: 3, size: 5, data: []byte("x")},
	} {
		t.Run(item.name, func(t *testing.T) {
			filename := packFixture(t, item.count, item.kind, item.size, item.data)
			if err := validatePack(context.Background(), filename); err == nil {
				t.Fatal("unsupported pack accepted")
			}
		})
	}
}

func TestPackHonorsCancellation(t *testing.T) {
	filename := packFixture(t, 1, 3, 1, []byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validatePack(ctx, filename); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
