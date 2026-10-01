#!/usr/bin/env bash
#
# Regenerate every checked-in generated file in candace/pkg/gotth.
#
# Generated code is committed. That is a requirement, not a convenience: a
# consumer of this module must be able to `go get` it and build, with no
# protoc, no buf and no plugin binary anywhere on their machine.
#
# protoc-gen-liquidproto is built from the canonical plugin source in this
# checkout rather than fetched, so the generator that produced the committed
# output is always the generator committed beside it. That source is
# pkg/liquidproto, a package of the SAME module, so what generation needs
# mounted is the EXPORT ROOT — candace/ — and no longer the repository root
# above it. From the export root:
#
#     docker run --rm -v "$PWD:/workspace" -w /workspace \
#         dis-gotth-live:latest bash pkg/gotth/gen.sh
#
# The image is built from pkg/gotth/.dis/Dockerfile and carries protoc,
# protoc-gen-go and the templ compiler. `dis up` builds and tags it, but binds
# only this directory at /workspace, which is why this runs as a plain
# docker run against the export root instead.
#
# What it produces, relative to the export root candace/:
#
#   pkg/gotth/internal/protocol/gotthlivepb/frame.pb.go   the frame messages
#   pkg/gotth/internal/protocol/gotthlivepb/frame_liquid.pb.go  their validators
#   examples/gotth/*/view_templ.go                        the templ views
#   pkg/gotth/docs/guide/_samples/*/view_templ.go         the guide's templ views
#
# The templ outputs were covered by nothing until O-1. FR-7 says generated code
# is byte-reproducible and calls this script's --check the gate, and half the
# generated code in this repository is written by `templ generate`, committed,
# and was outside the enumeration below — so an edit to a .templ committed
# without regenerating produced exactly the drift the check exists to catch,
# and the check agreed the tree was clean.
#
# The script is cwd-independent: every path is derived from its own location.

set -euo pipefail

# module_dir is this library's own tree; export_root is candace/, the root of
# the module every path below belongs to, the tree the examples moved into when
# gotth-live became candace/pkg/gotth, AND the shallowest thing that has to be
# mounted — the plugin source is pkg/liquidproto, inside it.
#
# owner_ref is what the generated files are handed back to at the end. It is
# the export root rather than the repository root above it, because the
# repository root is not necessarily mounted: a container given only candace/
# would find "${export_root}/.." at the container filesystem root and chown
# every output to root, which is precisely the failure this chown exists to
# undo.
module_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export_root="$(cd "${module_dir}/../.." && pwd)"
owner_ref="${export_root}"
liquid_include_dir="${export_root}/pkg"
plugin_module_dir="${export_root}"

module="github.com/candacelabs/csf/pkg/gotth"

# --check regenerates and asserts the committed output is byte-identical,
# restoring the working tree either way. It is the reproducibility gate the
# protocol document promises, and it is a mode of this script rather than a
# second script so that the thing CI runs and the thing a developer runs cannot
# drift from each other.
#
# The failure it exists to catch is mundane and has already happened once: an
# edit to a comment in the .proto that is committed without regenerating, so
# HEAD contains generated code no run of this script would produce.
check_mode=0
case "${1:-}" in
  --check) check_mode=1 ;;
  "") ;;
  *)
    echo "gen.sh: unknown argument ${1}; usage: gen.sh [--check]" >&2
    exit 2
    ;;
esac

# generated lists every file this script produces, relative to the EXPORT ROOT
# rather than to this library's directory. A new output must be added here or
# the check silently stops covering it.
#
# The base is the export root because the templ outputs appended below are no
# longer all inside this tree: the three examples sit at examples/gotth/, a
# sibling of pkg/, and a list with "../../" in it would send the --check
# baseline copies outside the scratch directory they are meant to stay in.
generated=(
  pkg/gotth/internal/protocol/gotthlivepb/frame.pb.go
  pkg/gotth/internal/protocol/gotthlivepb/frame_liquid.pb.go
  pkg/gotth/client/codec.gen.js
  pkg/gotth/client/predicates.manifest.txt
  pkg/gotth/client/test/golden.json
)

# templ_sources lists every .templ file this generator owns, relative to the
# export root. Each one's output is derived — foo.templ becomes
# foo_templ.go — and appended to `generated` below, so the templ outputs get
# the same baseline-and-compare treatment as the protobuf ones.
#
# Enumerated rather than globbed, for the reason the comment above gives: the
# list is what a reader consults to learn what is covered. But an enumeration
# that must be edited by hand is exactly the thing that goes stale, and O-1 is
# what a stale enumeration costs — so the list is *also* checked against a walk
# of the tree, below. Explicit and complete, rather than one or the other.
#
# The docs/guide/_samples entries are the walk's second catch, and they are here
# rather than on an exclusion list on purpose. The samples module landed five
# .templ files with their generated output committed beside them, and the
# question the gate had to answer was whether FR-7 covers generated code that
# happens to live under docs/. It does: "generated code is byte-reproducible" is
# a property of the code, not of the directory it sits in, and a reader who
# copies a sample gets the committed _templ.go with it. Excluding them would
# have made the guard green by shrinking what it looks at, which is this
# repository's recurring defect rather than a fix for it.
templ_sources=(
  examples/gotth/counter/view.templ
  examples/gotth/chat/view.templ
  examples/gotth/dashboard/view.templ
  pkg/gotth/docs/guide/_samples/apptest/view.templ
  pkg/gotth/docs/guide/_samples/events/view.templ
  pkg/gotth/docs/guide/_samples/fragments/view.templ
  pkg/gotth/docs/guide/_samples/htmxinterop/view.templ
  pkg/gotth/docs/guide/_samples/quickstart/view.templ
  # The benchmark's gotth-live apps. They are measured artifacts rather than
  # shipped ones, and they are here for the same reason the samples are: their
  # view_templ.go is committed, so a .templ edit landed without regenerating
  # leaves committed generated code no run of this script would produce,
  # whatever the directory is called. The walk below found the counter the day
  # it landed and the chat room the day after, which is the enumeration working
  # as designed — the list is what a reader consults, and the walk is what keeps
  # the list honest. LISTED, NOT EXCLUDED, in both cases.
  pkg/gotth/bench/apps/counter/gotth/view.templ
  pkg/gotth/bench/apps/chat/gotth/view.templ
  pkg/gotth/bench/apps/dashboard/gotth/view.templ
)

templ_outputs=()
for src in "${templ_sources[@]}"; do
  templ_outputs+=("${src%.templ}_templ.go")
done
generated+=("${templ_outputs[@]}")

for tool in protoc protoc-gen-go templ go gofmt; do
  command -v "${tool}" >/dev/null || {
    echo "gen.sh: ${tool} is not on PATH; run this inside the dev container" >&2
    exit 1
  }
done
[ -d "${plugin_module_dir}/pkg/liquidproto/cmd/protoc-gen-liquidproto" ] || {
  echo "gen.sh: ${plugin_module_dir}/pkg/liquidproto/cmd/protoc-gen-liquidproto not found." >&2
  echo "gen.sh: mount the EXPORT ROOT candace/, not just this directory — see the header." >&2
  exit 1
}

# The enumeration above, held complete against the tree it claims to describe.
#
# This is the check O-1 was missing one level up: an output nobody listed is an
# output nobody compares, and the failure is silent in both cases. Node modules
# are excluded because the Phase 5 comparison app lands one by design and it is
# not this module's source — the same exclusion internal/arch's package walk
# already makes, for the same reason.
found_templ="$(cd "${export_root}" && find pkg/gotth examples/gotth -name '*.templ' \
  -not -path '*/node_modules/*' | sort)"
listed_templ="$(printf '%s\n' "${templ_sources[@]}" | sort)"
if [ "${found_templ}" != "${listed_templ}" ]; then
  echo "gen.sh: templ_sources does not match the .templ files in this checkout." >&2
  echo "gen.sh: listed:" >&2
  printf '  %s\n' ${listed_templ:-"(none)"} >&2
  echo "gen.sh: found:" >&2
  printf '  %s\n' ${found_templ:-"(none)"} >&2
  echo "gen.sh: add the new source to templ_sources, or this script stops covering it." >&2
  exit 1
fi

# There is one module now, so there is no per-source module to find.
#
# This is where enclosing_module_dir used to be: a walk up from each .templ for
# the nearest go.mod, because every example, sample and benchmark application
# was its own module and each had its own templ requirement to check against.
# The single-module fold left that walk with one possible answer — the export
# root — so it is named rather than searched for. D-8's sentinel bug went with
# it; a loop that cannot run cannot lose its sentinel again.
# The templ CLI must be the version of the templ runtime the code being
# generated compiles against. It is not a preference: the generated file names
# its generator in a comment and calls into that runtime's API, so a CLI newer
# or older than the module's requirement produces bytes the committed output
# does not have, and --check reports it as drift with no clue as to why.
#
# Checked against each module's own go.mod rather than against a literal here,
# because a literal would be a third place to keep in step with .dis/Dockerfile
# and go.mod — and .dis/Dockerfile's own comment already states the invariant
# ("the templ CLI is the same version as the templ runtime") without anything
# enforcing it.
templ_cli_version="$(templ version 2>/dev/null | tr -d '[:space:]')"
templ_runtime_version="$(cd "${export_root}" && go list -m -f '{{.Version}}' github.com/a-h/templ)"
if [ "${templ_cli_version}" != "${templ_runtime_version}" ]; then
  echo "gen.sh: the templ CLI is ${templ_cli_version}, but the module requires the templ runtime ${templ_runtime_version}." >&2
  echo "gen.sh: they must match, or the committed output is not what this generator produces." >&2
  echo "gen.sh: the CLI is pinned in .dis/Dockerfile; rebuild the image, or move the go.mod requirement." >&2
  exit 1
fi

bin_dir=""
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
  [ -n "${bin_dir}" ] && rm -rf "${bin_dir}"
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
# 1. Build the canonical protoc-gen-liquidproto from the public candace module.
# ---------------------------------------------------------------------------
echo "==> building protoc-gen-liquidproto from ${plugin_module_dir}/pkg/liquidproto"
bin_dir="$(mktemp -d)"
(cd "${plugin_module_dir}" && go build -o "${bin_dir}/protoc-gen-liquidproto" ./pkg/liquidproto/cmd/protoc-gen-liquidproto)

# ---------------------------------------------------------------------------
# 2. Generate the wire protocol: base messages plus Liquid Proto validators.
# ---------------------------------------------------------------------------
echo "==> generating internal/protocol/gotthlivepb (frames + validators)"
protoc \
  -I "${liquid_include_dir}" \
  -I "${module_dir}/proto" \
  -I /usr/local/include \
  --plugin=protoc-gen-liquidproto="${bin_dir}/protoc-gen-liquidproto" \
  --go_out="module=${module}:${module_dir}" \
  --liquidproto_out="module=${module}:${module_dir}" \
  gotthlive/v1/frame.proto

# ---------------------------------------------------------------------------
# 2b. Generate the templ views.
#
# One invocation per source rather than `templ generate -path .`, because the
# .templ files live in the example modules and a path walk from here would make
# what is covered depend on where the files happen to sit rather than on the
# enumeration above. -f also keeps the failure attributable: a template that
# does not compile names itself.
#
# -lazy is deliberately not passed. It regenerates only when the .templ is
# newer than its output, which is a build optimisation and a reproducibility
# hole: a checkout whose mtimes arrive in the wrong order would have --check
# compare the committed file against itself.
# ---------------------------------------------------------------------------
echo "==> generating the templ views"
for src in "${templ_sources[@]}"; do
  (cd "$(dirname "${export_root}/${src}")" && templ generate -f "$(basename "${src}")")
done

# Handed back immediately rather than at the end, because templ replaces the
# file rather than rewriting it in place, so the output arrives owned by root
# even in --check mode — where the tree is meant to be left exactly as it was
# found, ownership included. The protobuf outputs are chowned at the end
# because protoc writes in place and --check never changes their owner.
for rel in "${templ_outputs[@]}"; do
  chown --reference="${owner_ref}" "${export_root}/${rel}" 2>/dev/null || true
done

# ---------------------------------------------------------------------------
# 3. Check the output.
# ---------------------------------------------------------------------------
echo "==> checking"
unformatted="$(cd "${export_root}" && gofmt -l pkg/gotth/internal "${templ_outputs[@]}")"
[ -z "${unformatted}" ] || {
  echo "gen.sh: generated files are not gofmt-clean:" >&2
  echo "${unformatted}" >&2
  exit 1
}
(cd "${module_dir}" && go build ./...)

# The examples sit outside this tree, so `go build ./...` above does not reach
# them and a templ output that does not compile would otherwise be found by
# ci.sh rather than by the script that wrote it. One build of the whole module
# covers every templ output wherever it landed.
(cd "${export_root}" && go build ./...)

# protoc, templ and go run as root inside the container; hand the files back to
# whoever owns the checkout on the host.
chown -R --reference="${owner_ref}" "${module_dir}/internal" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4. Generate the browser client's codec, its predicate manifest, and the
#    cross-runtime golden vectors.
#
# Appended after the Go generation rather than folded into it, because it
# consumes the same descriptors from a different angle: protoc writes a
# FileDescriptorSet, and gen-clientcodec reads it. That is the mechanism
# docs/protocol.md §10.2 asks for — the client and the server cannot disagree
# about a field number or a wire type, because neither of them was told one.
#
# --include_imports pulls liquidproto/v1/refinement.proto in, without which extension
# 51234 does not resolve and every predicate silently vanishes from the
# manifest. --descriptor_set_out output is not committed; it is an
# intermediate, so it goes to the scratch directory.
#
# The minified single file the library serves is NOT built here. It is built
# by tools/ (its own module, so esbuild stays out of the library's go.mod) and
# committed, so a clean clone needs no minifier and no node. See client/SIZE.md.
# ---------------------------------------------------------------------------
echo "==> generating client/ (codec, predicate manifest, golden vectors)"
protoc \
  -I "${liquid_include_dir}" \
  -I "${module_dir}/proto" \
  -I /usr/local/include \
  --include_imports \
  --descriptor_set_out="${bin_dir}/frame.descset" \
  gotthlive/v1/frame.proto

(cd "${module_dir}" && go run ./internal/cmd/gen-clientcodec \
  -descriptor_set "${bin_dir}/frame.descset" \
  -out "${module_dir}/client")

chown -R --reference="${owner_ref}" "${module_dir}/client" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4b. --check: assert every committed output is what this generator produces.
#
# This comparison deliberately runs after both the Go/templ generation and the
# browser codec/manifest/vector generation. The comparison is byte-for-byte
# rather than a diff of meaning, because the claim being made is
# reproducibility. The EXIT trap restores every listed output even when a
# generator or build fails midway through the check.
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
