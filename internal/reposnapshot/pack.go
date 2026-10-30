package reposnapshot

import (
	"bufio"
	"compress/zlib"
	"context"
	"encoding/binary"
	"io"
	"os"
	"strings"
)

// validatePack bounds decompression before Git can import an untrusted bundle.
// Snapshot packs use complete objects. Delta objects and tags are unsupported.
func validatePack(ctx context.Context, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, (256<<20)+1))
	headerBytes := 0
	for {
		raw, err := reader.ReadSlice('\n')
		line := string(raw)
		headerBytes += len(line)
		if err != nil || headerBytes > 4096 {
			return invalidBundle()
		}
		if line == "\n" {
			break
		}
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "@") {
			return invalidBundle()
		}
	}
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || string(header[:4]) != "PACK" {
		return invalidBundle()
	}
	version := binary.BigEndian.Uint32(header[4:8])
	count := binary.BigEndian.Uint32(header[8:])
	if (version != 2 && version != 3) || count > 4*DefaultMaxFiles+2 {
		return invalidBundle()
	}
	var total uint64
	for i := uint32(0); i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		first, err := reader.ReadByte()
		if err != nil {
			return invalidBundle()
		}
		kind := (first >> 4) & 7
		size := uint64(first & 15)
		shift := uint(4)
		for first&128 != 0 {
			if shift > 56 {
				return invalidBundle()
			}
			first, err = reader.ReadByte()
			if err != nil {
				return invalidBundle()
			}
			size |= uint64(first&127) << shift
			shift += 7
		}
		var limit uint64
		switch kind {
		case 1: // Commit.
			limit = 4096
		case 2: // Tree.
			limit = 16 << 20
		case 3: // Blob.
			limit = uint64(DefaultMaxFileBytes)
		default:
			return invalidBundle()
		}
		total += size
		if size > limit || total > uint64(2*DefaultMaxBytes+(32<<20)) {
			return invalidBundle()
		}
		compressed, err := zlib.NewReader(reader)
		if err != nil {
			return invalidBundle()
		}
		written, copyErr := io.Copy(io.Discard, io.LimitReader(compressed, int64(size)+1))
		closeErr := compressed.Close()
		if copyErr != nil || closeErr != nil || written != int64(size) {
			return invalidBundle()
		}
	}
	var trailer [20]byte
	if _, err := io.ReadFull(reader, trailer[:]); err != nil {
		return invalidBundle()
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return invalidBundle()
	}
	return nil
}
