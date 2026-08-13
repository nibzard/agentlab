# Global flags, environment, and exit codes

Reference for the `agentlab` CLI global flags, environment variables, client configuration files, JSON error envelope, and exit codes. For the full per-command flag list, see [cli.md](cli.md).

## Global flags

These flags may appear before any subcommand that talks to the daemon.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--endpoint` | URL | none | Control-plane HTTP(S) endpoint. Must include an `http://` or `https://` scheme. |
| `--token` | string | none | Control-plane bearer token, sent as `Authorization: Bearer`. |
| `--socket` | path | `/run/agentlab/agentlabd.sock` | Path to the `agentlabd` Unix socket. Used when `--endpoint` is not set. |
| `--json` | bool | `false` | Emit JSON output. Errors are emitted as a JSON object. |
| `--timeout` | duration | `10m` | Request timeout, for example `30s` or `2m`. |
| `--allow-insecure-http` | bool | `false` | Permit plaintext HTTP to a non-loopback endpoint. Intended only inside a trusted tunnel such as Tailscale. |
| `--version` | bool | `false` | Print CLI version and exit. |
| `--help`, `-h` | bool | `false` | Print usage and exit. |

## Connection resolution

When the CLI needs to reach the daemon it resolves the endpoint and token in this precedence order, highest first:

1. `--endpoint` / `--token` flags
1. `AGENTLAB_ENDPOINT` / `AGENTLAB_TOKEN` environment variables
1. Saved `client.json`
1. The Unix socket at `--socket` (default `/run/agentlab/agentlabd.sock`)

If `--endpoint` is set the CLI uses HTTP(S) to the remote daemon; otherwise it dials the Unix socket.

!!! warning "Endpoints need a scheme"
    A remote endpoint must include an explicit `http://` or `https://` scheme. A bare `host:port` is rejected so the bearer token is never sent in cleartext by accident. Plaintext HTTP to a non-loopback host is blocked unless you pass `--allow-insecure-http` or set `AGENTLAB_ALLOW_INSECURE_HTTP`.

## Environment variables

| Variable | Scope | Description |
| --- | --- | --- |
| `AGENTLAB_ENDPOINT` | client | Overrides the saved endpoint; below flags, above `client.json`. |
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

Both files live in the OS user configuration directory, in an `agentlab/` subdirectory (`$XDG_CONFIG_HOME` on Linux), and are written with mode `0600`. The CLI forces `client.json` to `0600` on load if the permissions drift.

| File | Purpose |
| --- | --- |
| `client.json` | Saved remote connection: `endpoint`, `token`, `jump_host`, `jump_user`, `allow_insecure_http`, `tailscale_admin`. Written by `agentlab connect`. |
| `defaults.json` | Persistent CLI preferences managed by `agentlab defaults`. |

### defaults.json keys

| Key | Values | Description |
| --- | --- | --- |
| `default-profile` | profile name | Default profile used by `sandbox new`. |
| `default-image` | image | Default container image used by `sandbox new`. |
| `default-backend` | `proxmox`, `docker`, `libvirt` | Default backend. |
| `output-format` | `text`, `json` | Default output format. |
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
