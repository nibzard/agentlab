# Agent notes for agentlab

Guidance for AI agents and contributors who work in this repository. This
file covers the release pipeline. See `README.md` and `docs/` for the rest
of the project.

## Repository identity

The code lives at `nibzard/agentlab` on GitHub. The Go module path is
`github.com/agentlab/agentlab`, which is intentionally different. Do not
change the module path. It appears in `go.mod`, the package imports, and
the `-ldflags` in the Makefile and `.goreleaser.yaml`.

User-facing URLs point at the real repo:

- `scripts/install.sh` sets `REPO="nibzard/agentlab"`.
- The README, `docs/releases.md`, and `CONTRIBUTING.md` link to
  `nibzard/agentlab`.
- The release workflow publishes to `nibzard/agentlab` through
  `GITHUB_TOKEN`, which always targets the current repository.

If you rename the org or move the repo, change the module path, the `REPO`
variable, and the doc URLs together.

## Release pipeline

Releases are automated. A push of a `v*` git tag triggers the `release`
workflow, which runs GoReleaser to build, package, and publish a GitHub
Release. Do not build or upload release archives by hand.

### Key files

- `.goreleaser.yaml` - build, archive, checksum, and changelog config.
- `.github/workflows/release.yml` - the tag-triggered workflow that runs
  GoReleaser with `GITHUB_TOKEN`.
- `scripts/install.sh` - the one-liner installer. It downloads a release
  archive by name.
- `internal/buildinfo` - holds `Version`, `Commit`, and `Date`. The values
  are injected at build time through `-ldflags`.

### Version source

The git tag is the source of truth. GoReleaser reads the version from the
tag. Keep the `VERSION` file in sync before you tag. It is a human-readable
record; the build does not read it.

### Archive name contract

The installer downloads an archive named
`agentlab_<tag>_<os>-<arch>.tar.gz`. For example,
`agentlab_v0.3.0_linux-amd64.tar.gz`. Note the hyphen between the OS and
the architecture, and the `v` prefix on the tag. The GoReleaser
`name_template` matches this exactly. Do not change one without the other,
or `install.sh` breaks.

### What a release contains

- `agentlab` CLI - `linux` and `darwin`, `amd64` and `arm64`.
- `agentlabd` daemon - `linux`, `amd64` and `arm64`.
- `checksums.txt` - SHA-256 sums for every archive.

The `agentlab-ssh-gateway` binary is not part of the release. It builds
with the `sshgateway` build tag and stays out of scope.

Cross-compilation uses `CGO_ENABLED=0`, so all targets build on a Linux
runner without a C cross-toolchain.

### Cut a release

1. Confirm CI is green on `main`.
2. Set the `VERSION` file to the new version. For example, `0.3.1`.
3. Commit the version bump.
4. Tag the commit and push the tag:

   ```bash
   git tag v0.3.1
   git push origin v0.3.1
   ```

5. Watch the `release` workflow. It creates the GitHub Release.

Pre-release tags such as `v1.2.3-rc.1` or `v1.2.3-beta.1` publish as
GitHub prereleases.

### Validate before you tag

Build the archives locally without a publish. GoReleaser writes them to
`dist/`, which is gitignored.

```bash
make release-check      # validate the GoReleaser config schema
make release-snapshot   # build all targets into dist/, no publish
```
