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
```

For the full development and docs-as-code workflow, see
../meta/contributing.md.
