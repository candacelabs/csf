#!/usr/bin/env bash
# Build one Debian 12 amd64 native payload. Docker is build-time isolation only;
# assembled artifacts run as native systemd processes and contain no images.
set -Eeuo pipefail

native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$native_root/../../.." && pwd -P)
manifest="$native_root/manifest.json"
debian_image="debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241"
node_image="node:24-bookworm@sha256:da4221677e02b54ef6335adfa447578d512ad14f251024fb92ea433c2c102760"
go_image="golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599"
rust_image="rust:1.91-bookworm@sha256:c1e5f19e773b7878c3f7a805dd00a495e747acbdc76fb2337a4ebf0418896b33"

die() { printf 'native payload: %s\n' "$*" >&2; exit 1; }

field() {
  python3 - "$manifest" "$component" "$1" <<'PY'
import json, sys
value = json.load(open(sys.argv[1]))["components"][sys.argv[2]]
for part in sys.argv[3].split("."):
    value = value[int(part)] if isinstance(value, list) else value[part]
print(value)
PY
}

download() {
  local url=$1 expected=$2 algorithm=$3 destination=$4 actual
  curl --fail --location --retry 3 --output "$destination" "$url"
  actual=$($algorithm"sum" "$destination" | cut -d' ' -f1)
  [[ "$actual" == "$expected" ]] || die "$algorithm mismatch for $url"
}

write_receipt() {
  mkdir -p "$scratch/cargo-home" "$scratch/receipt-target"
  docker run --rm --user "$(id -u):$(id -g)" \
    --env CARGO_HOME=/cargo-home --env CARGO_TARGET_DIR=/receipt-target \
    --volume "$native_root/receipt-rs:/receipt-source:ro" \
    --volume "$scratch/cargo-home:/cargo-home" --volume "$scratch/receipt-target:/receipt-target" \
    --volume "$payload:/payload" --workdir /receipt-source "$rust_image" \
    cargo run --quiet --locked --release -- --component "$component" --version "$version" \
    --target "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["target"])' "$manifest")" \
    --source "$source_url" --source-sha256 "$source_sha256" --payload /payload
}

[[ $# -eq 2 ]] || die "usage: build-payload.sh COMPONENT OUTPUT_DIRECTORY"
component=$1
payload=$(realpath -m "$2")
python3 - "$manifest" "$component" <<'PY' || exit 1
import json, sys
if sys.argv[2] not in json.load(open(sys.argv[1]))["components"]:
    raise SystemExit("unknown component: " + sys.argv[2])
PY
[[ ! -e "$payload" ]] || die "output already exists: $payload"
mkdir -p "$payload"
scratch=$(mktemp -d)
cleanup() {
  chmod -R u+w "$scratch" 2>/dev/null || true
  rm -rf -- "$scratch"
}
trap cleanup EXIT
version=$(field version)
source_url=""
source_sha256=""

case "$component" in
  csf)
    git_root=$(git -C "$module_root" rev-parse --show-toplevel)
    module_relative=$(realpath --relative-to "$git_root" "$module_root")
    [[ -z "$(git -C "$git_root" status --porcelain --untracked-files=all -- "$module_relative")" ]] || \
      die "csf source tree must be clean"
    if [[ "$module_relative" == "." ]]; then
      tree_ref=HEAD
    else
      tree_ref="HEAD:$module_relative"
    fi
    git -C "$git_root" archive --format=tar "$tree_ref" > "$scratch/csf-source.tar"
    source_sha256=$(sha256sum "$scratch/csf-source.tar" | cut -d' ' -f1)
    source_url="git-tree:$(git -C "$git_root" rev-parse "$tree_ref")"
    mkdir "$scratch/csf-source"
    tar -xf "$scratch/csf-source.tar" -C "$scratch/csf-source"
    mkdir -p "$payload/bin" "$scratch/go-cache"
    docker run --rm --user "$(id -u):$(id -g)" \
      --env GOTOOLCHAIN=local --env GOPATH=/go-cache --env GOMODCACHE=/go-cache/pkg/mod \
      --env GOPROXY=https://proxy.golang.org --env GOSUMDB=sum.golang.org \
      --volume "$scratch/csf-source:/src:ro" --volume "$scratch/go-cache:/go-cache" \
      --workdir /src "$go_image" go mod download
    docker run --rm --user "$(id -u):$(id -g)" --network none \
      --env CGO_ENABLED=0 --env GOTOOLCHAIN=local --env GOCACHE=/tmp/go-build \
      --env GOPATH=/go-cache --env GOMODCACHE=/go-cache/pkg/mod \
      --volume "$scratch/csf-source:/src:ro" --volume "$scratch/go-cache:/go-cache:ro" \
      --volume "$payload:/payload" --workdir /src "$go_image" sh -euc \
      'go mod verify && go build -mod=readonly -trimpath -o /payload/bin/csf ./app/csf/cmd'
    install -m 0644 "$scratch/csf-source/LICENSE" "$payload/LICENSE"
    ;;
  postgresql)
    source_url=$(field source.url); source_sha256=$(field source.sha256)
    download "$source_url" "$source_sha256" sha256 "$scratch/source.tar.gz"
    tar -xzf "$scratch/source.tar.gz" -C "$scratch"
    docker run --rm --env PAYLOAD_UID="$(id -u)" --env PAYLOAD_GID="$(id -g)" \
      --volume "$scratch:/work" --volume "$payload:/payload" "$debian_image" bash -euc '
      trap '\''chown -R "$PAYLOAD_UID:$PAYLOAD_GID" /work /payload 2>/dev/null || true'\'' EXIT
      apt-get update
      apt-get install -y --no-install-recommends build-essential ca-certificates flex bison libreadline-dev libssl-dev zlib1g-dev
      cd /work/postgresql-*
      ./configure --prefix=/opt/candace/csf/components/postgresql --with-openssl --without-icu
      make -j2
      make DESTDIR=/payload-root install
      cp COPYRIGHT /payload-root/opt/candace/csf/components/postgresql/COPYRIGHT
      cp -a /payload-root/opt/candace/csf/components/postgresql/. /payload/
    '
    ;;
  redis)
    source_url=$(field source.url); source_sha256=$(field source.sha256)
    download "$source_url" "$source_sha256" sha256 "$scratch/source.tar.gz"
    tar -xzf "$scratch/source.tar.gz" -C "$scratch"
    docker run --rm --env PAYLOAD_UID="$(id -u)" --env PAYLOAD_GID="$(id -g)" \
      --volume "$scratch:/work" --volume "$payload:/payload" "$debian_image" bash -euc '
      trap '\''chown -R "$PAYLOAD_UID:$PAYLOAD_GID" /work /payload 2>/dev/null || true'\'' EXIT
      apt-get update
      apt-get install -y --no-install-recommends build-essential ca-certificates libssl-dev pkg-config
      cd /work/redis-*
      make -j2 BUILD_TLS=yes MALLOC=libc
      make PREFIX=/payload install
      cp LICENSE.txt /payload/LICENSE
    '
    ;;
  opensearch)
    source_url=$(field source.url); expected=$(field source.sha512)
    download "$source_url" "$expected" sha512 "$scratch/source.tar.gz"
    source_sha256=$(sha256sum "$scratch/source.tar.gz" | cut -d' ' -f1)
    mkdir "$scratch/extracted"
    tar -xzf "$scratch/source.tar.gz" -C "$scratch/extracted" --strip-components=1
    cp -aL "$scratch/extracted/." "$payload/"
    ;;
  clickhouse)
    source_url=$(field source.url); expected=$(field source.sha512)
    download "$source_url" "$expected" sha512 "$scratch/source.tgz"
    source_sha256=$(sha256sum "$scratch/source.tgz" | cut -d' ' -f1)
    tar -xzf "$scratch/source.tgz" -C "$scratch"
    mkdir -p "$payload/bin"
    install -m 0755 "$(find "$scratch" -path '*/usr/bin/clickhouse' -type f -print -quit)" "$payload/bin/clickhouse"
    copyright=$(find "$scratch" -path '*/usr/share/doc/*/copyright' -type f -print -quit)
    [[ -n "$copyright" ]] && install -m 0644 "$copyright" "$payload/COPYRIGHT"
    ;;
  minio)
    mkdir -p "$payload/bin"
    download "$(field sources.0.url)" "$(field sources.0.sha256)" sha256 "$payload/bin/minio"
    download "$(field sources.1.url)" "$(field sources.1.sha256)" sha256 "$payload/bin/mc"
    chmod 0755 "$payload/bin/minio" "$payload/bin/mc"
    curl --fail --location --output "$payload/LICENSE.minio" \
      "https://raw.githubusercontent.com/minio/minio/$(field sources.0.commit)/LICENSE"
    curl --fail --location --output "$payload/LICENSE.mc" \
      "https://raw.githubusercontent.com/minio/mc/$(field sources.1.commit)/LICENSE"
    source_url=$(python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["components"]["minio"]["sources"], separators=(",", ":"), sort_keys=True))' "$manifest")
    source_sha256=$(printf '%s' "$source_url" | sha256sum | cut -d' ' -f1)
    ;;
  object-storage)
    git_root=$(git -C "$module_root" rev-parse --show-toplevel)
    module_relative=$(realpath --relative-to "$git_root" "$module_root")
    crate_relative="$module_relative/app/csf/native/object_storage"
    [[ -z "$(git -C "$git_root" status --porcelain --untracked-files=all -- "$crate_relative")" ]] || \
      die "object-storage source tree must be clean"
    tree_ref="HEAD:$crate_relative"
    git -C "$git_root" archive --format=tar "$tree_ref" > "$scratch/object-storage-source.tar"
    source_sha256=$(sha256sum "$scratch/object-storage-source.tar" | cut -d' ' -f1)
    source_url="git-tree:$(git -C "$git_root" rev-parse "$tree_ref")"
    mkdir "$scratch/object-storage-source" "$scratch/cargo-home" "$scratch/rust-target"
    tar -xf "$scratch/object-storage-source.tar" -C "$scratch/object-storage-source"
    mkdir -p "$payload/bin"
    docker run --rm --user "$(id -u):$(id -g)" \
      --env CARGO_HOME=/cargo-home --env CARGO_TARGET_DIR=/rust-target \
      --volume "$scratch/object-storage-source:/src:ro" \
      --volume "$scratch/cargo-home:/cargo-home" --volume "$scratch/rust-target:/rust-target" \
      --volume "$payload:/payload" --workdir /src "$rust_image" sh -euc \
      'cargo build --release --locked && install -m 0755 "$CARGO_TARGET_DIR/release/object-storage" /payload/bin/object-storage'
    ;;
  langfuse)
    source_url=$(field source.url); source_sha256=$(field source.sha256)
    download "$source_url" "$source_sha256" sha256 "$scratch/source.tar.gz"
    mkdir "$scratch/source"
    tar -xzf "$scratch/source.tar.gz" -C "$scratch/source" --strip-components=1
    docker run --rm --env PAYLOAD_UID="$(id -u)" --env PAYLOAD_GID="$(id -g)" \
      --volume "$scratch/source:/source" --volume "$payload:/payload" --workdir /source "$node_image" bash -euc '
      trap '\''chown -R "$PAYLOAD_UID:$PAYLOAD_GID" /source /payload 2>/dev/null || true'\'' EXIT
      npm install --global corepack@0.36.0 turbo@2.10.5
      corepack enable
      corepack prepare pnpm@12.3.1 --activate
      pnpm install --frozen-lockfile
      export DOCKER_BUILD=1 NEXT_MANUAL_SIG_HANDLE=true NEXT_TELEMETRY_DISABLED=1 CI=true
      export pnpm_config_verify_deps_before_run=false
      node packages/shared/clickhouse/scripts/prepare-migrations.mjs materialize packages/shared/clickhouse/migrations
      chmod 0755 packages/shared/clickhouse/migrations/clustered packages/shared/clickhouse/migrations/unclustered
      rm -f web/src/middleware.ts
      pnpm turbo run build --filter=web... --filter=worker...
      pnpm --filter worker deploy --prod /payload/worker
      builder_prisma_client_dir=$(find /source/node_modules/.pnpm -path '\''*/node_modules/@prisma/client'\'' -type d | head -n 1)
      deployed_prisma_client_dir=$(find /payload/worker/node_modules/.pnpm -path '\''*/node_modules/@prisma/client'\'' -type d | head -n 1)
      builder_prisma_runtime_dir=$(dirname "$(dirname "$builder_prisma_client_dir")")/.prisma
      deployed_prisma_runtime_dir=$(dirname "$(dirname "$deployed_prisma_client_dir")")/.prisma
      rm -rf "$deployed_prisma_client_dir" "$deployed_prisma_runtime_dir"
      cp -a "$builder_prisma_client_dir" "$deployed_prisma_client_dir"
      cp -a "$builder_prisma_runtime_dir" "$deployed_prisma_runtime_dir"
      cp -a web/.next/standalone/. /payload/
      mkdir -p /payload/web/.next /payload/packages/shared/scripts /payload/bin
      cp -aL web/.next/static /payload/web/.next/static
      cp -aL web/public /payload/web/public
      cp -aL packages/shared/prisma /payload/packages/shared/prisma
      cp -aL packages/shared/clickhouse /payload/packages/shared/clickhouse
      cp packages/shared/scripts/cleanup.sql /payload/packages/shared/scripts/cleanup.sql
      cp web/entrypoint.sh /payload/web/entrypoint.sh
      npm install --prefix /payload/prisma-runtime --omit=dev prisma@6.19.3
      cp /usr/local/bin/node /payload/bin/node
      cp LICENSE /payload/LICENSE
      chmod 0755 /payload/bin/node /payload/web/entrypoint.sh
      printf "#!/bin/sh\nexec /opt/candace/csf/components/langfuse/bin/node /opt/candace/csf/components/langfuse/prisma-runtime/node_modules/prisma/build/index.js \"\$@\"\n" > /payload/bin/prisma
      chmod 0755 /payload/bin/prisma
    '
    docker run --rm --user "$(id -u):$(id -g)" --volume "$payload:/payload" "$go_image" \
      sh -euc 'GOBIN=/payload/bin CGO_ENABLED=0 go install -trimpath -tags clickhouse -ldflags="-s -w" github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1'
    ;;
  *) die "unsupported component: $component" ;;
esac

write_receipt
printf 'native payload: wrote %s (%s %s)\n' "$payload" "$component" "$version"
