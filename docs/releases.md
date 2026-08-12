# Releases

agentlab ships as prebuilt binaries. A tag push builds the binaries,
packs them, and publishes a GitHub Release. No manual packaging is needed.

## What a release contains

Each release has one archive per target, plus a checksums file.

- `agentlab_<tag>_<os>-<arch>.tar.gz` holds the CLI. Built for
  `linux` and `darwin` on `amd64` and `arm64`.
- `agentlabd_<tag>_<os>-<arch>.tar.gz` holds the daemon. Built for
  `linux` on `amd64` and `arm64`.
- `checksums.txt` lists the SHA-256 sum of every archive.

The CLI archive name matches `scripts/install.sh`, so the one-liner
installer works against a published release:

```bash
curl -fsSL https://raw.githubusercontent.com/agentlab/agentlab/main/scripts/install.sh | bash
```

## Versioning

agentlab follows semantic versioning. Pre-release tags such as
`v1.2.3-rc.1` or `v1.2.3-beta.1` publish as GitHub prereleases. See
`docs/upgrading.md` for the full policy.

## Cut a release

1. Merge your changes to `main`. Confirm CI is green.
2. Update the `VERSION` file to the new version, for example `0.3.1`.
3. Commit the bump.
4. Tag the commit and push the tag:

   ```bash
   git tag v0.3.1
   git push origin v0.3.1
   ```

5. Watch the **release** workflow. It builds the archives and creates
   the GitHub Release.

The workflow does not bump the `VERSION` file for you. Set it before you
tag.

## Validate a release locally

Build the archives without publishing. GoReleaser writes them to `dist/`.

```bash
make release-check       # validate the config schema
make release-snapshot    # build all targets into dist/, no publish
```
