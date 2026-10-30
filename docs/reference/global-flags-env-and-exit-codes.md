# Global flags, environment, and exit codes

Reference for the `agentlab` CLI global flags, environment variables, client configuration files, JSON error envelope, and exit codes. For the full per-command flag list, see [cli.md](cli.md).

## Global flags

These flags may appear before any subcommand that talks to the daemon.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--context` | string | active context | Select a named server context before the command. |
| `--endpoint` | URL | none | Control-plane HTTP(S) endpoint. Must include an `http://` or `https://` scheme. |
| `--token` | string | none | Control-plane bearer token, sent as `Authorization: Bearer`. |
| `--socket` | path | `/run/agentlab/agentlabd.sock` | Explicitly select the trusted local socket, overriding a saved or environment endpoint. |
| `--json` | bool | `false` | Emit JSON output. Errors are emitted as a JSON object. See [JSON output for agents](agent-json-output.md) for the full contract. |
| `--timeout` | duration | `10m` | Request timeout, for example `30s` or `2m`. |
| `--allow-insecure-http` | bool | `false` | Permit plaintext HTTP to a non-loopback endpoint. Intended only inside a trusted tunnel such as Tailscale. |
| `--version` | bool | `false` | Print CLI version and exit. |
| `--help`, `-h` | bool | `false` | Print usage and exit. |

## Connection resolution

The CLI and compatibility dashboard use the same connection resolver.
Connection values follow this order:

1. Explicit connection flags.
1. `AGENTLAB_ENDPOINT` and `AGENTLAB_TOKEN`.
1. The context selected with `--context`, `AGENTLAB_CONTEXT`, or the active context.
1. Legacy `client.json`, when no context index exists.
1. The default Unix socket, when no remote connection is configured.

The CLI reads context metadata before command flags. It reads the saved credential
after those flags select the final target. An unavailable saved credential does
not block an explicit replacement token, a different endpoint, or an explicit socket.
Changing the endpoint clears the saved credential, server identity, SSH jump
defaults, and HTTP acknowledgement. A token supplied through the environment
remains explicit input for the final endpoint. Equivalent trailing slashes do
not select a different endpoint.

An explicit `--socket` selects local trust and clears remote credentials.
Supplying both `--socket` and a non-empty `--endpoint` returns a conflict.
An empty `--endpoint` does not select local trust. Remote failures never fall
back to a socket. Removing the active context requires a new explicit selection.

A running command retains its selected context when another process changes
the active context. Replacing that context's metadata before credential loading
blocks the command. The resolver also checks credential ownership, endpoint,
and server identity before it returns a saved token.

Help, version, skill commands, local token inspection, local repository commands,
and local recovery commands bypass saved connections and output preferences.
Pass `--json` explicitly for those commands when needed.

!!! warning "Endpoints need a scheme"
    A remote endpoint must include an explicit `http://` or `https://` scheme. A bare `host:port` is rejected so the bearer token is never sent in cleartext by accident. Plaintext HTTP to a non-loopback host is blocked unless you pass `--allow-insecure-http` or set `AGENTLAB_ALLOW_INSECURE_HTTP`.

## Environment variables

| Variable | Scope | Description |
| --- | --- | --- |
| `AGENTLAB_CONFIG_DIR` | client and dashboard | Absolute directory for client settings, contexts, and credential references. Defaults to the platform configuration directory plus `agentlab`. |
| `AGENTLAB_CREDENTIAL_STORE` | client | Storage for new credentials: `auto`, `os`, or `file`. Existing references retain their store. |
| `AGENTLAB_CONTEXT` | client and dashboard | Selects a named context below `--context`. |
| `AGENTLAB_ENDPOINT` | client | Overrides the saved context or legacy endpoint; below flags. |
| `AGENTLAB_TOKEN` | client | Overrides the saved control-plane token. |
| `AGENTLAB_ALLOW_INSECURE_HTTP` | client | Permit plaintext HTTP to a non-loopback endpoint inside a trusted tunnel. |
| `AGENTLAB_SSH_IDENTITY` | client | SSH identity file used by `agentlab ssh` when `--identity` is not supplied. |
| `AGENTLAB_TAILSCALE_TAILNET` | client | Tailscale admin API tailnet for remote bootstrap and admin operations. |
| `AGENTLAB_TAILSCALE_API_KEY` | client | Tailscale admin API key. |
| `AGENTLAB_TAILSCALE_OAUTH_CLIENT_ID` | client | Tailscale OAuth client id for admin API access. |
| `AGENTLAB_TAILSCALE_OAUTH_CLIENT_SECRET` | client | Tailscale OAuth client secret. |
| `AGENTLAB_TAILSCALE_OAUTH_SCOPES` | client | Comma-separated Tailscale OAuth scopes. |

!!! note "Env vars do not configure the daemon"
    `AGENTLAB_*` environment variables drive CLI and client connection behavior only. They do **not** override fields in the daemon configuration file (`/etc/agentlab/config.yaml`). To change daemon behavior, edit the config file and restart `agentlabd`. See [configuration.md](configuration.md).

## Client configuration files

Client files live in the operating system configuration directory, under `agentlab/`
(`$XDG_CONFIG_HOME/agentlab` on Linux), unless `AGENTLAB_CONFIG_DIR` selects an
absolute directory. AgentLab writes private files with mode `0600`. The CLI
forces the legacy `client.json` to `0600` on load if its permissions change.

| File | Purpose |
| --- | --- |
| `client.json` | Legacy remote settings and credentials, retained for migration and recovery. New `connect` calls save a context. |
| `contexts.json` | Public connection metadata, selected context, credential references, and pending credential retirement. |
| `credentials/<reference>.json` | Protected-file fallback for a credential record. Native references do not create this file. |
| `credential-transactions/<reference>.json` | Private intent and ownership metadata for interrupted credential writes. Contains no token. |
| `defaults.json` | Persistent CLI preferences managed by `agentlab defaults`. |

Credential selection and migration are described in
[choose credential storage](../how-to/connect-remote-daemon-over-tailnet.md#choose-credential-storage).

### defaults.json keys

| Key | Values | Description |
| --- | --- | --- |
| `default-profile` | profile name | Default profile used by `sandbox new`. |
| `default-image` | image | Default container image used by `sandbox new`. |
| `default-backend` | `proxmox`, `docker`, `libvirt` | Default backend. |
| `output-format` | `text`, `json` | Default output format for commands that load preferences. Local recovery commands require an explicit `--json`. |
| `default-timeout` | duration | Default request timeout, for example `30s`. |
| `default-socket` | path | Default daemon socket path. |

Manage these with `agentlab defaults <write|read|list|delete>`.

## JSON error envelope

With `--json` set, errors are emitted as a single JSON object:

```json
{"error":"message","code":"...","message":"...","details":"..."}
```

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success, or help displayed. |
| `1` | Command or request failed. |
| `2` | Invalid arguments or usage. |

`--version` or no arguments prints version or help and exits `0`. `-h` / `--help` prints usage and exits `0`.

## Shell completion

Generate a completion script for `bash`, `zsh`, or `fish`:

```bash
agentlab completion bash
agentlab completion zsh
agentlab completion fish
```

## Related

- [CLI reference](cli.md) for the full command and per-command flag list.
- [Configuration reference](configuration.md) for daemon-side configuration.
- [Listeners and ports](listeners-and-ports.md) for the socket and TCP addresses the CLI connects to.

Template commands use the saved endpoint/token and global connection flags.
`template create` submits immediately; `--wait` follows state with a separate
`--wait-timeout` (default 45m). `template build wait ID --timeout 45m` bounds
client waiting; the daemon enforces `--timeout-seconds` independently. Ctrl-C
stops waiting/following and prints a reconnect command. Success exits 0;
failed/cancelled builds or wait timeouts exit 1; invalid arguments exit 2.
JSON create/show/list/wait output is one document. JSON log output is NDJSON,
one record per sequence, UTC timestamp, stage, and message.
