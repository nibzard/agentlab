# Scripts

- `install_host.sh`: install agentlab binaries, directories, and systemd unit on the host.
- `systemd/agentlabd.service`: systemd unit for the agentlabd daemon.
- `net/agent_nat.nft`: nftables rules template for agent subnet NAT + egress/tailnet blocks.
- `net/apply.sh`: install and optionally enable the agentlab nftables rules.
- `net/setup_vmbr1.sh`: configure the vmbr1 bridge and enable IP forwarding on the host.
- `net/setup_tailscale_router.sh`: advertise the agent subnet via Tailscale for tailnet access.
