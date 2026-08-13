# Secrets reference

AgentLab stores host-side secrets in encrypted bundles and delivers them to
guests through one-time tokens. This page documents the bundle format,
encryption, token lifecycles, and redaction. For operational steps, see
../how-to/manage-secrets-bundles.md and
../how-to/rotate-secrets-and-age-keys.md. For the delivery model, see
../explanation/secrets-delivery-model.md.

## Bundle format (version 1)

Bundles are YAML or JSON before encryption. The decrypted payload uses the
schema below. Fields are optional unless noted.

```yaml
version: 1
metadata:
  owner: platform

git:
  username: "x-access-token"
  token: "ghp_..."
  ssh_private_key: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
  ssh_public_key: "ssh-ed25519 AAAA..."
  known_hosts: |
    github.com ssh-ed25519 AAAA...

env:
  ANTHROPIC_API_KEY: "..."
  OPENAI_API_KEY: "..."

claude:
  settings:
    model: "claude-3-5-sonnet-20241022"
    max_tokens: 8000

artifact:
  endpoint: "http://10.77.0.1:8846/upload"

ssh:
  keys:
    laptop:
      key: "ssh-ed25519 AAAA..."
      type: "ssh-ed25519"
      comment: "laptop"

tailscale:
  authkey: "tskey-auth-..."
  hostname_template: "agentlab-{vmid}"
  tags:
    - "tag:agentlab"
  extra_args:
    - "--ssh"
```

Top-level sections:

| Section | Contents |
| --- | --- |
| `git` | Git username, token, SSH private and public key, `known_hosts`. |
| `env` | Environment variables delivered to the guest. |
| `claude` | Claude settings, as a `settings` object or a `settings_json` string. |
| `artifact` | Optional artifact endpoint override. Ignored when the embedded upload service is enabled. |
| `ssh` | Named guest SSH public keys. |
| `tailscale` | Guest Tailscale enrollment: `authkey` or `admin_api_key`, `tailnet`, `hostname_template`, `tags`, `extra_args`. |

## Encryption

- Bundles live in `secrets_dir` (default `/etc/agentlab/secrets`).
- The age private key lives at `secrets_age_key_path` (default
  `/etc/agentlab/keys/age.key`).
- The daemon decrypts bundles in memory on demand. Bundles and the age key
  should be `chmod 600`.

Supported file types:

| Operation | Formats |
| --- | --- |
| Read | `.age`, `.sops.*`, `.yaml`, `.yml`, `.json` |
| Write | `.age`, `.yaml`, `.yml`, `.json` |

Existing `.sops.*` bundles can be read and validated but not written in place.
Plaintext reads and writes require `--allow-plaintext`.

## Config keys

| Key | Default | Description |
| --- | --- | --- |
| `secrets_dir` | `/etc/agentlab/secrets` | Directory for encrypted bundles. |
| `secrets_bundle` | `default` | Bundle name to load. |
| `secrets_age_key_path` | `/etc/agentlab/keys/age.key` | age private key path. |
| `secrets_sops_path` | `sops` | sops binary path. |
| `artifact_token_ttl_minutes` | `1440` (24h) | Per-job artifact upload token lifetime. |

See reference/configuration.md for the full configuration reference.

## Token lifecycles

| Token | Default TTL | Scope | Notes |
| --- | --- | --- | --- |
| Bootstrap | 10 minutes | Single sandbox | Hashed at rest, single-use, consumed after a successful fetch; written into the cloud-init snippet. |
| Artifact upload | `artifact_token_ttl_minutes` (24h) | Single job | Minted by the daemon; not stored in the bundle. |
| Tailscale per-VM auth key | 1 hour | Single VM | Single-use, preauthorized only after the bootstrap token is consumed. |

The secrets bundle does not contain the bootstrap token or the artifact upload
token. The daemon mints both at runtime.

## Redaction

- API responses and CLI output never include raw secret values unless you pass
  `--reveal`.
- The daemon scrubs staged secrets from logs with a Redactor.
- The guest runner replaces the bootstrap token, artifact, git, and Tailscale
  tokens, and environment values of at least six characters, with `[REDACTED]`
  in streamed logs.

## CLI summary

```bash
agentlab secrets show
agentlab secrets show --reveal
agentlab secrets validate
agentlab secrets set-env --name ANTHROPIC_API_KEY --value sk-...
agentlab secrets add-ssh-key --name laptop --key-file ~/.ssh/id_ed25519.pub
agentlab secrets set-tailscale --authkey tskey-auth-... --hostname-template 'agentlab-{vmid}'
agentlab secrets clear-tailscale
```

`set-env` and `set-git` are remote-only; they go through the daemon over HTTP.
`validate` is local-only. See reference/cli.md for the full command reference,
and reference/security.md for the security model.

## Rotation

See ../how-to/rotate-secrets-and-age-keys.md for the bundle and age-key
rotation procedure.
