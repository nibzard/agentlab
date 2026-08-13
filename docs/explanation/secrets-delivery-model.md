# Secrets delivery model

A sandbox needs credentials to do useful work: API keys for the agent, git
tokens to clone the repository, SSH keys for the operator, and Tailscale auth
material. None of these may ever live on the VM disk or leak through a log. This
page explains the model that delivers secrets into a guest without writing them
to persistent storage.

## Encrypted at rest

Host-side secrets are stored in encrypted bundles under `/etc/agentlab/secrets`.
The default bundle is named `default`, decrypted in memory on demand with an age
private key at `/etc/agentlab/keys/age.key`. The bundle schema is version 1,
with optional sections for `git`, `env`, `claude`, `artifact`, `ssh`, and
`tailscale`. The daemon never accepts plaintext bundles in normal operation;
plaintext reads and writes require the explicit `--allow-plaintext` flag, and
existing `.sops.*` bundles can be read but not rewritten in place. The on-disk
format is covered in the [Secrets reference](../reference/secrets.md), the
workflow in [Manage secrets bundles](../how-to/manage-secrets-bundles.md).

## One bootstrap token, one use

When the JobOrchestrator provisions a sandbox, it mints a single-use bootstrap
token bound to that VMID. The token is hashed at rest, has a default TTL of 10
minutes, and is written into the cloud-init snippet alongside the controller URL
and the VMID. Nothing else carries authority to fetch that sandbox's secrets.

On boot the in-guest `agent-runner` reads its bootstrap descriptor and POSTs the
token plus the VMID to `POST /v1/bootstrap/fetch` on the bootstrap listener. The
daemon validates the token, returns the decrypted payload, and immediately
consumes the token so it cannot be replayed. A successful fetch is the only way
to obtain that payload, and it can happen at most once.

The endpoint is restricted to the agent subnet and is rate-limited per source IP
to one request per second with a burst of three. A caller outside the subnet
gets HTTP 403. The trust boundary around this listener is explained in
[Control plane and trust boundaries](control-plane-and-trust-boundaries.md).

## Tmpfs in the guest, wiped on exit

The bootstrap payload is never written to the VM root disk. The `agent-runner`
service runs as user `agent` with umask `077` and writes the decrypted material,
such as `env.sh`, git keys, and the Claude settings, into `/run/agentlab/secrets`
under the runtime directory. Because `/run` is a tmpfs, the secrets vanish on
shutdown. An `agent-secrets-cleanup` step runs as `ExecStopPost` and wipes the
secrets directory, refusing to touch anything outside `/run` or `/dev/shm`.

Raw secret values are redacted everywhere they might appear in the clear. The
`agent-runner` builds a sed filter from the bootstrap token, the artifact and
git tokens, the Tailscale tokens, and every environment value of at least six
characters, replacing each occurrence with `[REDACTED]` in streamed logs. The
Secrets API and CLI output redact by default, and a Redactor scrubs staged
secrets from daemon logs.

## Per-job artifact tokens

Artifact upload uses a separate credential path. At bootstrap the daemon mints a
per-job bearer token for the artifact listener and returns it in the payload.
The guest uses that token to POST its `agentlab-artifacts.tar.gz` bundle to
`POST /upload`. The token is governed by `artifact_token_ttl_minutes`, defaults
to 1440 minutes, and is never stored in the bundle. The upload endpoint is
bound to the agent subnet and rate-limited per IP.

## Tailscale auth keys

If a sandbox enrolls on the tailnet, the daemon mints a per-VM Tailscale auth
key with a one-hour TTL. The key is single-use and is preauthorized only after
the bootstrap token is consumed, so a lost consume race cannot orphan a key.
Mint failure degrades to a shared key from the bundle. The Tailscale Admin API
key that drives minting is strictly opt-in and is stored encrypted as
`admin_api_key` in the secrets bundle under `/etc/agentlab/secrets`.

## The snippet caveat

The one place secret material sits in cleartext is the cloud-init snippet under
`/var/lib/vz/snippets` on storage `local`. The snippet holds the bootstrap
token, the controller URL, and the VMID, and it is visible in the Proxmox UI and
API to anyone who can read VM config or snippet storage. Snippets are deleted
when a sandbox is destroyed; a VM kept or snapshotted needs manual cleanup.
Restrict Proxmox access and treat the snippet directory as sensitive.

## Related reading

- For the full bundle format and token lifetimes:
  [Secrets reference](../reference/secrets.md).
- For rotating keys and bundles safely:
  [Rotate secrets and age keys](../how-to/rotate-secrets-and-age-keys.md).
- For the guest side of the fetch:
  [Guest runner flow](guest-runner-flow.md).
