#!/usr/bin/env bash
# Exercise the built binary and LFS checkout with no network or provider credentials.
set -Eeuo pipefail
csf_test_image="${1:?usage: test-image.sh IMAGE}"
docker run --rm --network none --entrypoint /bin/bash "${csf_test_image}" -euo pipefail -c '
  test -s /app/workbench-ui/index.html
  git lfs version
  copilot --version
  csf_test_root="$(mktemp -d)"
  git init --initial-branch=main "${csf_test_root}/repo"
  cd "${csf_test_root}/repo"
  git config user.name "Runtime test"
  git config user.email "test@example.invalid"
  git lfs track "*.bin"
  printf "LFS checkout fixture\n" > fixture.bin
  git add .gitattributes fixture.bin
  git commit -m fixture
  git worktree add -b smoke "${csf_test_root}/worktree"
  cmp fixture.bin "${csf_test_root}/worktree/fixture.bin"
  git worktree remove "${csf_test_root}/worktree"
  /app/candace-runtime serve --listen 127.0.0.1:14111 > "${csf_test_root}/runtime.log" 2>&1 &
  csf_test_pid=$!
  trap '\''kill "${csf_test_pid}" 2>/dev/null || true'\'' EXIT
  if ! curl --silent --show-error --fail --retry 10 --retry-connrefused --retry-delay 1 \
      --max-time 2 http://127.0.0.1:14111/ > /dev/null; then
    cat "${csf_test_root}/runtime.log"
    exit 1
  fi
  kill -TERM "${csf_test_pid}"
  wait "${csf_test_pid}"
  trap - EXIT
'
