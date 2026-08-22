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

1. Run the dashboard on loopback with an inbound browser token. Every bind
   requires a token, loopback included, because the dashboard forwards every
   `/api/*` request to the trusted daemon socket.

    ```bash
    agentlab-dashboard --listen 127.0.0.1:8080 --browser-token <inbound-token>
    ```

    If the daemon's local socket is protected by a bearer token, pass it with
    `--token`. That token authenticates the outbound dashboard-to-daemon hop
    only.

    ```bash
    agentlab-dashboard --listen 127.0.0.1:8080 \
        --browser-token <inbound-token> --token <daemon-bearer-token>
    ```

    Open <http://127.0.0.1:8080> in a browser. The UI shows five views:
    Sandboxes, Jobs, Workspaces, Exposures, Events.

2. Alternatively, start without `--browser-token`. The server then generates a
   random token, logs it, and expects you to enter it in the browser.

    ```bash
    agentlab-dashboard --listen 127.0.0.1:8080
    ```

    ```text
    dashboard: listening on 127.0.0.1:8080 (socket=/run/agentlab/agentlabd.sock)
    dashboard: no --browser-token configured; generated a session token: <token>
    dashboard: enter this token in the browser prompt to use the dashboard (it gates every /api/* request; restart generates a new one)
    ```

    The first `/api/*` request from the browser prompts for the token. The
    token is valid until the process restarts; a restart mints a new one.

3. To expose the dashboard on a non-loopback interface, pass an explicit
   `--browser-token` so it is stable across restarts.

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

4. Override the socket path without a flag, if needed, through the environment.

    ```bash
    AGENTLABD_SOCKET=/run/agentlab/agentlabd.sock agentlab-dashboard --listen 127.0.0.1:8080
    ```

## Verify

Confirm the binary starts and proxies the daemon.

```bash
agentlab-dashboard --version
curl -s -H "X-Dashboard-Token: <inbound-token>" http://127.0.0.1:8080/api/v1/status
```

A successful `status` response means the dashboard reached the daemon over the
Unix socket. Without the header, `/api/*` returns 401. The Sandboxes view offers
Stop All, Prune Stopped, and New Sandbox actions; forwarded request bodies are
capped at 2 MiB.

Every response also carries a `Content-Security-Policy` that allows only
same-origin script, style, and fetch. The UI attaches all event handlers from
`/assets/app.js`, so the policy forbids inline handlers.
