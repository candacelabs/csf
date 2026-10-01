#!/usr/bin/env bash
# Regenerate or verify the relational PostgreSQL bindings for the adapter.
set -euo pipefail

store_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:-write}"
sqlc_image="sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537"

case "${mode}" in
  write | check) ;;
  *)
    echo "usage: $0 [write|check]" >&2
    exit 2
    ;;
esac

run_sqlc() {
  local source_dir="$1"
  shift
  docker run --rm \
    --platform linux/amd64 \
    --user "$(id -u):$(id -g)" \
    --volume "${source_dir}:/src" \
    --workdir /src/store \
    "${sqlc_image}" \
    "$@"
}

if [[ "${mode}" == write ]]; then
  case "${store_dir}" in
    */services/copilot-adapter/store) ;;
    *)
      echo "refusing to generate in unexpected path: ${store_dir}" >&2
      exit 1
      ;;
  esac
  service_dir="$(dirname "${store_dir}")"
  mkdir -p "${service_dir}/storedb"
  run_sqlc "${service_dir}" vet -f sqlc.yaml
  run_sqlc "${service_dir}" generate -f sqlc.yaml
  exit 0
fi

service_dir="$(dirname "${store_dir}")"
check_dir="$(mktemp -d "${TMPDIR:-/tmp}/candace-copilot-adapter-sqlc.XXXXXX")"
cleanup() {
  rm -rf -- "${check_dir}"
}
trap cleanup EXIT

mkdir -p "${check_dir}/store" "${check_dir}/storedb"
cp "${store_dir}/sqlc.yaml" "${store_dir}/queries.sql" "${check_dir}/store/"
cp -R "${store_dir}/migrations" "${check_dir}/store/migrations"
run_sqlc "${check_dir}" vet -f sqlc.yaml
run_sqlc "${check_dir}" generate -f sqlc.yaml

for generated_file in db.go models.go querier.go queries.sql.go; do
  diff -u "${service_dir}/storedb/${generated_file}" "${check_dir}/storedb/${generated_file}"
done
