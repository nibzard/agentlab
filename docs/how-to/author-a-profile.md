# How to author a profile

Write a profile YAML file that selects a template, resources, network mode, and
behavior defaults for new sandboxes.

## Prerequisites

- A registered VM template with `qemu-guest-agent` enabled. The profile
  references it by `template_vmid`. See
  ../tutorials/build-a-vm-template.md.
- Write access to the profiles directory, `/etc/agentlab/profiles` by default.

## Steps

1. Create a YAML file under the profiles directory. `name` and `template_vmid`
   are required; every other key is optional:

    ```yaml
    ---
    name: my-profile
    template_vmid: 9000
    network:
      bridge: vmbr1
      model: virtio
      mode: nat
    resources:
      cores: 4
      memory_mb: 6144
    storage:
      root_size_gb: 40
    behavior:
      keepalive_default: false
      ttl_minutes_default: 180
    ```

   The example omits three keys that are not profile fields. Behavior `mode`
   is set per request with `--mode` (it defaults to `dangerous`), not in the
   profile. Workspace attachment is request-time, controlled by `--workspace`;
   the `storage.workspace` key is ignored. `secrets_bundle` is a daemon-wide
   config key, not a per-profile field.

2. Choose a network mode. `mode` maps to a Proxmox firewall group:

   | `mode`     | Firewall group          |
   |------------|-------------------------|
   | `off`      | `agent_nat_off`         |
   | `nat`      | `agent_nat_default`     |
   | `allowlist`| `agent_nat_allowlist`   |

3. Set behavior overrides as needed. Common fields:

   - `ttl_minutes_default` and `keepalive_default` control lease behavior.
   - `idle_stop_minutes_default` overrides the global idle-stop window for this
     profile. Set it to `0` to disable idle stop for the profile.
   - `inner_sandbox: bubblewrap` enables the in-guest containment layer. See
     ../how-to/use-the-inner-bubblewrap-sandbox.md.

4. Restart `agentlabd` so the daemon loads the new profile:

    ```bash
    sudo systemctl restart agentlabd.service
    ```

## Verify

- `agentlab profile list` shows the new profile name and template VMID.
- `agentlab sandbox validate --profile my-profile` runs preflight validation
  without provisioning.
- `agentlab sandbox new --profile my-profile` provisions a sandbox from it.

!!! warning "Host mounts are rejected"
    Profiles that request host bind mounts (`host_mount`, `bind_mount`,
    `virtiofs`, and similar keys) are rejected at provisioning time. Use a
    workspace volume for durable state instead. See
    ../how-to/use-workspaces-and-rebind.md.
