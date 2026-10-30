package proxmox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerCancellationStopsStorageChildren(t *testing.T) {
	for _, kind := range []string{"exec", "bash"} {
		t.Run(kind, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "writes")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var runner CommandRunner = ExecRunner{}
			if kind == "bash" {
				runner = BashRunner{}
			}
			done := make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, "/bin/sh", "-c", `(while :; do printf x >> "$1"; sleep 0.02; done) & wait`, "storage-child", marker)
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				info, err := os.Stat(marker)
				if err == nil && info.Size() > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("storage child did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled process claimed success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("storage process did not stop")
			}
			before, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			after, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != len(after) {
				t.Fatal("storage child kept mutating after cancellation")
			}
		})
	}
}
