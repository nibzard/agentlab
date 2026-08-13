# Documentation style guide

This guide keeps AgentLab docs consistent and easy to review. Use it for all new and updated docs. The docs build with MkDocs Material, and a docs-as-code pipeline validates every page in CI.

## Structure

The docs follow a Diátaxis structure. Each page lives in exactly one quadrant, and its folder sets the mode. Pick the quadrant before you write.

| Folder | Mode | Goal |
| --- | --- | --- |
| `tutorials/` | Learning-oriented | Numbered steps that take a newcomer to a concrete win. |
| `how-to/` | Goal-oriented | A recipe for one operator task. |
| `reference/` | Information-oriented | Dry, exhaustive lookup tables. No tutorial prose. |
| `explanation/` | Understanding-oriented | Discursive prose that explains design choices. |
| `meta/` | Project-oriented | This guide, the glossary, and the contributor guide. |

### Where new content goes

- A guided lesson for someone new to AgentLab: `tutorials/`.
- A recipe for one operator task: `how-to/`.
- A dry lookup of commands, flags, config, routes, or states: `reference/`.
- The why behind a design choice: `explanation/`.
- A term definition or a docs convention: `meta/`.

Prefer adding a short section to an existing page over creating a new file. If the content does not fit any existing page, add a new page to the right quadrant and link it from the relevant neighbors.

## Headings

- Use exactly one H1 (`#`) per file. The first line is `# <Title>`.
- Use sentence case for headings.
- Do not skip heading levels. Go H1 to H2 to H3.
- Keep headings short and descriptive.

## Code fences

- Always include a language tag on fenced blocks: `bash`, `sh`, `text`, `yaml`, `json`, `ini`, `toml`, `prometheus`, `mermaid`.
- Use `bash` for commands, `text` for output, `yaml` for config, `json` for API payloads, `ini` for systemd snippets.
- Keep commands and output in separate blocks.
- Avoid shell prompts (`$`, `#`) inside command blocks.

    ```bash
    agentlab status
    ```

Output:

```text
STATUS  HEALTHY
```

## Command output blocks

- Label output with `Output:` or `Expected output:` before the block.
- Use `text` for output blocks, never `bash`.
- Keep output short and representative. Use ellipses only when you must.

## Commands

Every `agentlab ...` line inside a `bash` or `sh` block must be a real command whose prefix appears in [CLI reference](../reference/cli.md). The snippet checker parses each such line and fails CI on unknown commands.

- When unsure, copy the exact usage line from the CLI reference.
- A block that is intentionally non-executable opts out with `skip-snippet-check` in the fence info string, for example <code>```bash skip-snippet-check</code>.
- `sudo agentlab ...` is accepted; the checker skips `sudo` and its value flags.

## Config snippets

- Show the minimal key paths needed for the topic.
- Use placeholders instead of real secrets: `<token>`, `<hostname>`, `REDACTED`.
- Add short inline comments for defaults or important context.
- Use two-space indentation. The YAML check rejects tabs and odd indentation.
- Prefer YAML for config examples unless the real file format differs.

    ```yaml
    control_listen: 127.0.0.1:8845  # loopback; published via Tailscale Serve
    control_auth_token: <token>     # required when control_listen is set
    ```

## Admonitions

Use MkDocs Material admonitions, not GitHub-style blockquotes. The supported qualifiers are `note`, `warning`, `tip`, and `example`.

```markdown
!!! note "Short title"
    Keep notes short and action-oriented.

!!! warning "Destructive operation"
    Call out risky or destructive operations.
```

Use `note` for helpful context, `warning` for risky operations, and `tip` for a better way. Keep the body to a few lines.

## Tables

Use pipe tables for reference data. Pick columns that fit the data, for example `flag | type | default | description`. Keep tables narrow enough to read on a half-width window.

## Links and cross-links

- Use relative links to other docs, and keep the `.md` extension.
- Link to the most specific section possible.
- From `tutorials/` or `how-to/`, a reference page is `../reference/configuration.md`.
- From `reference/` to an explanation page is `../explanation/architecture.md`.
- Within the same folder, use the bare filename, for example `renew-a-lease.md`.
- Verify links when headings or files change. CI runs a link checker.

## Tone

- Use the active voice and the imperative mood for steps.
- Prefer concrete commands over vague instructions.
- Keep sentences short. Aim for 20 words or fewer.
- Spell out each acronym on first use.
- Define one term for each concept and reuse it.

## Page template: feature

Use this skeleton for a new feature page.

```markdown
# Feature: <short name>

## Summary
What this feature does and who it is for.

## Audience
Who should read this doc (operators, contributors, users, or maintainers).

## Goals
- <goal>

## Non-goals
- <non-goal>

## Prerequisites
Required versions, permissions, or environment assumptions.

## How it works
Describe the flow at a high level.

## Configuration
yaml snippet

## Usage
bash command

Output:
text output

## Security and safety
Security implications, sensitive data, access control.

## Observability
Logs, metrics, or events that validate the feature.

## Rollout and upgrade notes
Backwards compatibility and migrations.

## Troubleshooting
Short checklist or links.

## Related docs
- <related doc>
```

## Page template: troubleshooting

Use this skeleton for a symptom-and-fix entry. How-to pages that debug a problem follow the same shape.

```markdown
# <Short problem summary>

## Symptoms
What the user sees and any error messages.

## Environment
Versions, profile names, or host details.

## Checks
bash commands to collect context

Output:
text output

## Root cause
The underlying issue.

## Fix
Step-by-step resolution.

## Validation
How to confirm the fix worked.

## Prevention
How to avoid the issue in the future.

## Related docs
- <related doc>
```

## Page template: architecture decision record

Use this skeleton for an ADR when you record a design decision.

```markdown
# ADR <number>: <short title>

Date: YYYY-MM-DD

## Status
Proposed | Accepted | Deprecated | Superseded

## Context
What problem are we solving? What constraints matter?

## Decision
What was decided and why?

## Consequences
What trade-offs and follow-up work does this create?

## Alternatives considered
Other options and why they were not chosen.

## References
- <reference>
```

## Docs-as-code checks

CI runs `make docs-check`, which aggregates four checks. Run them locally before you open a pull request.

- `docs-lint` — `markdownlint-cli2` through `npx`. Requires Node.js.
- `docs-links` — `lychee` link check. Install with `make docs-tools`.
- `docs-typos` — `typos` spell check. Install with `make docs-tools`.
- `docs-snippets` — bash and YAML snippet validation plus `agentlab` command drift.

The generated [CLI reference](../reference/cli.md) comes from `agentlab --help`. Never edit it by hand. Run `make docs-gen` to refresh it and `make docs-verify` to fail CI when it drifts. See [Contributing](contributing.md) for the full workflow.
