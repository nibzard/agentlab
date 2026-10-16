# Standalone sandbox Tailscale bootstrap

Status: implemented and live-verified on `pve2`, 2026-10-16.

Bootstrap authenticates a sandbox independently of any assigned job. The guest
runner initializes secret delivery and redaction, attempts Tailscale enrollment,
and reports initialization before optional repository or agent execution. With
no job assigned it stays alive and monitors enrollment state.

The bootstrap API preserves subnet gating, single-use token semantics, per-VM
key minting, encrypted secret storage, and cleanup. It claims a token before
rotating credentials, preventing a concurrent losing request from invalidating
the winner's sandbox secret. Undelivered server failures release their claim
for retry. Profile-specific secret bundles use the coherent profile snapshot;
unconfigured standalone guests can initialize without a default bundle.

The authenticated `/v1/sandbox/bootstrap-status` endpoint accepts only fixed
states and diagnostic codes. `runner_ready`, `tailscale_state`, and
`bootstrap_error` appear in sandbox health. Installed capability, VM readiness,
and tailnet connectivity remain separate signals. Guest-agent, IP and SSH
banner checks gate VM readiness; a running QEMU process is insufficient.

Enrollment is bounded, uses a per-VM hostname, and removes the auth key from the
cached payload after its attempt. Base templates and boot-verification clones
receive no enrollment credentials. Each fresh sandbox initializes its own
Tailscale state.

## Live acceptance

- Built template **9015**, build `tb_4e72c57d123cb938`, with automatic isolated
  smoke-clone verification. Its manifest reports `boot_verified=true` and
  `enrolled=false`; verification clone 9016 was removed automatically.
- Standalone sandbox **1119** booted from the dedicated test profile without a
  job, obtained DHCP, accepted SSH, and answered its guest agent. After the
  operator replaced an invalid host key, it joined Tailscale as
  **sandbox-1119**. Guest status reported `BackendState=Running`,
  `Self.Online=true`; AgentLab reported `runner_ready=true`,
  `tailscale_state=CONNECTED`, and current guest readiness.
- A separate encrypted invalid-key bundle and sandbox **1120** produced
  `tailscale_enrollment_failed` and `tailscale_state=FAILED` while the usable
  guest remained healthy. No credential values were included in diagnostics.
- Stopping the test guest agent made current and aggregate health unhealthy
  while QEMU remained running. Restoring it recovered guest health without
  hiding the independent invalid-key diagnostic.
- Unit/regression coverage includes missing-job bootstrap, token reuse,
  retry after key-mint failure, profile policy, failed guest readiness, observed
  health projection, smoke-clone failure/cancellation and disk ownership,
  shell bootstrap with connected/failed/unconfigured enrollment, and CLI
  preservation of diagnostic fields.

Template 9015 and the dedicated valid profile are retained for review. Temporary
sandboxes, the intermediate template, and invalid-key configuration are cleaned
up after acceptance. Existing production resources and profiles remain intact.
The refreshed operator-provided enrollment key remains in encrypted host storage.
