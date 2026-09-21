#!/usr/bin/env bash
# Generate or verify the checked-in SQLC bindings for durable cron state.
set -euo pipefail

postgres_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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
    --workdir /src \
    "${sqlc_image}" \
    "$@"
}

if [[ "${mode}" == write ]]; then
  run_sqlc "${postgres_dir}" vet -f sqlc.yaml
  run_sqlc "${postgres_dir}" generate -f sqlc.yaml
  exit 0
fi

check_dir="$(mktemp -d "${TMPDIR:-/tmp}/candace-cron-sqlc.XXXXXX")"
cleanup() {
  rm -rf -- "${check_dir}"
}
trap cleanup EXIT

cp "${postgres_dir}/sqlc.yaml" "${postgres_dir}/queries.sql" "${check_dir}/"
cp -R "${postgres_dir}/migrations" "${check_dir}/migrations"
run_sqlc "${check_dir}" vet -f sqlc.yaml
run_sqlc "${check_dir}" generate -f sqlc.yaml

generated_files=(db.go models.go queries.sql.go)
for generated_file in "${generated_files[@]}"; do
  diff -u "${postgres_dir}/${generated_file}" "${check_dir}/${generated_file}"
done
