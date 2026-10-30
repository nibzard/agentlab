# Releases and versioning

AgentLab ships as prebuilt binaries. A `v*` git tag triggers GoReleaser to
build and publish per-target archives plus a SHA-256 checksums file to GitHub.
This page documents the version scheme, archive layout, and build
configuration. For upgrade and rollback steps, see
../how-to/upgrade-and-migrate.md. For installation, see
../tutorials/install-agentlab.md.

## Versioning

AgentLab follows semantic versioning. The version source is the `VERSION` file
(for example, `0.4.0`). Pre-release tags such as `v1.2.3-rc.1` or
`v1.2.3-beta.1` publish as GitHub prereleases.

The release workflow does not bump the `VERSION` file for you. Set it before
you tag.

## Archive layout

Each release publishes one archive per target plus a checksums file.

| Archive | Binary | OS | Arch |
| --- | --- | --- | --- |
| `agentlab_<tag>_<os>-<arch>.tar.gz` | `agentlab` (CLI) | linux, darwin | amd64, arm64 |
| `agentlabd_<tag>_<os>-<arch>.tar.gz` | `agentlabd` (daemon) | linux | amd64, arm64 |
| `checksums.txt` | - | SHA-256 sum of every archive | - |

The CLI archive name matches `scripts/install.sh`, so the one-liner installer
works against a published release:

```bash
curl -fsSL https://raw.githubusercontent.com/nibzard/agentlab/main/scripts/install.sh | bash
```

## Build configuration

GoReleaser builds are defined in `.goreleaser.yaml`:

- `CGO_ENABLED=0` for static cross-compilation.
- Build flags (`-s -w`) strip debug information.
- Version, commit, and date are injected into
  `github.com/agentlab/agentlab/internal/buildinfo` through `-X` ldflags.

The Go module path is `github.com/agentlab/agentlab`. The user-facing repository
and release target is `nibzard/agentlab`. The two are intentionally different;
the module path is the import path, and the repository path is where releases
and the installer live.

## Version commands

```bash
agentlab --version
agentlabd --version
```

See reference/cli.md for the CLI and reference/agentlabd-flags.md for the
daemon.

## Cut a release

1. Merge your changes to `main`. Confirm CI is green.
2. Update the `VERSION` file to the new version, for example `0.4.0`.
3. Commit the bump.
4. Tag the commit and push the tag:

    ```bash
    git tag v0.4.0
    git push origin v0.4.0
    ```

5. Watch the release workflow. It builds the archives and creates the GitHub
   Release.

## Validate a release locally

Build the archives without publishing. GoReleaser writes them to `dist/`.

```bash
make release-check       # validate the GoReleaser config schema
make release-snapshot    # build all targets into dist/, no publish
make release-smoke       # verify packages and native entry points
```

The smoke check verifies all six archive names, checksums, and binary architectures.
It runs the native client outside the checkout. It checks the embedded dashboard,
browser enrollment, remote authorization, and local result export. A Linux run
also executes the packaged daemon's result helper. All network requests use a
local fixture. The check does not provision a host or publish a release.

For the full development and docs-as-code workflow, see
../meta/contributing.md.

The CLI embeds the maintained agent skill. `agentlab skill show --json` inspects
its metadata. `agentlab skill export --json` provides the complete skill without
a checkout or a daemon connection. Package smoke checks verify the exported
bytes against the advertised digest and preserve modified instructions on retry.

The native package smoke check uses Git, Python 3, and OpenSSH `ssh-keygen`.
It creates an ephemeral signing key in its private temporary directory and checks
protected stdin inspection with the packaged CLI. No key is enrolled on a server.
The output verifies matching revocation identifiers without printing credentials.

The native smoke check also saves a default context through protected stdin.
It checks the private fallback file, resolves that credential for a status call,
moves the credential, resumes cleanup, and disconnects. The fixture uses a
temporary configuration directory and a local HTTP server. It does not access
the desktop credential service or establish native macOS Keychain acceptance.

## Verify native macOS credentials

The client archives include `third_party/purego/LICENSE` and
`third_party/purego/NOTICE.txt`. The pinned binding calls system frameworks on
macOS without a C compiler. Linux continues to use Secret Service or file storage.

On a Mac with an accessible, unlocked default Keychain, run:

```bash
go test -tags=integration ./internal/connection -run '^TestKeychainNative'
python3 scripts/tests/native_keychain_smoke.py --client /path/to/extracted/agentlab
```

The Go checks cover native persistence, configuration isolation, and recovery
after client termination. The package check covers connection, replacement,
migration to files and back, and disconnect. It creates only disposable records.
It never creates, unlocks, or selects a Keychain. If cleanup fails, it reports
the retained fixture directory for inspection.

Pass `--previous-client /path/to/previous/agentlab` to test an upgrade. The earlier
client must already support `macos-keychain`. The check creates a native context
with that client, then reads and replaces it with the new client. A macOS access
denial fails the check without a hidden prompt. Restore application access
through macOS before retrying. Linux smoke checks cannot satisfy this native gate.
