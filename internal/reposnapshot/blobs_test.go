package reposnapshot

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestReadBlobsPreservesBinaryRecordBoundaries(t *testing.T) {
	root := testRepo(t)
	contents := [][]byte{[]byte("first\n" + strings.Repeat("0", 40) + " blob 999\n\x00binary\n"), []byte("second\x00\nrecord\n")}
	var requests []blobRequest
	for i, data := range contents {
		name := []string{"first.bin", "second.bin"}[i]
		writeTestFile(t, root, name, string(data))
		id := testGit(t, root, "hash-object", "-w", name)
		requests = append(requests, blobRequest{Hash: id, Size: int64(len(data))})
	}
	blobs, err := readBlobs(context.Background(), root, false, requests, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range contents {
		if !bytes.Equal(data, blobs[i]) {
			t.Fatal("binary object data changed a batch record boundary")
		}
	}
	requests[0].Size--
	if _, err := readBlobs(context.Background(), root, false, requests, 1<<20); err == nil {
		t.Fatal("a changed object size was accepted")
	}
}

func TestReadBlobsRejectsRevisionExpressions(t *testing.T) {
	root := testRepo(t)
	if _, err := readBlobs(context.Background(), root, false, []blobRequest{{Hash: "HEAD:code.txt", Size: 9}}, 1<<20); err == nil {
		t.Fatal("a revision expression was accepted as an object ID")
	}
}
