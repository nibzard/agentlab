# Listeners and ports

Reference for every network listener `agentlabd` opens, its default address, the routes it serves, and its trust level. Bind order during init is: Unix socket, bootstrap, artifact, control (optional), metrics (optional).

## Listener summary

| Listener | Config key | Default | Routes | Trust |
| --- | --- | --- | --- | --- |
| Local control socket | `socket_path` | `/run/agentlab/agentlabd.sock` | `/v1/*`, `/healthz` | Trusted full access. Bypasses network auth. |
| Remote control (TCP) | `control_listen` | `""` (disabled) | `/v1/*`, `/healthz` | Network boundary. Requires bearer token. |
| Bootstrap (guest) | `bootstrap_listen` | `10.77.0.1:8844` | `/v1/bootstrap/fetch`, `/v1/runner/report`, `/metadata/*`, `/proxy/`, `/healthz` | Agent subnet only. Rate-limited. |
| Artifact (guest) | `artifact_listen` | `10.77.0.1:8846` | `/upload`, `/healthz` | Agent subnet only. Rate-limited. |
| Metrics | `metrics_listen` | `""` (disabled) | `/metrics`, `/healthz` | Loopback only. |

The conventional ports are `8844` (bootstrap), `8845` (control TCP), `8846` (artifact), and `8847` (metrics).

## Local control socket

| Attribute | Value |
| --- | --- |
| Config | `socket_path` |
| Default | `/run/agentlab/agentlabd.sock` |
| Routes | `/v1/*`, `/healthz` |
| Auth | None. The Unix socket is the trusted path. |

The `agentlab` CLI dials this socket when `--endpoint` is not set.

## Remote control listener

| Attribute | Value |
| --- | --- |
| Config | `control_listen` |
| Default | `""` (disabled) |
| Conventional port | `8845` (`defaultControlPort`) |
| Routes | `/v1/*`, `/healthz` |
| Auth | `control_auth_token` bearer, plus optional `control_allow_cidrs`. |

Requirements:

- `control_auth_token` is mandatory whenever `control_listen` is set.
- A wildcard bind (`0.0.0.0` or `[::]`) is rejected unless `control_allow_cidrs` is set.
- The auth middleware (`auth.NewMiddleware`) accepts SSH-signed tokens from `authorized_keys_path` plus the legacy bearer token. Scoped SSH tokens are admitted on the TCP control listener and enforced per-route by the authorizer (`ControlAPI.authorize` in `internal/daemon/authz.go`).

For topology options, see [../how-to/connect-remote-daemon-over-tailnet.md](../how-to/connect-remote-daemon-over-tailnet.md).

## Bootstrap listener

| Attribute | Value |
| --- | --- |
| Config | `bootstrap_listen` |
| Default | `10.77.0.1:8844` |
| Routes | `POST /v1/bootstrap/fetch`, `POST /v1/runner/report`, `GET /metadata/`, `/metadata/identity`, `/metadata/metadata`, `/metadata/secrets/`, `ANY /proxy/`, `GET /healthz` |
| Auth | One-time bootstrap token plus VMID; agent subnet only. |

A wildcard bind requires `agent_subnet` and `controller_url` (with an `http(s)` scheme). Per-IP rate limits are `bootstrap_rate_limit_qps` (default `1`) and `bootstrap_rate_limit_burst` (default `3`).

## Artifact listener

| Attribute | Value |
| --- | --- |
| Config | `artifact_listen` |
| Default | `10.77.0.1:8846` |
| Routes | `POST /upload`, `GET /healthz` |
| Auth | Per-job artifact bearer token; agent subnet only. |

A wildcard bind requires `agent_subnet` and `artifact_upload_url` (with an `http(s)` scheme). Per-IP rate limits are `artifact_rate_limit_qps` (default `5`) and `artifact_rate_limit_burst` (default `10`).

## Metrics listener

| Attribute | Value |
| --- | --- |
| Config | `metrics_listen` |
| Default | `""` (disabled) |
| Conventional address | `127.0.0.1:8847` |
| Routes | `GET /metrics`, `GET /healthz` |
| Auth | None. Loopback only. |

Validation rejects any non-loopback host. See [metrics.md](metrics.md).

## HTTP server timeouts

The Unix, control, bootstrap, artifact, and metrics servers all set `ReadHeaderTimeout` to 5 seconds and `IdleTimeout` to 2 minutes.

## Health checks

`GET /healthz` is registered on every mux and returns `ok`. It is the recommended liveness probe for the daemon.

```bash
curl --unix-socket /run/agentlab/agentlabd.sock http://localhost/healthz
```

## Related listeners (separate binaries)

These are not opened by `agentlabd` but are part of the platform.

| Binary | Default listen | Notes |
| --- | --- | --- |
| `agentlab-dashboard` | `127.0.0.1:8080` | Optional web UI. Proxies the browser to the daemon over the Unix socket. A non-loopback bind requires `--browser-token`. |
| `agentlab-ssh-gateway` | `0.0.0.0:2222` | Optional SSH gateway. Built behind the `sshgateway` tag and excluded from releases. |

## Related

- [Configuration reference](configuration.md) for the keys that govern each bind.
- [HTTP API reference](http-api.md) for the routes served on each listener.
- [../explanation/control-plane-and-trust-boundaries.md](../explanation/control-plane-and-trust-boundaries.md) for the trust model behind these listeners.
