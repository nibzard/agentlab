package assets

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/stretchr/testify/require"
)

func TestGuestRequestedTestsExecuteAndAccountForFailures(t *testing.T) {
	for _, mode := range []string{"commands", "agent_failed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			for _, name := range []string{"bash", "jq", "timeout", "sha256sum"} {
				if _, err := exec.LookPath(name); err != nil {
					t.Skip("requires " + name)
				}
			}
			runner, err := os.ReadFile("guest/agent-runner")
			require.NoError(t, err)
			text := string(runner)
			start := strings.Index(text, "run_requested_tests() {")
			end := strings.Index(text[start:], "\n}\n") + start + 3
			require.Greater(t, end, start)
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			require.NoError(t, os.Mkdir(repo, 0700))
			commands := []string{"printf 'first\\n'; printf changed > result.txt\n", "test -f result.txt; exit 7", "test -f result.txt"}
			agentExit := "0"
			budget := "10"
			if mode == "agent_failed" {
				agentExit = "9"
			}
			if mode == "timeout" {
				budget = "1"
				commands = []string{"sleep 20", "printf unexpected > unexpected"}
			}
			body, err := json.Marshal(map[string]any{"job": map[string]any{"test_commands": commands}})
			require.NoError(t, err)
			bootstrap := filepath.Join(root, "bootstrap.json")
			require.NoError(t, os.WriteFile(bootstrap, body, 0600))
			harness := `set -euo pipefail
redact_stream() { cat; }
INNER_SANDBOX_PREFIX=()
` + text[start:end] + "\nrun_requested_tests \"$TEST_BUDGET\" || exit 1\nprintf '%s' \"$TEST_RUNS\"\n"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", harness)
			cmd.Env = append(os.Environ(), "BOOTSTRAP_RESPONSE="+bootstrap, "RUN_DIR="+filepath.Join(root, "run"), "REPO_DIR="+repo, "AGENT_EXIT="+agentExit, "TEST_BUDGET="+budget)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			var runs []executionarchive.TestRun
			require.NoError(t, json.Unmarshal(output, &runs))
			require.Len(t, runs, len(commands))
			for i, run := range runs {
				require.Equal(t, commands[i], run.Command)
			}
			switch mode {
			case "commands":
				require.Equal(t, "passed", runs[0].State)
				require.Equal(t, "failed", runs[1].State)
				require.Equal(t, 7, *runs[1].ExitCode)
				require.Equal(t, "passed", runs[2].State)
			case "agent_failed":
				for _, run := range runs {
					require.Equal(t, "skipped", run.State)
					require.Equal(t, "agent_failed", run.Reason)
				}
				_, err := os.Stat(filepath.Join(repo, "result.txt"))
				require.True(t, os.IsNotExist(err))
			case "timeout":
				require.Equal(t, "timed_out", runs[0].State)
				require.Equal(t, 124, *runs[0].ExitCode)
				require.Equal(t, "skipped", runs[1].State)
				_, err := os.Stat(filepath.Join(repo, "unexpected"))
				require.True(t, os.IsNotExist(err))
			}
		})
	}
}
