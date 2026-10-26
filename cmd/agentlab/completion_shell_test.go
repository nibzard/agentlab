package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the shipped wrappers in their real shells. The zsh completion-system
// callbacks are captured so the wrapper can run without an interactive terminal.
func TestCompletionShellWrappers(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "agentlab")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable", shell)
			}
			script, err := exec.Command(binary, "completion", shell).Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				words   string
				current string
				want    string
			}{
				{"agentlab sandbox snapshot ''", "4", "restore"},
				{"agentlab --endpoint https://host sandbox snapshot ''", "6", "restore"},
				{"agentlab sandbox show 123 --", "5", "--help"},
			} {
				var harness string
				switch shell {
				case "bash":
					harness = string(script) + "\nCOMP_WORDS=(" + tc.words + "); COMP_CWORD=" + tc.current + "; COMP_CWORD=$((COMP_CWORD-1)); _agentlab_completion; printf '%s\\n' \"${COMPREPLY[@]}\"\n"
				case "zsh":
					harness = "compdef() { :; }; compadd() { shift; printf '%s\\n' \"$@\"; };\n" + string(script) + "\nwords=(" + tc.words + "); CURRENT=" + tc.current + "; _agentlab\n"
				case "fish":
					// complete -C runs the actual completion expression with commandline state.
					line := strings.TrimSuffix(tc.words, "''")
					harness = string(script) + "\ncomplete -C '" + line + "'\n"
				}
				shellArgs := []string{"-c", harness}
				switch shell {
				case "fish":
					shellArgs = append([]string{"--no-config"}, shellArgs...)
				case "zsh":
					shellArgs = append([]string{"-f"}, shellArgs...)
				case "bash":
					shellArgs = append([]string{"--noprofile", "--norc"}, shellArgs...)
				}
				cmd := exec.Command(executable, shellArgs...)
				cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "XDG_CONFIG_HOME="+dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("wrapper: %v %s", err, out)
				}
				found := false
				for _, line := range strings.Split(string(out), "\n") {
					if strings.SplitN(line, "\t", 2)[0] == tc.want {
						found = true
					}
				}
				if !found {
					t.Errorf("%s missing %s: %s", tc.words, tc.want, out)
				}
			}
		})
	}
}
