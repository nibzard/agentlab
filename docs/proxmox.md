# Proxmox Storage Notes

This document covers Proxmox-specific storage behavior used by AgentLab, with a focus on
workspace volumes and snapshot/clone operations.

## Workspace Volume Snapshot/Clone Support

Workspace volume snapshot/restore/clone operations are implemented for ZFS-backed
storages (Proxmox storage type `zfspool`, e.g. `local-zfs`). Other storage types are
reported as unsupported.

Operations:
- `VolumeSnapshotCreate`: create a snapshot for a workspace volume
- `VolumeSnapshotRestore`: restore a workspace volume to a snapshot
- `VolumeSnapshotDelete`: delete a snapshot from a workspace volume
- `VolumeClone`: clone a workspace volume to a new volume ID

## Consistency & Safety Model

These operations are **not** VM snapshots. They operate directly on workspace volumes.
For consistency and to avoid data loss:
- Volumes must be **detached** from all VMs before snapshot, restore, or clone.
- Restores are destructive and replace the current volume contents.
- If a volume is attached or in use, callers must detach it first.

AgentLab enforces detachments at higher layers. The backend primitives assume the volume
is detached and will not force-stop VMs automatically.

## Backend Behavior

Shell backend:
- Uses `pvesm status` to detect storage type.
- Uses `pvesm snapshot`, `pvesm rollback`, `pvesm delsnapshot`, and `pvesm clone` for
  ZFS-backed storages.

API backend:
- Uses Proxmox storage content endpoints for snapshots/clones when available.
- Optional shell fallback can be enabled to use `pvesm` if the API lacks storage
  snapshot/clone support (`proxmox_api_shell_fallback: true`).

## Host Requirements

- Proxmox host has `pvesm` available.
- ZFS pool storage configured for workspace volumes (e.g. `local-zfs`).
- If shell fallback is enabled, the daemon must be allowed to invoke `pvesm`.
