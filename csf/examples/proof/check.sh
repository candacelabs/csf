#!/usr/bin/env bash
set -euo pipefail

# This proof project downloads its own pinned toolchain; it installs nothing on
# the host and changes no shell profile. Dependencies: curl, sha256sum, tar,
# zstd, python3.
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
sha256sum BrainSpine.lean
"$lean_dir/bin/lean" --trust=0 --threads=2 --memory=2048 \
  -DwarningAsError=true BrainSpine.lean | tee "$cache_dir/check-output.txt"
python3 - "$cache_dir/check-output.txt" <<'PY'
import pathlib
import re
import sys

expected = {
    "BrainSpine.compile_correct",
    "BrainSpine.evaluate_bounds",
    "BrainSpine.compiled_actuator_correct",
    "BrainSpine.compiled_actuator_bounds",
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
if reports.keys() != expected:
    sys.exit(f"Incomplete axiom audit: expected {sorted(expected)}, got {sorted(reports)}")
for name, axioms in reports.items():
    if axioms - allowed:
        sys.exit(f"Disallowed axioms in {name}: {sorted(axioms - allowed)}")
print("PASS: 4 theorem axiom audits; no sorry, custom axioms, or compiler-trust axioms.")
PY
