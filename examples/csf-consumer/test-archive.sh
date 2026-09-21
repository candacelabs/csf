#!/usr/bin/env bash
# Exercise the example in a fresh Git repository against one source archive.
set -Eeuo pipefail

consumer_go_image='golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599'

consumer_die() {
  printf 'CSF consumer: %s\n' "$*" >&2
  exit 1
}

[[ $# == 2 ]] || consumer_die 'usage: test-archive.sh ARCHIVE NEW_OUTPUT_DIRECTORY'
consumer_archive=$(realpath "$1")
consumer_output=$(realpath -m "$2")
[[ -f "$consumer_archive" ]] || consumer_die 'archive does not exist'
[[ ! -e "$consumer_output" ]] || consumer_die 'output directory must not already exist'
consumer_prefix=$(tar -tzf "$consumer_archive" | awk -F/ 'NF { print $1 }' | sort -u)
[[ "$consumer_prefix" =~ ^candace-[0-9a-f]+$ ]] || consumer_die 'expected one revision-named candace archive root'
mkdir -p "$consumer_output"
tar -xzf "$consumer_archive" -C "$consumer_output"
consumer_module="$consumer_output/$consumer_prefix"
consumer_example="$consumer_module/examples/csf-consumer"
consumer_repo="$consumer_output/consumer"
mkdir "$consumer_repo" "$consumer_output/cache"
cp "$consumer_example/main.go" "$consumer_example/consumer_test.go" \
  "$consumer_example/workbench-theme.css" "$consumer_repo/"
printf '/vendor/\n/csf-consumer\n' > "$consumer_repo/.gitignore"

# Resolve and vendor with the extracted release available. No private checkout
# or private import path is mounted into this container.
docker run --rm --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e GOMODCACHE=/cache/modules -e GOCACHE=/cache/build -e GOMAXPROCS=2 \
  -e CANDACE_CONSUMER_PREFIX="$consumer_prefix" \
  -v "$consumer_output:/acceptance" -v "$consumer_output/cache:/cache" \
  -w /acceptance/consumer "$consumer_go_image" bash -euo pipefail -c '
    go mod init example.invalid/csf-consumer
    go mod edit -require=github.com/candacelabs/csf@v0.0.0
    go mod edit -replace="github.com/candacelabs/csf=../$CANDACE_CONSUMER_PREFIX"
    go mod tidy
    go mod vendor
  ' 2>&1 | tee "$consumer_output/vendor.log"

git -C "$consumer_repo" init --initial-branch=main
git -C "$consumer_repo" add .gitignore main.go consumer_test.go workbench-theme.css go.mod go.sum
git -C "$consumer_repo" -c user.name='CSF consumer acceptance' \
  -c user.email='consumer@example.invalid' commit -m 'Exercise CSF from a release archive'

# Only the independent consumer and a compiler cache are mounted. The extracted
# library is unavailable here; dependency source must come from vendor/.
docker run --rm --network none --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e GOMODCACHE=/cache/modules -e GOCACHE=/cache/build -e GOMAXPROCS=2 \
  -v "$consumer_repo:/consumer" -v "$consumer_output/cache:/cache" \
  -w /consumer "$consumer_go_image" bash -euo pipefail -c '
    go test -mod=vendor -race ./...
    go build -mod=vendor -o csf-consumer .
    ./csf-consumer -listen 127.0.0.1:8089 -theme-dir . &
    consumer_pid=$!
    trap '\''kill -TERM "$consumer_pid" 2>/dev/null || true; wait "$consumer_pid" || true'\'' EXIT
    curl --fail --silent --show-error --retry 5 --retry-connrefused --retry-delay 1 \
      --max-time 5 http://127.0.0.1:8089/consumer/snapshot
    curl --fail --silent --show-error --max-time 5 \
      -H "Content-Type: application/json" -d "{}" \
      http://127.0.0.1:8089/api/workbench/theme/get
    kill -TERM "$consumer_pid"
    wait "$consumer_pid"
    trap - EXIT
    if curl --silent --max-time 1 http://127.0.0.1:8089/consumer/snapshot; then
      printf "listener survived process shutdown\n" >&2
      exit 1
    fi
    printf "\nConsumer process exited cleanly; listener closed.\n"
  ' 2>&1 | tee "$consumer_output/acceptance.log"

printf 'archive_sha256=%s\nconsumer_repository=%s\nconsumer_commit=%s\n' \
  "$(sha256sum "$consumer_archive" | awk '{print $1}')" \
  "$consumer_repo" "$(git -C "$consumer_repo" rev-parse HEAD)" \
  | tee "$consumer_output/receipt.txt"
