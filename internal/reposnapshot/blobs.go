package reposnapshot

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
)

type blobRequest struct {
	Hash string
	Size int64
}

// readBlobs uses one bounded Git process for a tree. Requests contain full
// object IDs, so filenames cannot become revision expressions or batch commands.
func readBlobs(ctx context.Context, dir string, private bool, requests []blobRequest, limit int64) ([][]byte, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	var input strings.Builder
	var total int64
	for _, request := range requests {
		if !sourceRevisionPattern.MatchString(request.Hash) || request.Size < 0 || request.Size > limit {
			return nil, invalidBundle()
		}
		total += request.Size
		if total > limit {
			return nil, invalidBundle()
		}
		input.WriteString(request.Hash)
		input.WriteByte('\n')
	}
	output, err := git(ctx, dir, private, strings.NewReader(input.String()), limit+int64(len(requests))*256, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	result := make([][]byte, 0, len(requests))
	for _, request := range requests {
		header, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, invalidBundle()
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[0] != request.Hash || fields[1] != "blob" {
			return nil, &Error{Code: "repository_object_unavailable", Action: "verify all required Git objects exist locally before retrying"}
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size != request.Size {
			return nil, &Error{Code: "repository_object_changed", Action: "repair changed Git objects before retrying the snapshot"}
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, invalidBundle()
		}
		if newline, err := reader.ReadByte(); err != nil || newline != '\n' {
			return nil, invalidBundle()
		}
		result = append(result, data)
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return nil, invalidBundle()
	}
	return result, nil
}
