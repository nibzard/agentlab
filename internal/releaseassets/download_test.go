package releaseassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func archiveFixture(t *testing.T, name, content string, kind byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: kind}
	if kind == tar.TypeSymlink {
		header.Size = 0
		header.Linkname = "/outside"
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if kind != tar.TypeSymlink {
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseFixture(t *testing.T, change func(map[string][]byte)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	files := map[string][]byte{}
	for _, binary := range []string{"agentlab", "agentlabd"} {
		files[binary+"_v1.2.3_linux-arm64.tar.gz"] = archiveFixture(t, binary, "linux-arm64-"+binary, tar.TypeReg)
	}
	if change != nil {
		change(files)
	}
	var checksums strings.Builder
	for name, data := range files {
		sum := sha256.Sum256(data)
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	files["checksums.txt"] = []byte(checksums.String())
	count := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/v1.2.3/") {
			t.Errorf("not pinned: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/v1.2.3/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(data)
	}))
	return server, count
}

func TestVerifiedReleasePairAndOfflineCache(t *testing.T) {
	server, count := releaseFixture(t, nil)
	downloader := Downloader{Root: t.TempDir(), BaseURL: server.URL, Client: server.Client()}
	pair, err := downloader.Fetch(context.Background(), "1.2.3", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if pair.Tag != "v1.2.3" || pair.Architecture != "arm64" {
		t.Fatal("wrong release identity")
	}
	data, err := os.ReadFile(pair.Client)
	if err != nil || string(data) != "linux-arm64-agentlab" {
		t.Fatalf("client: %v", err)
	}
	if count.Load() != 3 {
		t.Fatalf("requests: %d", count.Load())
	}
	stale := filepath.Join(downloader.Root, ".release-"+filepath.Base(filepath.Dir(pair.Client))+"-interrupted")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	server.Close()
	cachedPair, err := downloader.Fetch(context.Background(), "v1.2.3", "arm64")
	if err != nil || cachedPair.Client != pair.Client {
		t.Fatalf("offline cache: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("interrupted download was not removed")
	}
	if err := os.WriteFile(pair.Daemon, []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := downloader.Fetch(context.Background(), "1.2.3", "arm64"); err == nil {
		t.Fatal("tampered cache accepted")
	}
}

func TestConcurrentRequestsDownloadOnce(t *testing.T) {
	server, count := releaseFixture(t, nil)
	defer server.Close()
	downloader := Downloader{Root: t.TempDir(), BaseURL: server.URL, Client: server.Client()}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := downloader.Fetch(context.Background(), "1.2.3", "arm64"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 3 {
		t.Fatalf("duplicate downloads: %d", count.Load())
	}
}

func TestUnsafeReleaseEntriesNeverPublish(t *testing.T) {
	for _, test := range []struct {
		name string
		kind byte
		want string
	}{{"../agentlab", tar.TypeReg, "unsafe"}, {"agentlab", tar.TypeSymlink, "non-regular"}} {
		t.Run(test.want, func(t *testing.T) {
			server, _ := releaseFixture(t, func(files map[string][]byte) {
				files["agentlab_v1.2.3_linux-arm64.tar.gz"] = archiveFixture(t, test.name, "payload", test.kind)
			})
			defer server.Close()
			root := t.TempDir()
			_, err := (Downloader{Root: root, BaseURL: server.URL, Client: server.Client()}).Fetch(context.Background(), "1.2.3", "arm64")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("accepted unsafe archive: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					t.Fatal("partial release directory remains")
				}
			}
		})
	}
}

func TestChecksumAndVersionValidation(t *testing.T) {
	name := "agentlab_v1.2.3_linux-arm64.tar.gz"
	sum := strings.Repeat("a", 64)
	for _, data := range []string{"", sum + "  " + name + "\n" + sum + "  " + name, "invalid  " + name} {
		if _, err := checksum([]byte(data), name); err == nil {
			t.Fatal("invalid checksums accepted")
		}
	}
	server, _ := releaseFixture(t, nil)
	defer server.Close()
	corrupt := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			fmt.Fprintf(w, "%s  %s\n", sum, name)
			return
		}
		w.Write([]byte("not-the-archive"))
	}))
	defer corrupt.Close()
	_, err := (Downloader{Root: t.TempDir(), BaseURL: corrupt.URL, Client: corrupt.Client()}).Fetch(context.Background(), "1.2.3", "arm64")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum: %v", err)
	}
	for _, version := range []string{"dev", "latest", "1.2", "v1.2.3/escape", "v01.2.3", "v1.2.3-..", "v1.2.3-01"} {
		if _, err := Tag(version); err == nil {
			t.Fatalf("accepted version %q", version)
		}
	}
	if _, err := Tag("v1.2.3-rc.1"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingForDownloadLockCanBeCancelled(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	downloader := Downloader{Root: t.TempDir(), BaseURL: server.URL, Client: server.Client()}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	done := make(chan error, 1)
	go func() { _, err := downloader.Fetch(firstCtx, "1.2.3", "arm64"); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	cancelSecond()
	if _, err := downloader.Fetch(secondCtx, "1.2.3", "arm64"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock cancellation: %v", err)
	}
	cancelFirst()
	<-done
}

func TestArchiveContractNames(t *testing.T) {
	server, count := releaseFixture(t, nil)
	defer server.Close()
	pair, err := (Downloader{Root: t.TempDir(), BaseURL: server.URL, Client: server.Client()}).Fetch(context.Background(), "1.2.3", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(pair.Client) != "agentlab" || filepath.Base(pair.Daemon) != "agentlabd" || count.Load() != 3 {
		t.Fatal("archive contract changed")
	}
}
