#!/bin/sh
# Assert that every bench app's committed bench/ready.js is byte-identical to
# bench/harness/ready.js. Exits 1 on drift, naming the file that drifted.
#
#   sh scripts/verify-ready.sh      (or: npm run verify:ready)
#
# The verifying half of D-6 (docs/reviews/deduplication.md §6). Regenerate with
# `node scripts/sync-ready.mjs`, which is where the model is written down.
#
# -----------------------------------------------------------------------------
# Why this is /bin/sh and not `node scripts/sync-ready.mjs --verify`
#
# sync-shim.mjs verifies in node because shim.js's copies only exist where node
# ran. ready.js's copies are `//go:embed`-ed, so they are tracked, so they can
# drift in a checkout where node was never installed — and the gate that would
# catch it, ci.sh's `bench/apps/*/gotth` step, runs in dis-gotth-live:latest,
# which has no node and no npm. A verifier reachable only through `npm run` is
# a verifier that gate cannot run, so this one needs nothing but a shell and
# cmp, both of which are in every image this repository builds in.
#
# It is ONE verifier with two callers rather than one per world: a duplication
# finding answered with a second copy of the check would be a poor joke.
set -eu

bench_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
source_file="$bench_root/harness/ready.js"

if [ ! -f "$source_file" ]; then
	echo "MISSING  $source_file (the source of truth; nothing to verify against)" >&2
	exit 1
fi

bad=0
for app in counter chat dashboard; do
	target="$bench_root/apps/$app/gotth/bench/ready.js"
	if [ ! -f "$target" ]; then
		# Not a soft failure: //go:embed resolves this path at compile time, so
		# an absent copy is a bench module that does not build.
		echo "MISSING  $target" >&2
		bad=$((bad + 1))
	elif cmp -s "$source_file" "$target"; then
		echo "ok       $target"
	else
		echo "DRIFTED  $target" >&2
		cmp "$source_file" "$target" >&2 || true
		bad=$((bad + 1))
	fi
done

# The digest is a convenience for reading two runs side by side; cmp above is
# what decides, so a stripped-down image without sha256sum still verifies.
if command -v sha256sum >/dev/null 2>&1; then
	echo "ready.js $(wc -c <"$source_file" | tr -d ' ') bytes  sha256:$(sha256sum "$source_file" | cut -d' ' -f1)"
fi

if [ "$bad" -ne 0 ]; then
	echo "" >&2
	echo "$bad copy/copies of harness/ready.js drifted or are missing (D-6)." >&2
	echo "harness/ready.js is the source of truth; regenerate the copies with:" >&2
	echo "    node scripts/sync-ready.mjs        (needs the bench image's node)" >&2
	exit 1
fi
