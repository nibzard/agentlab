# AgentLab CLI Reference

This file is auto-generated from `agentlab --help`.
Do not edit by hand. Run `make docs-gen` to refresh.

## Usage

```text
agentlab is the CLI for agentlabd.

Usage:
  agentlab --version
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] status
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] init [--apply] [--smoke-test] [--assets <path>] [--force] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve]
  agentlab [--json] bootstrap --host <ssh_host> [--ssh-user <user>] [--ssh-port <port>] [--identity <path>] [--assets <path>] [--control-port <port>] [--control-token <token>] [--rotate-control-token] [--tailscale-serve|--no-tailscale-serve] [--tailscale-authkey <key>] [--tailscale-hostname <name>] [--release-url <url>] [--agentlab-bin <path>] [--agentlabd-bin <path>] [--agentlab-url <url>] [--agentlabd-url <url>] [--force] [--keep-temp] [--verbose]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job run --repo <url> --task <task> --profile <profile> [--ref <ref>] [--branch <branch>] [--mode <mode>] [--ttl <ttl>] [--keepalive] [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>] [--workspace-wait <duration>] [--stateful]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job show <job_id> [--events-tail <n>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job artifacts <job_id>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job artifacts download <job_id> [--out <path>] [--path <path>] [--name <name>] [--latest] [--bundle]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] job doctor <job_id> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox new [--name <name>] [--ttl <ttl>] [--keepalive] [--workspace <id>] [--vmid <vmid>] [--job <id>] [--and-ssh] (--profile <profile> | +mod [+mod...])
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox show <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox start <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox stop <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox stop --all [--force]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox revert [--force] [--restart|--no-restart] <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox destroy [--force] <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox lease renew --ttl <ttl> <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox prune
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox expose [--force] <vmid> :<port>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox exposed
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox unexpose <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] sandbox doctor <vmid> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace create --name <name> --size <size> [--storage <storage>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace check <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace fsck <workspace> [--repair]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace attach <workspace> <vmid>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace detach <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace rebind <workspace> --profile <profile> [--ttl <ttl>] [--keep-old]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace fork <workspace> --name <name> [--from-snapshot <name>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot create <workspace> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot list <workspace>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] workspace snapshot restore <workspace> <name>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session create --name <name> --profile <profile> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--branch <branch>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session show <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session resume <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session stop <session>
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session fork <session> --name <name> (--workspace <id|name|new:name> | --workspace-create <name>) [--workspace-size <size>] [--workspace-storage <storage>] [--profile <profile>] [--branch <branch>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session branch <branch> --profile <profile> [--workspace <id|name|new:name>] [--workspace-create <name>] [--workspace-size <size>] [--workspace-storage <storage>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] session doctor <session> [--out <path>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] profile list
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] msg post (--job <id> | --workspace <id> | --session <id>) [--author <name>] [--kind <kind>] [--text <text>] [--payload <json>] [message...]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] msg tail (--job <id> | --workspace <id> | --session <id>) [--follow] [--tail <n>]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] ssh <vmid> [--user <user>] [--port <port>] [--identity <path>] [--jump-host <host>] [--jump-user <user>] [--exec] [--no-start] [--wait]
  agentlab [--endpoint URL] [--token TOKEN] [--socket PATH] [--json] [--timeout DURATION] logs <vmid> [--follow] [--tail <n>]
  agentlab connect --endpoint <url> --token <token> [--jump-host <host>] [--jump-user <user>]
  agentlab disconnect

Global Flags:
  --endpoint URL  Control plane endpoint (http(s)://host:port)
  --token TOKEN   Control plane auth token (Authorization: Bearer)
  --socket PATH   Path to agentlabd socket (default /run/agentlab/agentlabd.sock)
  --json          Output json
  --timeout       Request timeout (e.g. 30s, 2m)

Errors:
  When --json is set, errors are emitted as: {"error":"message"}

Exit codes:
  0: Success or help displayed
  1: Command or request failed
  2: Invalid arguments or usage
```
