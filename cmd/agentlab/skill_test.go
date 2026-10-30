package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	product "github.com/agentlab/agentlab"
	"github.com/agentlab/agentlab/internal/agentskill"
	"github.com/agentlab/agentlab/internal/argv"
	"github.com/agentlab/agentlab/internal/connection"
	"github.com/stretchr/testify/require"
)

func TestSkillExportIsLocalWithBrokenConnectionSettings(t *testing.T) {
	useTempClientConfig(t)
	config, err := clientConfigPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0700))
	require.NoError(t, os.WriteFile(config, []byte("invalid connection JSON"), 0600))
	t.Setenv("AGENTLAB_CONTEXT", "missing-context")
	t.Setenv("AGENTLAB_CONFIG_DIR", "invalid-relative-directory")
	t.Setenv("AGENTLAB_TOKEN", "credential-that-must-not-be-read")
	t.Setenv("AGENTLAB_ENDPOINT", "https://invalid.example")
	destination := filepath.Join(t.TempDir(), "agentlab")
	opts, args, err := parseGlobal([]string{"--json", "skill", "export", "--out", destination})
	require.NoError(t, err)
	require.Empty(t, effectiveGlobalToken(t, opts))
	require.Empty(t, opts.endpoint)
	var commandErr error
	output := captureStdout(t, func() { commandErr = dispatch(context.Background(), args, commonFlags{jsonOutput: opts.jsonOutput}) })
	require.NoError(t, commandErr)
	var exported agentskill.Exported
	require.NoError(t, json.Unmarshal([]byte(output), &exported))
	require.Equal(t, filepath.Join(destination, "SKILL.md"), exported.Entrypoint)
	require.NotEmpty(t, exported.Manifest.Version)
	require.NotContains(t, output, "credential-that-must-not-be-read")
}

func TestSkillSyntaxValidationDoesNotExport(t *testing.T) {
	original := validateCommandOnly
	validateCommandOnly = true
	t.Cleanup(func() { validateCommandOnly = original })
	destination := filepath.Join(t.TempDir(), "agentlab")
	err := runSkillCommand(context.Background(), []string{"export", "--out", destination}, commonFlags{})
	require.ErrorIs(t, err, errCommandValidated)
	_, err = os.Stat(destination)
	require.True(t, os.IsNotExist(err))
}

func TestCLIAndCompatibilityDashboardShareExplicitConfigDirectory(t *testing.T) {
	original := clientConfigRootOverride
	clientConfigRootOverride = ""
	t.Cleanup(func() { clientConfigRootOverride = original })
	root := t.TempDir()
	t.Setenv("AGENTLAB_CONFIG_DIR", root)
	t.Setenv("AGENTLAB_CONTEXT", "shared")
	t.Setenv("AGENTLAB_ENDPOINT", "")
	t.Setenv("AGENTLAB_TOKEN", "")
	store, err := contextStore()
	require.NoError(t, err)
	require.Equal(t, root, store.Root)
	require.NoError(t, store.Put("shared", connection.Settings{Endpoint: "https://shared.example", Token: "shared-private-credential", ServerIdentity: "shared-server"}))
	require.NoError(t, store.Use("shared"))
	saved, err := connection.Load()
	require.NoError(t, err)
	opts, _, err := parseGlobal([]string{"status"})
	require.NoError(t, err)
	require.Equal(t, saved.Endpoint, opts.endpoint)
	require.Equal(t, saved.Token, effectiveGlobalToken(t, opts))
	require.Equal(t, "shared", opts.contextName)
}

func TestPackagedSkillExamplesUseInstalledCommandGrammar(t *testing.T) {
	useTempClientConfig(t)
	original := validateCommandOnly
	validateCommandOnly = true
	t.Cleanup(func() { validateCommandOnly = original })
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	root, err := fs.Sub(product.InstallationAssets, "skills/agentlab")
	require.NoError(t, err)
	checked := 0
	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			return nil
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		inShell := false
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			if line == "```bash" {
				inShell = true
				continue
			}
			if line == "```" {
				inShell = false
				continue
			}
			if !inShell || line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			tokens, err := argv.Tokenize(line)
			if err != nil {
				return fmt.Errorf("%s:%d: %w", name, lineNumber, err)
			}
			if len(tokens) == 0 || tokens[0] != "agentlab" {
				return fmt.Errorf("%s:%d: unsupported shell example; validate it explicitly", name, lineNumber)
			}
			// The connect example uses protected stdin. Validate the CLI flags
			// without opening that file or executing a shell redirection.
			for i, token := range tokens {
				if token == "<" {
					if i != len(tokens)-2 {
						return fmt.Errorf("%s:%d: unexpected input redirection", name, lineNumber)
					}
					tokens = tokens[:i]
					break
				}
			}
			args := globalCommandArgs(tokens[1:])
			err = dispatch(context.Background(), args, commonFlags{endpoint: server.URL, socketPath: filepath.Join(t.TempDir(), "missing.socket"), timeout: time.Second, jsonOutput: true})
			if !errors.Is(err, errCommandValidated) {
				return fmt.Errorf("%s:%d: command grammar check failed: %v", name, lineNumber, err)
			}
			checked++
		}
		return scanner.Err()
	})
	require.NoError(t, err)
	require.Positive(t, checked)
	require.Zero(t, requests.Load(), "grammar validation must not contact a daemon")
}
