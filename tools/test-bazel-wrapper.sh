#!/usr/bin/env bash
# Assert optional caches reach their actual container and Bazel consumers.
set -Eeuo pipefail

tool_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$tool_directory/.." && pwd -P)
wrapper="$tool_directory/bazel.sh"
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
fake_bin="$scratch/bin"
arguments="$scratch/docker-arguments"
mkdir -p -- "$fake_bin"

cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$@" >"$FAKE_DOCKER_ARGUMENTS"
EOF
chmod 0755 "$fake_bin/docker"

run_wrapper() {
  PATH="$fake_bin:$PATH" \
    FAKE_DOCKER_ARGUMENTS="$arguments" \
    CANDACE_BAZEL_CACHE="$scratch/cache" \
    CANDACE_BAZEL_DISK_CACHE=/bazel-home/sharedtaskcache \
    CANDACE_BAZEL_WORKSPACE="$module_root" \
    "$wrapper" "$@"
}

assert_tail() {
  local count=$1
  shift
  tail -n "$count" "$arguments" | cmp - <(printf '%s\n' "$@")
}

run_wrapper build //...
assert_tail 3 build --disk_cache=/bazel-home/sharedtaskcache //...
! grep -F -- 'CSF_OCAML_TOOLCHAIN_ROOT=' "$arguments"
! grep -F -- ':/csf-ocaml-toolchain' "$arguments"

CANDACE_OCAML_TOOLCHAIN_CACHE="$scratch/installation with spaces" run_wrapper build //...
grep -Fx -- "$scratch/installation with spaces:/csf-ocaml-toolchain" "$arguments"
grep -Fx -- 'CSF_OCAML_TOOLCHAIN_ROOT=/csf-ocaml-toolchain' "$arguments"
assert_tail 3 build --disk_cache=/bazel-home/sharedtaskcache //...

run_wrapper test //tools/example:all
assert_tail 3 test --disk_cache=/bazel-home/sharedtaskcache //tools/example:all

run_wrapper --batch test //tools/example:batch
assert_tail 4 --batch test --disk_cache=/bazel-home/sharedtaskcache //tools/example:batch

run_wrapper run //tools/example:binary
assert_tail 3 run --disk_cache=/bazel-home/sharedtaskcache //tools/example:binary

run_wrapper run //tools/example:binary -- --argument
assert_tail 5 run --disk_cache=/bazel-home/sharedtaskcache //tools/example:binary -- --argument

for command in mod query version; do
  case "$command" in
    mod) run_wrapper mod tidy test ;;
    query) run_wrapper query build ;;
    version) run_wrapper version ;;
  esac
  ! grep -F -- '--disk_cache=' "$arguments"
done

if [[ "${BAZEL_WRAPPER_STANDALONE_TEST:-}" != true ]]; then
  standalone="$scratch/standalone"
  mkdir -p -- "$standalone/tools"
  printf 'module(name = "fixture")\n' >"$standalone/MODULE.bazel"
  cp -- "$tool_directory/bazel.sh" "$tool_directory/test-bazel-wrapper.sh" "$standalone/tools/"
  BAZEL_WRAPPER_STANDALONE_TEST=true bash "$standalone/tools/test-bazel-wrapper.sh"
fi
