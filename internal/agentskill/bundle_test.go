package agentskill

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	agentlab "github.com/agentlab/agentlab"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestExportIncludesCompletePackagedSkillOutsideCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	destination := filepath.Join(t.TempDir(), "agentlab")
	result, err := Export(context.Background(), destination)
	require.NoError(t, err)
	require.Equal(t, destination, result.Directory)
	require.Equal(t, filepath.Join(destination, "SKILL.md"), result.Entrypoint)
	root, err := fs.Sub(agentlab.InstallationAssets, "skills/agentlab")
	require.NoError(t, err)
	for _, name := range result.Files {
		want, err := fs.ReadFile(root, name)
		require.NoError(t, err)
		actual, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		require.NoError(t, err)
		require.Equal(t, want, actual)
	}
	again, err := Export(context.Background(), destination)
	require.NoError(t, err)
	require.Equal(t, result, again)
}

func TestSkillMetadataAndLinkedGuidesAgree(t *testing.T) {
	b, err := load()
	require.NoError(t, err)
	front := strings.SplitN(string(b.files["SKILL.md"]), "---", 3)
	require.Len(t, front, 3)
	var metadata struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		Metadata    map[string]string `yaml:"metadata"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(front[1]), &metadata))
	require.Equal(t, b.info.Manifest.Name, metadata.Name)
	require.Equal(t, b.info.Manifest.Version, metadata.Metadata["version"])
	require.NotEmpty(t, metadata.Description)
	links := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	for name, data := range b.files {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		for _, match := range links.FindAllSubmatch(data, -1) {
			target := string(match[1])
			require.True(t, fs.ValidPath(target), "nonlocal skill reference in %s", name)
			require.NotEmpty(t, b.files[target], "missing skill reference %s", target)
		}
	}
	var manifest Manifest
	require.NoError(t, json.Unmarshal(b.files["bundle/manifest.json"], &manifest))
	for _, guide := range manifest.Guides {
		require.Contains(t, string(b.files["SKILL.md"]), "("+guide.File+")", "guide cannot be discovered from entrypoint")
	}
}

func TestExportPreservesUserChangesAndUnexpectedFiles(t *testing.T) {
	for _, kind := range []string{"modified", "extra_file", "extra_directory", "symlink", "root_symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "agentlab")
			_, err := Export(context.Background(), root)
			require.NoError(t, err)
			modified := filepath.Join(root, "SKILL.md")
			switch kind {
			case "modified":
				require.NoError(t, os.WriteFile(modified, []byte("user customization"), 0600))
			case "extra_file":
				require.NoError(t, os.WriteFile(filepath.Join(root, "custom.md"), []byte("user guide"), 0600))
			case "extra_directory":
				require.NoError(t, os.Mkdir(filepath.Join(root, "custom"), 0700))
			case "symlink":
				target := filepath.Join(t.TempDir(), "original")
				require.NoError(t, os.Rename(modified, target))
				require.NoError(t, os.Symlink(target, modified))
			case "root_symlink":
				target := filepath.Join(t.TempDir(), "original")
				require.NoError(t, os.Rename(root, target))
				require.NoError(t, os.Symlink(target, root))
			}
			_, err = Export(context.Background(), root)
			var requirement *Error
			require.ErrorAs(t, err, &requirement)
			require.Equal(t, "skill_export_changed", requirement.Code)
			if kind == "modified" {
				data, err := os.ReadFile(modified)
				require.NoError(t, err)
				require.Equal(t, "user customization", string(data))
			}
			if kind == "extra_file" {
				data, err := os.ReadFile(filepath.Join(root, "custom.md"))
				require.NoError(t, err)
				require.Equal(t, "user guide", string(data))
			}
		})
	}
}

func TestConcurrentExportPublishesOneCompleteBundle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agentlab")
	var workers sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := Export(context.Background(), root); errors <- err }()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	b, err := load()
	require.NoError(t, err)
	require.NoError(t, b.verify(context.Background(), root))
	entries, err := os.ReadDir(filepath.Dir(root))
	require.NoError(t, err)
	require.Len(t, entries, 1, "staging files remain after completed exports")
}

func TestCancelledExportDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := filepath.Join(t.TempDir(), "agentlab")
	_, err := Export(ctx, root)
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(root)
	require.True(t, errors.Is(err, fs.ErrNotExist))
}
