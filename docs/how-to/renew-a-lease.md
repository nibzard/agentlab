# How to renew a sandbox lease

Extend the time-to-live (TTL) of a running keepalive sandbox so it does not time
out while you work.

## Prerequisites

- A sandbox with an active lease. Any `RUNNING` sandbox lease can be renewed;
  keepalive is not required to renew. A sandbox you do not renew is destroyed
  when its TTL expires.
- The sandbox is in the `RUNNING` state. The daemon rejects renewal from any
  other state with HTTP 409 and the message
  `cannot renew lease in <state> state. Valid states: RUNNING`.
- The `agentlab` CLI can reach the daemon. See
  [Connect to a remote daemon over the tailnet](connect-remote-daemon-over-tailnet.md)
  if you are not on the host.

## Steps

1. Confirm the sandbox is `RUNNING` and has a lease:

    ```bash
    agentlab sandbox show 1009
    ```

2. Renew the lease. The `--ttl` flag accepts minutes or a Go duration:

    ```bash
    agentlab sandbox lease renew --ttl 120 1009
    agentlab sandbox lease renew --ttl 2h 1009
    ```

   Place flags before the VMID, for example `--ttl 120 1009`.

3. To renew from automation, request JSON output and read `lease_expires_at`:

    ```bash
    agentlab sandbox lease renew --ttl 120 1009 --json
    ```

## Verify

- `agentlab sandbox show 1009` reports a later lease expiry.
- With `--json`, the response returns `vmid` and an RFC3339 `lease_expires_at`
  timestamp.

!!! note "Lease renewal is not idle-stop protection"
    Renewing the lease extends the TTL. It does not stop
    [idle auto-stop](../explanation/idle-stop-and-lease-model.md), which shuts
    down sandboxes with no SSH session and low CPU after the idle threshold.
    Keep an SSH session active, or tune `idle_stop_minutes_default` for the
    profile.

The daemon endpoint behind this command is
`POST /v1/sandboxes/{vmid}/lease/renew` with a `ttl_minutes` body. See the
[HTTP API reference](../reference/http-api.md) and
[state machine reference](../reference/state-machine.md) for details.
