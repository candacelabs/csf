#!/usr/bin/env bash
# The repository-wide sweep for house rule CS-8's FLAGGING lane: every function
# and method result, in every first-party Go module, through which an interface
# reaches a caller — the result's own type, or an interface-typed field of a
# struct it hands back (the 2026-09-03 amendment).
#
# The house runner (tools/house_lint) invokes it as the INTERFACE-RETURNS
# lane; by hand, run it from the repository root:
#
#   bash tools/check-ifacereturn.sh [--strict]
#
# Consumers of the published candacelabs/csf module run the analyzer directly
# instead, from their own module root:
#
#   go run github.com/candacelabs/csf/tools/ifacereturn/cmd/ifacereturn ./...
#
# Two lanes, by operator design (2026-09-02, "i want a ci lint check that
# checks to see if there are any interface return types and flags them"):
#
#   blocking-narrow  the native CS-8 rule in tools/house_lint/mandatory.ml:
#                    a receiverless function returning a corpus-declared
#                    interface, with its structural exemptions, fails the build.
#   flagging-exact   this script. Type-aware (go/types decides what an
#                    interface is; a lexer can only guess), no restrictions,
#                    `error` the only exemption, and it never fails the build.
#
# The wide lane necessarily reports code that has already been ruled correct:
# sealed sum types, hook implementations, pass-throughs of a library's own
# contract, and the method-position returns that survived the 2026-09-02
# data-shaped re-audit. Generated output contributes type information but
# receives no findings; the analyzer uses Go's standard generated-file marker.
# Handwritten code keeps the same semantic coverage (references/exceptions.md).
#
# The analyzer itself is tools/ifacereturn: public, self-contained, and
# scanning whatever module it is run in. This script knows where this
# repository's modules are (one, at the root; the list is derived from the
# tracked go.mod files so a module added later joins the sweep).
#
# Usage:
#   tools/check-ifacereturn.sh            # sweep every module, exit 0
#   tools/check-ifacereturn.sh --strict   # exit 1 if anything was reported
#
# Exit codes: 0 swept and reported; 1 reported under --strict; 2 a module could
# not be scanned, which is the one result a gate must never render as "0
# findings".
set -euo pipefail

# Pinned by digest, the same image .github/workflows/ci.yml runs, so a local
# sweep and a CI sweep are the same toolchain rather than two that happen to
# agree today.
readonly go_image='golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599'
readonly analyzer_package='./tools/ifacereturn/cmd/ifacereturn'

strict=0
for argument in "$@"; do
  case "$argument" in
    --strict) strict=1 ;;
    -h | --help)
      sed -n '2,40p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "check-ifacereturn: unknown argument: $argument" >&2
      echo "check-ifacereturn: usage: tools/check-ifacereturn.sh [--strict]" >&2
      exit 2
      ;;
  esac
done

command -v docker >/dev/null 2>&1 || {
  echo "check-ifacereturn: docker is required; there is no host Go toolchain in this fleet" >&2
  exit 2
}

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Every tracked first-party module, in path order. Derived rather than listed,
# so a module added later joins the sweep instead of quietly sitting outside
# it. research/ holds frozen artifacts and vendor/ is somebody else's code;
# both are outside every other Go gate here too.
modules=()
while IFS= read -r go_mod; do
  case "$go_mod" in
    research/* | vendor/* | */vendor/*) continue ;;
  esac
  modules+=("$(dirname "$go_mod")")
done < <(git ls-files -- '*go.mod' | sort)

if ((${#modules[@]} == 0)); then
  echo "check-ifacereturn: no Go modules selected; refusing a vacuous pass" >&2
  exit 2
fi

cache_root="${TMPDIR:-/tmp}/candace-ifacereturn-cache"
mkdir -p -- "$cache_root/gocache" "$cache_root/gomod" "$cache_root/bin"

# The step summary is written by the analyzer itself, inside the container, so
# the file has to be reachable there under the same path. Absent (a local run),
# nothing is mounted and the analyzer skips it.
summary_mount=()
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  summary_mount=(--volume "${GITHUB_STEP_SUMMARY}:${GITHUB_STEP_SUMMARY}")
fi

strict_flag=""
((strict == 1)) && strict_flag="-strict"

# One container for the whole sweep: build the analyzer once from this
# module, then run that binary from each module root in turn. go/packages reads
# the module in the working directory, so the binary's own module is irrelevant
# to what it scans.
#
# The two failure kinds are kept apart deliberately. A module that reported
# findings (exit 1, only reachable under --strict) is the gate working; a
# module that could not be scanned (exit 2) is the gate broken, and a broken
# gate must never be able to render itself as a clean one.
read -r -d '' sweep_script <<SWEEP || true
set -eu
cd /src
go build -o /cache/bin/ifacereturn ${analyzer_package} || exit 2
findings=0
unscannable=0
for module in ${modules[*]}; do
  printf '\n=== ifacereturn: %s ===\n' "\$module" >&2
  cd "/src/\$module"
  status=0
  /cache/bin/ifacereturn ${strict_flag} ./... || status=\$?
  if [ "\$status" -eq 1 ]; then
    findings=1
  elif [ "\$status" -ne 0 ]; then
    unscannable=\$((unscannable + 1))
  fi
done
if [ "\$unscannable" -gt 0 ]; then
  printf 'ifacereturn: %s module(s) could not be scanned\n' "\$unscannable" >&2
  exit 2
fi
exit "\$findings"
SWEEP

set +e
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/cache \
  --env GOCACHE=/cache/gocache \
  --env GOMODCACHE=/cache/gomod \
  --env GOPATH=/cache/gopath \
  --env GOTOOLCHAIN=local \
  --env GOFLAGS=-mod=readonly \
  --env "GITHUB_STEP_SUMMARY=${GITHUB_STEP_SUMMARY:-}" \
  "${summary_mount[@]}" \
  --volume "${repo_root}:/src" \
  --volume "${cache_root}:/cache" \
  --workdir /src \
  "$go_image" \
  sh -eu -c "$sweep_script"
sweep_status=$?
set -e

case "$sweep_status" in
  0)
    echo "check-ifacereturn: swept ${#modules[@]} module(s); the per-module counts are above" >&2
    ;;
  1)
    echo "check-ifacereturn: --strict was given and at least one module reported an interface-typed result" >&2
    exit 1
    ;;
  *)
    echo "check-ifacereturn: a module could not be scanned; this is a broken gate, not a clean one" >&2
    exit 2
    ;;
esac
