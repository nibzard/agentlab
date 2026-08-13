# Reconciliation and shutdown

The daemon holds a model of the world in a SQLite database, and Proxmox holds
the actual virtual machines. The two can drift. A VM can be destroyed outside
AgentLab, a host can crash, or a lease can expire while the daemon is down.
Reconciliation is how AgentLab brings the database back in line with reality.
Shutdown is the matching concern: how the daemon stops without losing
in-flight work.

## Why drift happens

AgentLab is not the only thing that can touch a Proxmox host. An operator can
destroy a VM at the `qm` command line. A host reboot can leave a sandbox
mid-provision. The daemon can restart while sandboxes keep running. In each
case the database no longer matches Proxmox, and the next operation that
trusts the database would be wrong.

## The reconciler loop

At startup, and then on a fixed interval, the sandbox manager runs
`ReconcileState`. The reconciler queries Proxmox for the real state of every
VM, compares it with the database records, and corrects three common forms of
drift:

- A VM destroyed outside AgentLab is marked `DESTROYED` in the database.
- A VM that is stopped while the database says `RUNNING` is marked `FAILED`.
- A VM that is running while the database says `REQUESTED` is advanced to
  `READY`.

The reconciler can also adopt restored VMs that were previously marked
`DESTROYED`, and it clears stale lease expiry metadata so a restarted sandbox
is not immediately timed out.

The reconciler shares the lease-GC interval, which defaults to 30 seconds.
You can run it on demand without waiting for the next tick:

```bash
agentlab sandbox reconcile
agentlab sandbox reconcile --apply
```

Without `--apply`, reconcile reports drift only. With `--apply`, it writes the
corrections.

## Orphan grace period

Adopting a VM that Proxmox has but the database does not is dangerous if the
VM is only briefly absent during a restart. AgentLab uses an orphan grace
period before treating a missing VM as gone. The default is twice the
provisioning timeout, floored at five minutes and capped at two hours. Within
that window, a VM that reappears is adopted rather than purged.

## Pruning orphans

Some sandboxes reach `TIMEOUT` while their VMs are still present in Proxmox.
Prune destroys those VMs and marks the records `DESTROYED`; it does not delete
the database row:

```bash
agentlab sandbox prune
```

Prune is the cleanup tool of last resort. Reconcile first; prune what
reconcile cannot fix.

## Graceful shutdown

The daemon runs background work on a `BackgroundRunner`: sandbox provisioning,
job execution, and workspace lease renewal. Each unit of work registers with a
task tracker so shutdown can wait for it.

On `SIGINT` or `SIGTERM`, the daemon stops accepting new background work by
setting a draining flag. The task tracker then waits for in-flight work to
finish, bounded by a short shutdown timeout. Once that window closes, or once
all work finishes, the daemon exits.

This ordering exists so a controlled restart does not strand a sandbox
half-provisioned or a job half-uploaded. It is also why you should prefer
`systemctl restart agentlabd` over a hard kill.

## What survives a restart

Running sandbox VMs keep running across a daemon restart because they live in
Proxmox, not in the daemon process. The reconciler picks them back up. What
does **not** work during a restart is creating new sandboxes: the control API
is unavailable while the daemon is down. Jobs that were `QUEUED` wait; jobs
that were `RUNNING` keep running inside their VMs and report back when the
daemon returns.

## Where to go next

- Diagnose a sandbox that drift got wrong: [Debug a stuck sandbox](../how-to/debug-a-stuck-sandbox.md).
- States and allowed transitions: [State machine reference](../reference/state-machine.md).
- Component overview: [Architecture](architecture.md).
