#!/usr/bin/env bash
set -euo pipefail

# The decision-tree proof downloads its own pinned toolchain; it installs
# nothing on the host and changes no shell profile. Dependencies: curl,
# sha256sum, tar, zstd, python3. CSFCVerifier.lean is emitted by csfc and never
# hand-written: the golden test //csf/compiler/architecture:decision_test holds
# the committed file to the emitter's output byte for byte.
proof_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
toolchain="$(cat "$proof_dir/lean-toolchain")"
expected_toolchain='leanprover/lean4:v4.34.0'
archive_sha256='caaa98356098c85dc0fcbbd28e1ec66f39eb6551829972b752ff20e1286b646b'
lean_commit='293d5d0c0c3f3dded4688b3ccd6a33939ac5102b'
archive_name='lean-4.34.0-linux.tar.zst'
archive_url="https://github.com/leanprover/lean4/releases/download/v4.34.0/$archive_name"

if [[ "$toolchain" != "$expected_toolchain" ]]; then
  printf 'Toolchain pin and bootstrap metadata disagree.\n' >&2
  exit 1
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'This bootstrap is pinned for Linux x86_64.\n' >&2
  exit 1
fi

cache_dir="$proof_dir/.cache"
archive="$cache_dir/$archive_name"
lean_dir="$cache_dir/lean-4.34.0-linux"
mkdir -p "$cache_dir"
if [[ ! -f "$archive" ]]; then
  curl --fail --location --retry 3 --proto '=https' --tlsv1.2 \
    --output "$archive.part" "$archive_url"
  mv -- "$archive.part" "$archive"
fi
printf '%s  %s\n' "$archive_sha256" "$archive" | sha256sum --check --status
if [[ ! -x "$lean_dir/bin/lean" ]]; then
  tar --zstd -xf "$archive" -C "$cache_dir"
fi

if [[ "$("$lean_dir/bin/lean" --short-version)" != '4.34.0' || \
      "$("$lean_dir/bin/lean" --githash)" != "$lean_commit" ]]; then
  printf 'Extracted Lean executable does not match the pinned release.\n' >&2
  exit 1
fi
printf 'Verified toolchain archive SHA256: %s\n' "$archive_sha256"
"$lean_dir/bin/lean" --version

cd "$proof_dir"
sha256sum CSFCVerifier.lean

# The emitted file is a Lean library, so the lake project builds.
PATH="$lean_dir/bin:$PATH" lake build

# The same file with compiler trust disabled and warnings as errors: any `sorry`
# becomes a build failure, and the theorems print the axioms they rest on.
output_file="$cache_dir/check-output.txt"
"$lean_dir/bin/lean" --trust=0 --threads=2 --memory=2048 \
  -DwarningAsError=true CSFCVerifier.lean | tee "$output_file"
python3 - "$output_file" <<'PY'
import pathlib
import re
import sys

expected = {
    "CSFC.Verification.at_most_16",
    "CSFC.Verification.one_way",
    "CSFC.Verification.composes",
}
allowed = {"propext", "Classical.choice", "Quot.sound"}
reports = {}
for line in pathlib.Path(sys.argv[1]).read_text().splitlines():
    match = re.fullmatch(r"'([^']+)' depends on axioms: \[([^]]*)\]", line)
    if match:
        name, axioms = match.groups()
        if name in reports:
            sys.exit(f"Duplicate axiom audit for {name}")
        reports[name] = set(filter(None, (item.strip() for item in axioms.split(","))))
        continue
    match = re.fullmatch(r"'([^']+)' does not depend on any axioms", line)
    if match:
        name = match.group(1)
        if name in reports:
            sys.exit(f"Duplicate axiom audit for {name}")
        reports[name] = set()
if reports.keys() != expected:
    sys.exit(f"Incomplete axiom audit: expected {sorted(expected)}, got {sorted(reports)}")
for name, axioms in reports.items():
    if axioms - allowed:
        sys.exit(f"Disallowed axioms in {name}: {sorted(axioms - allowed)}")
print("PASS: 3 decision-tree theorem audits; no sorry, custom axioms, or compiler-trust axioms.")
PY
