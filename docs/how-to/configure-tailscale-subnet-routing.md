# How to configure Tailscale subnet routing

Publish the agent subnet as a Tailscale subnet route so workstations on the
tailnet reach sandbox VMs directly. With the route advertised and approved, a
laptop on the tailnet can `ssh` to a sandbox IP on `10.77.0.0/16` without a jump
host.

For the agent subnet and isolation model, see
[Network isolation model](../explanation/network-isolation-model.md). To connect
a workstation to the daemon over the same tailnet, see
[Connect to a remote daemon over the tailnet](connect-remote-daemon-over-tailnet.md).

## Prerequisites

- A Proxmox host running `agentlabd`, with the agent bridge `vmbr1` and the
  default agent subnet `10.77.0.0/16` already configured.
- Tailscale installed and signed in on the host.
- `root` on the host. The setup script refuses to run otherwise.

## Steps

1. Advertise the agent subnet route. The helper script prints the exact
   `tailscale` commands; add `--apply` to run them.

    ```bash
    sudo scripts/net/setup_tailscale_router.sh --subnet 10.77.0.0/16
    sudo scripts/net/setup_tailscale_router.sh --subnet 10.77.0.0/16 --apply
    ```

    On a node that is not yet logged in, pass an auth key so the command can
    bring Tailscale up unattended.

    ```bash
    sudo scripts/net/setup_tailscale_router.sh --apply \
        --subnet 10.77.0.0/16 --authkey <tailscale-authkey> --hostname agentlab-proxmox
    ```

    The script prefers `tailscale set --advertise-routes`. On clients that lack
    `tailscale set`, it prints the `tailscale up --advertise-routes` command in
    a dry run; with `--apply` it errors out and asks you to run `tailscale up`
    with the existing flags manually.

2. Approve the subnet route. Advertising a route does not make Tailscale route
   traffic through it until an admin approves it.

    Open the Tailscale admin console, find the host under **Machines**, and
    approve `10.77.0.0/16` as a subnet route. This step is required and is the
    most common reason a route appears advertised but stays unreachable.

    !!! note "Tailnet ACLs"
        Advertising and approving a route is necessary but not always
        sufficient. Tailnet-wide ACL and auto-approvers setup that decides which
        nodes may use the route is not fully documented here. Configure ACLs in
        the admin console to allow your workstation to reach the agent subnet.

3. Accept the route on each client. On Linux clients, enable the route with
   `--accept-routes`; most other clients accept routes by default.

    ```bash
    sudo tailscale up --accept-routes
    ```

## Verify

From a workstation on the tailnet, confirm the agent subnet is reachable and a
sandbox responds.

```bash
tailscale status
ssh agent@10.77.0.<sandbox-ip>
```

If `ssh` reaches the sandbox, the subnet route works. If the host advertises the
route but the workstation cannot connect, re-check the admin-console approval
and the workstation's `--accept-routes` flag.
