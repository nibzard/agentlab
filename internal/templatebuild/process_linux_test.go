//go:build linux

package templatebuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerCancellationTerminatesChildren(t *testing.T) {
	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "logs-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w := &worker{cfg: WorkerConfig{Directory: dir}, log: file}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := w.run(ctx, "/bin/sh", "-c", `sleep 60 & printf '%s' "$!" > child.pid; wait`)
		done <- err
	}()
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "child.pid"))
		if err == nil {
			pid, err = strconv.Atoi(string(raw))
			if err == nil && pid > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled worker reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child retained command pipes after cancellation")
	}
	// A killed child may remain as a zombie until PID 1 reaps it. Neither a
	// missing process nor a zombie can continue customization/import work.
	deadline = time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndex(string(raw), ")")
		if end >= 0 && strings.HasPrefix(string(raw)[end+1:], " Z") {
			return
		}
		// SIGKILL can close inherited descriptors before the kernel publishes a
		// zombie state. Re-observe this same PID until termination is authoritative.
		if time.Now().After(deadline) {
			t.Fatalf("child remains running after cancellation: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}

}

func TestCacheLockCancellationAndSymlinkRejection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.lock")
	unlock, err := lockCache(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = lockCache(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait ignored cancellation: %v", err)
	}
	unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	next, err := lockCache(ctx2, path)
	if err != nil {
		t.Fatal(err)
	}
	next()
	link := filepath.Join(dir, "symlink.lock")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if release, err := lockCache(context.Background(), link); err == nil {
		release()
		t.Fatal("followed cache lock symlink")
	}
}
