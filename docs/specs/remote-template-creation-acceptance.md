# Remote template creation acceptance

Verified on the user-designated Proxmox host `pve2` on 2026-10-16
(Proxmox 8.4.21, Linux amd64, libguestfs 1.48).

## Installed workflow

Installed standalone Linux CLI and daemon binaries with embedded recipe assets;
no source checkout was used by the worker. The macOS CLI submitted all builds,
read logs, waited, activated the test profile, and created/destroyed test
sandboxes through the saved authenticated HTTPS control endpoint. SSH was used
for installation, host diagnostics, and guest acceptance inspection.

The previous binaries, configuration, original profiles, and a SQLite backup
are retained under `/var/backups/agentlab-remote-template-20261016` on the host.
The daemon retains `ProtectSystem=strict`; only workers gain the `/tmp` write
access required by this libguestfs version's appliance socket paths.

## Results

- Capabilities and plan validation succeeded for local ZFS storage and `vmbr1`.
- Idempotent resubmission returned the same build ID and VMID.
- A client wait timeout and interrupted log follow left execution running.
  Reconnection after cursor 23 returned contiguous records beginning at 24.
- Restarting the daemon during customization preserved the independent worker
  and its durable running record, without launching customization again.
- Queued and running cancellation reached `CANCELLED` with
  `cleanup_required=false`. A deliberately missing image returned a durable
  download failure without allocating a VM. Failed builds removed working
  images and private temporary directories while retaining verified cache data.
- Final build `tb_4a610e263fa2dffd` succeeded as template **9010**. Its live
  configuration has the owned ZFS base disk, 40 GiB capacity, cloud-init drive,
  DHCP, boot order, QEMU guest-agent setting, and Proxmox template flag.
  `template show 9010` reported `exists=true`, `drift=false`, `enrolled=false`.
- Atomic activation changed only the dedicated `remote-template-acceptance`
  profile. A stale expected VMID was rejected. Original profile files were
  compared against their backups and remained byte-for-byte unchanged.
- A clone from the activated profile booted, obtained DHCP, accepted SSH, and
  answered QEMU guest-agent ping. The runner executable and service were
  installed; SSH, Tailscale, and runner services were enabled, and the static
  QEMU guest-agent unit was available. The root filesystem had 35 GiB free.
- Installed versions: Tailscale **1.104.1**, runner **v1**, QEMU guest agent
  **8.2.2**, OpenSSH **9.6p1**, Claude Code **1.0.100**, Codex CLI **0.28.0**,
  and OpenCode **0.6.4**. The clone reported Tailscale `NeedsLogin`, confirming
  installation without inherited enrollment.

Live testing caught and fixed three recipe/runtime issues: libguestfs's short
socket paths under `/tmp`, insufficient root filesystem space before package
installation, and the guest check's missing `/usr/local/bin` search path.
The boot test also required refreshing GRUB after `virt-resize` renumbered
Ubuntu's GPT partitions. The final clone booted successfully with that fix.

## Retained state and checks

Both temporary acceptance sandboxes were destroyed. The earlier test template
with stale GRUB references was removed after checking its ownership tag.
Template **9010** and its dedicated profile remain available for review.
Existing VM 1055 and templates 9000, 9001, and 9999 were retained unchanged.
Build diagnostics and manifests remain in the daemon's normal storage.

The full Go test suite, `go vet ./...`, targeted race tests, Linux runtime and
embedded-asset tests, shell syntax checks, and `git diff --check` passed.

The subsequent [standalone bootstrap and readiness follow-up](standalone-sandbox-tailscale-bootstrap.md)
implemented and live-verified enrollment, invalid-key diagnostics, current guest
health, and automatic isolated boot verification. Template 9015 is the current
fully verified follow-up artifact; the historical results above describe the
initial template-creation acceptance run.
