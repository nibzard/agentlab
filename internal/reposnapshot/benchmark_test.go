package reposnapshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// BenchmarkValidateResult measures the verification work inside a guest report.
// Run with -benchtime=1x to measure each supported file count once.
func BenchmarkValidateResult(b *testing.B) {
	for _, count := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("files_%d", count), func(b *testing.B) {
			ctx := context.Background()
			root := b.TempDir()
			run := func(dir string, args ...string) {
				command := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
				command.Env = append(cleanGitEnv(true), "GIT_ALLOW_PROTOCOL=file")
				if output, err := command.CombinedOutput(); err != nil {
					b.Fatalf("fixture Git failed: %v: %s", err, output)
				}
			}
			run(root, "init", "--initial-branch=main")
			run(root, "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid", "commit", "--allow-empty", "-m", "Initial")
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%05d.txt", i)), []byte(fmt.Sprintf("original content %d\n", i)), 0600); err != nil {
					b.Fatal(err)
				}
			}
			initial, err := Create(ctx, Options{Repository: root, OutputDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			clone := filepath.Join(b.TempDir(), "task")
			run(filepath.Dir(clone), "clone", "--quiet", "-b", "agentlab-snapshot", initial.BundlePath, clone)
			if err := os.WriteFile(filepath.Join(clone, "file-00000.txt"), []byte("task result\n"), 0600); err != nil {
				b.Fatal(err)
			}
			result, err := Create(ctx, Options{Repository: clone, OutputDir: b.TempDir(), BaseRevision: initial.Manifest.Revision})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := ValidateResult(ctx, result.BundlePath, result.Manifest, initial.Manifest); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
