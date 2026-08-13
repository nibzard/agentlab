# agentlabd flags

Reference for the command-line flags accepted by the `agentlabd` host daemon. `agentlabd` parses flags with the Go standard library `flag` package, so both `-flag` and `--flag` are accepted. After parsing it calls `daemon.Run(ctx, cfg)`.

## Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `-version` | bool | `false` | Print build information (`internal/buildinfo.String()`) and exit without starting the daemon. |
| `-config` | path | `""` | Path to the YAML configuration file. When empty, `agentlabd` reads `/etc/agentlab/config.yaml`; that file must exist and be valid. |
| `-offline` | bool | `false` | Force `cfg.Offline = true`, overriding the config file. Blocks all external network calls. |

## Examples

```bash
agentlabd -config /etc/agentlab/config.yaml
agentlabd -version
agentlabd -offline
agentlabd -config /tmp/agentlab.yaml -offline
```

## Precedence

1. `-offline` forces offline mode regardless of the config file's `offline` field.
1. `-config` selects the configuration file. An empty value reads `/etc/agentlab/config.yaml`, which must exist and be valid. `DefaultConfig()` is the base layer the YAML overrides; a missing file is fatal.
1. The configuration file supplies every other value. See [configuration.md](configuration.md).

Validation runs after flags and config are merged. A fatal config or run error exits with status `1`.

## Offline mode

`-offline` blocks outbound HTTP to public addresses and allows only loopback, link-local, RFC-1918, and `fc00::/7` destinations. It disables the Tailscale exposure publisher and rejects `proxy_tls_mode: letsencrypt` (ACME needs Internet). Pre-pull Docker images and VM templates before running offline.

## Startup and shutdown

`agentlabd` loads and validates the configuration, applies pending SQLite migrations, and starts the Unix control socket plus the bootstrap, artifact, and (when configured) metrics and TCP control listeners. It runs until it receives `SIGINT` or `SIGTERM`, then drains background work through the task tracker before exiting.

## Related

- [Configuration reference](configuration.md) for every config key and default.
- [Listeners and ports](listeners-and-ports.md) for the addresses the daemon binds.
- [Prometheus metrics](metrics.md) for the metrics endpoint.
