package executionarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// TestRun records an explicitly requested command, not inferred test assertions.
type TestRun struct {
	Command         string `json:"command"`
	State           string `json:"state"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	DurationSeconds *int64 `json:"duration_seconds,omitempty"`
	LogPath         string `json:"log_path,omitempty"`
	LogSHA256       string `json:"log_sha256,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

// ValidateTestCommands bounds executable inputs before a job creates resources.
func ValidateTestCommands(commands []string) error {
	if len(commands) > 16 {
		return fmt.Errorf("test_commands permits at most 16 commands")
	}
	total := 0
	for _, command := range commands {
		total += len(command)
		if !utf8.ValidString(command) || strings.TrimSpace(command) == "" || len(command) > 4096 || strings.ContainsRune(command, 0) || total > 16384 {
			return fmt.Errorf("test_commands contains an empty, invalid, or oversized command")
		}
	}
	return nil
}

// Equal compares all execution and test evidence, including ordered commands.
func Equal(a, b Evidence) bool { return reflect.DeepEqual(a, b) }

func validateTests(body report, expected Expected, agentExit int) (string, error) {
	if err := ValidateTestCommands(expected.TestCommands); err != nil {
		return "", err
	}
	if len(expected.TestCommands) == 0 {
		if body.AgentExitCode != nil && agentExit != *body.ExitCode {
			return "", fmt.Errorf("execution exit codes conflict")
		}
		if len(body.TestRuns) != 0 {
			return "", fmt.Errorf("execution report contains unrequested tests")
		}
		return "not_reported", nil
	}
	if body.AgentExitCode == nil || len(body.TestRuns) != len(expected.TestCommands) {
		return "", fmt.Errorf("requested test evidence is missing")
	}
	verdict := "passed"
	for index, run := range body.TestRuns {
		if run.Command != expected.TestCommands[index] {
			return "", fmt.Errorf("test command does not match the admitted job")
		}
		if run.State == "skipped" {
			if run.ExitCode != nil || run.DurationSeconds != nil || run.LogPath != "" || run.LogSHA256 != "" {
				return "", fmt.Errorf("skipped test contains execution claims")
			}
			if (run.Reason == "agent_failed" && agentExit == 0) || (run.Reason != "agent_failed" && run.Reason != "budget_exhausted" && run.Reason != "runner_unavailable") {
				return "", fmt.Errorf("invalid skipped test reason")
			}
			if verdict == "passed" {
				verdict = "skipped"
			}
			continue
		}
		if agentExit != 0 {
			return "", fmt.Errorf("test execution conflicts with agent failure")
		}
		if run.ExitCode == nil || *run.ExitCode < 0 || *run.ExitCode > 255 || run.DurationSeconds == nil || *run.DurationSeconds < 0 || *run.DurationSeconds > 330 || run.Reason != "" || run.LogPath != fmt.Sprintf("tests/%03d.log", index+1) {
			return "", fmt.Errorf("invalid test execution fields")
		}
		hash, err := hex.DecodeString(run.LogSHA256)
		if err != nil || len(hash) != sha256.Size || strings.ToLower(run.LogSHA256) != run.LogSHA256 {
			return "", fmt.Errorf("invalid test log digest")
		}
		state := "failed"
		if *run.ExitCode == 0 {
			state = "passed"
		} else if *run.ExitCode == 124 {
			state = "timed_out"
		}
		if run.State != state {
			return "", fmt.Errorf("test state conflicts with its exit code")
		}
		if state != "passed" {
			verdict = "failed"
		}
	}
	if body.Status == "COMPLETED" && verdict != "passed" {
		return "", fmt.Errorf("completed job has unsuccessful tests")
	}
	if agentExit == 0 && body.Status != "COMPLETED" && verdict == "passed" {
		return "", fmt.Errorf("failed job has no reported execution failure")
	}
	return verdict, nil
}
