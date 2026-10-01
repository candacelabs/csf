#!/usr/bin/env bash
# Regenerate the warden proto Go bindings and run the schema gates.
#
# Usage: bash generate.sh            # lint + generate
#        bash generate.sh --check    # lint only (no codegen), for CI parity
#
# The buf binary and the two protoc plugins (protoc-gen-go, protoc-gen-go-grpc)
# are expected on PATH. They are installed with:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# and buf from https://github.com/bufbuild/buf/releases. This script prepends the
# Go tool bin dir to PATH so a fresh `go install` is picked up automatically.
set -euo pipefail

# Resolve this script's own directory (the buf module root) so the script works
# regardless of the caller's working directory (e.g. under `go generate`).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# Make `go install`-ed plugins discoverable without assuming a global PATH setup.
GO_BIN="$(go env GOBIN)"
if [[ -z "${GO_BIN}" ]]; then
  GO_BIN="$(go env GOPATH)/bin"
fi
export PATH="${GO_BIN}:${PATH}"

echo "buf: $(buf --version)"
echo "==> buf lint"
buf lint

if [[ "${1:-}" == "--check" ]]; then
  echo "lint-only check complete"
  exit 0
fi

echo "==> buf generate"
buf generate

echo "generation complete"
