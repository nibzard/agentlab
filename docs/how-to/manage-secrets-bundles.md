# How to manage secrets bundles

Create, populate, and apply an encrypted secrets bundle. The bundle carries the
environment variables, git credentials, SSH keys, and Tailscale enrollment
material that sandbox guests receive at bootstrap.

For the bundle format and token lifecycles, see
[Secrets reference](../reference/secrets.md). For how values reach the guest,
see [Secrets delivery model](../explanation/secrets-delivery-model.md).

## Prerequisites

- An age key pair. The daemon decrypts bundles with the private key at
  `/etc/agentlab/keys/age.key`.
- A running `agentlabd` for the remote subcommands (`set-env`, `set-git`).
  `secrets validate` is local-only and works without the daemon.

## Steps

1. Generate an age key pair if you do not have one, and lock down the private
   key.

    ```bash
    age-keygen -o /etc/agentlab/keys/age.key
    chmod 600 /etc/agentlab/keys/age.key
    ```

    The recipient string (the public key, starting with `age1`) is printed to
    stderr by `age-keygen` and lives in the header of `age.key`.

2. Populate environment variables and git credentials through the daemon. Both
   subcommands are remote-only; they merge into the bundle over HTTP.

    ```bash
    agentlab secrets set-env --name ANTHROPIC_API_KEY --value sk-ant-...
    agentlab secrets set-git --git-token ghp_... --username x-access-token
    ```

    You can also load env values from a file.

    ```bash
    agentlab secrets set-env --from-file env.json
    ```

3. Add the guest SSH public key and Tailscale enrollment that each sandbox
   receives.

    ```bash
    agentlab secrets add-ssh-key --name laptop --key-file ~/.ssh/id_ed25519.pub
    agentlab secrets set-tailscale --authkey <tailscale-authkey> \
        --hostname-template 'agentlab-{vmid}' --tag tag:agentlab
    ```

4. Encrypt the bundle on disk. The `agentlab secrets` subcommands store values
   in the daemon's bundle directory, which is `/etc/agentlab/secrets` by
   default. To encrypt a hand-written plaintext file, use `age` or `sops`
   directly.

    ```bash
    age -r "$RECIPIENT" -o /etc/agentlab/secrets/default.age secrets.yaml
    sops --encrypt --age "$RECIPIENT" -o /etc/agentlab/secrets/default.sops.yaml secrets.yaml
    ```

    !!! warning
        Plaintext bundle reads and writes are refused unless you pass
        `--allow-plaintext`. `.sops.*` bundles can be read and validated but
        cannot be rewritten in place by the CLI.

## Verify

Inspect the populated bundle. Raw values are redacted by default.

```bash
agentlab secrets validate default
agentlab secrets show default
agentlab secrets show default --reveal
```

`secrets validate` reports the SSH key count and Tailscale status. `secrets show`
prints the full structure; add `--reveal` only when you need the literal values.
Confirm the bundle name the daemon loads matches `secrets_bundle` (default
`default`) in `/etc/agentlab/config.yaml` before you boot a sandbox.
