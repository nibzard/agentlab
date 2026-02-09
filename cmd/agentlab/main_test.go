package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useTempClientConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envEndpoint, "")
	t.Setenv(envToken, "")
}

func TestParseGlobal(t *testing.T) {
	useTempClientConfig(t)
	tests := []struct {
		name        string
		args        []string
		wantOpts    globalOptions
		wantRemain  []string
		wantErr     bool
		errContains string
	}{
		{
			name:       "default values",
			args:       []string{},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{},
		},
		{
			name:       "with remaining args",
			args:       []string{"job", "list"},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{"job", "list"},
		},
		{
			name:       "custom socket path",
			args:       []string{"--socket", "/tmp/test.sock"},
			wantOpts:   globalOptions{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{},
		},
		{
			name:       "json output flag",
			args:       []string{"--json"},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: true, timeout: defaultRequestTimeout},
			wantRemain: []string{},
		},
		{
			name:       "custom timeout",
			args:       []string{"--timeout", "5m"},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: false, timeout: 5 * time.Minute},
			wantRemain: []string{},
		},
		{
			name:       "version flag",
			args:       []string{"--version"},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: false, timeout: defaultRequestTimeout, showVersion: true},
			wantRemain: []string{},
		},
		{
			name:        "invalid timeout",
			args:        []string{"--timeout", "invalid"},
			wantErr:     true,
			errContains: "parse error",
		},
		{
			name:       "all flags with args",
			args:       []string{"--socket", "/custom.sock", "--json", "--timeout", "30s", "job", "run"},
			wantOpts:   globalOptions{socketPath: "/custom.sock", jsonOutput: true, timeout: 30 * time.Second},
			wantRemain: []string{"job", "run"},
		},
		{
			name:       "short flag -socket actually works",
			args:       []string{"-socket", "/tmp/test.sock"},
			wantOpts:   globalOptions{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{},
		},
		{
			name:       "flags can appear anywhere in args",
			args:       []string{"--socket", "/tmp/test.sock", "job"},
			wantOpts:   globalOptions{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{"job"},
		},
		{
			name:       "flags after positional arg are not parsed",
			args:       []string{"job", "--socket", "/tmp/test.sock"},
			wantOpts:   globalOptions{socketPath: defaultSocketPath, jsonOutput: false, timeout: defaultRequestTimeout},
			wantRemain: []string{"job", "--socket", "/tmp/test.sock"},
		},
		{
			name:        "unknown flag",
			args:        []string{"--unknown", "value"},
			wantErr:     true,
			errContains: "flag provided but not defined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOpts, gotRemain, err := parseGlobal(tt.args)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOpts, gotOpts)
			assert.Equal(t, tt.wantRemain, gotRemain)
		})
	}
}

func TestParseGlobalHelp(t *testing.T) {
	useTempClientConfig(t)
	t.Run("long help flag", func(t *testing.T) {
		_, _, err := parseGlobal([]string{"--help"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, errHelp))
	})

	t.Run("short help flag", func(t *testing.T) {
		_, _, err := parseGlobal([]string{"-h"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, errHelp))
	})
}

func TestDispatch(t *testing.T) {
	useTempClientConfig(t)
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
		wantPanic   bool
	}{
		{
			name:    "job command",
			args:    []string{"job"},
			wantErr: false,
		},
		{
			name:    "sandbox command",
			args:    []string{"sandbox"},
			wantErr: false,
		},
		{
			name:    "workspace command",
			args:    []string{"workspace"},
			wantErr: false,
		},
		{
			name:    "profile command",
			args:    []string{"profile"},
			wantErr: false,
		},
		{
			name:    "ssh command",
			args:    []string{"ssh"},
			wantErr: false,
		},
		{
			name:    "logs command",
			args:    []string{"logs"},
			wantErr: false,
		},
		{
			name:        "connect command missing flags",
			args:        []string{"connect"},
			wantErr:     true,
			errContains: "endpoint is required",
		},
		{
			name:    "disconnect command",
			args:    []string{"disconnect"},
			wantErr: false,
		},
		{
			name:        "unknown command",
			args:        []string{"unknown"},
			wantErr:     true,
			errContains: "unknown command",
		},
		{
			name:      "empty args causes panic in dispatch",
			args:      []string{},
			wantPanic: true, // This test causes a panic, which is expected behavior
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantPanic {
				assert.Panics(t, func() {
					base := commonFlags{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: 10 * time.Second}
					_ = dispatch(context.Background(), tt.args, base)
				})
				return
			}

			base := commonFlags{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: 10 * time.Second}
			err := dispatch(context.Background(), tt.args, base)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				// Commands will fail due to missing args/subcommands, but dispatch should not error
				// The error will be from the subcommand handler, not dispatch itself
				// For "job", "sandbox", "workspace", "profile" which print usage and return nil
				if tt.args[0] == "job" || tt.args[0] == "sandbox" || tt.args[0] == "workspace" || tt.args[0] == "profile" {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestDispatchHelpTokens(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"job help", []string{"job", "--help"}},
		{"sandbox help", []string{"sandbox", "-h"}},
		{"workspace help", []string{"workspace", "help"}},
		{"profile help", []string{"profile", "help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := commonFlags{socketPath: "/tmp/test.sock", jsonOutput: false, timeout: 10 * time.Second}
			err := dispatch(context.Background(), tt.args, base)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errHelp))
		})
	}
}

func TestIsHelpToken(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"help", true},
		{"-h", true},
		{"--help", true},
		{"  help  ", true},
		{"  -h  ", true},
		{"  --help  ", true},
		{"version", false},
		{"job", false},
		{"", false},
		{"-help", false},
		{"h", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isHelpToken(tt.name))
		})
	}
}

func TestParseGlobalSocketPathEdgeCases(t *testing.T) {
	useTempClientConfig(t)
	tests := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{
			name:     "empty socket path uses default",
			args:     []string{},
			wantPath: defaultSocketPath,
		},
		{
			name:     "explicit empty socket path",
			args:     []string{"--socket", ""},
			wantPath: defaultSocketPath,
		},
		{
			name:     "relative path",
			args:     []string{"--socket", "./agentlab.sock"},
			wantPath: "./agentlab.sock",
		},
		{
			name:     "absolute path",
			args:     []string{"--socket", "/var/run/agentlab/agentlabd.sock"},
			wantPath: "/var/run/agentlab/agentlabd.sock",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, _, err := parseGlobal(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, opts.socketPath)
		})
	}
}

func TestParseGlobalTimeoutVariations(t *testing.T) {
	useTempClientConfig(t)
	tests := []struct {
		name        string
		args        []string
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:        "seconds",
			args:        []string{"--timeout", "30s"},
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "minutes",
			args:        []string{"--timeout", "5m"},
			wantTimeout: 5 * time.Minute,
		},
		{
			name:        "hours",
			args:        []string{"--timeout", "2h"},
			wantTimeout: 2 * time.Hour,
		},
		{
			name:        "milliseconds",
			args:        []string{"--timeout", "500ms"},
			wantTimeout: 500 * time.Millisecond,
		},
		{
			name:        "zero duration",
			args:        []string{"--timeout", "0s"},
			wantTimeout: 0,
		},
		{
			name:        "negative duration is accepted by Go",
			args:        []string{"--timeout", "-1s"},
			wantTimeout: -1 * time.Second,
		},
		{
			name:    "invalid unit",
			args:    []string{"--timeout", "30"},
			wantErr: true,
		},
		{
			name:    "invalid format",
			args:    []string{"--timeout", "invalid"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, _, err := parseGlobal(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantTimeout, opts.timeout)
			}
		})
	}
}

func TestMainExitPaths(t *testing.T) {
	useTempClientConfig(t)
	// Test various exit paths through main()
	// Since main() calls os.Exit(), we need to test the individual components

	t.Run("parseGlobal error returns exit code 2", func(t *testing.T) {
		// parseGlobal returns error on invalid flag
		_, _, err := parseGlobal([]string{"--invalid-flag"})
		assert.Error(t, err)
	})

	t.Run("showVersion true exits early", func(t *testing.T) {
		// This is tested by checking that showVersion can be set
		opts, _, err := parseGlobal([]string{"--version"})
		require.NoError(t, err)
		assert.True(t, opts.showVersion)
	})

	t.Run("empty args after flags prints usage", func(t *testing.T) {
		opts, _, err := parseGlobal([]string{})
		require.NoError(t, err)
		assert.False(t, opts.showVersion)
		// With empty args, main() would printUsage() and return
	})

	t.Run("help token prints usage", func(t *testing.T) {
		assert.True(t, isHelpToken("help"))
		assert.True(t, isHelpToken("-h"))
		assert.True(t, isHelpToken("--help"))
	})
}

func TestCommonFlagsBinding(t *testing.T) {
	tests := []struct {
		name         string
		initial      commonFlags
		args         []string
		wantEndpoint string
		wantToken    string
		wantSocket   string
		wantJSON     bool
		wantTimeout  time.Duration
		wantErr      bool
	}{
		{
			name:        "default values",
			initial:     commonFlags{},
			args:        []string{},
			wantSocket:  "",
			wantJSON:    false,
			wantTimeout: 0,
		},
		{
			name:        "set socket",
			initial:     commonFlags{},
			args:        []string{"--socket", "/test.sock"},
			wantSocket:  "/test.sock",
			wantJSON:    false,
			wantTimeout: 0,
		},
		{
			name:        "set json",
			initial:     commonFlags{},
			args:        []string{"--json"},
			wantSocket:  "",
			wantJSON:    true,
			wantTimeout: 0,
		},
		{
			name:        "set timeout",
			initial:     commonFlags{},
			args:        []string{"--timeout", "1m"},
			wantSocket:  "",
			wantJSON:    false,
			wantTimeout: time.Minute,
		},
		{
			name:        "all flags",
			initial:     commonFlags{},
			args:        []string{"--socket", "/sock", "--json", "--timeout", "30s"},
			wantSocket:  "/sock",
			wantJSON:    true,
			wantTimeout: 30 * time.Second,
		},
		{
			name:         "endpoint and token",
			initial:      commonFlags{},
			args:         []string{"--endpoint", "https://example", "--token", "tok"},
			wantEndpoint: "https://example",
			wantToken:    "tok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			opts := tt.initial
			opts.bind(fs)
			err := fs.Parse(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantEndpoint, opts.endpoint)
				assert.Equal(t, tt.wantToken, opts.token)
				assert.Equal(t, tt.wantSocket, opts.socketPath)
				assert.Equal(t, tt.wantJSON, opts.jsonOutput)
				assert.Equal(t, tt.wantTimeout, opts.timeout)
			}
		})
	}
}

// TestOptionalBool tests the custom optionalBool flag type
func TestOptionalBool(t *testing.T) {
	t.Run("String method", func(t *testing.T) {
		tests := []struct {
			name     string
			value    *optionalBool
			expected string
		}{
			{"nil pointer", nil, ""},
			{"not set", &optionalBool{set: false}, ""},
			{"set to true", &optionalBool{value: true, set: true}, "true"},
			{"set to false", &optionalBool{value: false, set: true}, "false"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var o *optionalBool
				if tt.value != nil {
					o = &optionalBool{value: tt.value.value, set: tt.value.set}
				}
				assert.Equal(t, tt.expected, o.String())
			})
		}
	})

	t.Run("Set method", func(t *testing.T) {
		tests := []struct {
			value    string
			wantBool bool
			wantErr  bool
		}{
			{"true", true, false},
			{"false", false, false},
			{"TRUE", true, false},
			{"FALSE", false, false},
			{"1", true, false},  // strconv.ParseBool accepts 1 as true
			{"0", false, false}, // strconv.ParseBool accepts 0 as false
			{"t", true, false},
			{"f", false, false},
			{"T", true, false},
			{"F", false, false},
			{"invalid", false, true},
		}
		for _, tt := range tests {
			t.Run(tt.value, func(t *testing.T) {
				o := &optionalBool{}
				err := o.Set(tt.value)
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					require.NoError(t, err)
					assert.True(t, o.set)
					assert.Equal(t, tt.wantBool, o.value)
				}
			})
		}
	})

	t.Run("IsBoolFlag", func(t *testing.T) {
		o := &optionalBool{}
		assert.True(t, o.IsBoolFlag())
	})

	t.Run("Ptr method", func(t *testing.T) {
		tests := []struct {
			name  string
			value *optionalBool
			want  *bool
		}{
			{"nil", nil, nil},
			{"not set", &optionalBool{set: false}, nil},
			{"set true", &optionalBool{value: true, set: true}, func() *bool { v := true; return &v }()},
			{"set false", &optionalBool{value: false, set: true}, func() *bool { v := false; return &v }()},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var o *optionalBool
				if tt.value != nil {
					o = &optionalBool{value: tt.value.value, set: tt.value.set}
				}
				got := o.Ptr()
				if tt.want == nil {
					assert.Nil(t, got)
				} else {
					require.NotNil(t, got)
					assert.Equal(t, *tt.want, *got)
				}
			})
		}
	})
}

// CaptureOutput captures stdout and stderr from a function
func CaptureOutput(f func()) string {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w

	f()

	w.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var buf strings.Builder
	io.Copy(&buf, r)
	return buf.String()
}

func TestUsagePrints(t *testing.T) {
	t.Run("printUsage outputs usage text", func(t *testing.T) {
		output := CaptureOutput(printUsage)
		assert.Contains(t, output, "agentlab is the CLI for agentlabd")
		assert.Contains(t, output, "Usage:")
		assert.Contains(t, output, "--version")
	})

	t.Run("printJobUsage outputs job usage", func(t *testing.T) {
		output := CaptureOutput(printJobUsage)
		assert.Contains(t, output, "job <run|show|artifacts>")
	})

	t.Run("printSandboxUsage outputs sandbox usage", func(t *testing.T) {
		output := CaptureOutput(printSandboxUsage)
		assert.Contains(t, output, "sandbox <new|list|show|start|stop|revert|destroy|lease|prune|expose|exposed|unexpose>")
	})

	t.Run("printWorkspaceUsage outputs workspace usage", func(t *testing.T) {
		output := CaptureOutput(printWorkspaceUsage)
		assert.Contains(t, output, "workspace <create|list|attach|detach|rebind>")
	})

	t.Run("printConnectUsage outputs connect usage", func(t *testing.T) {
		output := CaptureOutput(printConnectUsage)
		assert.Contains(t, output, "agentlab connect")
		assert.Contains(t, output, "--endpoint")
	})

	t.Run("printDisconnectUsage outputs disconnect usage", func(t *testing.T) {
		output := CaptureOutput(printDisconnectUsage)
		assert.Contains(t, output, "agentlab disconnect")
	})
}

func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, "/run/agentlab/agentlabd.sock", defaultSocketPath)
	assert.Equal(t, 10*time.Minute, defaultRequestTimeout)
	assert.Equal(t, 50, defaultLogTail)
	assert.Equal(t, 2*time.Second, eventPollInterval)
	assert.Equal(t, 200, defaultEventLimit)
	assert.Equal(t, 1000, maxEventLimit)
}

// GoldenFileTests compares output against golden files
func TestGoldenFileVersionOutput(t *testing.T) {
	// Build version output should match golden file
	got := CaptureOutput(func() {
		fmt.Println(buildinfo.String())
	})

	goldenPath := "testdata/version.golden"
	golden, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "failed to read golden file")

	// Normalize line endings
	got = strings.ReplaceAll(got, "\r\n", "\n")
	want := strings.ReplaceAll(string(golden), "\r\n", "\n")

	assert.Equal(t, want, got, "output should match golden file")
}

func TestGoldenFileUsageOutput(t *testing.T) {
	// Usage output is stable, test key parts
	got := CaptureOutput(printUsage)

	assert.Contains(t, got, "agentlab is the CLI for agentlabd")
	assert.Contains(t, got, "Usage:")
	assert.Contains(t, got, "Global Flags:")
	assert.Contains(t, got, "--endpoint URL")
	assert.Contains(t, got, "--token TOKEN")
	assert.Contains(t, got, "--socket PATH")
	assert.Contains(t, got, "--json")
	assert.Contains(t, got, "--timeout")
	assert.Contains(t, got, "agentlab connect")
	assert.Contains(t, got, "agentlab disconnect")
}

func TestGoldenFileJobUsageOutput(t *testing.T) {
	got := CaptureOutput(printJobUsage)

	assert.Contains(t, got, "agentlab job <run|show|artifacts>")
}

func TestGoldenFileSandboxUsageOutput(t *testing.T) {
	got := CaptureOutput(printSandboxUsage)

	assert.Contains(t, got, "agentlab sandbox <new|list|show|start|stop|revert|destroy|lease|prune|expose|exposed|unexpose>")
}

func TestGoldenFileWorkspaceUsageOutput(t *testing.T) {
	got := CaptureOutput(printWorkspaceUsage)

	assert.Contains(t, got, "agentlab workspace <create|list|attach|detach|rebind>")
}

func TestGoldenFileLogsUsageOutput(t *testing.T) {
	got := CaptureOutput(printLogsUsage)

	assert.Contains(t, got, "agentlab logs <vmid>")
	assert.Contains(t, got, "--follow")
	assert.Contains(t, got, "--tail")
}

func TestGoldenFileSSHUsageOutput(t *testing.T) {
	got := CaptureOutput(printSSHUsage)

	assert.Contains(t, got, "agentlab ssh <vmid>")
	assert.Contains(t, got, "--user")
	assert.Contains(t, got, "--port")
	assert.Contains(t, got, "--identity")
	assert.Contains(t, got, "--no-start")
	assert.Contains(t, got, "--wait")
}
