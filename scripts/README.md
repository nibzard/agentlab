# Scripts

- `install_host.sh`: install agentlab binaries, directories, and systemd unit on the host.
- `create_template.sh`: build the Ubuntu cloud-init template VM on Proxmox.
- `guest/agentlab-agent`: wrapper script baked into the guest template to dispatch agent CLIs.
- `guest/agent-runner`: guest bootstrap + execution script invoked by systemd.
- `guest/agent-runner.service`: systemd unit for the guest runner.
- `guest/agent-runner.env`: optional runner environment overrides.
- `guest/agent-secrets-cleanup`: guest cleanup helper invoked on service stop.
- `systemd/agentlabd.service`: systemd unit for the agentlabd daemon.
- `net/agent_nat.nft`: nftables rules template for agent subnet NAT + egress/tailnet blocks.
- `net/apply.sh`: install and optionally enable the agentlab nftables rules.
- `net/smoke_test.sh`: SSH-based connectivity smoke test for sandbox egress and blocks.
- `net/setup_vmbr1.sh`: configure the vmbr1 bridge and enable IP forwarding on the host.
- `net/setup_tailscale_router.sh`: advertise the agent subnet via Tailscale for tailnet access.
- `tests/golden_path.sh`: end-to-end golden path integration test for jobs + artifacts.
