# How to run the dashboard

Serve and secure the optional `agentlab-dashboard` web UI. The dashboard is a
standalone binary that proxies a browser to the `agentlabd` daemon over its Unix
socket. It does not talk to Proxmox directly.

For the dashboard's API proxy routes, see
[Listeners and ports](../reference/listeners-and-ports.md). For the trust model,
see [Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).

## Prerequisites

- A running `agentlabd` with the control socket at
  `/run/agentlab/agentlabd.sock` (the default).
- The `agentlab-dashboard` binary built from `cmd/agentlab-dashboard`.

## Steps

1. Run the dashboard on loopback. The default bind is `127.0.0.1:8080`, which
   trusts only the local browser.

    ```bash
    agentlab-dashboard --listen 127.0.0.1:8080 --socket /run/agentlab/agentlabd.sock
    ```

    If the daemon's local socket is protected by a bearer token, pass it with
    `--token`. That token authenticates the outbound dashboard-to-daemon hop
    only.

    ```bash
    agentlab-dashboard --listen 127.0.0.1:8080 --token <daemon-bearer-token>
    ```

    Open <http://127.0.0.1:8080> in a browser. The UI shows five views:
    Sandboxes, Jobs, Workspaces, Exposures, Events.

2. To expose the dashboard on a non-loopback interface, you must set an inbound
   browser token. The server refuses to start otherwise.

    ```bash
    agentlab-dashboard --listen 0.0.0.0:8080 \
        --browser-token <inbound-token> --token <daemon-bearer-token>
    ```

    Every `/api/*` request must then carry the browser token through an
    `Authorization: Bearer` header, an `X-Dashboard-Token` header, or a
    `dashboard_token` cookie. State-changing requests must also send
    `X-Requested-With: XMLHttpRequest` and a same-origin `Origin`.

    !!! warning "Cleartext browser token over HTTP"
        The browser token travels in cleartext over plain HTTP. For non-loopback
        access, put the dashboard behind TLS or a trusted encrypted tunnel such
        as a Tailscale sidecar. A concrete reverse-proxy TLS recipe is not yet
        documented.

3. Override the socket path without a flag, if needed, through the environment.

    ```bash
    AGENTLABD_SOCKET=/run/agentlab/agentlabd.sock agentlab-dashboard --listen 127.0.0.1:8080
    ```

## Verify

Confirm the binary starts and proxies the daemon.

```bash
agentlab-dashboard --version
curl -s http://127.0.0.1:8080/api/v1/status
```

A successful `status` response means the dashboard reached the daemon over the
Unix socket. The Sandboxes view offers Stop All, Prune Stopped, and New Sandbox
actions; forwarded request bodies are capped at 2 MiB.
