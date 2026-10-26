// ABOUTME: Shell completion command for bash, zsh, and fish.
// ABOUTME: Generates completion scripts from the agentlab command tree.

package main

import (
	"fmt"
	"os"
)

const completionUsage = `Usage:
  agentlab completion <bash|zsh|fish>

Generate shell completion scripts for agentlab.

Examples:
  # Bash:
  agentlab completion bash > /etc/bash_completion.d/agentlab
  source ~/.bashrc

  # Zsh:
  agentlab completion zsh > "${fpath[1]}/_agentlab"
  autoload -U compinit && compinit

  # Fish:
  agentlab completion fish > ~/.config/fish/completions/agentlab.fish

  # Or add to your shell profile:
  echo 'eval "$(agentlab completion bash)"' >> ~/.bashrc
  echo 'eval "$(agentlab completion zsh)"' >> ~/.zshrc
  echo 'agentlab completion fish | source' >> ~/.config/fish/config.fish
`

func runCompletionCommand(args []string, base commonFlags) error {
	fs := newFlagSet("completion")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)
	if err := parseFlags(fs, args, func() { fmt.Fprint(os.Stdout, completionUsage) }, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return newUsageError(fmt.Errorf("one shell is required"), false)
	}
	shell := fs.Arg(0)

	switch shell {
	case "bash":
		return writeBashCompletion(os.Stdout)
	case "zsh":
		return writeZshCompletion(os.Stdout)
	case "fish":
		return writeFishCompletion(os.Stdout)
	default:
		return newUsageError(fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell), true)
	}
}

func writeBashCompletion(w *os.File) error {
	_, err := fmt.Fprint(w, `# bash completion for agentlab (generated command metadata)
_agentlab_completion() {
  local IFS=$'\n'
  COMPREPLY=($(agentlab __complete "${COMP_WORDS[@]:1:COMP_CWORD}"))
}
complete -F _agentlab_completion agentlab
`)
	return err
}
func writeZshCompletion(w *os.File) error {
	_, err := fmt.Fprint(w, `#compdef agentlab
_agentlab() {
  local -a candidates
  candidates=("${(@f)$(agentlab __complete "${words[@]:1:$((CURRENT-1))}")}")
  compadd -- "${candidates[@]}"
}
compdef _agentlab agentlab
`)
	return err
}
func writeFishCompletion(w *os.File) error {
	_, err := fmt.Fprint(w, `# fish completion for agentlab (generated command metadata)
function __agentlab_complete
  set -l words (commandline -opc)
  command agentlab __complete $words[2..-1] (commandline -ct)
end
complete -c agentlab -f -a '(__agentlab_complete)'
`)
	return err
}
