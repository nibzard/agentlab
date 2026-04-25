// ABOUTME: Shell completion command for bash, zsh, and fish.
// ABOUTME: Generates completion scripts from the agentlab command tree.

package main

import (
	"fmt"
	"os"
	"strings"
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
	if len(args) < 1 || isHelpToken(args[0]) {
		fmt.Fprint(os.Stdout, completionUsage)
		return errHelp
	}
	shell := args[0]
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

// Top-level commands and aliases for completion.
var (
	topLevelCommands = []string{
		"new", "ls", "rm", "show", "start", "stop",
		"status", "schema", "init", "bootstrap",
		"job", "sandbox", "workspace", "session",
		"profile", "secrets", "msg", "ssh", "logs",
		"connect", "disconnect", "token", "integration",
		"user", "team", "defaults", "version", "completion",
	}

	jobSubcommands = []string{"run", "validate", "show", "artifacts", "doctor"}
	sandboxSubcommands = []string{
		"new", "validate", "list", "inventory", "reconcile",
		"show", "update", "start", "stop", "pause", "resume",
		"revert", "destroy", "lease", "prune", "expose",
		"exposed", "unexpose", "doctor",
	}
	sandboxSnapshotSubcommands = []string{"save", "list", "restore"}
	workspaceSubcommands = []string{
		"create", "list", "check", "fsck",
		"attach", "detach", "lease", "rebind", "fork", "snapshot",
	}
	workspaceSnapshotSubcommands = []string{"create", "list", "restore"}
	sessionSubcommands = []string{
		"create", "list", "show", "resume", "stop",
		"fork", "branch", "doctor",
	}
	profileSubcommands = []string{"list"}
	msgSubcommands = []string{"post", "tail"}
	tokenSubcommands = []string{"create", "list", "inspect"}
	integrationSubcommands = []string{"add", "list", "rm", "status"}
	userSubcommands = []string{"add", "list", "rm"}
	teamSubcommands = []string{"add", "members", "rm"}
	secretsSubcommands = []string{"show", "validate", "add-ssh-key", "remove-ssh-key", "set-tailscale", "clear-tailscale"}
	defaultsSubcommands = []string{"write", "read", "list", "delete"}
	completionShells = []string{"bash", "zsh", "fish"}
)

func writeBashCompletion(w *os.File) error {
	script := `# bash completion for agentlab
_agentlab_completion() {
	local cur prev words cword
	_init_completion -n = || return

	# If completing after --, offer file completions
	if [[ ${words[*]} == *" -- "* ]]; then
		COMPREPLY=($(compgen -f -- "$cur"))
		return
	fi

	# Build the command context
	local cmd=""
	local subcmd=""
	local subsub=""
	for ((i = 1; i < ${#words[@]}; i++)); do
		case "${words[i]}" in
			-*) continue ;;
			*)
				if [[ -z "$cmd" ]]; then
					cmd="${words[i]}"
				elif [[ -z "$subcmd" ]]; then
					subcmd="${words[i]}"
				elif [[ -z "$subsub" ]]; then
					subsub="${words[i]}"
				fi
				;;
		esac
	done

	# Top-level commands
	if [[ -z "$cmd" ]]; then
		COMPREPLY=($(compgen -W "` + strings.Join(topLevelCommands, " ") + `" -- "$cur"))
		return
	fi

	# Flag completions per command
	case "$cmd" in
		new)
			COMPREPLY=($(compgen -W "--name --ttl --keepalive --workspace --vmid --job --and-ssh --type --image --prompt --profile --json --help -h" -- "$cur"))
			return
			;;
		ls|show|start|stop|rm)
			COMPREPLY=($(compgen -W "--json --help -h" -- "$cur"))
			return
			;;
		job)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(jobSubcommands, " ") + `" -- "$cur")) ;;
				run|validate)
					COMPREPLY=($(compgen -W "--repo --task --profile --ref --branch --mode --ttl --keepalive --workspace --workspace-create --workspace-size --workspace-storage --workspace-wait --stateful --json --help" -- "$cur")) ;;
				show) COMPREPLY=($(compgen -W "--events-tail --json --help" -- "$cur")) ;;
				artifacts)
					if [[ "$subsub" == "download" ]]; then
						COMPREPLY=($(compgen -W "--out --path --name --latest --bundle --json --help" -- "$cur"))
					else
						COMPREPLY=($(compgen -W "download --json --help" -- "$cur"))
					fi
					;;
				doctor) COMPREPLY=($(compgen -W "--out --json --help" -- "$cur")) ;;
			esac
			return
			;;
		sandbox)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(sandboxSubcommands, " ") + `" -- "$cur")) ;;
				snapshot)
					case "$subsub" in
						"") COMPREPLY=($(compgen -W "` + strings.Join(sandboxSnapshotSubcommands, " ") + `" -- "$cur")) ;;
						save) COMPREPLY=($(compgen -W "--force --json --help" -- "$cur")) ;;
						list) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
						restore) COMPREPLY=($(compgen -W "--force --json --help" -- "$cur")) ;;
					esac
					;;
				new) COMPREPLY=($(compgen -W "--name --ttl --keepalive --workspace --vmid --job --and-ssh --type --image --prompt --profile --json --help -h" -- "$cur")) ;;
				validate) COMPREPLY=($(compgen -W "--name --ttl --keepalive --workspace --vmid --job --profile --json --help" -- "$cur")) ;;
				update) COMPREPLY=($(compgen -W "--cores --memory --json --help" -- "$cur")) ;;
				stop) COMPREPLY=($(compgen -W "--all --force --json --help" -- "$cur")) ;;
				revert) COMPREPLY=($(compgen -W "--force --restart --no-restart --json --help" -- "$cur")) ;;
				destroy) COMPREPLY=($(compgen -W "--force --json --help" -- "$cur")) ;;
				lease)
					case "$subsub" in
						"") COMPREPLY=($(compgen -W "renew --json --help" -- "$cur")) ;;
						renew) COMPREPLY=($(compgen -W "--ttl --json --help" -- "$cur")) ;;
					esac
					;;
				expose) COMPREPLY=($(compgen -W "--force --json --help" -- "$cur")) ;;
				doctor) COMPREPLY=($(compgen -W "--out --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		workspace)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(workspaceSubcommands, " ") + `" -- "$cur")) ;;
				snapshot)
					case "$subsub" in
						"") COMPREPLY=($(compgen -W "` + strings.Join(workspaceSnapshotSubcommands, " ") + `" -- "$cur")) ;;
					esac
					;;
				create) COMPREPLY=($(compgen -W "--name --size --storage --json --help" -- "$cur")) ;;
				fsck) COMPREPLY=($(compgen -W "--repair --json --help" -- "$cur")) ;;
				rebind) COMPREPLY=($(compgen -W "--profile --ttl --keep-old --json --help" -- "$cur")) ;;
				fork) COMPREPLY=($(compgen -W "--name --from-snapshot --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		session)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(sessionSubcommands, " ") + `" -- "$cur")) ;;
				create) COMPREPLY=($(compgen -W "--name --profile --workspace --workspace-create --workspace-size --workspace-storage --branch --json --help" -- "$cur")) ;;
				fork) COMPREPLY=($(compgen -W "--name --workspace --workspace-create --workspace-size --workspace-storage --profile --branch --json --help" -- "$cur")) ;;
				branch) COMPREPLY=($(compgen -W "--profile --workspace --workspace-create --workspace-size --workspace-storage --json --help" -- "$cur")) ;;
				doctor) COMPREPLY=($(compgen -W "--out --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		profile)
			COMPREPLY=($(compgen -W "list --json --help" -- "$cur"))
			return
			;;
		secrets)
			COMPREPLY=($(compgen -W "` + strings.Join(secretsSubcommands, " ") + `" -- "$cur"))
			return
			;;
		token)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(tokenSubcommands, " ") + `" -- "$cur")) ;;
				create) COMPREPLY=($(compgen -W "--key --cmds --scope --ttl --subject --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		integration)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(integrationSubcommands, " ") + `" -- "$cur")) ;;
				add) COMPREPLY=($(compgen -W "--type --name --target --attach --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		user)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(userSubcommands, " ") + `" -- "$cur")) ;;
				add) COMPREPLY=($(compgen -W "--key --name --role --json --help" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		team)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(teamSubcommands, " ") + `" -- "$cur")) ;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		defaults)
			case "$subcmd" in
				"") COMPREPLY=($(compgen -W "` + strings.Join(defaultsSubcommands, " ") + `" -- "$cur")) ;;
				write|read|delete)
					# Offer well-known keys as completions
					COMPREPLY=($(compgen -W "default-profile default-image default-backend output-format default-timeout default-socket --json --help" -- "$cur"))
					;;
				*) COMPREPLY=($(compgen -W "--json --help" -- "$cur")) ;;
			esac
			return
			;;
		completion)
			COMPREPLY=($(compgen -W "` + strings.Join(completionShells, " ") + `" -- "$cur"))
			return
			;;
		ssh)
			COMPREPLY=($(compgen -W "--user --port --identity --jump-host --jump-user --exec --no-start --wait --json --help" -- "$cur"))
			return
			;;
	esac

	# Global flags fallback
	COMPREPLY=($(compgen -W "--endpoint --token --socket --json --timeout --version --help -h" -- "$cur"))
}
complete -F _agentlab_completion agentlab
`
	fmt.Fprint(w, script)
	return nil
}

func writeZshCompletion(w *os.File) error {
	script := `#compdef agentlab
# zsh completion for agentlab

_agentlab() {
	local -a commands subcommands
	local curcontext="$curcontext" state line
	typeset -A opt_args

	_arguments -C \
		'--endpoint[Control plane endpoint]:url:' \
		'--token[Auth token]:token:' \
		'--socket[Daemon socket path]:path:_files' \
		'--json[Output JSON]' \
		'--timeout[Request timeout]:duration:' \
		'--version[Print version]' \
		'--help[Show help]' \
		'1:command:->command' \
		'*::arg:->args'

	case $state in
		command)
			commands=(
				'new:Create a sandbox (alias)'
				'ls:List sandboxes (alias)'
				'rm:Destroy a sandbox (alias)'
				'show:Show sandbox details (alias)'
				'start:Start a sandbox (alias)'
				'stop:Stop a sandbox (alias)'
				'status:Daemon status'
				'schema:Show API schema'
				'init:Initialize agentlab'
				'bootstrap:Bootstrap a remote host'
				'job:Manage jobs'
				'sandbox:Manage sandboxes'
				'workspace:Manage workspaces'
				'session:Manage sessions'
				'profile:List profiles'
				'secrets:Manage secrets'
				'msg:Message box'
				'ssh:SSH into a sandbox'
				'logs:View sandbox logs'
				'connect:Connect to control plane'
				'disconnect:Disconnect from control plane'
				'token:Manage API tokens'
				'integration:Manage integrations'
				'user:Manage users'
				'team:Manage teams'
				'defaults:Set CLI preferences'
				'version:Show version info'
				'completion:Generate shell completions'
			)
			_describe 'command' commands
			;;
		args)
			case $words[1] in
				job)
					case $words[2] in
						run) _arguments '--repo[Repository URL]:url:' '--task[Task description]:task:' '--profile[Profile name]:profile:' '--ref[Git ref]:ref:' '--branch[Git branch]:branch:' '--mode[Mode]:mode:' '--ttl[Time to live]:duration:' '--keepalive[Keep alive]' '--workspace[Workspace]:workspace:' '--stateful[Stateful]' ;;
						validate) _arguments '--repo[Repository URL]:url:' '--task[Task description]:task:' '--profile[Profile name]:profile:' ;;
						show) _arguments '--events-tail[Tail events]:n:' ;;
						artifacts) _arguments '2:subcommand:(download)' ;;
						doctor) _arguments '--out[Output path]:path:_files' ;;
						*) _describe 'job subcommand' '(run validate show artifacts doctor)' ;;
					esac
					;;
				sandbox)
					case $words[2] in
						new) _arguments '--name[Name]:name:' '--profile[Profile]:profile:' '--ttl[TTL]:duration:' '--type[Type]:type:(lxc vm)' '--image[Image]:image:' '--prompt[Prompt]:text:' ;;
						snapshot) _describe 'snapshot subcommand' '(save list restore)' ;;
						*) _describe 'sandbox subcommand' '(new validate list inventory reconcile show update start stop pause resume revert snapshot destroy lease prune expose exposed unexpose doctor)' ;;
					esac
					;;
				workspace)
					case $words[2] in
						create) _arguments '--name[Name]:name:' '--size[Size]:size:' '--storage[Storage]:storage:' ;;
						snapshot) _describe 'snapshot subcommand' '(create list restore)' ;;
						*) _describe 'workspace subcommand' '(create list check fsck attach detach lease rebind fork snapshot)' ;;
					esac
					;;
				session)
					case $words[2] in
						create) _arguments '--name[Name]:name:' '--profile[Profile]:profile:' '--workspace[Workspace]:workspace:' ;;
						*) _describe 'session subcommand' '(create list show resume stop fork branch doctor)' ;;
					esac
					;;
				token)
					_describe 'token subcommand' '(create list inspect)' ;;
				integration)
					_describe 'integration subcommand' '(add list rm status)' ;;
				user)
					_describe 'user subcommand' '(add list rm)' ;;
				team)
					_describe 'team subcommand' '(add members rm)' ;;
				defaults)
					case $words[2] in
						write|read|delete) _describe 'defaults key' '(default-profile default-image default-backend output-format default-timeout default-socket)' ;;
						*) _describe 'defaults subcommand' '(write read list delete)' ;;
					esac
					;;
				completion)
					_describe 'shell' '(bash zsh fish)' ;;
			esac
			;;
	esac
}

_agentlab "$@"
`
	fmt.Fprint(w, script)
	return nil
}

func writeFishCompletion(w *os.File) error {
	script := `# fish completion for agentlab

# Disable file completions
complete -c agentlab -f

# Top-level commands
complete -c agentlab -n '__fish_use_subcommand' -a 'new' -d 'Create a sandbox'
complete -c agentlab -n '__fish_use_subcommand' -a 'ls' -d 'List sandboxes'
complete -c agentlab -n '__fish_use_subcommand' -a 'rm' -d 'Destroy a sandbox'
complete -c agentlab -n '__fish_use_subcommand' -a 'show' -d 'Show sandbox details'
complete -c agentlab -n '__fish_use_subcommand' -a 'start' -d 'Start a sandbox'
complete -c agentlab -n '__fish_use_subcommand' -a 'stop' -d 'Stop a sandbox'
complete -c agentlab -n '__fish_use_subcommand' -a 'status' -d 'Daemon status'
complete -c agentlab -n '__fish_use_subcommand' -a 'schema' -d 'API schema'
complete -c agentlab -n '__fish_use_subcommand' -a 'init' -d 'Initialize agentlab'
complete -c agentlab -n '__fish_use_subcommand' -a 'bootstrap' -d 'Bootstrap remote host'
complete -c agentlab -n '__fish_use_subcommand' -a 'job' -d 'Manage jobs'
complete -c agentlab -n '__fish_use_subcommand' -a 'sandbox' -d 'Manage sandboxes'
complete -c agentlab -n '__fish_use_subcommand' -a 'workspace' -d 'Manage workspaces'
complete -c agentlab -n '__fish_use_subcommand' -a 'session' -d 'Manage sessions'
complete -c agentlab -n '__fish_use_subcommand' -a 'profile' -d 'List profiles'
complete -c agentlab -n '__fish_use_subcommand' -a 'secrets' -d 'Manage secrets'
complete -c agentlab -n '__fish_use_subcommand' -a 'msg' -d 'Message box'
complete -c agentlab -n '__fish_use_subcommand' -a 'ssh' -d 'SSH into sandbox'
complete -c agentlab -n '__fish_use_subcommand' -a 'logs' -d 'View logs'
complete -c agentlab -n '__fish_use_subcommand' -a 'connect' -d 'Connect to control plane'
complete -c agentlab -n '__fish_use_subcommand' -a 'disconnect' -d 'Disconnect'
complete -c agentlab -n '__fish_use_subcommand' -a 'token' -d 'Manage API tokens'
complete -c agentlab -n '__fish_use_subcommand' -a 'integration' -d 'Manage integrations'
complete -c agentlab -n '__fish_use_subcommand' -a 'user' -d 'Manage users'
complete -c agentlab -n '__fish_use_subcommand' -a 'team' -d 'Manage teams'
complete -c agentlab -n '__fish_use_subcommand' -a 'defaults' -d 'CLI preferences'
complete -c agentlab -n '__fish_use_subcommand' -a 'version' -d 'Show version'
complete -c agentlab -n '__fish_use_subcommand' -a 'completion' -d 'Shell completions'

# Global flags
complete -c agentlab -l endpoint -d 'Control plane endpoint' -r
complete -c agentlab -l token -d 'Auth token' -r
complete -c agentlab -l socket -d 'Daemon socket path' -r
complete -c agentlab -l json -d 'Output JSON'
complete -c agentlab -l timeout -d 'Request timeout' -r
complete -c agentlab -l version -d 'Print version'
complete -c agentlab -l help -s h -d 'Show help'

# Job subcommands
complete -c agentlab -n '__fish_seen_subcommand_from job' -a 'run' -d 'Run a job'
complete -c agentlab -n '__fish_seen_subcommand_from job' -a 'validate' -d 'Validate job config'
complete -c agentlab -n '__fish_seen_subcommand_from job' -a 'show' -d 'Show job details'
complete -c agentlab -n '__fish_seen_subcommand_from job' -a 'artifacts' -d 'Job artifacts'
complete -c agentlab -n '__fish_seen_subcommand_from job' -a 'doctor' -d 'Debug job'

# Sandbox subcommands
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'new' -d 'Create sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'list' -d 'List sandboxes'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'show' -d 'Show sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'start' -d 'Start sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'stop' -d 'Stop sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'destroy' -d 'Destroy sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'snapshot' -d 'Snapshots'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'expose' -d 'Expose port'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'ssh' -d 'SSH into sandbox'
complete -c agentlab -n '__fish_seen_subcommand_from sandbox' -a 'logs' -d 'View logs'

# Defaults subcommands
complete -c agentlab -n '__fish_seen_subcommand_from defaults' -a 'write' -d 'Set a default'
complete -c agentlab -n '__fish_seen_subcommand_from defaults' -a 'read' -d 'Read a default'
complete -c agentlab -n '__fish_seen_subcommand_from defaults' -a 'list' -d 'List defaults'
complete -c agentlab -n '__fish_seen_subcommand_from defaults' -a 'delete' -d 'Delete a default'

# Completion shells
complete -c agentlab -n '__fish_seen_subcommand_from completion' -a 'bash'
complete -c agentlab -n '__fish_seen_subcommand_from completion' -a 'zsh'
complete -c agentlab -n '__fish_seen_subcommand_from completion' -a 'fish'

# Version flags
complete -c agentlab -n '__fish_seen_subcommand_from version' -l json -d 'Output JSON'

# Token subcommands
complete -c agentlab -n '__fish_seen_subcommand_from token' -a 'create' -d 'Create token'
complete -c agentlab -n '__fish_seen_subcommand_from token' -a 'list' -d 'List tokens'
complete -c agentlab -n '__fish_seen_subcommand_from token' -a 'inspect' -d 'Inspect token'

# Integration subcommands
complete -c agentlab -n '__fish_seen_subcommand_from integration' -a 'add' -d 'Add integration'
complete -c agentlab -n '__fish_seen_subcommand_from integration' -a 'list' -d 'List integrations'
complete -c agentlab -n '__fish_seen_subcommand_from integration' -a 'rm' -d 'Remove integration'
complete -c agentlab -n '__fish_seen_subcommand_from integration' -a 'status' -d 'Integration status'

# User subcommands
complete -c agentlab -n '__fish_seen_subcommand_from user' -a 'add' -d 'Add user'
complete -c agentlab -n '__fish_seen_subcommand_from user' -a 'list' -d 'List users'
complete -c agentlab -n '__fish_seen_subcommand_from user' -a 'rm' -d 'Remove user'

# Team subcommands
complete -c agentlab -n '__fish_seen_subcommand_from team' -a 'add' -d 'Add team'
complete -c agentlab -n '__fish_seen_subcommand_from team' -a 'members' -d 'List members'
complete -c agentlab -n '__fish_seen_subcommand_from team' -a 'rm' -d 'Remove team'
`
	fmt.Fprint(w, script)
	return nil
}
