#!/usr/bin/env bash
set -euo pipefail

# Verifies T16: the agent-runner reuse path treats an existing
# /work/repo/.git as untrusted. Every planted trap below is a config path
# that executes a command or redirects a fetch:
#   - a post-checkout hook in the default hooks directory,
#   - a post-checkout hook in a residue core.hooksPath directory,
#   - a smudge filter wired through .git/info/attributes,
#   - the same filter smuggled through an include.path file,
#   - a credential helper,
#   - a core.fsmonitor probe,
#   - a core.sshCommand substitute,
#   - an url.*.insteadOf rewrite,
#   - an http.<url>.sslVerify override,
#   - residue origin URLs (single and multi) and a second residue remote.
# The hardening code is extracted from the real script, so this test fails
# when the shipped sequence changes.

log() {
  printf "[agent-runner-repo-reuse] %s\n" "$*"
}

die() {
  printf "[agent-runner-repo-reuse] ERROR: %s\n" "$*" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TARGET="$ROOT_DIR/scripts/guest/agent-runner"

[[ -f "$TARGET" ]] || die "agent-runner not found at $TARGET"

bash -n "$TARGET" || die "agent-runner fails bash -n"

# Extract the hardening block: from the GIT_HARDEN_ARGS assignment up to the
# clone_or_update_repo call. Sourcing it gives this test the exact functions
# the guest runs.
HARNESS="$(mktemp)"
awk '/^GIT_HARDEN_ARGS=\(/{f=1} f && /^clone_or_update_repo$/{f=0} f' "$TARGET" >"$HARNESS"
grep -q 'core.hooksPath=/dev/null' "$HARNESS" || die "GIT_HARDEN_ARGS missing or changed in agent-runner"
grep -q 'core.fsmonitor=false' "$HARNESS" || die "GIT_HARDEN_ARGS lost the fsmonitor override"
grep -q '^remove_local_sections()' "$HARNESS" || die "remove_local_sections not found in agent-runner"
grep -q '^scrub_residue_config()' "$HARNESS" || die "scrub_residue_config not found in agent-runner"
grep -q '^clone_or_update_repo()' "$HARNESS" || die "clone_or_update_repo not found in agent-runner"
bash -n "$HARNESS" || die "extracted hardening block fails bash -n"

T="$(mktemp -d /tmp/agent-runner-reuse.XXXXXX)"
trap 'rm -rf "$T" "$HARNESS"' EXIT

# Keep the fixtures off any host or global git config.
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export HOME="$T/home"
mkdir -p "$HOME"
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid
export GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
export GIT_TERMINAL_PROMPT=0

ORIGIN_DIR="$T/origin"
git init -q --initial-branch=main "$ORIGIN_DIR" || die "git init origin failed"
printf 'clean blob content\n' >"$ORIGIN_DIR/file.txt"
printf 'clean bin content\n' >"$ORIGIN_DIR/data.bin"
git -C "$ORIGIN_DIR" add -A
git -C "$ORIGIN_DIR" commit -q -m "fixture commit"
ORIGIN_SHA="$(git -C "$ORIGIN_DIR" rev-parse HEAD)"

make_trap() {
  # Write an executable script that appends "fired" to its marker file.
  local path="$1" marker="$2"
  printf '#!/bin/sh\necho fired >> "%s"\n' "$marker" >"$path"
  chmod +x "$path"
}

# Plants residue in an existing .git: hooks in the default directory and in
# a custom directory selected by core.hooksPath, a smudge filter wired
# through .git/info/attributes, a second filter smuggled through an
# include.path file, a credential helper, an fsmonitor probe, an ssh
# substitute, an insteadOf rewrite of the job repo URL, and an http.<url>
# override. Each trap writes its own marker file, so one case cannot pollute
# the assertions of another.
plant_residue_traps() {
  local dir="$1" origin_url="$2" case_id="$3"
  local marker_default marker_evil marker_smudge marker_include
  local marker_cred marker_fsm marker_ssh
  local smudge_cmd include_file evil_base
  marker_default="$T/marker-$case_id-post-checkout"
  marker_evil="$T/marker-$case_id-evilhook"
  marker_smudge="$T/marker-$case_id-smudge"
  marker_include="$T/marker-$case_id-include"
  marker_cred="$T/marker-$case_id-credhelper"
  marker_fsm="$T/marker-$case_id-fsmonitor"
  marker_ssh="$T/marker-$case_id-ssh"
  evil_base="file:///nonexistent-evil-$case_id"

  mkdir -p "$dir/.git/hooks" "$dir/.git/evilhooks" "$dir/.git/info"
  make_trap "$dir/.git/hooks/post-checkout" "$marker_default"
  make_trap "$dir/.git/evilhooks/post-checkout" "$marker_evil"
  git -C "$dir" config --local core.hooksPath .git/evilhooks

  smudge_cmd="sh -c 'sed s/clean/SMUDGED/; echo fired >> \"$marker_smudge\"'"
  git -C "$dir" config --local filter.evil.smudge "$smudge_cmd"
  git -C "$dir" config --local filter.evil.clean cat

  # The include lives outside .git/config, so a local-only enumeration
  # cannot even see the filter section it carries. The filter runs on
  # *.bin, a different pattern from the planted .txt filter, so both paths
  # are checked independently. The smudge command is a plain script path:
  # a semicolon or hash inside a config-file value would truncate it.
  include_file="$T/include-$case_id"
  printf '#!/bin/sh\nsed s/clean/INCLUDED/\necho fired >> "%s"\n' "$marker_include" >"$T/evil-incl-filter-$case_id"
  chmod +x "$T/evil-incl-filter-$case_id"
  printf '[filter "inc"]\n\tsmudge = %s\n\tclean = cat\n' "$T/evil-incl-filter-$case_id" >"$include_file"
  git -C "$dir" config --local include.path "$include_file"

  printf '*.txt filter=evil\n*.bin filter=inc\n' >"$dir/.git/info/attributes"

  make_trap "$T/evil-credhelper-$case_id" "$marker_cred"
  git -C "$dir" config --local credential.helper "$T/evil-credhelper-$case_id"
  make_trap "$T/evil-fsmonitor-$case_id" "$marker_fsm"
  git -C "$dir" config --local core.fsmonitor "$T/evil-fsmonitor-$case_id"
  make_trap "$T/evil-ssh-$case_id" "$marker_ssh"
  git -C "$dir" config --local core.sshCommand "$T/evil-ssh-$case_id"

  git -C "$dir" config --local "url.$evil_base.insteadOf" "$origin_url"
  git -C "$dir" config --local "http.$origin_url.sslVerify" false
}

new_residue_repo() {
  local dir="$1"
  git init -q --initial-branch=main "$dir"
  printf 'leftover\n' >"$dir/leftover.txt"
}

run_hardened_reuse() {
  local dir="$1"
  REPO_DIR="$dir"
  JOB_REPO="$ORIGIN_DIR"
  JOB_REF="main"
  clone_or_update_repo
}

verify_clean_reuse() {
  local dir="$1" case_id="$2" urls content others
  [[ -x "$dir/.git/hooks/post-checkout" ]] || die "case $case_id: plant missing (fixture broken)"
  [[ ! -e "$T/marker-$case_id-post-checkout" ]] || die "case $case_id: default-dir post-checkout hook ran"
  [[ ! -e "$T/marker-$case_id-evilhook" ]] || die "case $case_id: residue core.hooksPath hook ran"
  [[ ! -e "$T/marker-$case_id-smudge" ]] || die "case $case_id: residue smudge filter ran"
  [[ ! -e "$T/marker-$case_id-include" ]] || die "case $case_id: include-carried filter ran"
  [[ ! -e "$T/marker-$case_id-credhelper" ]] || die "case $case_id: residue credential helper ran"
  [[ ! -e "$T/marker-$case_id-fsmonitor" ]] || die "case $case_id: residue fsmonitor probe ran"
  [[ ! -e "$T/marker-$case_id-ssh" ]] || die "case $case_id: residue ssh substitute ran"

  urls="$(git -C "$dir" config --local --get-all remote.origin.url)"
  [[ "$urls" == "$ORIGIN_DIR" ]] || die "case $case_id: origin not reset to job repo (got: $urls)"
  others="$(git -C "$dir" config --local --name-only --get-regexp '^remote\.' 2>/dev/null | grep -v '^remote\.origin\.' || true)"
  [[ -z "$others" ]] || die "case $case_id: residue remote survived (got: $others)"
  [[ "$(git -C "$dir" rev-parse HEAD)" == "$ORIGIN_SHA" ]] || die "case $case_id: HEAD is not the fetched commit"

  content="$(cat "$dir/file.txt")"
  [[ "$content" == "clean blob content" ]] || die "case $case_id: worktree content was smudged (got: $content)"
  content="$(cat "$dir/data.bin")"
  [[ "$content" == "clean bin content" ]] || die "case $case_id: worktree content passed through the include filter (got: $content)"
  [[ -f "$dir/leftover.txt" ]] || die "case $case_id: untracked leftover file lost"

  if git -C "$dir" config --local --name-only --get-regexp '^(filter|url|credential|http|include|includeif)\.' >/dev/null 2>&1; then
    die "case $case_id: residue filter, url, credential, http, or include config survived the reuse path"
  fi
  if git -C "$dir" config --local --get-regexp '^(core\.hooksPath|core\.fsmonitor|core\.sshCommand)$' >/dev/null 2>&1; then
    die "case $case_id: residue command key survived the reuse path"
  fi
}

# shellcheck source=/dev/null
source "$HARNESS"

# Case single-url: residue points origin at a repository the previous
# tenant controls, and adds a second remote on the side.
R1="$T/case-single-url"
new_residue_repo "$R1"
git -C "$R1" remote add origin "$T/previous-tenant-remote"
git -C "$R1" remote add evil "$T/previous-tenant-remote-2"
plant_residue_traps "$R1" "$ORIGIN_DIR" single-url
run_hardened_reuse "$R1"
verify_clean_reuse "$R1" single-url
log "case single-url: hooks, filters, includes, and url rewrites neutralized"

# Case multi-url: residue lists two origin URLs. Plain "remote set-url"
# exits fatal on this shape, and fetch reads every configured URL, so any
# residue URL left in place would receive the job credentials.
R2="$T/case-multi-url"
new_residue_repo "$R2"
git -C "$R2" remote add origin "$T/previous-tenant-remote"
git -C "$R2" config --local --add remote.origin.url "$T/second-previous-tenant-remote"
plant_residue_traps "$R2" "$ORIGIN_DIR" multi-url
run_hardened_reuse "$R2"
verify_clean_reuse "$R2" multi-url
log "case multi-url: residue URL list replaced with the job URL"

# Case no-remote: residue carries no origin remote at all.
R3="$T/case-no-remote"
new_residue_repo "$R3"
plant_residue_traps "$R3" "$ORIGIN_DIR" no-remote
run_hardened_reuse "$R3"
verify_clean_reuse "$R3" no-remote
log "case no-remote: origin added from the job spec"

# Control instead-of: without the scrub, the insteadOf rewrite hijacks the
# fetch even after the URL was reset correctly. The fetch must fail here.
C1="$T/control-instead-of"
new_residue_repo "$C1"
git -C "$C1" remote add origin "$ORIGIN_DIR"
plant_residue_traps "$C1" "$ORIGIN_DIR" instead-of
if git -C "$C1" fetch --force origin main >/dev/null 2>&1; then
  die "control instead-of: fetch succeeded despite the planted insteadOf rewrite"
fi
log "control instead-of: planted rewrite is live without the scrub"

# Control plain-git: without GIT_HARDEN_ARGS and the scrub, checkout runs
# the planted hook and the planted smudge filter. The default-dir hook
# stays silent here only because the residue core.hooksPath redirects to
# the custom directory.
C2="$T/control-plain-git"
new_residue_repo "$C2"
git -C "$C2" remote add origin "$ORIGIN_DIR"
plant_residue_traps "$C2" "$ORIGIN_DIR" plain-git
git -C "$C2" config --local --remove-section "url.file:///nonexistent-evil-plain-git"
git -C "$C2" fetch --force origin main
git -C "$C2" checkout -B agentlab FETCH_HEAD
[[ -e "$T/marker-plain-git-evilhook" ]] || die "control plain-git: planted hook did not fire without hardening"
[[ -e "$T/marker-plain-git-smudge" ]] || die "control plain-git: planted filter did not run without hardening"
content="$(cat "$C2/file.txt")"
[[ "$content" == "SMUDGED blob content" ]] || die "control plain-git: expected smudged content, got: $content"
log "control plain-git: planted hook and filter are live without hardening"

# Control include: the include-carried filter runs on a plain checkout.
# The scrub cannot remove it by name, so it must drop the include itself.
C3="$T/control-include"
new_residue_repo "$C3"
git -C "$C3" remote add origin "$ORIGIN_DIR"
plant_residue_traps "$C3" "$ORIGIN_DIR" include
git -C "$C3" config --local --remove-section "url.file:///nonexistent-evil-include"
git -C "$C3" fetch --force origin main
git -C "$C3" checkout -B agentlab FETCH_HEAD >/dev/null 2>&1
[[ -e "$T/marker-include-include" ]] || die "control include: include-carried filter did not run without hardening"
content="$(cat "$C3/data.bin")"
[[ "$content" == "INCLUDED bin content" ]] || die "control include: expected include-smudged content, got: $content"
log "control include: include-carried filter is live without the scrub"

# Control fsmonitor: checkout probes the configured fsmonitor command, and
# -c core.hooksPath does not stop it.
C4="$T/control-fsmonitor"
new_residue_repo "$C4"
git -C "$C4" remote add origin "$ORIGIN_DIR"
plant_residue_traps "$C4" "$ORIGIN_DIR" fsmonitor
git -C "$C4" config --local --remove-section "url.file:///nonexistent-evil-fsmonitor"
git -C "$C4" fetch --force origin main
git -C "$C4" -c core.hooksPath=/dev/null checkout -B agentlab FETCH_HEAD >/dev/null 2>&1
[[ -e "$T/marker-fsmonitor-fsmonitor" ]] || die "control fsmonitor: planted probe did not run without hardening"
log "control fsmonitor: planted probe is live even with the hooks override alone"

# Control credential: git consults configured helpers before GIT_ASKPASS,
# so a residue helper would receive the job credentials during a fetch.
C5="$T/control-credential"
new_residue_repo "$C5"
plant_residue_traps "$C5" "$ORIGIN_DIR" credential
printf 'protocol=https\nhost=example.invalid\n\n' | git -C "$C5" credential fill >/dev/null 2>&1 || true
[[ -e "$T/marker-credential-credhelper" ]] || die "control credential: planted helper did not run"
log "control credential: planted helper is live through git credential"

# Control ssh: with no bundle ssh key, fetch uses core.sshCommand as the
# ssh client for an ssh remote. The planted command runs in its place.
C6="$T/control-ssh"
new_residue_repo "$C6"
git -C "$C6" remote add origin ssh://localhost/nonexistent
plant_residue_traps "$C6" "$ORIGIN_DIR" ssh
git -C "$C6" fetch --force origin main >/dev/null 2>&1 || true
[[ -e "$T/marker-ssh-ssh" ]] || die "control ssh: planted ssh substitute did not run"
log "control ssh: planted ssh substitute is live on an ssh fetch"

log "ok"
