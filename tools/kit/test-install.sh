#!/usr/bin/env bash
# Copyright 2026 Candace Labs
#
# Hermetic check of tools/kit/install.sh and `csf init`: install into
# temporary directories, run `csf init` in a temporary repository against a
# private state directory and port, then assert that both binaries answer,
# that init started a host, and that the written sample recipe names the
# repository. The host is stopped on exit. Needs docker, git and python3.

set -Eeuo pipefail

die() {
  printf 'kit test: %s\n' "$*" >&2
  exit 1
}

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly ROOT
work="$(mktemp -d "${TMPDIR:-/tmp}/csf-kit-test.XXXXXXXX")"
readonly work
# The Go module cache is written read-only; make it writable before removing.
readonly repo="$work/repo" prefix="$work/bin" state="$work/state" listen=127.0.0.1:14199
cleanup() {
  "$prefix/csf" stop -state "$state" >/dev/null 2>&1 || true
  chmod -R u+w -- "$work" 2>/dev/null
  rm -rf -- "$work"
}
trap cleanup EXIT

git init --quiet --initial-branch=main "$repo"
printf '# kit test\n' >"$repo/README.md"
git -C "$repo" add README.md
git -C "$repo" -c user.name=kit-test -c user.email=kit-test@example.invalid commit --quiet -m 'Initial commit'

"$ROOT/tools/kit/install.sh" --prefix "$prefix"

usage="$("$prefix/csf" 2>&1 || true)"
[[ "$usage" == *"usage: csf init|serve|submit"* ]] || die "csf did not print its usage: $usage"

initialized="$(cd -- "$repo" && "$prefix/csf" init -state "$state" -listen "$listen")"
[[ "$initialized" == *"[PASS] Started the host at http://$listen"* ]] || die "csf init did not start a host: $initialized"
"$prefix/csf" list -state "$state" >/dev/null || die "the host csf init started does not answer"

miners="$("$prefix/rrsi-mine" miners)"
[[ "$miners" == *'"pr-gap"'* ]] || die "rrsi-mine miners did not list pr-gap: $miners"

python3 - "$repo" <<'EOF'
import json, os, re, sys
repo = os.path.realpath(sys.argv[1])
sample = os.path.join(repo, ".csf", "assignments", "sample")
with open(os.path.join(sample, "agent.json")) as handle:
    recipe = json.load(handle)
workspace = recipe["workspace"]
assert re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", recipe["assignment_id"]), recipe["assignment_id"]
assert workspace["repository_path"] == repo, workspace["repository_path"]
assert workspace["base_branch"] == "main", workspace["base_branch"]
assert workspace["branch"] != workspace["base_branch"], workspace["branch"]
assert workspace["brief_path"] == "brief.md", workspace["brief_path"]
assert os.path.isfile(os.path.join(sample, "brief.md"))
assert recipe["repository_id"] == os.path.basename(repo), recipe["repository_id"]
EOF

printf '[PASS] kit install: csf and rrsi-mine answer; csf init started a host and wrote a sample recipe naming %s.\n' "$repo"
