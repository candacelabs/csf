#!/usr/bin/env bash
#
# G11 — "consumable from a clean clone" — run rather than asserted.
#
# G11 reads: `git clone && go run ./examples/gotth/<name>` works for all three
# examples with no node, npm, protoc, or protoc-gen-liquidproto installed. For four phases the
# evidence for it was one sentence in docs/README.md. The Phase 4 gate graded it
# NOT MET on exactly that ground — `grep -n "G11" ci.sh` returned nothing and no
# artifact recorded the invocation on a machine without those four tools — and
# this script is the thing that was missing.
#
# Three properties make it a gate rather than a rehearsal, and each of them is
# something a cheaper harness would have quietly dropped:
#
#   * It is a real `git clone`, over the file:// transport, NOT a copy of this
#     directory and not a local hardlinked clone. The whole content of the
#     requirement is "what a stranger gets": a copy carries the built example
#     binaries, the node_modules under bench/, and anything a previous run
#     generated, and every one of those could make a failing tree pass. The
#     clone carries committed objects and nothing else, and the script asserts
#     that — `git status --porcelain --ignored` empty, the three 16 MB example
#     binaries absent.
#
#   * It runs in a STOCK upstream `golang` image. dis-gotth-live:latest is not a
#     valid environment for this gate and never was: it ships templ and protoc,
#     which are two of the four things G11 is about. Proving the absence is part
#     of the run, printed with the result, because a gate that does not check
#     its own precondition is asserting the thing it was written to measure.
#
#   * Each example must SERVE. The process not having exited is not evidence —
#     a server that binds a port and renders an empty document satisfies it. So
#     each example's page is fetched over HTTP and checked for the live-region
#     markup the client morphs against, and the shipped client runtime is
#     fetched from the URL that page names. That last fetch is the load-bearing
#     one for the no-npm half: the JavaScript comes out of the Go binary, so if
#     it arrives, nothing on this machine built it.
#
# # Where this runs
#
# On a HOST with a working docker, from anywhere — the script finds its own
# module and repository, so any path that reaches it works. From this directory
# that is:
#
#     bash tools/g11/run.sh
#
# NOT inside dis-gotth-live:latest, and not inside any of the gate images. That
# is the opposite of every other skip in ci.sh, which sends a reader to a bigger
# image; this one sends them out of the containers entirely, because the gate
# needs to start a container of its own and the gate images have no docker
# socket. ci.sh's G11 step therefore skips in the normal development image and
# prints this command. It is not a check that can never run — the artifact at
# docs/qa/g11-clean-clone.md is its output — it is a check that runs one layer
# out from the rest of the gate.
#
# # What it does NOT do
#
# It does not go offline. `go run` downloads seven modules from the module
# proxy, and G11 says nothing about a network; a stranger cloning from GitHub
# has one. The artifact records the download so that nobody later reads this
# gate as an offline claim it never made.
#
# Options:
#   --image IMAGE   the stock Go image to use (default below)
#   --keep          leave the clone on disk and print its path
#   --deadline N    seconds to wait for an example to serve (default 90)
#
# Exit codes are a contract with ci.sh, so that "could not run" and "ran and
# failed" can never be read as each other — which is the confusion every skip in
# ci.sh exists to prevent:
#
#   0   the property holds
#   1   G11 fails: something in the clone, the image or the three examples is
#       not what the criterion requires
#   2   the gate did not run, because a prerequisite it refuses to work around
#       is missing — no docker daemon, no git, no checkout, no image and no
#       network to fetch one. ci.sh turns this into an announced skip.

set -uo pipefail

image="golang:1.26-bookworm"
keep=0
deadline=90

# The default image is a MINOR tag, not a patch tag, and that is deliberate for
# the reason .dis/Dockerfile.bench gives about chromium: the property under test
# is "a stock Go toolchain and none of those four tools", not "this exact
# patch release", and a pinned patch produces a gate that stops running. What
# stops the tag from drifting somewhere useless is that the container checks the
# toolchain it got against go.mod's own `go` directive and fails if it is older,
# and that the resolved digest is printed with every result so a measurement can
# name the image that produced it.

while [ $# -gt 0 ]; do
  case "$1" in
    --image) image="${2:?--image needs a value}"; shift 2 ;;
    --keep) keep=1; shift ;;
    --deadline) deadline="${2:?--deadline needs a value}"; shift 2 ;;
    # The whole leading comment block, however long it grows: a --help with a
    # line range in it goes stale the first time anyone edits above it.
    -h|--help)
      awk 'NR==1{next} /^#/{sub(/^# ?/,""); print; next} {exit}' "${BASH_SOURCE[0]}"
      exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
module_root="$(cd "${here}/../.." && pwd)"

failures=()
notes=()

step() {
  printf '\n\033[1m==> %s\033[0m\n' "$1"
}

# --- prerequisites -----------------------------------------------------------
#
# These are hard failures here rather than skips. ci.sh is where the skip lives,
# because the skip is a statement about the invocation's context and ci.sh is
# what knows its context; a runner invoked directly was invoked on purpose.

step "prerequisites"
prereq_ok=1
for tool in docker git; do
  if ! command -v "${tool}" >/dev/null; then
    echo "${tool} is not on PATH." >&2
    prereq_ok=0
  fi
done
if [ "${prereq_ok}" = 1 ] && ! docker info >/dev/null 2>&1; then
  echo "docker is on PATH but the daemon is not reachable (no socket, or no permission)." >&2
  prereq_ok=0
fi

repo_root=""
if [ "${prereq_ok}" = 1 ]; then
  repo_root="$(git -C "${module_root}" rev-parse --show-toplevel 2>/dev/null)"
  if [ -z "${repo_root}" ]; then
    echo "${module_root} is not inside a git checkout, so there is nothing to clone." >&2
    echo "  This gate clones the REPOSITORY, not this directory: the library lives in a" >&2
    echo "  monorepo, under the candace/ export root that carries its go.mod." >&2
    prereq_ok=0
  fi
fi
if [ "${prereq_ok}" != 1 ]; then
  echo "G11 did not run." >&2
  exit 2
fi
echo "docker:      $(docker version --format '{{.Server.Version}}' 2>/dev/null)"
echo "git:         $(git --version)"
echo "repository:  ${repo_root}"
echo "module:      ${module_root}"

# --- the image ---------------------------------------------------------------

step "the stock Go image"
if ! docker image inspect "${image}" >/dev/null 2>&1; then
  echo "pulling ${image} (not present locally)"
  if ! docker pull --quiet "${image}"; then
    echo "could not pull ${image}: this gate needs either the image or a network." >&2
    exit 2
  fi
fi
image_digest="$(docker image inspect "${image}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}(no repo digest; built locally?){{end}}')"
image_id="$(docker image inspect "${image}" --format '{{.Id}}')"
image_created="$(docker image inspect "${image}" --format '{{.Created}}')"
echo "image:       ${image}"
echo "digest:      ${image_digest}"
echo "id:          ${image_id}"
echo "created:     ${image_created}"
case "${image}" in
  dis-gotth-live*)
    # Said in the script rather than only in a comment, because this is the
    # substitution that makes the gate meaningless while looking like a
    # convenience: that image ships templ and protoc.
    echo "${image} is not a valid G11 environment: the project's own images ship templ and protoc." >&2
    echo "  Use a stock upstream golang image. The absence check below would fail anyway." >&2
    failures+=("the image under test is one of the project's own")
    ;;
esac

# --- the clone ---------------------------------------------------------------

work=""
cleanup() {
  if [ -n "${work}" ] && [ -d "${work}" ]; then
    if [ "${keep}" = 1 ]; then
      echo "kept: ${work}"
    else
      rm -rf "${work}"
    fi
  fi
}
trap cleanup EXIT

step "the clone"
work="$(mktemp -d -t g11-clean-clone-XXXXXX)"
clone="${work}/clone"

source_head="$(git -C "${repo_root}" rev-parse HEAD)"
source_branch="$(git -C "${repo_root}" rev-parse --abbrev-ref HEAD)"

# file:// rather than a plain path, and --depth 1 with it. A plain local path
# makes git take the local shortcut and HARDLINK the object store, which is
# fast but leaves the clone sharing objects with the checkout it is supposed to
# be independent of; file:// forces the same pack protocol a stranger gets over
# the network. --depth 1 is what keeps that affordable here: this monorepo's
# object store is about 5 GB and the working tree at HEAD is about 30 MB. The
# depth changes nothing the gate looks at, because the gate looks at the
# checked-out files.
if ! git clone --quiet --depth 1 "file://${repo_root}" "${clone}"; then
  echo "git clone failed." >&2
  exit 2
fi

clone_head="$(git -C "${clone}" rev-parse HEAD)"

# Where the export root sits inside the clone. This library ships inside a
# monorepo, under a candace/ export root that carries its go.mod, and that
# export root also publishes as a repository of its own — where it IS the
# repository root. Both are clones a stranger can have, so the gate whose
# entire subject is a stranger's clone detects the layout instead of assuming
# one; hardcoding the monorepo's shape here is the exact defect this gate is
# supposed to catch in everybody else's scripts. Keyed off go.mod, because the
# directory name is the thing that differs.
if [ -f "${clone}/candace/go.mod" ]; then
  clone_prefix='candace/'
else
  clone_prefix=''
fi

echo "source:      ${repo_root} @ ${source_head} (${source_branch})"
echo "clone:       ${clone} @ ${clone_head}"
echo "export root: ${clone}/${clone_prefix}"
if [ "${clone_head}" != "${source_head}" ]; then
  echo "the clone is not at the source's HEAD; the result would describe a different tree." >&2
  failures+=("clone HEAD does not match source HEAD")
fi

# A dirty source is not a failure — the gate is about the committed tree, which
# is what a stranger can get — but it has to be SAID, because otherwise a green
# run reads as a statement about the files on disk and it is not one.
dirty="$(git -C "${repo_root}" status --porcelain -- "${module_root}")"
if [ -n "${dirty}" ]; then
  echo
  echo "NOTE: the source worktree has uncommitted changes under this library."
  echo "  This gate tests ${clone_head}, so none of the following is in the clone:"
  printf '%s\n' "${dirty}" | sed 's/^/    /'
  notes+=("the source worktree was dirty; the gate covers HEAD only")
fi

# The three assertions that make this a clone rather than a copy. If any of them
# is false the run below proves nothing, because whatever it proved could have
# come from a file the stranger does not get.
step "the clone carries committed files and nothing else"
clone_clean=1
stray="$(git -C "${clone}" status --porcelain --ignored)"
if [ -n "${stray}" ]; then
  echo "the fresh clone is not pristine, which should be impossible:" >&2
  printf '%s\n' "${stray}" | sed 's/^/  /' >&2
  clone_clean=0
fi
for binary in counter chat dashboard; do
  if [ -e "${clone}/${clone_prefix}examples/gotth/${binary}/${binary}" ]; then
    echo "examples/gotth/${binary}/${binary} is in the clone: a built binary would hide a build failure." >&2
    clone_clean=0
  fi
done
# The other half of the same question, in the other direction: the generated
# files G11 is ABOUT have to be present, or the examples would need templ and
# protoc to build and the gate would be measuring the wrong thing. FR-7 is what
# keeps them honest; this is what keeps them committed.
generated=(
  examples/gotth/counter/view_templ.go
  examples/gotth/chat/view_templ.go
  examples/gotth/dashboard/view_templ.go
  pkg/gotth/internal/protocol/gotthlivepb/frame.pb.go
  pkg/gotth/internal/protocol/gotthlivepb/frame_liquid.pb.go
  pkg/gotth/live/clientjs/gotth-live.min.js
)
for f in "${generated[@]}"; do
  if [ ! -f "${clone}/${clone_prefix}${f}" ]; then
    echo "${f} is NOT in the clone: a consumer would have to generate it, and G11 fails." >&2
    clone_clean=0
  fi
done
if [ "${clone_clean}" = 1 ]; then
  echo "clean: no untracked, no ignored, no built binaries, and all ${#generated[@]} generated files committed"
else
  failures+=("the clone is not a clean clone")
fi

# --- the run -----------------------------------------------------------------

step "the run, in ${image}"

# inside.sh is copied from the WORKING TREE rather than run out of the clone,
# and the distinction matters twice. The harness is not the subject: it has to
# be possible to point this at an older ref, and an edit to the runner should
# not need a commit before it can be tested. The clone's own copy is at
# ${clone}/${clone_prefix}pkg/gotth/tools/g11/inside.sh and is deliberately not
# what runs.
cp "${here}/inside.sh" "${work}/inside.sh"

# No -p. Nothing here binds a host port: the examples listen on 127.0.0.1 inside
# the container and curl runs beside them, so this cannot collide with anything
# on a machine that is serving real traffic. --rm, and the work directory is
# removed on exit.
#
# --network bridge, explicitly, because `go run` has to reach the module proxy:
# the module's dependencies are not vendored.
#
# --user is the invoking user, not root, and that is a correctness point rather
# than a hygiene one. The clone belongs to whoever ran this script; a root
# process reading it hits git's dubious-ownership refusal, `go build` cannot
# stamp its VCS metadata, and every example fails for a reason that has nothing
# to do with G11 — the same confound ci.sh's GOFLAGS=-buildvcs=false line exists
# to work around one layer in. Matching the uid removes the confound instead of
# suppressing it, so the command the container runs is the unmodified `go run .`
# a stranger types, and it also means everything the run writes is deletable
# afterwards by the user who has to delete it.
#
# HOME is inside the work directory, so GOMODCACHE and GOCACHE start EMPTY on
# every run and are thrown away with it. A gate whose module cache survives
# between runs would eventually stop proving that the clone's go.sum can still
# be resolved from a machine that has never seen this project.
mkdir -p "${work}/home"
docker run --rm \
  --network bridge \
  --user "$(id -u):$(id -g)" \
  -v "${work}:/g11" \
  -w /g11 \
  -e HOME=/g11/home \
  -e "G11_DEADLINE=${deadline}" \
  "${image}" bash /g11/inside.sh
run_status=$?
if [ "${run_status}" -ne 0 ]; then
  failures+=("the in-container gate (exit ${run_status})")
fi

# --- what the run left behind ------------------------------------------------
#
# `go run` must not need to write into the tree. If it did — a go.sum update, a
# generated file, a cache dropped in the source — then "clone and run" is not
# what a stranger does, it is what a stranger does plus a step nobody wrote
# down. Checked here rather than inside the container because the container ran
# as root and this is the same question asked by the owner of the files.

step "the clone after the run"
after="$(git -C "${clone}" status --porcelain --ignored)"
if [ -n "${after}" ]; then
  echo "running the examples modified or added files in the clone:" >&2
  printf '%s\n' "${after}" | sed 's/^/  /' >&2
  failures+=("the run was not read-only against the clone")
else
  echo "clean: the run added and changed nothing in the clone"
fi

# --- verdict -----------------------------------------------------------------

printf '\n\033[1m==> G11 verdict\033[0m\n'
echo "tree:   ${clone_head}"
echo "image:  ${image} ${image_digest}"
echo "date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The two halves are reported separately and always, in both directions, because
# a single word cannot carry them. "as worded" is a fact about PRD G11's
# sentence; "as documented" is a fact about the tree. Only the second can go
# green by anyone's work here, and a reader who is told only the second one has
# been told the box is met when the box's own text is unsatisfiable.
as_worded="$(sed -n 's/^as_worded=//p' "${work}/summary.txt" 2>/dev/null)"
echo "as worded  (go run ./examples/gotth/<name>):  ${as_worded:-unknown}"
for ex in counter chat dashboard; do
  printf 'as documented (cd examples/gotth/%-9s && go run .): %s\n' \
    "${ex}" "$(sed -n "s/^${ex}=//p" "${work}/summary.txt" 2>/dev/null || echo unknown)"
done

if [ "${#notes[@]}" -ne 0 ]; then
  echo "notes:"
  for n in "${notes[@]}"; do echo "  - ${n}"; done
fi

if [ "${#failures[@]}" -ne 0 ]; then
  echo "G11 FAILS:"
  for name in "${failures[@]}"; do
    echo "  - ${name}"
  done
  exit 1
fi

echo "G11's PROPERTY HOLDS: a clean clone plus a stock Go toolchain serves all three"
echo "examples, with node, npm, protoc and protoc-gen-liquidproto proven absent. G11's WORDING does"
echo "not hold and cannot; the DISCREPANCY block above says why and who owns it."
