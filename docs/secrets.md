# Secrets bundles (age / sops)

AgentLab stores host-side secrets in encrypted bundles on disk. The daemon decrypts them
in memory on demand.

Default locations:
- Bundles: `/etc/agentlab/secrets`
- Age private key: `/etc/agentlab/keys/age.key`

## Bundle format (version 1)

Bundles are YAML or JSON **before encryption**. The decrypted payload must match this
schema (fields are optional unless noted).

```yaml
version: 1
metadata:
  owner: platform

git:
  username: "x-access-token"   # optional
  token: "ghp_..."             # optional
  ssh_private_key: |            # optional
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
  ssh_public_key: "ssh-ed25519 AAAA..."  # optional
  known_hosts: |
    github.com ssh-ed25519 AAAA...

env:
  ANTHROPIC_API_KEY: "..."
  OPENAI_API_KEY: "..."

claude:
  # Either settings_json (string) or settings (object)
  settings:
    model: "claude-3-5-sonnet-20241022"
    max_tokens: 8000

artifact:
  endpoint: "http://10.77.0.1:8846/upload"
  token: "upload-token"
```

## Encrypt with age

```bash
age-keygen -o /etc/agentlab/keys/age.key
chmod 600 /etc/agentlab/keys/age.key
RECIPIENT=$(age-keygen -y /etc/agentlab/keys/age.key)

age -r "$RECIPIENT" -o /etc/agentlab/secrets/default.age secrets.yaml
chmod 600 /etc/agentlab/secrets/default.age
```

## Encrypt with sops (age recipients)

```bash
RECIPIENT=$(age-keygen -y /etc/agentlab/keys/age.key)

sops --encrypt --age "$RECIPIENT" \
  --output /etc/agentlab/secrets/default.sops.yaml \
  secrets.yaml
chmod 600 /etc/agentlab/secrets/default.sops.yaml
```

## Config keys

Add to `/etc/agentlab/config.yaml` if you want to override defaults:

```yaml
secrets_dir: /etc/agentlab/secrets
secrets_bundle: default
secrets_age_key_path: /etc/agentlab/keys/age.key
secrets_sops_path: sops
```

## Rotation workflow

1. Create a new bundle file with new tokens (e.g., `default-2026-01-30.age`).
2. Update `secrets_bundle` to the new name and restart `agentlabd`.
3. Keep the old bundle around until all running sandboxes finish.
4. Revoke the old tokens and delete the old bundle file.

Age key rotation:
1. Generate a new age key in `/etc/agentlab/keys/`.
2. Re-encrypt the bundle with the new recipient.
3. Update `secrets_age_key_path` and restart `agentlabd`.
4. Remove the old key once all sandboxes are rotated.
