# How to debug a stuck sandbox

Diagnose a sandbox that stalls in `PROVISIONING`, `BOOTING`, or `TIMEOUT`, or
that no longer matches Proxmox.

## Prerequisites

- SSH or console access to the Proxmox host, with permission to run `qm`.
- The `agentlab` CLI talking to the daemon on the host socket.

## Steps

Work top to bottom. Stop when you find the cause.

1. Identify the sandbox and its state:

    ```bash
    agentlab sandbox list
    agentlab sandbox show <vmid>
    ```

2. Read recent sandbox logs and, for a job, the job events:

    ```bash
    agentlab logs <vmid> --tail 200
    agentlab job show <job_id> --events-tail 200
    ```

3. Check the daemon and its logs:

    ```bash
    systemctl status agentlabd.service
    journalctl -u agentlabd.service -n 200 --no-pager
    tail -n 200 /var/log/agentlab/agentlabd.log
    ```

4. Check the live Proxmox VM and guest agent:

    ```bash
    qm status <vmid>
    qm config <vmid>
    qm agent <vmid> ping
    ```

5. Confirm the cloud-init snippet exists and is referenced:

    ```bash
    ls -l /var/lib/vz/snippets
    qm config <vmid> | grep -E 'cicustom|cloudinit'
    ```

6. Validate network policy from a tailnet device:

    ```bash
    scripts/net/smoke_test.sh --ip <sandbox_ip> --ssh-key <path>
    ```

## Common causes and recovery

| Symptom | Likely cause | Recovery |
| --- | --- | --- |
| Stuck in `BOOTING` | QEMU guest agent missing or stopped in the template | Rebuild the template with the guest agent, or wait for DHCP lease fallback |
| `template VM <vmid> does not have qemu-guest-agent enabled` | Invalid `template_vmid` after a template rebuild | Point the profile at the new VMID |
| `TIMEOUT` with no VM in Proxmox | Orphaned database record | `agentlab sandbox prune` |
| Bad root disk | Corruption or bad state | `agentlab sandbox revert <vmid> --force --restart` |
| Unrecoverable | None of the above worked | `agentlab sandbox destroy --force <vmid>` and recreate |
| Missing secrets at bootstrap | Missing bundle or invalid `secrets_bundle` name | See [Manage secrets bundles](manage-secrets-bundles.md) |

Collect a redacted diagnostic bundle before you destroy anything:

```bash
agentlab sandbox doctor <vmid> --out /tmp/
```

## Verify

- `agentlab sandbox show <vmid>` reports `RUNNING` (or `DESTROYED` after a
  cleanup).
- A new sandbox boots and reports `RUNNING` through the full lifecycle.

For the state transitions behind these symptoms, see the
[state machine reference](../reference/state-machine.md). For deeper diagnostic
collection, see [Collect doctor diagnostics](collect-doctor-diagnostics.md).
