# Enable guest recovery after restart and reboot

Recovery requires the updated daemon and a managed template whose verified
manifest reports `agent_runner: v2`. Existing guests cannot manufacture a
recovery identity after their original token has been consumed. Reprovision
those guests from an updated template; preserve any needed work in a workspace
first. Native SSH-only provisioning does not provide this bootstrap protocol.

## Enable the controller

Back up the daemon configuration and SQLite database before upgrading. Migration
23 is additive; do not edit or remove earlier migrations. Add a dedicated address
to `/etc/agentlab/config.yaml`:

```yaml
guest_recovery_listen: 10.77.0.1:8847
```

Allow TCP 8847 from the guest bridge to the host. The repository's network recipe
includes this port; existing installations must update their own allowlist.
Keep this listener restricted to `agent_subnet`, separate from the control API.
Restart `agentlabd`, then build and activate a new managed template through
`agentlab template create` and `agentlab template activate`. Use a dedicated
profile for verification before changing an existing production profile.

The daemon creates a private `guest-recovery/` directory under `data_dir` with
`server.crt`, `server.key`, and `delivery.key`. Back up all three securely with
the database. The certificate is self-signed, valid for five years, and includes
the configured host. New cloud-init descriptors pin that certificate. Do not
replace these files during routine upgrades or restart: replacing the TLS
identity requires a deliberate trust migration/reprovision, and replacing the
delivery key invalidates outstanding encrypted retries. There is no HTTP fallback
when TLS verification fails. Use a concrete host, not a wildcard address.

Legacy templates keep their HTTP enrollment format and report
`guest_recovery_unsupported_reprovision_required`. They do not gain restart
recovery merely because the daemon was upgraded. Updated templates require the
TLS listener; they fail with an explicit unsupported-bootstrap diagnostic when
used without it. Already enrolled legacy guests remain unchanged.

## Verify a new sandbox

Check `agentlab sandbox show VMID` for guest readiness, Tailscale state, and the
separate Recovery diagnostic. In the guest, inspect `agent-runner.service` and
`agent-bootstrap-refresh.timer` with trusted operator access through Proxmox
QGA. The recovery-enabled agent user no longer has unrestricted sudo; only root can access the
persistent recovery identity or enroll Tailscale.

Restart the runner, reboot the guest, and stop/start it through AgentLab. Each
operation should retain `/var/lib/agentlab/identity/private.pem`, recreate tmpfs
credentials, report authenticated readiness, and preserve the same Tailscale
node. The template itself must contain neither the identity directory nor a
bootstrap descriptor or enrollment state.

Runtime data lives under `/run/agentlab-bootstrap/secrets`, with a public,
root-owned descriptor and controller CA alongside it. The private identity has
mode `0600` in a root-owned `0700` directory. Cleanup still removes runtime
secrets when the runner stops. A root timer renews endpoint credentials every
20 minutes while the runner is active; credentials expire after one hour.

## Diagnose failures

`journalctl -u agent-runner -u agent-bootstrap-refresh` exposes fixed diagnostic
codes without printing complete responses or signing material. Controller-side
`sandbox.recovery` events project into the CLI's Recovery field, independently
of guest health and Tailscale status.

| Diagnostic | Action |
| --- | --- |
| `guest_recovery_unsupported_reprovision_required` | Enable TLS recovery and use a verified v2 template; reprovision legacy guests. |
| `guest_identity_descriptor_mismatch` | Check for copied disks/identities or a replaced descriptor; reprovision instead of adopting another incarnation. |
| `guest_identity_invalid_or_revoked` | Verify the VM is still the current sandbox incarnation; destroyed/reused identities cannot recover. |
| `guest_challenge_expired_or_replayed` | Acquire a fresh challenge and sign a new proof; never replay the previous proof. |
| `guest_controller_unavailable` / `guest_controller_unavailable_or_tls_failed` | Check controller reachability, pinned certificate, subnet/rate limits, and daemon recovery events. The helper makes six bounded attempts. |
| `guest_job_interrupted` | The previous job is `FAILED` with `retry_required: true`; submit an explicit new job after reviewing partial work. |
| `tailscale_explicit_reenrollment_required` | Recovery does not reenroll logged-out/revoked devices. Arrange a separate trusted enrollment or reprovision. |

Completed jobs remain terminal. Recovery never gives an interrupted job another
execution claim. A failed/ambiguous execution-start request also fails closed;
submit a new job rather than retrying side effects automatically.

## Rollback

Stop creating new recovery-enabled guests before rolling back. Keep the database
and all recovery key files together. Older daemon binaries reject a database
with unknown migration 23, so restore the matching pre-upgrade database and
configuration as well as the binary during a maintenance window. Restoring an
old database loses changes made since that backup; reconcile owned resources
before resuming. Guests created with pinned recovery descriptors require the
recovery-capable daemon or reprovisioning. Do not delete identity directories,
reset consumed tokens, or disable cleanup as a rollback shortcut.
