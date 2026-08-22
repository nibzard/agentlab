# Network isolation model

Every AgentLab sandbox is a VM that deliberately runs untrusted code. The
network model gives that VM full outbound Internet access while denying it any
path into the host network, the Proxmox LAN, or other sandboxes. This page
explains the bridge, the address plan, the egress filter, the host input
filter, the L2 anti-spoofing rules, the per-sandbox endpoint secret, and the
per-profile firewall groups that together produce that result.

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

## The host input filter

Traffic to the host itself never crosses the forward chain. It takes the input
path. A second chain in the same ruleset polices that path for frames that
arrive on the agent bridge:

- The guest-facing listeners stay reachable: bootstrap and artifacts on ports
  `8844` and `8846`, the metadata address `169.254.169.254` on port `80`, DNS
  on port `53`, DHCP on port `67`, ICMP echo, and ICMPv6.
- Every other new connection is dropped, whatever host address it targets.

This closes the Proxmox VE API on `10.77.0.1:8006`, sshd on `10.77.0.1:22`,
and every other host listener, including the LAN address and the tailscale
address. Replies to connections the host started count as established traffic
and still pass.

## L2 anti-spoofing

A sandbox runs untrusted root, so the host must not trust the MAC or IP source
address a frame claims. A second nftables table in the bridge family binds
each sandbox tap to its MAC address and its IPv4 address. The bridge `input`
and `forward` hooks drop every frame from a bound tap that does not carry the
bound pair, for ARP and for IPv4. One sandbox therefore cannot speak or ARP as
a neighbour. DHCP requests and ARP probes from a booting client are matched on
the tap and MAC pair alone, because such a client has no address to compare
yet.

The bindings are runtime state, not template text. `scripts/net/apply.sh`
rebuilds them from the Proxmox NIC configuration and the DHCP leases each time
the rules are applied. Use `--bind-tap`, `--unbind-tap`, or `--sync-taps` to
update them at run time. A tap without a binding falls back to subnet-wide
checks: any in-subnet source passes, but no tap may claim the bridge address.
A binding tightens the rules. It never opens them.

## The per-sandbox endpoint secret

The L2 rules police the bridge, but the daemon also defends its own
guest-facing endpoints. The metadata and credential-proxy endpoints no longer
accept a source address as proof of identity.

- Every bootstrap fetch issues a fresh 256-bit secret for that sandbox. The
  bootstrap response delivers the secret once, and the daemon keeps only its
  SHA-256 hash.
- Every request to `/metadata/*` and `/proxy/{name}` must carry the secret in
  the `X-AgentLab-Sandbox-Secret` header. The daemon hashes the presented
  value and compares the two digests in constant time.
- The source IP still selects which sandbox row a request maps to. The secret
  decides whether that mapping is trusted.

The guest runner stores the secret at `/run/agentlab/secrets/sandbox-secret`
with mode `0600`. The `agentlab-guest` helper reads that file and sends the
header on the caller's behalf.

A sandbox with no stored secret is rejected, not excused. A sandbox that was
running before the daemon upgrade therefore loses metadata and proxy access
until its next bootstrap. This choice is deliberate. Sandboxes are short-lived
job workloads, and an allow-list for secret-less rows would keep the spoofing
path open for every one of them. An attacker inside a sandbox cannot tell a
legacy row from a fresh one, so no weaker rule closes the hole.

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
`--wan`, `--subnet`, `--bridge-addr`, `--guest-ports`, and tailnet CIDR flags.
The service refreshes the tap bindings from the Proxmox NIC configuration and
the DHCP leases every time it starts.

`scripts/net/smoke_test.sh` verifies the result from inside a sandbox. It
asserts that `10.77.0.1:8006` and `10.77.0.1:22` are blocked, that the
bootstrap and metadata endpoints still answer, and, with `--spoof-ip`, that a
claimed neighbour address cannot reach the gateway.

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
