#!/bin/sh
# Docker supplies bootstrap tooling; the release's installer owns the CSF build.
set -eu

main() {
  case "${1:-}" in -h|--help) printf 'Usage: sh bootstrap.sh [--version vMAJOR.MINOR.PATCH]\n'; return 0 ;; esac
  [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || { printf 'CSF currently supports Linux x86_64 hosts.\n' >&2; exit 1; }
  for command_name in curl docker; do
    command -v "$command_name" >/dev/null 2>&1 || { printf 'CSF requires %s.\n' "$command_name" >&2; exit 1; }
  done
  docker info >/dev/null 2>&1 || { printf 'CSF requires a running local Docker daemon accessible to this user.\n' >&2; exit 1; }
  releases=${CSF_RELEASES_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/csf/releases}
  case "$releases" in /*) ;; *) printf 'CSF_RELEASES_DIR must be absolute.\n' >&2; exit 1 ;; esac
  case "$releases" in *:*) printf 'CSF_RELEASES_DIR cannot contain a colon.\n' >&2; exit 1 ;; esac
  umask 077
  mkdir -p "$releases"
  releases=$(cd "$releases" && pwd -P)
  mkdir "$releases/.install-lock" 2>/dev/null || {
    printf '[FAIL] Another installation is active, or .install-lock needs inspection.\n' >&2; exit 1;
  }
  temporary=
  trap 'if [ -n "$temporary" ]; then rm -rf -- "$temporary"; fi; rmdir "$releases/.install-lock"' 0
  temporary=$(mktemp -d)
  trap 'exit 1' HUP INT TERM
  curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 20 --retry 3 \
    https://raw.githubusercontent.com/candacelabs/csf/main/tools/csf-operator/bootstrap.py \
    -o "$temporary/bootstrap.py" || { printf 'Public CSF installer is unavailable; no private or staging source will be used.\n' >&2; exit 1; }
  source_path=$(docker run --rm --user "$(id -u):$(id -g)" \
    --volume "$releases:$releases" --volume "$temporary/bootstrap.py:/opt/csf-bootstrap.py:ro" \
    python:3.12.11-bookworm@sha256:13c9584604a99ca134c4f41800f74ffc64ee6ac8cf555cf1e704a6087fc84f12 \
    python -I /opt/csf-bootstrap.py --releases "$releases" "$@")
  case "$source_path" in "$releases"/v*/source) ;; *) printf 'CSF bootstrap returned an invalid source path.\n' >&2; exit 1 ;; esac
  "$source_path/install.sh"
}

main "$@"
