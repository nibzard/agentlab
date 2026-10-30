# Diagnose the selected connection

Start with the saved context and server identity. Do not change endpoint, token,
or transport until the failure is understood.

```bash
agentlab context list --json
agentlab --context "$context" capabilities --json
agentlab --context "$context" status --json
agentlab --context "$context" sandbox show "$vmid" --json
```

Distinguish an unreachable endpoint from authentication, scope, identity, or
compatibility failures. Preserve the corrective action from the JSON error.
An authorization denial is not a reason to switch to the trusted local socket.

Inspect the sandbox's recorded state, IP, network mode, and available live
resource information before attempting guest access.

```bash
agentlab --context "$context" logs "$vmid" --tail 200
agentlab --context "$context" ssh "$vmid"
```

Use an existing SSH jump setting when direct subnet access is unavailable.
Connection setup accepts jump host and user options without a second daemon token.
Do not install a new tunnel or transport for an already working connection.
Tailcat is not currently advertised as implemented.

When guest SSH or Tailscale prerequisites are missing, inspect the selected
profile and run `agentlab secrets validate --json` through the selected context.
Keep credential values in protected files or supported secure input paths.
Do not copy secrets into command arguments, agent prompts, or diagnostic reports.
Changing shared guest access material requires authority for that shared change.
