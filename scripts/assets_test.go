package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPinnedAssetsRejectTampering(t *testing.T) {
	root := t.TempDir()
	dir, e := Extract(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = VerifyDigest(dir, Digest()); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "guest", "agent-runner")
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Extract(root); e == nil {
		t.Fatal("tampered recipe reused")
	}
	if e = VerifyDigest(dir, Digest()); e == nil {
		t.Fatal("tampering not detected")
	}
}

// Exercise the actual guest verification command with an isolated PATH. This
// catches command -v's success when only some requested executables exist and
// ensures a disabled service cannot be masked by another enabled service.
func TestGuestRecipeChecksEveryExecutableAndService(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required to exercise the host recipe")
	}
	script, err := Files.ReadFile("create_template.sh")
	if err != nil {
		t.Fatal(err)
	}
	command := ""
	for _, line := range strings.Split(string(script), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--run-command '") && strings.Contains(line, "tailscale") && strings.Contains(line, "systemctl is-enabled") {
			command = strings.TrimSuffix(strings.TrimPrefix(line, "--run-command '"), "'")
			break
		}
	}
	if command == "" {
		t.Fatal("guest capability verification command not found")
	}
	// Keep the test fixture PATH isolated; the real chroot needs /usr/local/bin
	// added explicitly to find the uploaded runner.
	command = strings.TrimPrefix(command, `export PATH="/usr/local/sbin:/usr/local/bin:$PATH"; `)

	command = strings.ReplaceAll(command, "/var/lib/tailscale/tailscaled.state", `"$FIXTURE_STATE"`)
	tests := []struct {
		name, missing, disabled string
		state                   bool
	}{{name: "valid"}}
	for _, executable := range []string{"tailscale", "agent-runner", "sshd", "qemu-ga", "jq", "curl"} {
		tests = append(tests, struct {
			name, missing, disabled string
			state                   bool
		}{name: "missing-" + executable, missing: executable})
	}
	for _, service := range []string{"tailscaled", "agent-runner", "qemu-guest-agent", "ssh"} {
		tests = append(tests, struct {
			name, missing, disabled string
			state                   bool
		}{name: "disabled-" + service, disabled: service})
	}
	tests = append(tests, struct {
		name, missing, disabled string
		state                   bool
	}{name: "inherited-enrollment", state: true})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, executable := range []string{"tailscale", "agent-runner", "sshd", "qemu-ga", "jq", "curl"} {
				if executable == test.missing {
					continue
				}
				if err = os.WriteFile(filepath.Join(dir, executable), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\ntest \"$2\" != \"$FAIL_SERVICE\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(dir, "state")
			if test.state {
				if err = os.WriteFile(state, []byte("fixture identity"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bash, "-c", command)
			cmd.Env = []string{"PATH=" + dir, "FAIL_SERVICE=" + test.disabled, "FIXTURE_STATE=" + state}
			output, runErr := cmd.CombinedOutput()
			wantFailure := test.missing != "" || test.disabled != "" || test.state
			if (runErr != nil) != wantFailure {
				t.Fatalf("guest verification failure=%v want=%v: %s", runErr, wantFailure, output)
			}
		})
	}
}

// A failed curl must not be hidden by tee or a shell on the right of a pipe:
// an empty signing key otherwise fails later as a misleading apt GPG error.
func TestGuestRepositoryDownloadsPropagateTransportFailure(t *testing.T) {
	script, err := Files.ReadFile("create_template.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"curl": "exit 35", "tee": "exit 0", "bash": "exit 0"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, line := range strings.Split(string(script), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `--run-command "curl `) {
			continue
		}
		command, err := strconv.Unquote(strings.TrimPrefix(line, "--run-command "))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/sh", "-c", command)
		cmd.Env = []string{"PATH=" + dir}
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("repository transport failure masked: %s: %s", command, out)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("expected all three repository downloads to be exercised, got %d", count)
	}
}
