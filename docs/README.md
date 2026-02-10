# Documentation index

This directory holds the canonical documentation for AgentLab. The docs are written for
operators running a Proxmox host, developers extending the system, and contributors
making changes to the repo.

## Start here

- Operator setup and day-2 tasks: `runbook.md`
- Remote CLI usage (tailnet): `remote-cli.md`
- Common issues and fixes: `troubleshooting.md`
- System architecture and state model: `architecture.md`, `state-model.md`
- Configuration reference: `configuration.md`
- Local control API reference: `api.md`
- Secrets handling: `secrets.md`
- Security notes: `security.md`
- Upgrades and migrations: `upgrading.md`
- Testing guidance: `testing.md`
- Performance notes: `performance.md`
- Proxmox-specific notes: `proxmox.md`
- Frequently asked questions: `faq.md`

## Where to put new content

- Runbook procedures, checklists, and operational commands: `runbook.md`
- Troubleshooting, symptoms, and step-by-step fixes: `troubleshooting.md`
- Architecture, data flow, and design decisions: `architecture.md`
- State transitions or lifecycle behavior: `state-model.md`
- Configuration options and defaults: `configuration.md`
- Upgrade notes and breaking changes: `upgrading.md`
- Security, credentials, and secrets workflows: `security.md`, `secrets.md`
- API request/response references: `api.md`
- Performance tuning and benchmarking: `performance.md`
- Proxmox-specific constraints or guides: `proxmox.md`

If the content does not fit one of the above, create a new doc in `docs/` and add it
here. Prefer adding a short section to an existing doc over creating a brand-new file.

## Templates and style

- Style guide: `STYLE.md`
- Feature doc template: `templates/feature.md`
- Troubleshooting template: `templates/troubleshooting.md`
- ADR template: `templates/adr.md`

Use the templates when creating new docs, and follow the style guide for headings,
code fences, and warnings.
