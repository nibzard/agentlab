# Network isolation model

Every AgentLab sandbox is a VM that deliberately runs untrusted code. The
network model gives that VM full outbound Internet access while denying it any
path into the host network, the Proxmox LAN, or other sandboxes. This page
explains the bridge, the address plan, the egress filter, and the per-profile
firewall groups that together produce that result.

## The agent bridge and subnet

Sandboxes live on a dedicated Proxmox Linux bridge called `vmbr1`, separate from
the WAN or LAN bridge `vmbr0`. The agent subnet defaults to `10.77.0.0/16`, with
the host holding `10.77.0.1`. The daemon's guest-facing listeners, bootstrap on
`10.77.0.1:8844` and artifact on `10.77.0.1:8846`, bind to that host address so
only VMs on the bridge can reach them.

The host masquerades traffic leaving the agent subnet for `vmbr0`, which is what
gives a sandbox its normal-looking outbound Internet. The bridge setup is part
of the host tutorial: [Set up the Proxmox host](../tutorials/set-up-proxmox-host.md).

## The egress filter

Outbound Internet is allowed. Private ranges are not. A managed nftables ruleset
on the forward chain enforces the boundary:

- Established and related connections return early, so replies to a sandbox's
  own outbound connections are allowed.
- Traffic from the agent subnet to RFC 1918 ranges, `10.0.0.0/8`,
  `172.16.0.0/12`, and `192.168.0.0/16`, is dropped.
- Traffic to the link-local range `169.254.0.0/16` is dropped.
- IPv6 traffic to the ULA range `fc00::/7` and the link-local range `fe80::/10`
  is dropped.
- A NAT masquerade rule applies only to traffic from the agent subnet leaving
  via `vmbr0`.

The net effect is that a sandbox can `curl` a public package mirror but cannot
probe `192.168.0.1`, reach a peer VM, or fall back to a link-local address.

## The tailnet boundary

Sandboxes are also denied the tailnet. New connections initiated from the agent
subnet into the tailnet IPv4 range `100.64.0.0/10` and the tailnet IPv6 range
`fd7a:115c:a1e0::/48` are dropped. The reverse direction is allowed: a connection
originated from the tailnet toward a sandbox is accepted. This asymmetry lets an
operator SSH into a sandbox through a Tailscale route, while preventing a
compromised sandbox from pivoting into the tailnet.

The ruleset is installed by `scripts/net/apply.sh`, which renders
`scripts/net/agent_nat.nft` into `/etc/nftables.d/agentlab.nft` and enables the
`agentlab-nftables.service`. The defaults can be overridden with `--bridge`,
`--wan`, `--subnet`, and tailnet CIDR flags.

## Per-profile firewall groups

A profile selects one of three network modes through `network.mode`, and each
mode maps to a Proxmox firewall group:

| `network.mode` | Proxmox firewall group | Meaning |
| --- | --- | --- |
| `off` | `agent_nat_off` | No NAT egress from the sandbox |
| `nat` (default) | `agent_nat_default` | Default NAT egress subject to the rules above |
| `allowlist` | `agent_nat_allowlist` | Egress restricted to an allowlist |

The default mode is `nat`. This puts the kernel-level nftables boundary and the
Proxmox-level firewall group on the same sandbox, so isolation holds even if one
layer is misconfigured.

## Persistence and the host mount guard

Network isolation is paired with a filesystem guard. A sandbox root disk is
ephemeral. The only durable state is a separate workspace volume, mounted at
`/work`, that can be detached and reattached. The daemon refuses to provision any
profile that requests a host mount, including `host_mount`, `bind_mount`,
`virtiofs`, or any key that reads like host plus mount, path, or bind. This
prevents a sandbox from being handed a slice of the host filesystem.

## Related reading

- For the trust model around the control plane and guest listeners:
  [Control plane and trust boundaries](control-plane-and-trust-boundaries.md).
- For how secrets cross this network without leaking:
  [Secrets delivery model](secrets-delivery-model.md).
- For the operational subnet-routing setup:
  [Configure Tailscale subnet routing](../how-to/configure-tailscale-subnet-routing.md).
