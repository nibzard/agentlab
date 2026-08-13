# Idle stop and lease model

Sandboxes cost real CPU and memory while they run. AgentLab reclaims idle
sandboxes with two cooperating mechanisms: a time-to-live (TTL) lease that
bounds the maximum lifetime of a sandbox, and an idle detector that stops
sandboxes no one is using. Both feed the same state machine and both end in
the same place, a transition to `STOPPED` or `TIMEOUT`.

## Leases and TTLs

Every sandbox gets a TTL when it is created. The default TTL comes from the
profile. When the lease expires without renewal, the daemon stops and then
destroys the sandbox.

A **keepalive** sandbox trades the fixed TTL for a renewable lease. The
sandbox records a lease expiry time instead of a hard deadline. As long as
someone renews the lease before it expires, the sandbox keeps running. For a
keepalive job, the orchestrator renews that lease automatically while the job
runs, so a long task does not time out mid-run. This is the mode to use for
interactive development sessions where you do not know in advance how long you
will need the machine.

Lease renewal is only allowed while the sandbox is `RUNNING`. Renewal of a
`READY` sandbox is rejected with a 409, and you cannot renew a stopped,
suspended, or terminal sandbox. Renewal extends the lease by the TTL you give
it.

```bash
agentlab sandbox lease renew --ttl 2h 1001
```

## Idle detection

Idle stop is the second reclaim path. It is enabled by default and watches
each `RUNNING` sandbox for two signals:

1. No active SSH session into the sandbox. The daemon inspects host conntrack
   for established flows to `sandbox_ip:22`.
2. CPU usage below a low threshold, read from the Proxmox guest status.

When both conditions hold for longer than the idle window, the daemon stops
the sandbox. The defaults are:

```yaml
idle_stop_enabled: true
idle_stop_interval: 1m
idle_stop_minutes_default: 30
idle_stop_cpu_threshold: 0.05
```

The daemon checks every `idle_stop_interval`. After `idle_stop_minutes_default`
minutes of inactivity, with CPU under 5 percent, the sandbox transitions to
`STOPPED`.

!!! note "Conntrack is required"
    Idle stop relies on the host conntrack table to see SSH sessions. If
    conntrack is missing or the daemon lacks the privileges to read it,
    detection fails and the idle loop **skips** that sandbox rather than
    stopping a sandbox with a live session. A sandbox with an active SSH
    session is never treated as idle.

## Per-profile overrides

Different workloads have different ideas of idle. A long-running build looks
idle on CPU but is busy. A paused review session should not be killed at 30
minutes. Profiles can override the global idle window:

```yaml
behavior:
  idle_stop_minutes_default: 0
```

Set the override to `0` to disable idle stop for that profile entirely. This
is how you keep a sandbox alive for a long, low-CPU task.

## The two paths converge

Both reclaim paths land in the state machine, but they end differently. An
idle sandbox goes to `STOPPED`, which is restartable; start it again if you
still need it. A lease that expires without renewal is collected by the lease
GC in one pass: the sandbox is marked `TIMEOUT`, stopped, and destroyed, so it
is gone and must be recreated.

The distinction matters for operators. `STOPPED` from idle means the machine
is fine, just paused to save resources. A lease that ran out means the sandbox
was destroyed; a keepalive sandbox that reaches this point was not renewed in
time.

## Where to go next

- Extend a running lease: [Renew a sandbox lease](../how-to/renew-a-lease.md).
- Tune idle stop per profile: [Author a profile](../how-to/author-a-profile.md).
- Default values and validation rules: [Configuration reference](../reference/configuration.md).
- The full transition graph: [State machine reference](../reference/state-machine.md).
