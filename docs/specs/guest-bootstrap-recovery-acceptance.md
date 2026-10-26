# Guest bootstrap recovery acceptance

Verified on `pve2` (Proxmox 8.4.21), 2026-10-16, with the implementation in the
working tree based on `1f0629e`. No release tag or production profile activation
was performed.

## Retained result

- Final managed build: `tb_79994d5a1db1a7bc`.
- Template: **9022**, `guest-recovery-final`, `boot_verified: true`,
  `enrolled: false`, runner capability `v2`.
- Trusted recipe asset digest:
  `d70f000250142b3f7494d8d990f7d2fdc936996031569de557ff7421db011df1`.
  This matches the repository assets used by the deployed daemon.
- Dedicated profile: `guest-recovery-acceptance`, activated from 9018 to 9022
  using the expected-template compare-and-swap operation.
- TLS recovery listener: `10.77.0.1:8847`; migration 23 applied. Existing HTTP
  listeners remain available for legacy guests.
- Deployed development daemon SHA-256:
  `6542fcc71f106c0720c2e3f6f3518ee4271692e3810045dba1ba2c4bf2f1bc76`.

The template intentionally excludes optional coding CLIs (`--agent-tools=false`).
Job execution was exercised with an owned Git fixture containing `.agentlab/run.sh`.
Existing production profiles keep their prior templates.

## Live observations

| Scenario | Evidence and outcome |
| --- | --- |
| Fresh final-template clones | 1125 and 1127 enrolled independently, both connected to Tailscale and received HTTP 200 from authenticated TLS `/metadata/identity`. |
| Distinct identities | Public-key SHA-256 fingerprints begin `55fae19f5584b331` (1125) and `ec6298ee7f623828` (1127); Tailscale device IDs were `nFCvFXZq3r11CNTRL` and `nwqdfqhYrh11CNTRL`. |
| Runner restart | 1121 and final clone 1125 reacquired runtime credentials; identity and device IDs remained stable. |
| Reboot | Fresh final-template clone 1125 restarted its runner automatically, retained its key/device, and restored authenticated metadata access. |
| Stop/start | 1121 stopped and restarted through the AgentLab API with the same public key/device and HTTP 200 metadata access. Its service had the ordering fix subsequently packaged in 9022. |
| Controller outage/restart | Controller stopped during 1121 runner recovery and restarted by an eight-second timer. Recovery completed within the helper retry budget with the same key/device and authenticated metadata access. Subsequent daemon upgrades also preserved both final clones' access. |
| Lost recovery response | An owned temporary curl wrapper in 1125 discarded one completed recovery response. The helper issued two proofs, retrieved the encrypted retry, and restored working authenticated credentials. The wrapper was removed immediately. |
| Credential renewal | Explicit `agent-bootstrap-refresh` completed on 1125 without runner restart or a different Tailscale device. |
| Runtime cleanup | With the runner stopped, both runtime bootstrap JSON and endpoint-secret files were absent; the root-only private identity remained. Starting the runner restored runtime access. |
| Completed job restart | `job_f988b29c41ede9c1`, VM 1123, remained `COMPLETED`; a durable counter under the agent home stayed at exactly **1** after restart. |
| Interrupted job restart | `job_eee76874eeef8c7c`, VM 1124, changed from `RUNNING` to `FAILED`, result `guest_job_interrupted`, `retry_required: true`. Its durable counter stayed at exactly **1**. The job timeline recorded the interruption. |
| Invalid enrollment key | 1126 used an isolated encrypted test bundle. It remained guest-ready and healthy while reporting `tailscale_enrollment_failed`; restart required explicit reenrollment rather than retrying an enrollment key. The default bundle was not changed. |
| Permissions | Identity directory root:root 0700; private key root:root 0600; runtime parent root:root 0755; endpoint credential agent:agent 0600. Agent could neither read the private key nor run unrestricted sudo. |
| Template/smoke state | Managed smoke verification asserted no identity directory, bootstrap descriptor, runtime state, or enrolled Tailscale device. The disposable smoke clone was removed. |
| Discovery | `/v1/host` advertises enabled protocol `agentlab-guest-recovery-v1`, required runner `v2`, legacy recovery `reprovision`, and no native SSH bootstrap support. |

## Bugs found during acceptance

Ubuntu's `cloud-final.service` is ordered after `multi-user.target`. The old
runner was wanted by that target and ordered after cloud-final, producing a
cycle that dropped its boot job. The runner is now wanted by `cloud-init.target`.
A fresh clone from 9022 passed reboot without an in-place unit patch.

The second final clone reused an address retained by an old destroyed sandbox.
Metadata selected that stale row and rejected the valid new credential. A
failing regression reproduced the 403. Metadata now uses the existing unique
live-sandbox lookup; the regression also verifies fail-closed behavior for two
live rows at the same address. Both final clones subsequently returned HTTP 200.

## Automated verification

Run `bash scripts/tests/guest_bootstrap_recovery.sh` to reproduce the Linux
fixtures with Go and Docker. It uses Ubuntu 24.04 tooling, an unprivileged test
user, read-only source mounts, and no external network during execution.

- Full Go suite with `TMPDIR=/tmp go test -p 1 ./...` and `go vet ./...`.
- Race checks for database, daemon, and template worker, plus recovery/metadata
  regressions after the final metadata change.
- Linux executable fixture using real Bash, OpenSSL Ed25519, curl, jq, flock,
  pinned HTTPS, an intentionally lost initial response, and two removals of
  runtime state. The durable private key stayed unchanged.
- API integration coverage for lost initial/recovery replies across API restart,
  encrypted cache persistence, concurrent claims and winner credential stability,
  replay, expiry, wrong scope/signature/operation, HTTP rejection, revocation,
  VMID reuse, duplicate active public keys, destruction races, execution claims,
  terminal jobs, refresh behavior, credential expiry and failed-registration
  rollback.
- Real guest shell fixtures for standalone initialization, repository directory,
  and repository reuse; shell syntax, service ordering, embedded recipe checks,
  documentation generation/snippet checks, and `git diff --check`.

Readiness probes previously shared the process-wide HTTP transport, and the
shutdown test intermittently reached its five-second deadline. Private transports
with keepalives disabled eliminated the observed flake; the graceful-shutdown
test passed ten consecutive runs.

Initial-response loss, adversarial proof cases, and destroy/VMID-reuse races
were injected in isolated integration fixtures rather than against production
resources. The live tests above used only dedicated acceptance sandboxes.

## Cleanup and preservation

Temporary sandboxes 1121–1127, the invalid-key bundle/profile, Git fixture,
intermediate template 9018, and owned helper scripts were removed after
verification. Connected test guests were logged out of Tailscale before removal.
Cancelled build `tb_3546cb5ce5663560` (9020) required no remaining cleanup.
Historical build/job records remain for audit. Final template 9022 and its
dedicated acceptance profile are retained.

All pre-existing profile files were compared with the pre-upgrade backup and
remain unchanged. Production VM 1055 was left running with its original PID.
The pre-upgrade database/configuration/binary backup and a separate post-upgrade
snapshot with the persistent recovery keys are root-only on `pve2` under
`/var/backups/agentlab-guest-recovery-20261016`.
