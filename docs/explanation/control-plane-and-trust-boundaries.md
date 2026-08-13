# Control plane and trust boundaries

AgentLab exposes several HTTP listeners, and each one implies a different trust
level. Confusing them is the fastest way to misconfigure the system. This page
explains the socket, the remote TCP control plane, the guest listeners, and the
metrics endpoint, and how authentication is enforced at each boundary.

## The trusted Unix socket

The primary control plane is a Unix domain socket at
`/run/agentlab/agentlabd.sock`. It is the path the `agentlab` CLI uses by
default, and it is the path the dashboard proxies through. The socket bypasses
the network auth wrapper entirely. `ControlAPI.authorize` treats any caller that
reaches the socket as fully trusted, because reaching it already required host
access. In single-host deployments this is the only control listener that needs
to exist.

This is why the daemon refuses to start if `/etc/agentlab/config.yaml` is
world-readable or group-writable, and warns on `0640`. Prefer `0600`. The
config file is a trusted input, and the socket grants the power that the config
describes.

## The remote TCP control plane

The optional TCP control listener is the actual network trust boundary. It is
disabled by default. Enabling it requires three deliberate choices:

- Set `control_listen` to a host:port, typically loopback `127.0.0.1:8845` or a
  tailnet IP.
- Set `control_auth_token`, the bearer token the CLI sends as
  `Authorization: Bearer`.
- Set `control_allow_cidrs` whenever `control_listen` binds to a wildcard
  address. A wildcard bind without an allowlist is rejected at config
  validation.

The auth middleware accepts two token kinds: SSH-signed tokens verified against
`authorized_keys_path`, and the legacy pre-shared bearer. The TCP listener is
wired with `WrapNetwork`, not `Wrap`. Scoped SSH tokens are admitted on TCP and
constrained per-route by `ControlAPI.authorize`, which is wired into every
handler.

The CLI takes this boundary seriously on its end as well. An endpoint must
include an explicit `http://` or `https://` scheme; a bare host:port is rejected
so a bearer token is never sent in cleartext by accident. Plaintext HTTP to a
non-loopback host is rejected unless `--allow-insecure-http` is set, intended
only inside a trusted tunnel such as Tailscale. Setup is in
[Connect to a remote daemon over the tailnet](../how-to/connect-remote-daemon-over-tailnet.md),
rotation in [Rotate the control-plane token](../how-to/rotate-control-token.md).

## The guest listeners

Bootstrap and artifact traffic crosses a different boundary. The bootstrap
listener on `10.77.0.1:8844` and the artifact listener on `10.77.0.1:8846` are
bound to the agent subnet and are reachable only by guests. A caller outside the
subnet gets HTTP 403.

Authority on these listeners does not come from the control token. It comes from
single-use, short-lived tokens. The bootstrap token is bound to a VMID, hashed
at rest, consumed on first use, and has a default 10-minute TTL. The artifact
token is minted per job and governed by `artifact_token_ttl_minutes`. Both
endpoints are rate-limited per source IP. The full delivery path is explained in
[Secrets delivery model](secrets-delivery-model.md).

A fifth guest-facing surface is the metadata API at `169.254.169.254`, installed
by an iptables DNAT rule when `metadata_routing_enabled` is true. Setup failure
is logged but not fatal.

## The metrics endpoint

The metrics listener is the most constrained of all. It is off by default, and
when it is set, validation requires it to bind to loopback only, `localhost`,
`127.0.0.1`, or `[::1]`. A non-loopback value is rejected. The daemon exposes
`GET /metrics` and `GET /healthz` there. See
[Listeners and ports](../reference/listeners-and-ports.md) for the full table.

## Errors and the debug header

Server error responses follow a stable envelope with `error`, `code`, and
`message` fields. They deliberately do not leak details. A client that sends
`X-AgentLab-Debug: true` receives redacted details only, which is enough to
diagnose a problem without exposing secret material. The full envelope is in the
[Security reference](../reference/security.md).

## A note on the dashboard

The dashboard is a thin proxy in front of the trusted socket, and its own trust
boundary is the browser hop. On a non-loopback bind it requires an inbound
`--browser-token`, but over plain HTTP that token travels in cleartext. A
concrete recommended TLS termination or reverse-proxy recipe for the dashboard
is not yet documented; until it is, keep the dashboard on loopback and reach it
through an SSH tunnel or a TLS-terminating reverse proxy. See
[Run the dashboard](../how-to/run-the-dashboard.md).
