# Documentation style guide

This guide keeps AgentLab docs consistent and easy to review. Use it for all new and
updated docs.

## Headings

- Use exactly one H1 (`#`) per file.
- Use sentence case for headings.
- Do not skip heading levels (H1 -> H2 -> H3).
- Keep headings short and descriptive.

## Code fences

- Always include a language tag on fenced blocks.
- Use `bash` for commands, `text` for output, `yaml` for config, `json` for API
  payloads, `ini` for systemd snippets, and `toml` for TOML files.
- Keep commands and output in separate blocks.
- Avoid shell prompts (`$`, `#`) inside command blocks.

Example:

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
- Keep output short and representative. Use ellipses only when necessary.

## Config snippets

- Show the minimal key paths needed for the topic.
- Use placeholders instead of real secrets: `<token>`, `<hostname>`, `REDACTED`.
- Add short inline comments for defaults or important context.
- Prefer YAML for config examples unless the actual file format differs.

## Notes and warnings

Use GitHub-style admonitions for notes and warnings:

```text
> [!NOTE]
> Keep notes short and action-oriented.

> [!WARNING]
> Call out risky or destructive operations.
```

Use `NOTE` for helpful context, `WARNING` for risky operations, and `IMPORTANT` for
backwards compatibility or security-sensitive changes.

## Links and references

- Use relative links for other docs in this repo.
- Link to the most specific section possible.
- Verify link anchors when headings change.

## Tone

- Use active voice and imperative mood for steps.
- Prefer concrete commands over vague instructions.
- Keep sentences short and avoid jargon unless it is defined.
