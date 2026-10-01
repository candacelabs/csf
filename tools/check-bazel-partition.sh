#!/usr/bin/env bash
# Partition the complete Bazel target inventory without dropping new targets.
# The runtime partitions do not resolve the compiler's development toolchains;
# the compiler partition builds and tests that closure explicitly.
set -Eeuo pipefail

module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
export CANDACE_BAZEL_WORKSPACE="$module_root"
bazel="$module_root/tools/bazel.sh"
partitions=(compiler go-libraries go-services go-programs go-chaos rust)

die() {
  printf 'CSF Bazel partition: %s\n' "$*" >&2
  exit 1
}

partition_for() {
  case "$1" in
    //csf/compiler:*|//csf/compiler/*|//csf/editor:*|//csf/editor/*|//csf/architecture/*|//tools/gorilla_mux_lint:*|//tools/devcontainer:*) printf '%s\n' compiler ;;
    //xetcas/go:*|//xetcas/go/*) printf '%s\n' go-libraries ;;
    //xetcas:*|//xetcas/*|//tools/csf-operator:*) printf '%s\n' rust ;;
    //pkg/gotth/test/internal/chaos:*|//pkg/gotth/test/internal/chaos/*) printf '%s\n' go-chaos ;;
    //pkg:*|//pkg/*|//proto:*|//proto/*) printf '%s\n' go-libraries ;;
    //services:*|//services/*) printf '%s\n' go-services ;;
    //*) printf '%s\n' go-programs ;;
    *) die "not a local target: $1" ;;
  esac
}

plan() {
  local directory=$1 target partition
  mkdir -p -- "$directory"
  # One machine-readable query retains tags and kinds without three cold
  # Docker/Bazel startups. Python's standard library is available on CI runners.
  "$bazel" query //... --lockfile_mode=error --output=xml > "$directory/targets.xml"
  python3 - "$directory" <<'PY'
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

directory = Path(sys.argv[1])
targets, manual, manual_tests = [], [], []
for rule in ET.parse(directory / "targets.xml").getroot().findall("rule"):
    name = rule.attrib["name"]
    targets.append(name)
    tags = {tag.attrib["value"] for tag in rule.findall("list[@name='tags']/string")}
    if "manual" in tags:
        manual.append(name)
        kind = rule.attrib["class"]
        if kind.endswith("_test") or kind == "test_suite":
            manual_tests.append(name)
for filename, labels in (("all.txt", targets), ("manual.txt", manual),
                         ("manual-tests.txt", manual_tests)):
    (directory / filename).write_text("".join(label + "\n" for label in sorted(set(labels))))
PY
  [[ -s "$directory/all.txt" ]] || die 'the target inventory is empty'
  sed '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$module_root/tools/bazel-manual-tests.txt" \
    | LC_ALL=C sort -u > "$directory/owned-manual.txt"
  diff -u "$directory/owned-manual.txt" "$directory/manual-tests.txt" \
    || die 'manual targets and their documented test owners differ'
  for partition in "${partitions[@]}"; do
    : > "$directory/$partition.txt"
  done
  while IFS= read -r target; do
    partition=$(partition_for "$target")
    printf '%s\n' "$target" >> "$directory/$partition.txt"
  done < "$directory/all.txt"
  git -C "$module_root" rev-parse HEAD > "$directory/source-sha.txt"
  for partition in "${partitions[@]}"; do
    printf '%s: %s targets\n' "$partition" "$(wc -l < "$directory/$partition.txt")"
  done
}

check() {
  local partition=$1 directory=$2
  local -a targets=() options=(--lockfile_mode=error)
  [[ " ${partitions[*]} " == *" $partition "* ]] || die "unknown partition: $partition"
  [[ "$(cat "$directory/source-sha.txt")" == "$(git -C "$module_root" rev-parse HEAD)" ]] \
    || die 'target inventory belongs to a different source revision'
  mapfile -t targets < "$directory/$partition.txt"
  [[ ${#targets[@]} -gt 0 ]] || die "empty partition: $partition"
  if [[ "$partition" != compiler ]]; then
    options+=(--ignore_dev_dependency)
  fi
  # One invocation builds every explicit target, including manual tests, but
  # executes only non-manual tests. Their documented Go/Cargo owners supply
  # the manual tests' runtime prerequisites. Override .bazelrc's test-only
  # default so binaries and libraries remain part of this complete gate.
  # Batch mode also avoids retaining a server in a disposable container.
  "$bazel" --batch test "${options[@]}" --nobuild_tests_only \
    --build_manual_tests --test_tag_filters=-manual --test_output=errors \
    -- "${targets[@]}"
}

case "${1:-}" in
  plan)
    [[ $# == 2 ]] || die 'usage: check-bazel-partition.sh plan OUTPUT_DIRECTORY'
    plan "$2"
    ;;
  check)
    [[ $# == 3 ]] || die 'usage: check-bazel-partition.sh check PARTITION PLAN_DIRECTORY'
    check "$2" "$3"
    ;;
  *) die 'expected plan or check' ;;
esac
