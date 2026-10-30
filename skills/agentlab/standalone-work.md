# Standalone sandbox work

Choose this path for authorized interactive work that does not use a job.
Read the selected profile, capacity, and supported sandbox settings first.

```bash
agentlab --context "$context" sandbox validate --profile "$profile" --json
agentlab --context "$context" sandbox new --profile "$profile" --name "$name" --ttl "$ttl" --json
```

Save the returned VM ID and server identity. Read the sandbox record and events
until readiness is verified. A returned ID alone does not prove provisioning
finished. Use the same context when reconnecting:

```bash
agentlab --context "$context" sandbox show "$vmid" --json
agentlab --context "$context" ssh "$vmid"
```

Do not submit a job for this sandbox as a second provisioning step.
Standalone creation does not yet provide the job request-key contract. If creation
has an ambiguous response, inspect sandbox inventory and existing records before
creating anything else.

Capture authorized work and test evidence before cleanup. Standalone work does
not inherit the verified repository result workflow automatically.
Preserve existing workspaces, snapshots, and user changes during recovery.

When the authorized task includes removing its temporary sandbox, inspect that
specific resource and destroy it through AgentLab:

```bash
agentlab --context "$context" sandbox show "$vmid" --json
agentlab --context "$context" sandbox destroy "$vmid" --json
```

Use a force option only when the user's authorized recovery scope requires it.
Do not run global prune or stop-all commands to clean up one task.
If the sandbox is retained, report its ID, lease expiry, and remaining work.
Verify the final record and backend outcome before claiming removal.
