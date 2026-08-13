# Shell versus API backend

AgentLab does not talk to Proxmox in one way. It talks to it through a single
`Backend` interface with two implementations: one that shells out to the
`qm`, `pvesh`, and `pvesm` tools, and one that calls the Proxmox REST API
with a token. The daemon picks one at startup from `proxmox_backend` and
uses it for every clone, configure, start, stop, snapshot, and volume
operation.

Both implementations satisfy the same interface, so the rest of the daemon
does not care which one is in use. The choice is about trust, privileges, and
Proxmox version.

## The shell backend

The shell backend drives Proxmox by running the command-line tools. It calls
`qm` for clone, status, config, resize, start, stop, suspend, resume, destroy,
and snapshots, `pvesh` for snapshot lists and status JSON, and `pvesm` for
volume allocation and status.

Because it runs the same tools an operator would type, it works on any
Proxmox version AgentLab supports, including 8.x. It needs no API token, and
it inherits whatever the `agentlabd` process can do.

That strength is also its constraint. The `qm` tool talks to the Proxmox
daemon over an IPC path that breaks under common systemd hardening. If the
`agentlabd` unit sets `NoNewPrivileges` or `PrivateTmp`, `qm clone` fails with
`ipcc_send_rec` errors. The shell backend works around this with a bash
wrapper, but the underlying rule stands: the daemon must run with the
privileges and environment that let `qm` reach its IPC socket.

## The API backend

The API backend speaks the Proxmox REST API directly. It authenticates with a
dedicated API token and performs the same operations over HTTP. It requires
no `qm` IPC, so it is free of the systemd constraints that constrain the
shell backend.

The API backend needs `proxmox_api_token` (required and validated at config
load) and a base URL (`proxmox_api_url`, default `https://localhost:8006`). No
specific Proxmox version is required. By default TLS verification is on; you
can set a CA bundle with `proxmox_tls_ca_path`, or disable verification with
`proxmox_tls_insecure` (the two are mutually exclusive).

The REST API does not expose every operation the shell can do. For volume
snapshot, restore, and clone, the API backend uses REST endpoints as the
primary path. It falls back to `pvesm` only on API error when
`proxmox_api_shell_fallback` is true.

## The default is shell

This is the part that trips people up. The default value of
`proxmox_backend` in the code is `shell`, not `api`. Older documentation
listed the default as `api`. That was wrong.

```text
proxmox_backend: shell   # the actual code default
proxmox_backend: api     # the recommended value for new deployments
```

If you want the API backend, you must set it explicitly. The API backend is
the recommended choice for new deployments because it avoids the IPC and
privilege problems of the shell path, but it is opt-in.

## When to pick which

Pick the **API backend** when you run Proxmox VE 9.x or newer and can mint a
dedicated API token. It is the cleaner long-term option and removes the
systemd hardening conflict.

Keep the **shell backend** when you are on Proxmox 8.x, when policy forbids
API tokens, or when you need an operation that only the shell path covers and
you do not want to enable the API shell fallback.

A `FakeBackend` also exists for offline and test usage. It implements the
same interface so the daemon can run without a real Proxmox host.

## Where to go next

- Switch backend with a token: [Configure the Proxmox API backend](../how-to/configure-proxmox-api-backend.md).
- All keys and defaults: [Configuration reference](../reference/configuration.md).
- Why Proxmox at all: [Why Proxmox](why-proxmox.md).
- How the backend fits into the daemon: [Architecture](architecture.md).
