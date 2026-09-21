#!/usr/bin/env bash
#
# Regenerate every checked-in generated widget in this checkout.
#
# Generated code is committed. That is a requirement rather than a convenience:
# a consumer of this module must be able to `go get` it and build, with no templ
# compiler and no generator binary anywhere on their machine.
#
# The generator is built from this checkout's own pkg/widget/internal/uigen
# rather than fetched, so the generator that produced the committed output is
# always the generator committed beside it. It is a package of the SAME module,
# so what generation needs mounted is the EXPORT ROOT — candace/ — rather than
# this directory. From the export root:
#
#     docker run --rm -v "$PWD:/workspace" -w /workspace \
#         dis-gotth-live:latest bash pkg/widget/gen.sh
#
# The image is pkg/gotth's (pkg/gotth/.dis/Dockerfile); it carries the pinned
# templ CLI, which is the only tool this script needs beyond the Go toolchain.
# There is deliberately no second image: two images pinning one templ CLI is two
# places to keep in step, and the version check below already asserts that the
# CLI matches the module's templ runtime.
#
# What it produces, relative to the export root candace/, per widget in the list
# below plus the demo host's own page:
#
#   examples/widget/<widget>/view.templ       the generated templ view
#   examples/widget/<widget>/view_templ.go    its compiled component
#   examples/widget/<widget>/widget.gen.go    the SDK contract implementation
#   examples/widget/page_templ.go             the demo host's own page
#
# The mockgen double at internal/mocks/mocks.go is NOT here, and generate.go
# says why: the image carries no mockgen, and adding a tool to it to regenerate
# a file that changes only when IWidget changes buys a check that fires roughly
# never.
#
# The script is cwd-independent: every path is derived from its own location.

set -euo pipefail

# module_dir is this package's own tree; export_root is candace/, the root of
# the module every path below belongs to, and the shallowest thing that has to
# be mounted — the generator source is pkg/widget/internal/uigen, inside it.
#
# owner_ref is what the generated files are handed back to at the end. It is the
# export root rather than the repository root above it, because the repository
# root is not necessarily mounted: a container given only candace/ would find
# "${export_root}/.." at the container filesystem root and chown every output to
# root, which is precisely the failure this chown exists to undo.
module_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export_root="$(cd "${module_dir}/../.." && pwd)"
owner_ref="${export_root}"

# --check regenerates and asserts the committed output is byte-identical,
# restoring the working tree either way. It is a mode of this script rather than
# a second script so that the thing CI runs and the thing a developer runs
# cannot drift from each other.
#
# The failure it exists to catch is mundane: an edit to a widget document that
# is committed without regenerating, so HEAD contains generated code no run of
# this script would produce.
check_mode=0
case "${1:-}" in
  --check) check_mode=1 ;;
  "") ;;
  *)
    echo "gen.sh: unknown argument ${1}; usage: gen.sh [--check]" >&2
    exit 2
    ;;
esac

# widgets lists every widget this script generates, as
# "<document>|<output directory>|<package>", relative to the EXPORT ROOT.
#
# Every exemplar that describes a real widget is here. The first is the flagship
# — every block of the dialect appears in it — and it is what makes this list a
# check rather than a formality: a construct the generator stops emitting fails
# here, on the document that uses it, rather than at the next release.
#
# The third arrived late, and how it arrived is the argument for the list being
# exhaustive. 03 generated and compiled the whole time; the P2 audit checked it
# by hand and said so, and nothing in the repository asserted it, so a generator
# change that broke a chain-shaped scene would have been found by the next person
# to read the file. An exemplar the gate does not generate is an exemplar the
# gate does not cover.
widgets=(
  "pkg/widget/docs/examples/01-cluster-heartbeats.widget|examples/widget/clusterheartbeats|clusterheartbeats"
  "pkg/widget/docs/examples/02-node-status.widget|examples/widget/nodestatus|nodestatus"
  "pkg/widget/docs/examples/03-relay-pipeline.widget|examples/widget/relaypipeline|relaypipeline"
  "examples/widget/candaws/docs/yakshave.widget|examples/widget/candaws/yakshave|yakshave"
  "examples/widget/candaws/docs/queuecumber.widget|examples/widget/candaws/queuecumber|queuecumber"
  "examples/widget/candaws/docs/blobfish.widget|examples/widget/candaws/blobfish|blobfish"
  "examples/widget/candaws/docs/coldstart.widget|examples/widget/candaws/coldstart|coldstart"
  "examples/widget/candaws/docs/dashbored.widget|examples/widget/candaws/dashbored|dashbored"
)

# handwritten_templ lists the .templ files a person wrote whose compiled output
# is committed. They are covered for the same reason the generated view is:
# "generated code is byte-reproducible" is a property of the code rather than of
# who wrote its source, and a .templ edit committed without regenerating leaves
# committed output no run of this script would produce.
handwritten_templ=(
  examples/widget/page.templ
  examples/widget/candaws/page.templ
)

# generated is every file this script writes, relative to the export root. A new
# output must appear here or the check silently stops covering it — and the walk
# below is what keeps the enumeration honest, because an enumeration maintained
# by hand is exactly the thing that goes stale.
generated=()
generated_templ=()
for entry in "${widgets[@]}"; do
  IFS='|' read -r _document output_dir _package <<<"${entry}"
  generated+=("${output_dir}/view.templ" "${output_dir}/widget.gen.go")
  generated_templ+=("${output_dir}/view.templ")
done

templ_sources=("${generated_templ[@]}" "${handwritten_templ[@]}")
for src in "${templ_sources[@]}"; do
  generated+=("${src%.templ}_templ.go")
done

for tool in templ go gofmt; do
  command -v "${tool}" >/dev/null || {
    echo "gen.sh: ${tool} is not on PATH; run this inside the toolchain container" >&2
    exit 1
  }
done
[ -d "${export_root}/pkg/widget/internal/uigen" ] || {
  echo "gen.sh: ${export_root}/pkg/widget/internal/uigen not found." >&2
  echo "gen.sh: mount the EXPORT ROOT candace/, not just this directory — see the header." >&2
  exit 1
}

# The enumeration above, held complete against the tree it claims to describe.
#
# An output nobody listed is an output nobody compares, and the failure is silent
# in both cases. The walk covers every directory this script writes into, which
# is why it starts from the widgets' own output directories rather than from a
# fixed path: a widget generated somewhere new is caught the day it lands.
walk_dirs=()
for entry in "${widgets[@]}"; do
  IFS='|' read -r _document output_dir _package <<<"${entry}"
  walk_dirs+=("${output_dir}")
done
for src in "${handwritten_templ[@]}"; do
  walk_dirs+=("$(dirname "${src}")")
done
readarray -t unique_walk_dirs < <(printf '%s\n' "${walk_dirs[@]}" | sort -u)

# A widget being generated for the first time has no output directory yet, and a
# directory that does not exist holds no .templ file this enumeration could be
# failing to cover. Dropping it keeps the check honest and lets the first run of
# a new widget create it; the --check mode below is what would catch a widget
# whose output was then never committed.
present_walk_dirs=()
for candidate in "${unique_walk_dirs[@]}"; do
  [ -d "${export_root}/${candidate}" ] && present_walk_dirs+=("${candidate}")
done

# The walk roots may nest — a widget's output directory sits inside the demo
# host's — so find visits a path once per enclosing root and the result is
# deduplicated.
#
# Only one direction of the comparison is a fault: a .templ file in this checkout
# that nothing here lists is a source this script silently stops covering. The
# reverse — a listed source that is not on disk yet — is what the first run of a
# new widget looks like, and every other way it can happen already fails loudly:
# a widgets entry naming a document that does not exist fails at the generator, a
# hand-written source that vanished fails at templ, and an output that is
# generated but never committed fails in --check.
found_templ="$(cd "${export_root}" && find "${present_walk_dirs[@]}" -name '*.templ' \
  -not -path '*/node_modules/*' | sort -u)"
listed_templ="$(printf '%s\n' "${templ_sources[@]}" | sort)"
unlisted="$(comm -23 \
  <(printf '%s\n' "${found_templ}" | grep -v '^$') \
  <(printf '%s\n' "${listed_templ}" | grep -v '^$'))"
if [ -n "${unlisted}" ]; then
  echo "gen.sh: this checkout holds a .templ file this script does not cover:" >&2
  printf '  %s\n' ${unlisted} >&2
  echo "gen.sh: listed:" >&2
  printf '  %s\n' ${listed_templ:-"(none)"} >&2
  echo "gen.sh: add the new source to widgets or handwritten_templ, or this script stops covering it." >&2
  exit 1
fi

# The templ CLI must be the version of the templ runtime the generated code
# compiles against. It is not a preference: the generated file names its
# generator in a comment and calls into that runtime's API, so a CLI newer or
# older than the module's requirement produces bytes the committed output does
# not have, and --check reports it as drift with no clue as to why.
templ_cli_version="$(templ version 2>/dev/null | tr -d '[:space:]')"
templ_runtime_version="$(cd "${export_root}" && go list -m -f '{{.Version}}' github.com/a-h/templ)"
if [ "${templ_cli_version}" != "${templ_runtime_version}" ]; then
  echo "gen.sh: the templ CLI is ${templ_cli_version}, but the module requires the templ runtime ${templ_runtime_version}." >&2
  echo "gen.sh: they must match, or the committed output is not what this generator produces." >&2
  echo "gen.sh: the CLI is pinned in pkg/gotth/.dis/Dockerfile; rebuild the image, or move the go.mod requirement." >&2
  exit 1
fi

baseline_dir=""
cleanup() {
  if [ "${check_mode}" = 1 ] && [ -n "${baseline_dir}" ] && [ -d "${baseline_dir}" ]; then
    for rel in "${generated[@]}"; do
      if [ -f "${baseline_dir}/${rel}" ]; then
        mkdir -p "${export_root}/$(dirname "${rel}")"
        cp "${baseline_dir}/${rel}" "${export_root}/${rel}"
      else
        rm -f "${export_root}/${rel}"
      fi
    done
  fi
  [ -n "${baseline_dir}" ] && rm -rf "${baseline_dir}"
  return 0
}
trap cleanup EXIT

if [ "${check_mode}" = 1 ]; then
  baseline_dir="$(mktemp -d)"
  for rel in "${generated[@]}"; do
    if [ -f "${export_root}/${rel}" ]; then
      mkdir -p "${baseline_dir}/$(dirname "${rel}")"
      cp "${export_root}/${rel}" "${baseline_dir}/${rel}"
    fi
  done
fi

# ---------------------------------------------------------------------------
# 1. Generate each widget from its document.
#
# widgetc refuses to generate from a document that reported anything, so a
# document edited into an invalid state stops here with its findings printed
# rather than emitting a widget nobody wrote.
# ---------------------------------------------------------------------------
for entry in "${widgets[@]}"; do
  IFS='|' read -r document output_dir package <<<"${entry}"
  echo "==> generating ${output_dir} from ${document}"
  (cd "${export_root}" && go run ./pkg/widget/internal/cmd/widgetc generate \
    -package "${package}" -out "${output_dir}" "${document}" >/dev/null)
done

# ---------------------------------------------------------------------------
# 2. Compile the templ views, generated and hand-written alike.
#
# One invocation per source rather than a path walk, so what is covered depends
# on the enumeration above rather than on where the files happen to sit. -f also
# keeps a failure attributable: a template that does not compile names itself.
#
# -lazy is deliberately not passed. It regenerates only when the .templ is newer
# than its output, which is a build optimisation and a reproducibility hole: a
# checkout whose mtimes arrive in the wrong order would have --check compare the
# committed file against itself.
# ---------------------------------------------------------------------------
echo "==> compiling the templ views"
for src in "${templ_sources[@]}"; do
  (cd "$(dirname "${export_root}/${src}")" && templ generate -f "$(basename "${src}")")
done

# Handed back immediately rather than at the end, because both generators replace
# a file rather than rewriting it in place, so the output arrives owned by root
# even in --check mode — where the tree is meant to be left exactly as it was
# found, ownership included.
for rel in "${generated[@]}"; do
  chown --reference="${owner_ref}" "${export_root}/${rel}" 2>/dev/null || true
done

# The output directories are handed back as well as the files in them. A widget
# generated for the first time has its directory created here, and a directory
# left owned by the container's user is one the next tool to write into it —
# gazelle, a formatter, the next run of this script — cannot.
for entry in "${widgets[@]}"; do
  IFS='|' read -r _document output_dir _package <<<"${entry}"
  chown --reference="${owner_ref}" "${export_root}/${output_dir}" 2>/dev/null || true
done

# ---------------------------------------------------------------------------
# 3. Check the output.
# ---------------------------------------------------------------------------
echo "==> checking"
emitted_go=()
for rel in "${generated[@]}"; do
  case "${rel}" in
    *.go) emitted_go+=("${rel}") ;;
  esac
done
unformatted="$(cd "${export_root}" && gofmt -l "${emitted_go[@]}")"
[ -z "${unformatted}" ] || {
  echo "gen.sh: generated files are not gofmt-clean:" >&2
  echo "${unformatted}" >&2
  exit 1
}

# Built at the export root rather than in this package's tree, because the
# widgets land under examples/ — a sibling of pkg/ — and a build scoped to
# pkg/widget would not reach a single one of them.
(cd "${export_root}" && go build ./...)

# ---------------------------------------------------------------------------
# 4. --check: assert every committed output is what this generator produces.
#
# The comparison is byte-for-byte rather than a diff of meaning, because the
# claim being made is reproducibility. The EXIT trap restores every listed output
# even when a generator or the build fails midway through the check.
# ---------------------------------------------------------------------------
if [ "${check_mode}" = 1 ]; then
  echo "==> comparing against the committed output"
  drift=0
  for rel in "${generated[@]}"; do
    if [ ! -f "${baseline_dir}/${rel}" ]; then
      echo "gen.sh: ${rel} is produced by this script but is not committed" >&2
      drift=1
      continue
    fi
    if ! cmp -s "${baseline_dir}/${rel}" "${export_root}/${rel}"; then
      echo "gen.sh: ${rel} is not what this generator produces" >&2
      drift=1
    fi
  done

  if [ "${drift}" != 0 ]; then
    echo "gen.sh: the committed generated code does not match its source." >&2
    echo "gen.sh: run gen.sh without --check and commit the result." >&2
    exit 1
  fi
  echo "==> every committed output is byte-identical to a fresh generation"
  exit 0
fi

echo "==> generated"
for rel in "${generated[@]}"; do
  echo "${export_root}/${rel}"
done
