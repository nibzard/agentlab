# AgentLab CLI Reference

This file is auto-generated from `agentlab --help`.
Do not edit by hand. Run `make docs-gen` to refresh.

## Usage

```text
agentlab is the CLI for agentlabd.

Usage:
  agentlab [global flags] <command> [flags]
  agentlab help <command> [subcommand]
  agentlab bootstrap
  agentlab completion
  agentlab connect
  agentlab defaults
  agentlab disconnect
  agentlab help
  agentlab init
  agentlab integration
  agentlab job
  agentlab logs
  agentlab ls
  agentlab msg
  agentlab new
  agentlab operation
  agentlab pool
  agentlab profile
  agentlab rm
  agentlab sandbox
  agentlab schema
  agentlab secrets
  agentlab session
  agentlab show
  agentlab ssh
  agentlab start
  agentlab status
  agentlab stop
  agentlab team
  agentlab token
  agentlab user
  agentlab version
  agentlab workspace

Global Flags:
  --endpoint URL  Control plane endpoint
  --token TOKEN   Control plane token
  --socket PATH   Daemon socket (default /run/agentlab/agentlabd.sock)
  --timeout DURATION Request timeout (default 10m)
  --json[=false]  JSON output, including errors
  --allow-insecure-http Permit HTTP inside a trusted tunnel
  --version      Print version

Preferences: explicit flag, existing environment override, saved preference, built-in default.
Exit codes: 0 success/help, 1 operational failure, 2 invalid arguments.
```

## Command catalog

Generated from the registered command definitions. `agentlab help <command>`
shows the same command flags. Common flags accept values before or after operands.
Saved preferences apply after explicit flags and existing environment overrides.

### `bootstrap`

```text
agentlab bootstrap [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--agentlab-bin` | string |  | path to agentlab linux binary to upload |
| `--agentlab-url` | string |  | download URL for agentlab linux binary |
| `--agentlabd-bin` | string |  | path to agentlabd linux binary to upload |
| `--agentlabd-url` | string |  | download URL for agentlabd linux binary |
| `--assets` | string |  | path to agentlab assets (scripts/) |
| `--control-port` | int | inherited or computed | control plane port |
| `--control-token` | string |  | control plane auth token (optional) |
| `--force` | bool | false | force overwrite of managed network files |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--host` | string |  | SSH host (required) |
| `--identity` | string |  | SSH identity file |
| `--keep-temp` | bool | false | keep remote bootstrap bundle directory |
| `--no-tailscale-serve` | bool | false | disable tailscale serve publishing |
| `--release-url` | string |  | base URL for downloading linux binaries |
| `--rotate-control-token` | bool | false | rotate control auth token |
| `--ssh-port` | int | inherited or computed | SSH port |
| `--ssh-user` | string | inherited or computed | SSH username |
| `--tailscale-api-key` | string |  | tailscale admin API key (optional) |
| `--tailscale-authkey` | string |  | tailscale auth key for unattended tailscale up |
| `--tailscale-hostname` | string |  | tailscale hostname (when using authkey) |
| `--tailscale-oauth-client-id` | string |  | tailscale oauth client id (optional) |
| `--tailscale-oauth-client-secret` | string |  | tailscale oauth client secret (optional) |
| `--tailscale-oauth-scopes` | string |  | tailscale oauth scopes (comma/space separated) |
| `--tailscale-serve` | bool | false | force tailscale serve publishing |
| `--tailscale-tailnet` | string |  | tailscale tailnet name for admin API (optional) |
| `--verbose` | bool | false | show remote command output |

Help example: `agentlab help bootstrap`.

### `completion`

```text
agentlab completion <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help completion`.

### `connect`

```text
agentlab connect [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--jump-host` | string |  | default SSH jump host (used when subnet route is unavailable) |
| `--jump-user` | string |  | default SSH jump username |

Help example: `agentlab help connect`.

### `defaults delete`

```text
agentlab defaults delete <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help defaults delete`.

### `defaults effective`

```text
agentlab defaults effective [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help defaults effective`.

### `defaults list`

```text
agentlab defaults list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help defaults list`.

### `defaults read`

```text
agentlab defaults read <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help defaults read`.

### `defaults write`

```text
agentlab defaults write <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help defaults write`.

### `disconnect`

```text
agentlab disconnect [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help disconnect`.

### `init`

```text
agentlab init [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--apply` | bool | false | apply recommended host setup changes |
| `--assets` | string |  | path to agentlab repo root (for scripts) |
| `--backend` | string |  | sandbox backend: proxmox, docker, libvirt (default proxmox) |
| `--control-port` | int | inherited or computed | control plane port |
| `--control-token` | string |  | control plane auth token (optional) |
| `--force` | bool | false | overwrite managed network files when applying |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--no-tailscale-serve` | bool | false | disable tailscale serve publishing |
| `--rotate-control-token` | bool | false | rotate control auth token |
| `--smoke-test` | bool | false | run end-to-end provisioning smoke test |
| `--tailscale-serve` | bool | false | force tailscale serve publishing |

Help example: `agentlab help init`.

### `integration add`

```text
agentlab integration add [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--attach` | string | auto:all | attachment mode (sandbox:<name>, tag:<value>, auto:all) |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--name` | string |  | integration name (unique identifier) |
| `--secret` | string |  | secret value (API key, token, password) |
| `--secret-header` | string |  | custom header name (for header secret-type) |
| `--secret-type` | string | bearer | how to inject secret (bearer, header, basic-auth) |
| `--target` | string |  | target URL for HTTP proxy (e.g., https://api.example.com) |
| `--type` | string | http-proxy | integration type (http-proxy or git-proxy) |
| `--username` | string |  | username for basic-auth or git proxy |

Help example: `agentlab help integration add`.

### `integration list`

```text
agentlab integration list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help integration list`.

### `integration rm`

```text
agentlab integration rm <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help integration rm`.

### `integration status`

```text
agentlab integration status [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--sandbox` | string |  | sandbox name to show integrations for |

Help example: `agentlab help integration status`.

### `job artifacts`

```text
agentlab job artifacts <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help job artifacts`.

### `job artifacts download`

```text
agentlab job artifacts download <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--bundle` | bool | false | download latest bundle (agentlab-artifacts.tar.gz) |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--latest` | bool | false | download latest artifact |
| `--name` | string |  | artifact name to download |
| `--out` | string |  | output file path or directory |
| `--path` | string |  | artifact path to download |

Help example: `agentlab help job artifacts download`.

### `job doctor`

```text
agentlab job doctor <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--out` | string |  | output file path or directory |

Help example: `agentlab help job doctor`.

### `job list`

```text
agentlab job list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--after` | string |  | opaque pagination cursor |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--limit` | int | 50 | page size (1-200) |
| `--profile` | string |  | profile filter |
| `--state` | string |  | job state |
| `--workspace` | string |  | workspace ID filter |

Help example: `agentlab help job list`.

### `job run`

```text
agentlab job run [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--branch` | string |  | branch name (session-backed) |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--keepalive` | bool | unset | keep sandbox after job completion |
| `--mode` | string |  | mode (default dangerous) |
| `--profile` | string |  | profile name |
| `--ref` | string |  | git ref (default main) |
| `--repo` | string |  | git repository url |
| `--stateful` | bool | false | create a default workspace for a stateful job |
| `--task` | string |  | task description |
| `--ttl` | string |  |  |
| `--workspace` | string |  | workspace id or name (or new:<name>) |
| `--workspace-create` | string |  | create workspace with name |
| `--workspace-size` | string |  | workspace size for creation (e.g. 80G) |
| `--workspace-storage` | string |  | workspace storage (default local-zfs) |
| `--workspace-wait` | string |  | wait for workspace detach (e.g. 2m, 30s) |

Help example: `agentlab help job run`.

### `job show`

```text
agentlab job show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--events-tail` | int | inherited or computed | number of recent events to include (0 to omit) |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help job show`.

### `job validate`

```text
agentlab job validate [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--branch` | string |  | branch name (session-backed) |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--keepalive` | bool | unset | keep sandbox after job completion |
| `--mode` | string |  | mode (default dangerous) |
| `--profile` | string |  | profile name |
| `--ref` | string |  | git ref (default main) |
| `--repo` | string |  | git repository url |
| `--stateful` | bool | false | create a default workspace for a stateful job |
| `--task` | string |  | task description |
| `--ttl` | string |  |  |
| `--workspace` | string |  | workspace id or name (or new:<name>) |
| `--workspace-create` | string |  | create workspace with name |
| `--workspace-size` | string |  | workspace size for creation (e.g. 80G) |
| `--workspace-storage` | string |  | workspace storage (default local-zfs) |
| `--workspace-wait` | string |  | wait for workspace detach (e.g. 2m, 30s) |

Help example: `agentlab help job validate`.

### `job wait`

```text
agentlab job wait <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help job wait`.

### `logs`

```text
agentlab logs <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--follow` | bool | false | follow new events |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--tail` | int | inherited or computed | show the last N events |

Help example: `agentlab help logs`.

### `msg post`

```text
agentlab msg post [+modifier ...] [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string |  | message author |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--job` | string |  | message scope job id |
| `--kind` | string |  | message kind |
| `--payload` | string |  | message json payload |
| `--session` | string |  | message scope session id |
| `--text` | string |  | message text |
| `--workspace` | string |  | message scope workspace id |

Help example: `agentlab help msg post`.

### `msg tail`

```text
agentlab msg tail [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--follow` | bool | false | follow new messages |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--job` | string |  | message scope job id |
| `--session` | string |  | message scope session id |
| `--tail` | int | inherited or computed | show the last N messages |
| `--workspace` | string |  | message scope workspace id |

Help example: `agentlab help msg tail`.

### `operation show`

```text
agentlab operation show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help operation show`.

### `operation wait`

```text
agentlab operation wait <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help operation wait`.

### `pool status`

```text
agentlab pool status [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help pool status`.

### `profile list`

```text
agentlab profile list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help profile list`.

### `sandbox destroy`

```text
agentlab sandbox destroy <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | false | compatibility alias for ordinary destruction and cleanup |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox destroy`.

### `sandbox doctor`

```text
agentlab sandbox doctor <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--out` | string |  | output file path or directory |

Help example: `agentlab help sandbox doctor`.

### `sandbox expose`

```text
agentlab sandbox expose <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | false | skip confirmation prompt |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox expose`.

### `sandbox exposed`

```text
agentlab sandbox exposed [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox exposed`.

### `sandbox inventory`

```text
agentlab sandbox inventory [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox inventory`.

### `sandbox lease renew`

```text
agentlab sandbox lease renew <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--ttl` | string |  |  |

Help example: `agentlab help sandbox lease renew`.

### `sandbox list`

```text
agentlab sandbox list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--a` | bool | false | include destroyed sandboxes |
| `--all` | bool | false | include destroyed sandboxes |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox list`.

### `sandbox new`

```text
agentlab sandbox new [+modifier ...] [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--and-ssh` | bool | false | create sandbox and immediately ssh into it |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--idempotency-key` | string |  | reuse this key for safe create retries |
| `--image` | string | inherited or computed | container image for LXC sandboxes (e.g., ubuntu:22.04) |
| `--job` | string |  | attach to existing job id |
| `--keepalive` | bool | unset | enable keepalive lease for sandbox |
| `--name` | string |  | sandbox name |
| `--no-wait` | bool | false | return an operation identifier before provisioning finishes |
| `--profile` | string |  | profile name |
| `--prompt` | string |  | initial agent prompt for agent-ready sandboxes |
| `--tag` | string | unset | integration-attachment tag (repeatable; normalized to lowercase) |
| `--ttl` | string |  |  |
| `--type` | string |  | sandbox type: vm (default) or lxc |
| `--vmid` | int | 0 | vmid override |
| `--workspace` | string |  | workspace id or name |

Help example: `agentlab help sandbox new`.

### `sandbox pause`

```text
agentlab sandbox pause <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox pause`.

### `sandbox prune`

```text
agentlab sandbox prune [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | false | preview timed-out sandboxes without destroying them |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--vmid` | string | unset | target VMID from a preview (repeatable) |

Help example: `agentlab help sandbox prune`.

### `sandbox reconcile`

```text
agentlab sandbox reconcile [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--apply` | bool | false | apply supported reconciliation fixes |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox reconcile`.

### `sandbox resume`

```text
agentlab sandbox resume <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox resume`.

### `sandbox revert`

```text
agentlab sandbox revert <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | false | force revert even if a job is running |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--no-restart` | bool | false | do not restart the sandbox after revert |
| `--restart` | bool | false | restart the sandbox after revert |

Help example: `agentlab help sandbox revert`.

### `sandbox show`

```text
agentlab sandbox show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox show`.

### `sandbox snapshot list`

```text
agentlab sandbox snapshot list <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox snapshot list`.

### `sandbox snapshot restore`

```text
agentlab sandbox snapshot restore <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | false | override snapshot safety checks |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox snapshot restore`.

### `sandbox snapshot save`

```text
agentlab sandbox snapshot save <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--force` | bool | false | override snapshot safety checks |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox snapshot save`.

### `sandbox start`

```text
agentlab sandbox start <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox start`.

### `sandbox stop`

```text
agentlab sandbox stop <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--all` | bool | false | stop all running sandboxes |
| `--force` | bool | false | skip confirmation prompt |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox stop`.

### `sandbox unexpose`

```text
agentlab sandbox unexpose <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox unexpose`.

### `sandbox update`

```text
agentlab sandbox update <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--clear-tags` | bool | false | remove all sandbox tags |
| `--cores` | int | 0 | number of CPU cores |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--memory` | string |  | memory size in MiB or GiB (for example 8192 or 8GiB) |
| `--tag` | string | unset | replace sandbox tags with these values (repeatable); omits leaves them unchanged |

Help example: `agentlab help sandbox update`.

### `sandbox validate`

```text
agentlab sandbox validate [+modifier ...] [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--job` | string |  | attach to existing job id |
| `--keepalive` | bool | unset | enable keepalive lease for sandbox |
| `--name` | string |  | sandbox name |
| `--profile` | string |  | profile name |
| `--ttl` | string |  |  |
| `--vmid` | int | 0 | vmid override |
| `--workspace` | string |  | workspace id or name |

Help example: `agentlab help sandbox validate`.

### `sandbox wait`

```text
agentlab sandbox wait <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help sandbox wait`.

### `schema`

```text
agentlab schema <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help schema`.

### `secrets add-ssh-key`

```text
agentlab secrets add-ssh-key [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--key` | string |  | SSH public key value |
| `--key-file` | string |  | path to an SSH public key file |
| `--name` | string |  | key name in the bundle |
| `--sops-path` | string |  | sops binary path override |

Help example: `agentlab help secrets add-ssh-key`.

### `secrets clear-tailscale`

```text
agentlab secrets clear-tailscale [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--sops-path` | string |  | sops binary path override |

Help example: `agentlab help secrets clear-tailscale`.

### `secrets remove-ssh-key`

```text
agentlab secrets remove-ssh-key [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | key name in the bundle |
| `--sops-path` | string |  | sops binary path override |

Help example: `agentlab help secrets remove-ssh-key`.

### `secrets set-env`

```text
agentlab secrets set-env [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--from-file` | string |  | bulk: path to a KEY=VAL line file or a JSON object |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | environment variable name (single-variable mode) |
| `--value` | string |  | environment variable value (single-variable mode) |
| `--value-file` | string |  | read the single value from a file (single-variable mode) |

Help example: `agentlab help secrets set-env`.

### `secrets set-git`

```text
agentlab secrets set-git [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--git-token` | string |  | git access token (e.g. GitHub PAT) |
| `--git-token-file` | string |  | read the git token from a file |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--known-hosts-file` | string |  | path to a known_hosts file |
| `--ssh-key-file` | string |  | path to an SSH private key |
| `--username` | string |  | git username (for HTTPS auth) |

Help example: `agentlab help secrets set-git`.

### `secrets set-tailscale`

```text
agentlab secrets set-tailscale [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--admin-api-key` | string |  | Tailscale Admin API key (tskey-api-...) to mint per-VM auth keys |
| `--admin-api-key-file` | string |  | read the Tailscale Admin API key from a file |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--authkey` | string |  | shared reusable Tailscale auth key (fallback for per-VM minting) |
| `--authkey-file` | string |  | read the shared Tailscale auth key from a file |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--extra-arg` | string | unset | additional `tailscale up` arg (repeatable) |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--hostname-template` | string |  | per-VM hostname template (supports {vmid} and {name}) |
| `--sops-path` | string |  | sops binary path override |
| `--tag` | string | unset | Tailscale tag (repeatable) |
| `--tailnet` | string |  | tailnet to mint keys in (default: the key's own tailnet, "-") |

Help example: `agentlab help secrets set-tailscale`.

### `secrets show`

```text
agentlab secrets show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--format` | string | inherited or computed | output format: yaml or json |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--reveal` | bool | false | show sensitive values instead of redacting them |
| `--sops-path` | string |  | sops binary path override |

Help example: `agentlab help secrets show`.

### `secrets validate`

```text
agentlab secrets validate <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--age-key` | string |  | age key path override |
| `--allow-plaintext` | bool | false | allow reading or writing plaintext bundles |
| `--bundle` | string |  | bundle name or path (defaults to config secrets_bundle) |
| `--config` | string | inherited or computed | path to agentlab config file |
| `--dir` | string |  | secrets directory override |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--sops-path` | string |  | sops binary path override |

Help example: `agentlab help secrets validate`.

### `session branch`

```text
agentlab session branch <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--profile` | string |  | profile name |
| `--workspace` | string |  | workspace id or name (or new:<name>) |
| `--workspace-create` | string |  | create workspace with name |
| `--workspace-size` | string |  | workspace size for creation (e.g. 80G) |
| `--workspace-storage` | string |  | workspace storage (default local-zfs) |

Help example: `agentlab help session branch`.

### `session create`

```text
agentlab session create [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--branch` | string |  | branch label |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | session name |
| `--profile` | string |  | profile name |
| `--workspace` | string |  | workspace id or name (or new:<name>) |
| `--workspace-create` | string |  | create workspace with name |
| `--workspace-size` | string |  | workspace size for creation (e.g. 80G) |
| `--workspace-storage` | string |  | workspace storage (default local-zfs) |

Help example: `agentlab help session create`.

### `session doctor`

```text
agentlab session doctor <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--out` | string |  | output file path or directory |

Help example: `agentlab help session doctor`.

### `session fork`

```text
agentlab session fork <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--branch` | string |  | branch label |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | new session name |
| `--profile` | string |  | profile name (defaults to source session) |
| `--workspace` | string |  | workspace id or name (or new:<name>) |
| `--workspace-create` | string |  | create workspace with name |
| `--workspace-size` | string |  | workspace size for creation (e.g. 80G) |
| `--workspace-storage` | string |  | workspace storage (default local-zfs) |

Help example: `agentlab help session fork`.

### `session list`

```text
agentlab session list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help session list`.

### `session resume`

```text
agentlab session resume <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help session resume`.

### `session show`

```text
agentlab session show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help session show`.

### `session stop`

```text
agentlab session stop <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help session stop`.

### `ssh`

```text
agentlab ssh <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--e` | bool | false | exec ssh instead of printing the command |
| `--exec` | bool | false | exec ssh instead of printing the command |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--i` | string |  | ssh identity file |
| `--identity` | string |  | ssh identity file |
| `--jump-host` | string |  | ssh jump host for ProxyJump fallback |
| `--jump-user` | string |  | ssh jump username for ProxyJump fallback |
| `--no-start` | bool | false | do not auto-start stopped sandboxes |
| `--p` | int | inherited or computed | ssh port |
| `--port` | int | inherited or computed | ssh port |
| `--u` | string | inherited or computed | ssh username |
| `--user` | string | inherited or computed | ssh username |
| `--wait` | bool | false | wait for ssh readiness before returning |

Help example: `agentlab help ssh`.

### `status`

```text
agentlab status [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help status`.

### `team add`

```text
agentlab team add [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--description` | string |  | team description |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | team name |

Help example: `agentlab help team add`.

### `team list`

```text
agentlab team list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help team list`.

### `team member add`

```text
agentlab team member add [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--role` | string | user | role within team (admin or user) |
| `--team` | string |  | team name |
| `--user` | string |  | username to add |

Help example: `agentlab help team member add`.

### `team member rm`

```text
agentlab team member rm [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--team` | string |  | team name |
| `--user` | string |  | username to remove |

Help example: `agentlab help team member rm`.

### `team members`

```text
agentlab team members <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help team members`.

### `team rm`

```text
agentlab team rm <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help team rm`.

### `token create`

```text
agentlab token create [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cmds` | string |  | comma-separated list of allowed command prefixes |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--key` | string |  | path to SSH private key |
| `--scope` | string |  | comma-separated list of sandbox scopes (e.g., sandbox:1001) |
| `--subject` | string |  | optional human-readable label |
| `--ttl` | string | 1h | token lifetime (e.g., 30m, 24h; zero uses the 1h default) |

Help example: `agentlab help token create`.

### `token inspect`

```text
agentlab token inspect <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |

Help example: `agentlab help token inspect`.

### `token list`

```text
agentlab token list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--key` | string |  | path to SSH private key to show fingerprint for |

Help example: `agentlab help token list`.

### `user add`

```text
agentlab user add [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool | false | show help |
| `--help` | bool | false | show help |
| `--key` | string |  | SSH public key (e.g., 'ssh-ed25519 AAAA...') |
| `--name` | string |  | user name (unique identifier) |
| `--role` | string | user | user role: admin or user |

Help example: `agentlab help user add`.

### `user key add`

```text
agentlab user key add [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--key` | string |  | SSH public key to add |
| `--name` | string |  | user name |

Help example: `agentlab help user key add`.

### `user key rm`

```text
agentlab user key rm [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--fingerprint` | string |  | SSH key fingerprint to remove |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | user name |

Help example: `agentlab help user key rm`.

### `user list`

```text
agentlab user list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help user list`.

### `user rm`

```text
agentlab user rm <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help user rm`.

### `user show`

```text
agentlab user show <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help user show`.

### `version`

```text
agentlab version [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--json` | bool |  |  |

Help example: `agentlab help version`.

### `workspace attach`

```text
agentlab workspace attach <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace attach`.

### `workspace check`

```text
agentlab workspace check <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace check`.

### `workspace create`

```text
agentlab workspace create [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | workspace name |
| `--size` | string |  | workspace size (e.g. 80G) |
| `--storage` | string |  | Proxmox storage (default local-zfs) |

Help example: `agentlab help workspace create`.

### `workspace detach`

```text
agentlab workspace detach <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace detach`.

### `workspace fork`

```text
agentlab workspace fork <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--from-snapshot` | string |  | snapshot name to fork from |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--name` | string |  | new workspace name |

Help example: `agentlab help workspace fork`.

### `workspace fsck`

```text
agentlab workspace fsck <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--repair` | bool | false | repair filesystem (requires detached workspace) |

Help example: `agentlab help workspace fsck`.

### `workspace lease clear`

```text
agentlab workspace lease clear <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace lease clear`.

### `workspace list`

```text
agentlab workspace list [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace list`.

### `workspace rebind`

```text
agentlab workspace rebind <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |
| `--keep-old` | bool | false | keep old sandbox running |
| `--profile` | string |  | profile name |
| `--ttl` | string |  |  |

Help example: `agentlab help workspace rebind`.

### `workspace snapshot create`

```text
agentlab workspace snapshot create <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace snapshot create`.

### `workspace snapshot list`

```text
agentlab workspace snapshot list <argument1> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace snapshot list`.

### `workspace snapshot restore`

```text
agentlab workspace snapshot restore <argument1> <argument2> [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--h` | bool |  |  |
| `--help` | bool |  |  |

Help example: `agentlab help workspace snapshot restore`.
