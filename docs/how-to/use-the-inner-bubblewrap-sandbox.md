# How to use the inner bubblewrap sandbox

Wrap the coding agent in an in-guest bubblewrap (`bwrap`) containment layer that
runs it inside its own mount namespace. The Proxmox VM is the primary isolation
boundary; the inner sandbox adds a second layer inside the guest.

## Prerequisites

- A profile whose template image has `bubblewrap` (`bwrap`) installed. The guest
  runner fails the job with `inner sandbox requested but bubblewrap (bwrap) not
  installed` if the binary is absent.
- A profile you can edit. See ../how-to/author-a-profile.md.

## Steps

1. Enable the inner sandbox in the profile by setting `behavior.inner_sandbox`.
   Only `bubblewrap` is supported besides empty or `none`:

    ```yaml
    behavior:
      inner_sandbox: bubblewrap
    ```

2. Add extra bubblewrap arguments with `behavior.inner_sandbox_args`. The runner
   appends the list token-by-token after its built-in binds:

    ```yaml
    behavior:
      inner_sandbox: bubblewrap
      inner_sandbox_args:
        - --bind
        - /scratch
        - /scratch
    ```

3. Restart the daemon and run a sandbox from the profile:

    ```bash
    sudo systemctl restart agentlabd.service
    agentlab sandbox new --profile my-profile
    ```

4. To emergency-disable the inner sandbox without editing the profile, set
   `AGENTLAB_INNER_SANDBOX=0` in `/etc/agentlab/agent-runner.env` on the guest
   image.

## Verify

- The guest runner log records `inner sandbox enabled: bubblewrap` at job start.
- `agentlab logs <vmid>` shows sandbox lifecycle events, and streamed runner
  output when `AGENTLAB_RUNNER_STREAM_LOGS=1`. For the `inner sandbox enabled:
  bubblewrap` line, check the guest runner journal
  (`journalctl -u agent-runner`).

## What the inner sandbox does

The runner invokes `bwrap` with `--die-with-parent`, `--unshare-all`, and
`--share-net`, binds the root filesystem read-only (`--ro-bind / /`), then
mounts `--proc /proc` and `--dev /dev`. It then adds read-write binds for
`/tmp`, `/var/tmp`, `/run`, `$HOME`, and the repo directory, and a read-only
bind for the secrets directory. Extra arguments from
`behavior.inner_sandbox_args` are appended before the agent command.

!!! warning "Not a full security boundary"
    The inner sandbox shares the network namespace (`--share-net`) and provides
    no network isolation. Treat it as defense in depth inside the VM, not as a
    replacement for the VM boundary described in
    ../explanation/network-isolation-model.md.
