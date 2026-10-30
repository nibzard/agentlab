# How to run the dashboard

The normal `agentlab` binary includes the dashboard assets. The dashboard uses
the same connection as the CLI. It does not talk to Proxmox directly.

## Start the dashboard

1. Connect to a running AgentLab server with `agentlab connect`. See the
   [connection guide](connect-remote-daemon-over-tailnet.md) for connection settings.
2. Run the dashboard:

   ```bash
   agentlab dashboard
   ```

3. Check the selected server printed in the terminal. The command checks status
   and compatible identity enforcement before it starts the local service.
4. Open <http://127.0.0.1:8080>. Enter the enrollment code from the terminal
   within five minutes. Each generated code permits one enrollment.
5. Press Control+C to stop the local service. Server jobs continue.

The browser receives an HttpOnly session cookie. The daemon credential stays
in the local process. Restart the dashboard to create a new enrollment code.
Restarting restores access to the same server resources.

Use a global connection option to select another server:

```bash
agentlab --endpoint https://server.example dashboard
```

Use `agentlab dashboard --listen 127.0.0.1:8081` to select another local port.
Remote calls use the authenticated API and retain the token's permissions.
A saved endpoint selects the remote daemon. An explicit `--socket` selects
local trust. A failed remote connection does not fall back to that socket.

## Create your first sandbox

1. Check the endpoint, server identity, and permissions in **Selected server**.
2. Select **New sandbox**. Choose a discovered profile and lease behavior.
3. Select **Check plan**. Correct reported errors before you continue.
4. Review the server, profile, lease, and requested resources. Capacity checks
   use the configured resource pool. An unavailable check is shown explicitly.
5. Select **Create on this server**. The dashboard checks the identity and plan
   again. Changed settings require another review.
6. Follow the sandbox state in **Sandboxes**. If creation loses its response,
   inspect the list before another attempt. The dashboard does not retry creation.

The profile controls the sandbox type and image. The guided form does not invent
a default profile when discovery fails. Pool validation does not reserve capacity.
Creation checks admission again. It can fail if another task consumes capacity.

The dialog supports Tab, Shift+Tab, and Escape. Escape returns focus to the
invoking control, or **Refresh** if that control is no longer present.
The creation dialog stays open while creation is pending.

## Inspect job results

Select **Jobs**, then **View** for a running or terminal job. Open details poll
for progress. Polling preserves a focused cancellation or artifact control.
The result view shows reported test commands, exit codes, execution evidence,
artifacts, cleanup, retained resources, and reported lease expiry.

Job completion does not establish that tests passed. Missing test evidence stays
explicit. For a verified repository result, select **View execution logs**.
Choose the runner log, a test log, or `report.json`. Use **Next chunk** and
**Previous chunk** to inspect long text. **Reload chunk** retries the current page.
The text area supports keyboard focus and narrow layouts.

Inline inspection requires `execution_inspection` and `job.artifacts` with the
job's sandbox scope. The server verifies the full archive and saved evidence
before returning text. Each page stays bound to the original archive digest.
Changed archives block viewing. Non-UTF-8 entries require a download.
Guest text is displayed as plain text and is never executed.

Download the execution archive when inline inspection is unavailable or when you
need all files. Downloads use the same scoped server credential. The browser
does not receive that credential. These views report execution evidence;
they do not establish test coverage or make an unverified result verified.

Use **Cancel job** when permitted. Its confirmation names the job and server.
Inspect work termination, cleanup, and retained resources after cancellation.
Stopping a job does not establish that every resource is removed.

Bulk stop and prune first list their target sandbox IDs and names. They operate
only on those IDs. A failure stops the sequence. Inspect remaining resources
before another attempt.

## Renew a saved credential

A dashboard launched from a verified named context follows that context's saved
credential. The connection panel identifies its credential source. Publishing a
compatible replacement keeps the same browser session. Changing the active context
does not redirect the running dashboard.

A replacement must authenticate with the same issuer, subject, command permissions,
and scope. SSH replacements must expire within 24 hours. The saved endpoint,
server identity, transport policy, and jump settings must still match. The server
must report the issuer and support `request_server_identity`.

The dashboard checks replacement authority before forwarding requests. Bound
requests include a server identity header that the daemon checks before dispatch.
State-changing requests also recheck the current server and authority. A missing
credential store, changed binding, rejected credential, or changed authority blocks
forwarding. The dashboard does not use an older credential or a local socket.

During a pending renewal, the context can still point at its revoked parent.
Finish the same renewal request, then select **Refresh**. The dashboard does not
mint credentials or publish the renewal for you.

Explicit token options, environment credentials, legacy settings, and contexts
without a saved server identity keep their startup credential. Remote dashboards
still verify and pin the server identity at startup. Every bound request checks
identity enforcement before forwarding. Restart these sessions after replacement. A deliberate authority or server change also requires
a new dashboard session. An older server that cannot prove the required identity
cannot enable saved credential refresh.

## Run the browser checks

Build a client binary, then run the fixture with `agent-browser` installed:

```bash
go build -o ./bin/agentlab ./cmd/agentlab
python3 scripts/tests/dashboard_browser_smoke.py --client ./bin/agentlab
python3 scripts/tests/dashboard_browser_smoke.py --client ./bin/agentlab --saved-credentials
```

The fixture starts a local API substitute and the supplied dashboard binary.
It checks profile discovery, capacity denial, review, creation, keyboard access,
a narrow viewport, job results, cancellation, revocation, and changed identity.
It contacts no Proxmox host. These checks do not replace live acceptance trials.

## Use the compatibility binary

The separate `agentlab-dashboard` binary remains available for source builds.
It reads the existing saved connection and the `AGENTLAB_ENDPOINT` and
`AGENTLAB_TOKEN` environment variables. Explicit flags take precedence.

```bash
agentlab-dashboard --endpoint https://server.example
```

For local daemon access, omit the endpoint and select a socket:

```bash
agentlab-dashboard --socket /run/agentlab/agentlabd.sock
```

The explicit socket overrides a saved or environment endpoint. Do not combine
`--socket` with an explicit `--endpoint`. The local socket is a trusted access
path. Remote scoped credentials must use the remote endpoint.

Use `--context NAME` to select a named connection. Both dashboard commands read
saved credentials after explicit connection flags. Help and version work without
saved credentials.

An explicit `--browser-token` remains available for compatibility. The browser
exchanges it for a session cookie. Existing API clients can also send that token
in an Authorization or X-Dashboard-Token header. Generated enrollment codes
cannot authenticate API requests directly.

Keep the default loopback listener for local use. A non-loopback deployment
requires HTTPS or a trusted encrypted tunnel. State-changing browser requests
require a matching origin and the X-Requested-With header.

## Troubleshoot a connection

Run `agentlab status` with the same connection options. Check the endpoint,
server availability, certificate, and credential. The dashboard preserves
remote authorization failures. It does not switch to another transport.

The connection panel shows one corrective action for connection or authentication
failure. Server credential failure does not request another browser enrollment
code. Select **Refresh** after restoring access. A changed server identity blocks
requests that carry its saved binding.

For proxy routes, see [Listeners and ports](../reference/listeners-and-ports.md).
For the trust model, see
[Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).
