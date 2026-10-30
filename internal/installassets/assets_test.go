package installassets

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPackagedAssetsWorkWithoutSourceTree(t *testing.T) {
	cache := t.TempDir()
	root, err := Materialize(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scripts/install_host.sh", "scripts/net/apply.sh", "scripts/create_template.sh", "scripts/guest/agent-runner", "scripts/systemd/agentlabd.service", "skills/agentlab/SKILL.md", "skills/agentlab/bundle/manifest.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Verify(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts/dev")); !os.IsNotExist(err) {
		t.Fatal("development tools shipped in installer assets")
	}
	again, err := Materialize(cache)
	if err != nil || again != root {
		t.Fatal("cache is not stable")
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/net/apply.sh"), []byte("modified"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(cache); err == nil {
		t.Fatal("modified cached code accepted")
	}
}

func TestConcurrentMaterialization(t *testing.T) {
	cache := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Materialize(cache); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestRejectUnexpectedCacheEntry(t *testing.T) {
	root, err := Materialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err == nil {
		t.Fatal("unexpected code accepted")
	}
}
