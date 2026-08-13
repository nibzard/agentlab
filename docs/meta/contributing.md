# Contributing

AgentLab is written in Go and ships as a daemon, a CLI, an optional dashboard, and an SSH gateway. This page covers the repository layout, the build and test targets, the docs-as-code pipeline, and the release flow. Keep a slim `CONTRIBUTING.md` in the repo root that points here.

## Prerequisites

- **Go 1.24.0 or higher.** The `go.mod` toolchain line pins this.
- **Git** for version control.
- **Make** to drive the build targets. Most systems ship it.
- Optional: **Node.js 20+** for `docs-lint` (`markdownlint-cli2` through `npx`).
- Optional: a Proxmox VE host for integration testing. You can develop without one using the mock backends in `internal/testing/`.

Verify the toolchain:

```bash
go version
```

## Repository identity

The code lives at `nibzard/agentlab` on GitHub. The Go module path is `github.com/agentlab/agentlab`, which is intentionally different. Do not change the module path. It appears in `go.mod`, the package imports, and the `-ldflags` in the Makefile and `.goreleaser.yaml`.

User-facing URLs point at the real repo:

- `scripts/install.sh` sets `REPO="nibzard/agentlab"`.
- The README, `docs/reference/releases.md`, and this page link to `nibzard/agentlab`.
- The release workflow publishes to `nibzard/agentlab` through `GITHUB_TOKEN`.

If you rename the org or move the repo, change the module path, the `REPO` variable, and the doc URLs together.

## Repository layout

```text
cmd/
  agentlab/            CLI application (hand-written dispatch, not cobra)
  agentlabd/           Daemon application (flag package)
  agentlab-dashboard/  Optional web UI
  agentlab-ssh-gateway/  SSH gateway (built behind the sshgateway tag)
internal/
  config/              Config load and validation
  daemon/              Daemon logic, HTTP API, and managers
  db/                  SQLite store
  models/              Data models and state constants
  proxmox/             Backend interface and api/shell backends
  secrets/             Secrets store
  testing/             Shared test helpers and mock backends
docs/                  This documentation (MkDocs Material)
scripts/               Setup, network, guest, and dev scripts
Makefile               Build, test, and docs targets
```

## Build

```bash
make build
```

This produces `bin/agentlab` and `bin/agentlabd`, plus the cross-compiled `dist/agentlab_linux_amd64` and `dist/agentlabd_linux_amd64`. Build a single component with `make bin/agentlab` or `make bin/agentlabd`. Build the SSH gateway with `make build-ssh-gateway`. Clean artifacts with `make clean`.

Cross-compile for another platform by setting `GOOS` and `GOARCH`. Cross-compilation uses `CGO_ENABLED=0`, so all targets build on a Linux runner without a C toolchain.

```bash
GOOS=linux GOARCH=arm64 go build -o bin/agentlab-linux-arm64 ./cmd/agentlab
```

## Tests

AgentLab uses a three-tier test approach: unit tests co-located with source, integration tests behind the `integration` tag (fake backend), and end-to-end tests behind the `e2e` tag (real Proxmox).

| Target | What it runs |
| --- | --- |
| `make test` | Unit tests. |
| `make test-coverage` | Unit tests plus `coverage.out` and `coverage.html`. |
| `make coverage-audit` | Coverage breakdown by package and lowest-coverage hotspots, written under `dist/coverage/`. |
| `make coverage-html` | Coverage HTML report under `dist/coverage/coverage.html`. |
| `make test-race` | Tests with the race detector. |
| `make test-integration` | Integration tests with `-tags=integration`. |
| `make test-all` | Unit tests, race detector, and coverage. |
| `make test-ci` | The CI parity run: `quality`, then tests with `-count=1 -shuffle=on`, race, and coverage. |
| `make fuzz` | Go fuzz tests for `cmd/agentlab` parsers and normalizers. Default 10 seconds per target. |

Run a single test or package directly:

```bash
go test -run TestCreateSandbox ./internal/db
go test -v ./internal/config/...
```

Reproduce a fuzzer failure. The `cmd/agentlab/testdata/fuzz/` directory does
not exist by default (only `version.golden` ships under `testdata/`); it is
created on demand only when a fuzz target finds a failing input:

```bash
go test ./cmd/agentlab -run=^$ -fuzz=FuzzNormalizeEndpoint -fuzztime=0
```

## Quality checks

Run the canonical gate before you open a pull request:

```bash
make quality
```

`make quality` runs `gofmt`, `go vet`, `staticcheck`, `govulncheck`, and the docs-as-code suite (`make docs-check`). It installs pinned versions of `staticcheck` and `govulncheck` into `bin/tools` when they are missing.

```bash
make staticcheck
make govulncheck
```

!!! note "govulncheck toolchain"
    `govulncheck` runs under the toolchain pinned by `GOVULNCHECK_GOTOOLCHAIN` in the Makefile. For stdlib findings, upgrade to the latest Go patch release or override the toolchain.

## Docs-as-code pipeline

Every markdown file under `docs/`, plus `README.md`, `CONTRIBUTING.md`,
`AGENTLAB_DEV_SPECIFICATION.md`, and `PROXMOX_SPECS.md`, is checked. Run all
four checks at once:

```bash
make docs-check
```

The four checks:

- `docs-lint` — `markdownlint-cli2` through `npx`. Requires Node.js.
- `docs-links` — `lychee` link check. Install with `make docs-tools`.
- `docs-typos` — `typos` spell check. Install with `make docs-tools`.
- `docs-snippets` — bash and YAML snippet validation plus `agentlab` command drift.

### Snippet rules

The snippet checker reads every `bash`, `sh`, and `yaml` fenced block:

- Bash and shell blocks are syntax-checked with `bash -n`.
- YAML blocks are checked for tabs and indentation that is not a multiple of two spaces.
- Every `agentlab ...` line must match a command prefix from [CLI reference](../reference/cli.md), which is generated from `agentlab --help`. Unknown commands fail CI.

To mark a block as intentionally non-executable, add `skip-snippet-check` to the fence info string, for example <code>```bash skip-snippet-check</code>.

### Generated CLI docs

The [CLI reference](../reference/cli.md) is generated from `agentlab --help`. Never edit it by hand.

```bash
make docs-gen      # regenerate docs/reference/cli.md
make docs-verify   # fail CI if the file has drifted
```

See the [Documentation style guide](style-guide.md) for the full authoring conventions.

## Writing tests

AgentLab uses table-driven tests with the `testify` assertion library.

- Use `require` for setup and critical checks where the test cannot continue.
- Use `assert` when you want to see every failure in a run.
- Co-locate `*_test.go` files with the code they test.
- Clean up with `t.Cleanup()` and `t.TempDir()`.
- Add `t.Helper()` to helper functions.

Use the shared helpers in `internal/testing/`:

```go
import testutil "github.com/agentlab/agentlab/internal/testing"

job := testutil.NewTestJob(testutil.JobOpts{Status: models.JobRunning, Task: "test-task"})
db := testutil.OpenTestDB(t)
path := testutil.TempFile(t, "key: value")
```

### Development without Proxmox

You can develop and test without a Proxmox host. `internal/testing/` ships mock backends and stores:

- `testing.NewMockProxmoxBackend()` simulates VM create, start, stop, and destroy, with configurable delays and failure injection.
- `testing.NewMockSecretsStore()` simulates the secrets store without real encryption.
- `testing.NewMockHTTPHandler()` serves canned responses for HTTP client tests.

Integration tests behind the `integration` tag run against a fake backend and require no external services. Real Proxmox tests live behind the `e2e` tag and need a running Proxmox host with valid credentials.

## Adding a CLI command

The CLI uses a hand-written dispatch switch in `cmd/agentlab/main.go`, not a framework.

1. Add the command to the usage text in `cmd/agentlab/main.go`.
2. Add a `case` to the `dispatch` function.
3. Implement the handler in a new file under `cmd/agentlab/` using the `flag` package and `newAPIClient`.
4. Add the API client method in `cmd/agentlab/api.go`.
5. Add a test in a matching `*_test.go` file.
6. Run `make docs-gen` so the new command appears in [CLI reference](../reference/cli.md).

## Adding a daemon endpoint

The control API is wired through `ControlAPI.Register` in `internal/daemon/api.go`.

1. Define request and response types in `internal/daemon/api_types.go`.
2. Register the route in `Register()` on the right mux.
3. Implement the handler using the shared helpers `decodeJSON`, `writeJSON`, `writeError`, and `writeMethodNotAllowed`.
4. Add a handler test in `internal/daemon/`.
5. Update [HTTP API reference](../reference/http-api.md) and the event contract page if the route emits events.

## Release pipeline

Releases are automated. A push of a `v*` git tag triggers the `release` workflow, which runs GoReleaser to build, package, and publish a GitHub Release. Do not build or upload release archives by hand.

Validate before you tag:

```bash
make release-check      # validate the GoReleaser config schema
make release-snapshot   # build all targets into dist/, no publish
```

Cut a release:

1. Confirm CI is green on `main`.
2. Set the `VERSION` file to the new version, for example `0.4.1`.
3. Commit the version bump.
4. Tag and push the tag.

    ```bash
    git tag v0.4.1
    git push origin v0.4.1
    ```

The git tag is the source of truth for the version. The `VERSION` file is a human-readable record that you keep in sync before you tag.

!!! note "Archive name contract"
    The installer downloads an archive named `agentlab_<tag>_<os>-<arch>.tar.gz`, for example `agentlab_v0.4.0_linux-amd64.tar.gz`. Note the hyphen between OS and architecture and the `v` prefix on the tag. The GoReleaser `name_template` matches this exactly. Do not change one without the other, or `install.sh` breaks.

A release contains the `agentlab` CLI for `linux` and `darwin` on `amd64` and `arm64`, the `agentlabd` daemon for `linux` on `amd64` and `arm64`, and `checksums.txt` with SHA-256 sums. The `agentlab-ssh-gateway` binary is not part of the release. See [Releases and versioning](../reference/releases.md) and [Upgrade and migrate](../how-to/upgrade-and-migrate.md).

## Commit messages and pull requests

Use conventional commits:

```text
<type>(<scope>): <description>

[optional body]
```

Types include `feat`, `fix`, `docs`, `style`, `refactor`, `test`, and `chore`. Keep the subject line short and imperative.

Before you submit a pull request:

- `make quality` is green.
- `make test` passes, and you added tests for new behavior.
- `make docs-check` passes when you touched docs.
- Links resolve and commands are copy-paste ready.
- The generated CLI reference is in sync (`make docs-verify`).
- Upgrade or backwards-compatibility notes are included when behavior changes.
