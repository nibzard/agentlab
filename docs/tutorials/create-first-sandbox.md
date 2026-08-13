# Create your first sandbox

Goal: boot an isolated VM sandbox from a profile, watch it reach `RUNNING`, and log in over SSH.

## Prerequisites

- The host is set up as described in [Set up the Proxmox host](set-up-proxmox-host.md).
- A registered VM template and profile. Complete [Build a VM template](build-a-vm-template.md) first, or use the bundled `yolo-ephemeral` profile against a template at VMID `9000`.
- The `agentlab` CLI on a host that can reach the daemon socket.

## Steps

1. Confirm the daemon is up and the profiles are loaded.

    ```bash
    agentlab status
    agentlab profile list
    ```

   `profile list` reads the YAML files the daemon loaded from `profiles_dir` (default `/etc/agentlab/profiles`).

2. Create a sandbox from the `yolo-ephemeral` profile. The default profile clones the template, applies the profile resources, and starts the VM.

    ```bash
    agentlab sandbox new --profile yolo-ephemeral --name first-sandbox
    ```

   The sandbox moves through the states `REQUESTED`, `PROVISIONING`, `BOOTING`, `READY`, and finally `RUNNING`.

3. Watch provisioning progress in the daemon log and the sandbox list.

    ```bash
    agentlab sandbox list
    agentlab logs <vmid> --follow
    ```

   Replace `<vmid>` with the numeric VMID that `sandbox new` returned.

4. When the sandbox reaches `RUNNING`, inspect it for its guest IP and lease.

    ```bash
    agentlab sandbox show <vmid>
    ```

5. Open a shell in the sandbox. By default `agentlab ssh <vmid>` prints the SSH command. Add `--exec` to run it and open a shell.

    ```bash
    agentlab ssh <vmid> --exec
    ```

6. When you are done, destroy the sandbox. This removes the VM and its disks.

    ```bash
    agentlab sandbox destroy <vmid>
    ```

## Expected result

`agentlab sandbox show <vmid>` reports state `RUNNING`, an IP on `10.77.0.0/16`, and the attached profile. `agentlab ssh <vmid> --exec` opens a shell inside the sandbox. After `sandbox destroy`, the VM no longer appears in `agentlab sandbox list` or `qm list`.

!!! note
    The sandbox root disk is ephemeral. Anything outside the workspace volume at `/work` is lost when the sandbox is destroyed or reverted. For persistent state, see [Run a stateful dev session](stateful-dev-session.md).

## Next

Now run an automated task inside a sandbox in [Run your first job](run-first-job.md). For the full sandbox state machine, see [State machine reference](../reference/state-machine.md).
