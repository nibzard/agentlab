# How to rotate secrets and age keys

Replace the encrypted secrets bundle and the age key without disrupting running
sandboxes.

## Prerequisites

- The age private key at `/etc/agentlab/keys/age.key` (config key
  `secrets_age_key_path`) and bundle directory `/etc/agentlab/secrets`
  (`secrets_dir`).
- The `age-keygen`, `age`, and `sops` binaries on the host.
- Root access to edit `/etc/agentlab/config.yaml` and restart `agentlabd`.

## Steps

### Rotate the bundle

1. Encrypt a new bundle with the existing recipient. Use a dated name:

    ```bash
    age-keygen -o /etc/agentlab/keys/age.key   # only if rotating the key too
    RECIPIENT="$(age-keygen -y /etc/agentlab/keys/age.key)"
    age -r "$RECIPIENT" -o /etc/agentlab/secrets/default-2026-08-12.age secrets.yaml
    ```

2. Point the daemon at the new bundle in `/etc/agentlab/config.yaml`:

    ```yaml
    secrets_bundle: default-2026-08-12
    ```

3. Restart the daemon and verify it loads the new bundle:

    ```bash
    sudo systemctl restart agentlabd.service
    agentlab secrets validate default-2026-08-12
    agentlab secrets show default-2026-08-12
    ```

4. Keep the old bundle until every running sandbox has completed and fetched its
   bootstrap payload. Then revoke any old tokens and remove the old bundle file.

### Rotate the age key

1. Generate a new key and re-encrypt the bundle with the new recipient (shown
   above).
2. Update `secrets_age_key_path` if the new key lives at a new path.
3. Restart `agentlabd` and confirm with `agentlab secrets validate`.
4. Remove the old key only after all sandboxes have rotated onto the new bundle.

## Verify

- `agentlab secrets validate <bundle>` reports the SSH key count and Tailscale
  status with no errors.
- `agentlab secrets show <bundle> --reveal` prints the decrypted contents.
- New sandboxes boot and receive secrets; existing sandboxes finish unaffected.

!!! warning "Plaintext needs an explicit opt-in"
    Reading or writing a plaintext bundle is refused unless you pass
    `--allow-plaintext`. Keep bundles encrypted at rest. See the
    [Secrets reference](../reference/secrets.md) and
    [Secrets delivery model](../explanation/secrets-delivery-model.md).
