# Security review 2026-08-14: findings and remediation tasks

Full review of the agentlab repository on `main` (commit 37ca197). Two audit
passes ran. Pass 1 covered the daemon API, auth, and secrets surfaces. Pass 2
ran seven specialized lenses, an adversarial verification round, and a
completeness round over host scripts and guest-side runners.

Every finding below survived independent verification. A refuter agent tried
to disprove it, and a tracer agent built the full attack path in code. Both
scored the finding at 8/10 confidence or higher. One candidate was refuted and
excluded; its reasoning is folded into finding F5.

Most control-plane findings need one deployment precondition: the TCP control
listener (`control_listen`) with scoped SSH tokens. This is the documented
remote-control deployment. It is the deployment the `Scope` claim exists for.

Severity counts: 6 High, 5 Medium, 2 Low.

## Remediation status 2026-08-22

All 35 tasks are implemented and verified on the `security-fixes` branch
(uncommitted working tree). Every implementation agent audited the fix against
the task's done-when criterion, and three adversarial reviewers re-walked each
original exploit chain against the final code: F1 through F13 plus the T16
hardening are all closed. Gates: `gofmt` clean on all 46 changed Go files,
`go build ./...` and `go vet` pass, and `go test -count=1` passes in all 25
packages. Changed shell scripts pass `bash -n`; the network ruleset passes
`nft -c` template validation and a live namespace lab test; the guest runner
was exercised against planted hooks, smudge filters, and `insteadOf` traps.

Follow-up backlog found during implementation (not part of the original 35):

- **T36** — `JobOrchestrator.Run` still destroys on any failure with a live
  VMID, including pre-clone failures, and the auto-allocated VMID is never
  checked against the Proxmox inventory (`job_orchestrator.go` around 942;
  lease GC can also destroy an untracked VM). Same hazard class as F1.
- **T37** — The sandbox-secret requirement (T12) blocks the documented LLM
  proxy flow: `images/agent-claude` points `ANTHROPIC_BASE_URL` at
  `/proxy/llm` but nothing injects `X-AgentLab-Sandbox-Secret` into Claude
  Code requests, so every LLM call returns 403. Needs a header-injecting
  wrapper or per-sandbox env wiring. Fail-closed until fixed.
- **T38** — `addUserKey` in `user_api.go` stores the raw key line as the
  fingerprint, so added keys cannot be removed by their real SHA256
  fingerprint. Pre-existing defect.
- **T39** — `RunnerAPI` (`/v1/runner/*`) on the bootstrap mux still trusts
  source IP alone. Extend the sandbox-secret check there.
- **T40** — Document the sandbox-secret header and the new
  `integration_target_allowlist` key: `docs/reference/configuration.md`,
  `http-api.md`, `secrets-delivery-model.md`,
  `control-plane-and-trust-boundaries.md`, and the isolation explainer.
- **T41** — Cleanups: prune the unused client-side `TargetIP` field in
  `cmd/agentlab/api.go`; wire `DeleteSandboxSecret` into sandbox destroy; the
  authz test plane mirrors the daemon registration set by hand, so consider a
  mechanical parity guard for future routes.

## Summary

| ID | Severity | Location | Finding | Tasks |
|----|----------|----------|---------|-------|
| F1 | High | `internal/daemon/api.go:2035` | Sandbox create with caller VMID destroys an unrelated Proxmox VM | T01-T03 |
| F2 | High | `internal/daemon/api.go:3194` | Exposure create forwards to an arbitrary host:port | T04-T06 |
| F3 | High | `internal/dashboard/static/assets/app.js:511` | Stored XSS in the dashboard Exposures view | T07-T10 |
| F4 | High | `scripts/net/agent_nat.nft:13` | No L2 isolation or ARP/IP anti-spoofing; tenant identity is source IP | T11-T12 |
| F5 | High | `internal/daemon/api.go:1740` | Detached workspace attach has no scope or ownership check; residue executes code | T13-T16 |
| F6 | High | `internal/daemon/secrets_api.go:57` | SecretsAPI has no per-route authorization | T17-T19 |
| F7 | Medium | `scripts/net/agent_nat.nft:26` | nftables filters only the forward chain; host services reachable | T20-T21 |
| F8 | Medium | `internal/proxy/publisher.go:138` | Exposure name collisions hijack another exposure's hostname | T22-T23 |
| F9 | Medium | `internal/dashboard/server.go:428` | Dashboard on loopback re-exposes the group-gated daemon socket | T24-T25 |
| F10 | Medium | `internal/daemon/sandbox_inventory.go:45` | Sandbox inventory skips the scope filter | T26-T27 |
| F11 | Medium | `internal/daemon/integration_api.go:32` | IntegrationAPI CRUD reachable with any scoped token | T28-T30 |
| F12 | Low | `internal/daemon/pool_api.go:24` | Pool status route performs no authorization | T31 |
| F13 | Low | `internal/daemon/user_api.go:21` | UserAPI mutations have no gate (latent until registry is wired) | T32 |

## Root cause behind four findings

F1, F2, F5, and F6 share one flaw: routes call `authorize(perm, nil, false)`
even when the request carries a concrete target VMID in the body.
`internal/daemon/authz.go:120-125` skips the scope check whenever the resolver
is nil. The header comment justifies command-only checks for routes with "no
single target VMID". But sandbox create, exposure create, job create
(`api.go:344`), and workspace attach all name one target. Fix the resolver
wiring across these routes and the whole class closes. See tasks T03 and T33.

## Findings

### F1: Sandbox create destroys an unrelated Proxmox VM (High)

`POST /v1/sandboxes` accepts a `vmid` from the body. The handler validates
only that it is positive (`api.go:2035-2038`). The route authorizes with a
nil resolver (`api.go:1551`), so a token scoped to other VMIDs is never
checked against the requested VMID. When the target VMID is occupied in
Proxmox, `backend.Clone` fails. The failure-cleanup defer
(`internal/daemon/job_orchestrator.go:431-442`) then calls
`sandboxManager.Destroy(vmid)`. Destroy needs only the database row that the
handler just created (`internal/daemon/sandbox_manager.go:796-806`). It runs
`qm stop` and `qm destroy <vmid> --purge 1`
(`internal/proxmox/shell_backend.go:257,291`).

Exploit: a token with only `sandbox.create`, scoped to `sandbox:2000`, posts
`{"profile":"default","vmid":100}`. VM 100 is an untracked production VM.
The clone fails, and cleanup destroys VM 100 and purges its disks. A second
variant uses a short lease so lease GC destroys the victim
(`sandbox_manager.go:852-901`). Verified with a live test.

Fix: never trust a caller-supplied `vmid`. Verify it is free in the Proxmox
inventory before the row is inserted, and scope-check it. Make the cleanup
destroy conditional on the clone having succeeded.

### F2: Exposure create forwards to an arbitrary host:port (High)

`handleExposureCreate` accepts a client-supplied `target_ip`. It validates it
only with `net.ParseIP` (`api.go:3189`). The CLI never sends this field. The
route authorizes with a nil resolver (`api.go:1909`) even though the request
carries a `vmid`. The `ip:port` pair flows to `tailscale serve --tcp=N
tcp://IP:N` on the daemon host (`internal/daemon/tailscale_serve.go:55`, the
default publisher) and to a Caddy `reverse_proxy` upstream on a public
subdomain (`internal/proxy/publisher.go:140,161`). No layer re-checks the
target. The DELETE route does resolve scope (`api.go:1936`), so create is
drift, not policy.

Exploit: a token scoped to `sandbox:101` with `exposure` permission posts
`{"name":"pivot","vmid":101,"port":8006,"target_ip":"127.0.0.1"}`. The daemon
publishes the Proxmox VE API on `127.0.0.1:8006` to the whole tailnet, or to
a public subdomain under Caddy. Other tenants' sandbox IPs, LAN hosts, and
the daemon's own listeners also work as targets.

Fix: drop `target_ip` from the API. Derive the target from the referenced
sandbox row. Pass a resolver so scope covers the VMID. Reject loopback,
link-local, and non-agent-subnet targets.

### F3: Stored XSS in the dashboard Exposures view (High)

The Exposures table builds rows by string concatenation into
`tbody.innerHTML`. The Remove button is `onclick="removeExposure('" +
esc(ex.name) + "')"` (`app.js:511-513`). The `esc()` helper (`app.js:115-120`)
escapes only `&`, `<`, `>` — not quotes. A `'` breaks out of the JS string.
A `"` breaks out of the attribute. The daemon accepts any non-empty exposure
name (`api.go:3147-3155`) and returns it verbatim. The dashboard proxies it
on every 5-second refresh. No CSP is set anywhere. Both verifiers executed
payloads in Chromium: hover and click handlers fired.

Exploit: a holder of a token with only `exposure.create` stores a crafted
name. When an operator's dashboard renders the row, the payload runs in the
dashboard origin. The `/api/*` proxy is a fully trusted Unix-socket client of
the daemon. Same-origin script can call `stop_all`, `prune`, per-VMID
`destroy`, and job create with an attacker-chosen `repo_url`. The browser
token does not help: the payload is same-origin and reads `sessionStorage`.

Fix: build rows with DOM APIs and `addEventListener`. At minimum, make
`esc()` encode quotes and escape the JS-string context separately. Validate
exposure names server-side against `^[a-z0-9][a-z0-9-]{0,62}$`.

### F4: No L2 isolation; tenant identity is source IP (High)

All sandboxes share one flat bridge (`scripts/net/setup_vmbr1.sh:94-100`).
The shipped nftables table has only an `inet forward` chain plus NAT. There
are no bridge-family rules, no ARP filtering, and no MAC/IP binding. No
script enables the Proxmox firewall or ipfilter. But the guest-facing trust
model is the TCP source address: `/proxy/{name}` resolves the caller with
`GetLiveSandboxByIP(r.RemoteAddr)`
(`internal/daemon/integration_proxy_api.go:136-149`), and metadata uses
`GetSandboxByIP` (`internal/daemon/metadata_api.go:213-226`). Untrusted root
inside a sandbox — the stated threat model — controls its network stack.

Exploit: sandbox A sends gratuitous ARP for victim B's IP, then opens TCP to
`10.77.0.1:8844` with source IP = B's IP. The daemon attributes the
connection to B. `MatchesSandbox` passes B's attachments. The git, LLM, and
HTTP proxies inject B's stored credentials into attacker-chosen requests and
stream back the responses (`internal/integrations/git_proxy.go:82-121`,
`http_proxy.go:108-135`, `llm_proxy.go:157-168`). A clones B's private repos
and impersonates B's LLM account. The audit log frames B. A 404-versus-403
oracle and ARP scanning make enumeration self-serve.

Fix: enforce per-tenant L2 identity — Proxmox firewall with a per-NIC
ipfilter-set, or bridge-family nftables rules that drop frames whose MAC or
IP source does not match the allocation. Stop deriving tenant identity from
source IP alone. Bind a per-sandbox secret issued at bootstrap into the proxy
path.

### F5: Detached workspace attach has no scope or ownership check (High)

`handleWorkspaceByID` resolves the token's sandbox scope from the workspace's
current attachment (`authz.go:157-163`). A detached workspace returns 0, so
the scope is never consulted. The `vmid` in the body of
`POST /v1/workspaces/{id}/attach` is never scope-checked at all
(`api.go:2336-2350`). `WorkspaceManager.Attach` has no lease or ownership
check (`workspace_manager.go:155-208`), unlike rebind, fork, and fsck. The
list endpoint leaks every detached workspace's ID and name to scoped tokens
(`api.go:2261-2267`). Residue persists by design: destroy and expire only
detach the volume (`sandbox_manager.go:834,885,984`), guest cleanup skips
`/work` (`scripts/guest/agent-secrets-cleanup:71-74`), and the runner reuses
the surviving `/work/repo/.git` and runs `git fetch` and `git checkout`
(`scripts/guest/agent-runner:442-464`).

Exploit: a token scoped to `sandbox:1002` with `workspace.attach` lists
detached workspaces, attaches the victim's workspace W into its own sandbox
(verified 200 even for a fully out-of-scope VMID), and reads everything under
`/work`. To escalate, it plants `/work/repo/.git/hooks/post-checkout` or a
filter driver, then detaches — allowed, because the attachment now resolves
to its own in-scope VM. The victim's next job checks out through the planted
hook and runs attacker code with the victim's credentials.

Caveat: verification refuted a near-identical candidate on the grounds that
workspace ownership does not exist as a concept, so a `workspace.attach`
grant arguably covers the shared pool. Two parts survive that argument: the
body VMID is never scope-checked, and the residue chain executes code inside
the victim's sandbox. Both cross the scoped-token boundary that the codebase
documents and tests.

Fix: scope-check the requested target VMID in attach. Reject detached
workspaces whose last attachment was outside the caller's scope, or add
ownership. Mirror the lease checks from rebind into plain Attach. Treat a
reused `/work/repo/.git` as untrusted.

### F6: SecretsAPI has no per-route authorization (High)

The TCP control listener serves `localMux` through `authMw.WrapNetwork`
(`daemon.go:589`), which authenticates only. Its contract says handlers
enforce authorization. `ControlAPI` does this via `authorize`
(`authz.go:107`), and `/v1/exec` does it via `execAllowed`
(`internal/api/exec.go:376`). `SecretsAPI` is registered on the same mux
(`daemon.go:467`), and none of its handlers check anything. No `secrets.*`
permission exists, and the deny-by-default matrix in
`internal/daemon/api_authz_test.go` does not cover these routes.

Exploit: a holder of any scoped token sends `PUT /v1/secrets/env` or
`PUT /v1/secrets/git`. Authentication passes, no authorization runs, and the
shared secrets bundle is mutated. The bundle ships to every subsequent
sandbox at bootstrap (`internal/daemon/bootstrap_api.go:141,167-169,317-333`).
The attacker gains environment injection into all future sandboxes,
replacement of the git token and `KnownHosts` (a path to MITM sandbox git
fetches), and Tailscale re-enrollment into an attacker-controlled tailnet.

Fix: gate every SecretsAPI mutation on trusted or full-access identity,
mirroring `execAllowed`. Add `secrets.*` permissions, extend the test matrix,
and fix the stale comment at `daemon.go:584-588`.

### F7: nftables filters only the forward chain (Medium)

The only filter chain hooks `forward`. Packets addressed to the host itself
take the INPUT path, which the table never touches. The private-range and
tailnet drops (`agent_nat.nft:23,26`) match forwarded traffic only. Nothing
in the repo enables the Proxmox firewall. The shipped smoke test
(`scripts/net/smoke_test.sh:136,270-302`) defines this reachability as FAIL,
and the docs promise the ruleset denies any path into the host network.

Exploit: untrusted code in any sandbox connects to `10.77.0.1:8006` (the
Proxmox VE API), `10.77.0.1:22` (host sshd), the host's LAN address, or its
tailscale0 address. Verified empirically: host-bound connections complete
with zero forward-chain hits. Full compromise still needs a credential or a
service flaw, but the hypervisor management API is one unauthenticated hop
from the actor the product contains.

Fix: add an `input` chain that drops new connections from the agent bridge
except the declared guest-facing ports (8844, 8846, and the metadata DNAT).
Extend the smoke test.

### F8: Exposure name collisions hijack a hostname (Medium)

Uniqueness is enforced on the raw name only (`api.go:3166`). The published
hostname is `sanitizeSubdomain(name) + "." + domain`
(`publisher.go:138-139`). Sanitization lowercases and strips everything
outside `[a-z0-9-]`, so `"SBX-202-443"`, `"sbx_202_443"`, and `"sbx 202 443"`
publish the same FQDN. `CaddyClient.AddRoute` deletes the existing route for
that host and installs the new upstream (`internal/proxy/caddy.go:88-100`).
The certificate is re-issued for the same FQDN, so the takeover is silent.

Exploit: an attacker with `exposure.create` creates an exposure named
`"SBX-202-443"` pointing at their own sandbox. All traffic to the victim's
`https://sbx-202-443.<domain>` reaches the attacker with a valid
certificate. Names follow the deterministic `sbx-<vmid>-<port>` scheme, and
`exposure.list` reveals them.

Fix: compute the sanitized subdomain at create time and enforce uniqueness
on it. Validate names to the subdomain-safe charset server-side.

### F9: Dashboard on loopback re-exposes the daemon socket (Medium)

The installer gates the daemon socket at `0660 root:agentlab`
(`scripts/install_host.sh:526-560`). The dashboard skips the browser-token
check on loopback binds by default (`server.go:96-103`, `server.go:428`;
defaults in `cmd/agentlab-dashboard/main.go:53-56`). The remaining CSRF check
only inspects two headers that a local process sets freely. Every `/api/*`
route is forwarded verbatim to the trusted socket.

Exploit: any local UID outside group `agentlab` sends
`POST /api/v1/sandboxes/stop_all` with `X-Requested-With: XMLHttpRequest`
and a matching `Origin` header — plus destroy, provision, prune, exposure
delete, and workspace detach — with no credentials. Verified live against
the documented default configuration.

Fix: require an inbound token on every bind. Drop the loopback exemption, or
generate a token at startup and print it.

### F10: Sandbox inventory skips the scope filter (Medium)

`GET /v1/sandboxes/inventory` authorizes `permSandboxList` but never applies
`sandboxScopeFilter`. The sibling `GET /v1/sandboxes` authorizes the same
permission and filters (`api.go:1949-1953`). `authz.go:30` states that list
routes must filter. The response includes every managed sandbox's VMID,
name, profile, agent IP, tags, and Tailscale names, plus unmanaged Proxmox
VMs. Verified with a live run through the real `WrapNetwork` stack.

Exploit: a token scoped to `sandbox:1001` receives every other tenant's
records. Read-only, but it hands out the VMIDs that F1, F2, and F5 accept.

Fix: filter records with `sandboxScopeFilter` before serialization. Drop
unmanaged-VM records for scoped callers. Add the route to the test matrix.

### F11: IntegrationAPI CRUD reachable with any scoped token (Medium)

`IntegrationAPI` is registered on `localMux` (`daemon.go:504-505`). Its
create, list, get, and delete handlers never check the caller's scope.
`Integration.Validate` (`internal/integrations/types.go:168-217`) imposes no
scheme or host restriction on `Target`. No `integration.*` permission exists.

Exploit: a scoped-token holder deletes the `github` integration, then
recreates the name with `type=git-proxy` and an attacker-controlled
`target`. The sandbox-side proxy resolves integrations by name at request
time (`internal/daemon/integration_proxy_api.go:72-90`) and forwards to
`integ.Target`. Every sandbox cloning through
`http://169.254.169.254/proxy/github/...` now fetches attacker content.
`GET /v1/integrations` also discloses internal target hosts and usernames.
Requires the opt-in `integrations_enabled: true`.

Fix: gate all integration mutations on trusted or full-access identity.
Validate `Target` against a scheme and host allowlist for proxy types.

### F12: Pool status route performs no authorization (Low)

`/v1/pool/status` is registered on `localMux` (`daemon.go:456`), which the
TCP control listener also serves. The handler checks only the HTTP method
and returns `pool.Status()`. Its `Allocations` list contains every sandbox's
VMID, name, profile, cores, and memory. A zero-permission token gets a 200.
The route is outside the deny-by-default matrix, which builds its mux from
`api.Register` only.

Fix: route it through `ControlAPI` with an `authorize` call, scope-filter
allocations, and add it to the matrix.

### F13: UserAPI mutations have no gate (Low, latent)

`/v1/users*` and `/v1/teams*` handlers never call `authorize`. `addUser`
accepts `role: "admin"`. Today the registry is inert: nothing reads it, and
`auth.NewMiddleware` loads keys only from `AuthorizedKeysPath`. The gap
becomes a live escalation path the moment the registry is wired into
authentication.

Fix: gate all UserAPI mutations on trusted or full-access identity now,
before the registry is connected.

## Atomic tasks

Each task is one change with one verifiable outcome. Order is by finding
severity. Mark a task done only when its test or check passes.

### F1: sandbox create (High)

- [x] **T01** — Reject caller-supplied VMIDs that are occupied. In
  `handleSandboxCreate` (`internal/daemon/api.go:2035`), call
  `backend.ListVMs` and return 409 when the requested `vmid` matches any
  existing Proxmox VM. Test: create with an occupied VMID returns 409 and
  provisions nothing.
- [x] **T02** — Make the failure-cleanup destroy in `ProvisionSandbox`
  (`internal/daemon/job_orchestrator.go:431-442`) conditional on the clone
  having succeeded. A pre-clone failure cannot have created the VM. Test:
  clone failure produces no `backend.Destroy` call.
- [x] **T03** — Pass a sandbox resolver to `authorize` on the sandbox create
  route (`api.go:1551`) so scope covers the requested VMID. Test: a token
  scoped to `sandbox:2000` cannot create VMID 100.

### F2: exposure create (High)

- [x] **T04** — Remove `target_ip` from `V1ExposureCreateRequest`
  (`internal/daemon/api_types.go:658`) and derive the target IP from the
  referenced sandbox row (`api.go:3182-3192`). Test: request with
  `target_ip` is ignored or rejected.
- [x] **T05** — Pass a resolver returning `req.VMID` to `authorize` on
  `POST /v1/exposures` (`api.go:1909`). Test: a token scoped to
  `sandbox:101` cannot create an exposure for VMID 202.
- [x] **T06** — Reject loopback, link-local, and non-agent-subnet targets in
  the publish path as defense in depth. Test: unit test over the rejected
  address classes.

### F3: dashboard XSS (High)

- [x] **T07** — Rebuild the Exposures table with `document.createElement`
  and `textContent`, and attach Remove handlers with `addEventListener`
  (`internal/dashboard/static/assets/app.js:470-525`). Test: an exposure
  name containing `'` and `"` renders as text and Remove still works.
- [x] **T08** — Audit every other `esc()` call site in `app.js` for the same
  quote-escaping flaw in attribute and JS-string contexts. Fix each unsafe
  site the same way. Test: grep shows no `esc(` interpolation inside
  attribute or handler strings.
- [x] **T09** — Validate exposure names server-side in
  `handleExposureCreate` (`internal/daemon/api.go:3147`) against
  `^[a-z0-9][a-z0-9-]{0,62}$`. Test: a name with quotes returns 400.
- [x] **T10** — Add a `Content-Security-Policy` header (no `unsafe-inline`
  handlers) to dashboard responses (`internal/dashboard/server.go:179-198,
  422-438`). Test: response carries the header and the dashboard still
  works.

### F4: L2 isolation (High)

- [x] **T11** — Ship bridge-family nftables rules for vmbr1 that drop
  frames whose Ethernet source MAC or ARP/IP source does not match the
  allocation for that tap (`scripts/net/agent_nat.nft`,
  `scripts/net/apply.sh`), or document and script the equivalent Proxmox
  ipfilter-set per sandbox NIC. Test: a VM that assumes a neighbor's IP
  cannot complete a TCP handshake to `10.77.0.1:8844` as that neighbor.
- [x] **T12** — Stop deriving tenant identity from source IP alone. Issue a
  per-sandbox secret at bootstrap and require it on `/proxy/{name}` and
  `/metadata/*` (`internal/daemon/integration_proxy_api.go:136-149`,
  `metadata_api.go:213-226`). Test: a request with the right source IP but
  the wrong secret is rejected.

### F5: workspace attach (High)

- [x] **T13** — Scope-check the requested target VMID in
  `handleWorkspaceAttach` (`internal/daemon/api.go:2336-2350`) against the
  token's scope. Test: a token scoped to `sandbox:1002` cannot attach into
  VMID 1001.
- [x] **T14** — Reject `workspace.attach` on detached workspaces whose last
  attachment was outside the caller's scope, or add an owner field to
  `models.Workspace` and enforce it (`internal/daemon/authz.go:157-163`).
  Test: a scoped token cannot attach a foreign detached workspace.
- [x] **T15** — Mirror the lease checks from rebind
  (`internal/daemon/workspace_rebind.go:99`) into
  `WorkspaceManager.Attach` (`workspace_manager.go:155-208`). Test: attach
  of a leased workspace fails.
- [x] **T16** — Treat a reused `/work/repo/.git` as untrusted in
  `scripts/guest/agent-runner:442-464`: reset remotes from the job spec and
  disable config hooks and filters on checkout (for example with
  `git -c core.hooksPath=/dev/null`). Test: a planted `post-checkout` hook
  does not execute on reuse.

### F6: SecretsAPI authorization (High)

- [x] **T17** — Gate every SecretsAPI mutation handler in
  `internal/daemon/secrets_api.go` on trusted or full-access identity,
  mirroring `execAllowed` (`internal/api/exec.go:376`): allow only when
  `auth.FromContext` is nil (Unix socket) or the token `IsFullAccess()`.
  Test: a scoped token gets 403 on `PUT /v1/secrets/env`.
- [x] **T18** — Add `secrets.*` permissions to the table in
  `internal/daemon/authz.go:31-88` and route the handlers through
  `authorize`. Test: unit tests cover each new permission.
- [x] **T19** — Fix the stale comment at `internal/daemon/daemon.go:584-588`
  that claims scoped tokens are rejected on the control listener. Test:
  none (comment-only); reviewed in the same PR as T17.

### F7: host firewall (Medium)

- [x] **T20** — Add an `input` chain to `table inet agentlab`
  (`scripts/net/agent_nat.nft`) that drops new connections arriving on the
  agent bridge to host addresses, except the guest-facing ports (8844,
  8846, and the metadata DNAT port). Test: from a sandbox,
  `10.77.0.1:8006` and `10.77.0.1:22` are unreachable while bootstrap and
  metadata still work.
- [x] **T21** — Extend `scripts/net/smoke_test.sh` to assert that
  `10.77.0.1:8006` and `10.77.0.1:22` are blocked from the sandbox side.
  Test: the smoke test fails when the input chain is removed.

### F8: hostname collision (Medium)

- [x] **T22** — Compute the sanitized subdomain in `handleExposureCreate`
  and reject the request when that subdomain is already published
  (`internal/daemon/api.go:3166`, `internal/proxy/publisher.go:269-286`).
  Test: creating `"SBX-202-443"` when `"sbx-202-443"` exists returns 409.
- [x] **T23** — Remove or limit the `AddRoute` delete-and-replace behavior
  for an existing host (`internal/proxy/caddy.go:88-100`) so a second
  publisher cannot silently displace a live route. Test: adding a duplicate
  host fails instead of replacing.

### F9: dashboard loopback (Medium)

- [x] **T24** — Require an inbound token on every dashboard bind: drop the
  loopback exemption in `validateConfig` (`internal/dashboard/server.go:96-103`),
  or generate a token at startup and print it. Test: starting with no
  `--browser-token` on loopback either fails or prints a generated token.
- [x] **T25** — Update `docs/how-to/run-the-dashboard.md` to remove the
  tokenless loopback example and document the token requirement. Test:
  docs build passes; example commands match the new behavior.

### F10: inventory scope (Medium)

- [x] **T26** — Apply `sandboxScopeFilter` to inventory records before
  serialization and drop unmanaged-VM records for scoped callers
  (`internal/daemon/sandbox_inventory.go:45-53`). Test: a token scoped to
  `sandbox:1001` sees only VMID 1001 in the inventory response.
- [x] **T27** — Add `/v1/sandboxes/inventory` to the scope-filter test in
  `internal/daemon/api_authz_test.go` (the pattern at lines 169-198).
  Test: the matrix fails if the filter is removed.

### F11: IntegrationAPI authorization (Medium)

- [x] **T28** — Gate all integration mutations in
  `internal/daemon/integration_api.go` on trusted or full-access identity,
  or introduce `integration.*` permissions enforced via `authorize`. Test:
  a scoped token gets 403 on `DELETE /v1/integrations/github`.
- [x] **T29** — Validate `Target` in `Integration.Validate`
  (`internal/integrations/types.go:168-217`): allow only `http` and
  `https`, and check the host against an operator-configured allowlist for
  proxy types. Test: a `file://` or allowlist-miss target is rejected.
- [x] **T30** — Require elevated permission to delete and recreate an
  existing integration name. Test: delete-then-create of the same name
  needs the elevated grant.

### F12: pool status (Low)

- [x] **T31** — Route `/v1/pool/status` through `ControlAPI` with an
  `authorize` call, scope-filter allocations, and add the route to the
  matrix (`internal/daemon/pool_api.go:24`, `api_authz_test.go`). Test: a
  zero-permission token gets 403.

### F13: UserAPI gate (Low, latent)

- [x] **T32** — Gate all UserAPI and TeamAPI mutations on trusted or
  full-access identity (`internal/daemon/user_api.go`). Test: a scoped
  token gets 403 on `POST /v1/users` and on key add and remove.

### Systemic (crosses findings)

- [x] **T33** — Audit every `authorize(..., nil, false)` call site in
  `internal/daemon/api.go` and sibling API files. For each route whose
  request body carries a concrete target VMID, pass a resolver (covers job
  create at `api.go:344` in addition to T03 and T05). Test: a checklist in
  the PR records each call site and its decision.
- [x] **T34** — Build the deny-by-default test mux in
  `internal/daemon/api_authz_test.go` from the same registration set as the
  daemon, so `PoolAPI`, `SecretsAPI`, `IntegrationAPI`, and `UserAPI` routes
  cannot escape the matrix again. Test: removing any `authorize` call fails
  the matrix.
- [x] **T35** — Extend the authz tests to assert scope enforcement, not
  just command enforcement, for every route that names a target: request an
  out-of-scope target with an in-scope token and expect 403. Test: the suite
  fails when a resolver is removed from any covered route.

## Suggested order

1. T02, T01, T03 — stop the destructive path first (F1).
2. T17, T18, T19, T28, T31, T32 — close the missing-authorization class
   (F6, F11, F12, F13). These are small and share one pattern.
3. T07, T08, T09, T10 — remove the dashboard XSS (F3).
4. T04, T05, T06, T22, T23 — constrain exposures (F2, F8).
5. T13, T14, T15, T16 — close the workspace attach path (F5).
6. T20, T21, T11 — network isolation (F7, F4).
7. T12 — per-sandbox proxy secret (F4), the largest change; needs its own
   design note.
8. T24, T25, T26, T27, T29, T30, T33, T34, T35 — remaining hardening and
   test debt.
