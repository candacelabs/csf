#!/usr/bin/env bash
#
# The full gotth gate, in one script.
#
# Six requirements name "CI" as the thing that enforces them, and for the first
# checkpoint nothing did: the reproducibility check existed and nothing called
# it, the client size gate existed and nothing called it, the no-eval scan
# existed and nothing ran it, and staticcheck was not installed in the image at
# all. A requirement whose gate is a tool nobody runs is a requirement in name
# only, which is why this is a script and not a list in a document.
#
#   FR-7    generated code is byte-reproducible          gen.sh --check
#   NFR-2   client bundle within its byte budget          minify -check
#   NFR-3   per-subsystem size ledger reported            minify -check
#   NFR-8   dev inspector within its own byte budget      minify -check
#   FR-57   dev-reload client within its byte budget      minify -check
#   FR-44   the inspector paints in a real browser        browser conformance
#   FR-57   the dev-reload loop end to end in a browser   browser conformance
#   NFR-4   no eval or new Function in the shipped file   client bundle suite
#   NFR-12  gofmt, vet, staticcheck, -race all clean      below
#   FR-65   exported-identifier delta against the ledger  apisurface
#   FR-66   a doc comment on every exported symbol        doccheck
#   FR-68   every godoc example runs under go test        doccheck, and the
#                                                         module steps that run
#                                                         the examples for real
#   G11     consumable from a clean clone                 tools/g11/run.sh
#
# G11's row is the newest and the odd one: its gate is named as QA-1 rather than
# CI, and it was graded NOT MET in Phase 4 for the reason every line above this
# one exists — `grep -n "G11" ci.sh` returned nothing. It is here now, and the
# step's own comment says why it is the only one that skips outward.
#
# The satellite trees run here too, though their requirements name a human gate
# rather than CI: examples/gotth/counter, examples/gotth/chat and
# examples/gotth/dashboard (FR-63, FR-68), test/routers (FR-33, the
# three-router mount suite), test/sampling (FR-36 clause 4's falsifier),
# test/memory (the G2 harness), docs/guide/_samples (the guide's samples) and
# the three benchmark applications under bench/apps/*/gotth (equivalence-spec
# §2's feature tables, quarantined by §5.3).
#
# Each of them was its own module until the single-module fold, so that what it
# needs could not reach a consumer — chi and gin in one case, the OpenTelemetry
# SDK in the other. They are packages of github.com/candacelabs/csf now, and
# what that argument bought is gone: chi, gin, the OTel SDK and esbuild are all
# in the one go.mod. The steps stay, because a suite that runs only as part of
# somebody else's `./...` is a suite whose failures name the wrong subject, and
# because three of these trees are still invisible to this directory's `./...` —
# the examples are outside it and the samples directory begins with an
# underscore.
#
# Every step prints its name before it runs, so a failure in CI names the
# requirement it broke rather than a line number in a log.
#
# # Where this runs
#
# Inside dis-gotth-live:latest, from this library's root:
#
#     dis run bash ci.sh
#
# `dis` is the monorepo's container-workspace tool and is not part of this
# export. The image is built from .dis/Dockerfile, so the equivalent from the
# export root — this repository's root — is:
#
#     docker build -t dis-gotth-live:latest pkg/gotth/.dis
#     docker run --rm -v "$PWD:/workspace" -w /workspace/pkg/gotth \
#         dis-gotth-live:latest bash ci.sh
#
# Four steps need more than the module directory and are skipped with a loud
# notice naming the command that runs them, rather than passing quietly:
#
#   * gen.sh --check needs the EXPORT ROOT, candace/, mounted — not just this
#     directory — because protoc-gen-liquidproto is built from the canonical
#     pkg/liquidproto source so that the generator always matches the committed
#     generator. That source is a sibling package in the same module, so the
#     export root is enough and the repository root above it is not required.
#     The check also needs protoc and templ, so a module-only invocation cannot
#     run any useful subset of the byte-reproducibility gate.
#   * the client bundle suite needs node, which the library image does not have
#     and will not have — that absence is what makes the no-node property
#     structural. It runs in the bench image.
#   * the browser-labelled conformance specs need a browser, which the library
#     image also does not have. This is D-20: for one round they were skipped
#     with no notice at all, at exit 0, while this script printed "every gate
#     this invocation could run is green" — 123 of 154 specs in the library
#     image against 142 in the bench image, and the 19-spec difference was the
#     whole of that round's DOM-preservation and HTMX evidence. A skip that
#     announces itself is the minimum; the workflow runs them for real.
#   * G11's clean-clone gate needs a docker DAEMON, because it starts a
#     container of its own — a stock upstream golang image, chosen for the four
#     tools it does not have. It is the only skip here that points OUT of the
#     images rather than into a bigger one, and dis-gotth-live:latest is not a
#     substitute for it at any price: that image ships templ and protoc.
#
# The GitHub workflow runs the first three contexts, so those three are not
# skipped there. **It does not run G11**, and that is a real gap rather than a
# rounding of this paragraph: the library job runs ci.sh inside a container with
# no docker socket, so the G11 step below skips in CI exactly as it does in a
# developer's `dis run`. The runner needs the RUNNER's docker, not a container's
# — `bash candace/pkg/gotth/tools/g11/run.sh` as a workflow step, beside
# `docker build`, not inside `docker run`. That job does not exist yet, and
# .github/workflows/ is not this file's to write. Until it does, G11's evidence
# is the recorded run in docs/qa/g11-clean-clone.md, not a green CI badge.
#
# Three of the four notices print a `docker run` that mounts the REPOSITORY ROOT
# at /workspace and works from /workspace/candace/pkg/gotth, and they say so, because
# until they agreed they were three recipes with two different answers to "what
# must $PWD be". Two wanted the repository root; the client one wanted
# this directory, and none of them said which. That is the uniform shape all three
# jobs in .github/workflows/gotth-live-checks.yml use, so a recipe that needs a
# different one is a recipe contradicting the CI it stands in for. The
# repository root is a superset of what gen.sh --check now needs — the export
# root candace/ would do — and the recipes keep the wider mount so that all
# three stay one shape.
#
# The fourth notice deliberately does not follow that shape, and departing from
# a convention this file argued for is worth one sentence: G11's recipe is a
# bare `bash candace/pkg/gotth/tools/g11/run.sh` because a `docker run` recipe would be
# the exact error it exists to catch. Every image named in the three recipes
# above has templ and protoc in it.
#
# Not cosmetic, on two counts. `docker run` CREATES a missing -w rather than
# refusing, as root — the empty root-owned nested tree that
# turned up in this checkout is what a paste from the wrong directory leaves
# behind. And the client recipe did not fail from the wrong directory: run from
# the repository root, its glob matched nothing, node reported `tests 0` and
# exited 0. A stand-in for a skipped gate that passes without running the gate
# is the same defect as the section below, which is why that recipe now counts
# what it ran.
#
# # Steps assert their tool exists before believing its output
#
# A check that cannot fail is indistinguishable from a check that passes, and
# the failure is always silent. This script shipped two instances of it (D-19):
# `gofmt -l . || true` reported "clean" when gofmt was not on PATH at all, and
# `node --test client/test/*.test.mjs` reported success when the glob matched
# nothing, because node 24 expands its own glob arguments and running zero
# tests is not an error to it. Both now assert first. Any step added below
# should do the same.

set -uo pipefail

# CI mounts the checkout into the gate containers under a different owner, so
# git inside them refuses to report status (exit 128) and default -buildvcs
# stamping turns every `go build`/`go test` into a failure. Nothing this gate
# verifies reads a VCS stamp; exported here so every step, including gen.sh's
# plugin build, inherits it.
export GOFLAGS="${GOFLAGS:-} -buildvcs=false"

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The export root, candace/. Every step below still runs from this library's own
# directory; this is for the two things that are no longer under it — the
# examples, which moved to examples/gotth/ when gotth-live became
# candace/pkg/gotth, and the canonical plugin source at pkg/liquidproto. Both
# are inside the export root, which is why there is no repository-root variable
# here any more: nothing this script does reaches above candace/.
export_root="$(cd ../.. && pwd)"

failures=()
skipped=()

step() {
  printf '\n\033[1m==> %s\033[0m\n' "$1"
}

check() {
  local name="$1"
  shift
  step "${name}"
  if "$@"; then
    return 0
  fi
  failures+=("${name}")
  return 1
}

# --- NFR-12: the toolchain ---------------------------------------------------

check "build (NFR-12)" go build ./...
check "vet (NFR-12)" go vet ./...

# The `|| true` below is load-bearing — `grep -v` exits 1 on no match and
# pipefail would make that the assignment's status — but it also swallows the
# case where gofmt itself never ran, and empty output means "clean" either way.
# That was D-19: with gofmt off PATH this step printed `command not found` and
# then `clean`, and appeared in no verdict, three lines above a staticcheck
# step that was already guarded. Assert the tool first, like staticcheck does.
step "gofmt (NFR-12)"
if ! command -v gofmt >/dev/null; then
  echo "gofmt is not on PATH, so its output cannot be believed:" >&2
  echo "  rebuild the dev image, or check that /etc/profile has not reset PATH" >&2
  echo "  (a LOGIN shell in this image does exactly that: use 'bash ci.sh', not 'bash -lc')" >&2
  failures+=("gofmt (NFR-12)")
else
  unformatted="$(gofmt -l . | grep -v '^$' || true)"
  if [ -n "${unformatted}" ]; then
    echo "these files are not gofmt-clean:" >&2
    echo "${unformatted}" >&2
    failures+=("gofmt (NFR-12)")
  else
    echo "clean"
  fi
fi

# The scope is named rather than `./...`, and it is the scope this step has
# always had. Until the single-module fold `./...` from here reached the library
# and nothing else, because bench/, test/{memory,routers,sampling} and tools/
# each had their own go.mod; folding them in silently widened this step and it
# went red on five findings in code it had never looked at. Widening a linter is
# a decision with a fix attached, and it is not this change's to make — so the
# three trees the library occupies are named, and bench/, the three test
# modules and tools/ stay outside the scope they were outside of before. The
# guide's samples run staticcheck in their own step below.
step "staticcheck (NFR-12)"
if ! command -v staticcheck >/dev/null; then
  echo "staticcheck is not on PATH: rebuild the dev image (.dis/Dockerfile pins it)" >&2
  failures+=("staticcheck (NFR-12)")
elif staticcheck ./live/... ./internal/... ./test/internal/...; then
  echo "clean"
else
  failures+=("staticcheck (NFR-12)")
fi

check "tests, race detector (NFR-12)" go test -race -count=1 ./...

# The checkpoint-3 chaos suite: PRD Phase 3's eight minimum cases, which are
# the gate that checkpoint 3 is. It runs inside `go test -race ./...` above as
# well — it is in this module, deliberately, because unlike test/routers and
# test/sampling it needs no dependency the library must not carry. So why a
# step of its own?
#
# Because six of its specs do not run there. Two are soak-class and four are
# Appendix-B measurements, and both classes gate on an environment variable so
# that a plain `go test ./...` stays seconds rather than minutes. A suite whose
# most expensive half is invisible to CI is the same defect one label out, so
# this step turns both on and names what it costs: about seven minutes.
#
# -ginkgo.fail-on-empty because a label filter matching nothing is a silent
# pass, which this repository has now caught four times.
step "chaos suite, soak and measurements (PRD Phase 3, the checkpoint-3 gate)"
if GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1 go test -race -count=1 -timeout 35m \
    ./test/internal/chaos/ -args -ginkgo.fail-on-empty; then
  echo "clean"
else
  failures+=("chaos suite (PRD Phase 3 cases 1-8, QA3-1/2/3)")
fi

# --- D-5: the tree list, held honest against the tree ------------------------

# Every step from here down — the tree steps below, and the FR-65 block that
# runs tools/ — enters one directory of its own, and until this guard existed
# nothing checked that the set of those steps matched the set of those trees.
# D-5 measured what that costs: each of the three bench/apps/*/gotth trees
# carries a Ginkgo suite, and from the day they landed until af9057d1 they were
# run by NOTHING. No decision was ever made to skip them. They were simply never
# added here, because adding a tree and adding a step are two separate acts and
# only the first one is forced by anything.
#
# The guard's mechanism changed with the single-module fold and it is weaker
# now, which is worth stating rather than discovering. It used to walk the tree
# for go.mod files and compare that set against this list, so a NEW module was
# red by construction. There is one module now — the export root's — so the walk
# has nothing to find, and the two halves that survive are the ones that read
# this list: an entry naming a directory that is gone is red, and an entry
# named nowhere else in this file is red. A new suite in a new directory is no
# longer caught by anything here, and the invariant that replaced the walk is
# the one the export depends on instead: no nested go.mod may exist at all.
#
# ci_trees does NOT drive the steps below, and that is the deliberate half.
# The steps are not interchangeable — tools/ runs neither a build nor -race,
# docs/guide/_samples adds staticcheck and a gofmt assertion, the bench steps
# announce which of two fixture cases they took — and the paragraph above each
# one is the argument for that tree being entered separately at all, which a
# loop would delete. Documentation also quotes these steps by name, by output
# string, and in one case by line range. So the list is checked against the
# steps rather than substituted for them.
#
# The three example paths are relative to the export root rather than to this
# directory: the examples left this tree when gotth-live became
# candace/pkg/gotth, and each step below spells that out where it runs.
ci_trees=(
  examples/gotth/counter
  examples/gotth/chat
  examples/gotth/dashboard
  docs/guide/_samples
  test/routers
  test/sampling
  test/memory
  bench/apps/counter/gotth
  bench/apps/chat/gotth
  bench/apps/dashboard/gotth
  tools
)

# Deliberately empty. An entry here says CI does not run a tree that exists,
# which is a coverage decision and has to be argued in place, beside the entry.
# A guard made green by shrinking what it looks at is the defect this file keeps
# catching rather than a fix for it — gen.sh's templ_sources comment makes the
# same refusal about docs/guide/_samples, in those words, for the same reason.
ci_trees_unrun=()

step "the tree list matches the trees on disk, and nothing nests a module (D-5)"
module_list_ok=1
ci_self="$(basename "${BASH_SOURCE[0]}")"

# The export's own invariant, asserted where it can fail rather than at publish
# time: one module, whose go.mod is at the export root. A go.mod anywhere under
# it is a nested module, and a nested module is a tree the export cannot ship.
# node_modules is excluded the way gen.sh's walk excludes it — nothing under
# bench/node_modules ships a go.mod today, so the exclusion answers no case that
# exists, and it is here so that a Go module vendored inside a JS dependency
# tree could never turn this guard red for source that is not ours.
nested_modules="$(find "${export_root}" -name go.mod -not -path "${export_root}/go.mod" \
  -not -path '*/node_modules/*' | sed -e "s|^${export_root}/||" | sort)"
if [ -n "${nested_modules}" ]; then
  echo "these go.mod files are nested inside the export root; the export is one module:" >&2
  printf '  %s\n' ${nested_modules} >&2
  module_list_ok=0
fi

for tree in "${ci_trees[@]}" "${ci_trees_unrun[@]}"; do
  case "${tree}" in
    examples/*) dir="${export_root}/${tree}" ;;
    *) dir="${tree}" ;;
  esac
  if [ ! -d "${dir}" ]; then
    echo "${tree} is listed in ${ci_self}, but ${dir} does not exist." >&2
    echo "  The tree moved or was deleted: drop the entry and the step that names it." >&2
    module_list_ok=0
  fi
done

for tree in "${ci_trees[@]}"; do
  if [ "$(grep -v '^[[:space:]]*#' "${ci_self}" | grep -cF -- "${tree}")" -lt 2 ]; then
    echo "${tree} is in ci_trees but is named nowhere else in ${ci_self}." >&2
    echo "  The entry claims a step that does not exist. Write the step, or move the" >&2
    echo "  entry to ci_trees_unrun with the reason it is not run." >&2
    module_list_ok=0
  fi
done

if [ "${module_list_ok}" = 1 ]; then
  echo "clean: ${#ci_trees[@]} listed, ${#ci_trees_unrun[@]} opted out, that is what is on disk, and no module is nested"
else
  failures+=("the tree list vs the tree (D-5)")
fi

# The examples sit outside this tree — at ${export_root}/examples/gotth — so
# `go test ./...` above does not reach them and each needs its own invocation.
# An example that CI does not run is a regression suite in name only, which is
# what FR-63 exists to prevent.
step "examples/gotth/counter"
if (cd "${export_root}/examples/gotth/counter" && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("examples/gotth/counter")
fi

step "examples/gotth/chat"
if (cd "${export_root}/examples/gotth/chat" && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("examples/gotth/chat")
fi

# The dashboard is FR-62's evidence and FR-63's regression suite for it: the
# five properties that requirement names — high-frequency server-initiated
# updates, independent live regions, coalescing that keeps its provenance,
# backpressure under a slow client, and a plain-HTMX region on the same page —
# are each asserted on the frames by a spec in examples/gotth/dashboard/wire_test.go.
# Three of the five are claims about what is on the wire and are unfalsifiable
# from inside the application, so those specs dial a real socket and decode what
# arrives; they take about ten seconds, most of it deliberate waiting for
# silence, and that is the price of the property being checked at all.
#
# It also carries D-16's executable half: a spec that goes red if any live
# region ever renders an hx-* attribute outside a live.Preserve subtree, because
# markup a morph INSERTS is inert until htmx.process runs.
step "examples/gotth/dashboard (FR-62)"
if (cd "${export_root}/examples/gotth/dashboard" && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("examples/gotth/dashboard (FR-62)")
fi

# The guide's samples. Its own module until the single-module fold, on the
# precedent of examples/*, and invisible to `./...` — its directory name begins
# with an underscore, which Go's package patterns skip on their own. So
# until this step existed nothing ran it — `go test -race ./...` above did not
# reach it, no examples block named it, and its Ginkgo suite (the samples
# package's own specs plus apptest/) was green in the sense that it never ran.
# That is the failure the header of this file describes, one directory out and
# with the guide's credibility attached: a documentation sample that no longer
# compiles is documentation that is wrong.
#
# staticcheck runs here where the examples blocks above do not run it, and the
# reason is what a sample is FOR: a reader copies these files into their own
# program, so the advice they encode should survive the linter that reader is
# likely to run. gofmt is belt-and-braces — the top-level gofmt step walks the
# filesystem rather than the package graph and so already descends into
# _samples, unlike everything else in this script — and it is named here rather
# than assumed, because "covered by a different step's incidental behaviour" is
# how coverage goes missing.
#
# The generated half: the five docs/guide/_samples/*/view_templ.go files were
# outside gen.sh's byte-reproducibility check when this module landed — exactly
# the gap O-1 recorded for the chat example's view_templ.go, which was committed
# generated code that FR-7's gate did not look at. They are inside it now:
# gen.sh's templ_sources lists all five, and its walk of the tree fails the gate
# if a sixth .templ ever appears here unlisted. The FR-7 step below is what
# proves it; this step only compiles and runs them.
step "docs/guide/_samples (the guide's samples)"
if (
  cd docs/guide/_samples || exit 1
  go build ./... || exit 1
  go vet ./... || exit 1
  staticcheck ./... || exit 1
  # D-19's shape again, and the one sub-check here that could go silently green:
  # `gofmt -l .` prints nothing when the tree is clean AND when gofmt is not on
  # PATH at all. staticcheck and go announce their own absence with a non-zero
  # exit; gofmt's silence has to be distinguished by hand.
  command -v gofmt >/dev/null || {
    echo "gofmt is not on PATH, so its output cannot be believed" >&2
    exit 1
  }
  unformatted="$(gofmt -l .)"
  [ -z "${unformatted}" ] || {
    echo "these sample files are not gofmt-clean:" >&2
    echo "${unformatted}" >&2
    exit 1
  }
  go test -race -count=1 ./... || exit 1
); then
  echo "clean"
else
  failures+=("docs/guide/_samples (the guide's samples)")
fi

# The FR-33 three-router mount suite: one application under net/http, chi and
# gin at three distinct prefixes, two of which are not /live (C-23). Its own
# module for a reason worth stating where CI can see it — chi and gin are
# needed by nothing else in this repository, and a module required by
# gotth-live/go.mod enters the build list of everybody who requires
# gotth-live, whether or not anything links it. Running it from here is what
# keeps that separation from quietly costing the coverage it bought.
step "test/routers (FR-33)"
if (cd test/routers && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("three-router mount suite (FR-33)")
fi

# FR-36 clause 4's falsifier: the server-side event path is one sampling
# decision, measured over 300 interactions at three rates against a real
# ParentBased(TraceIDRatioBased(p)). Its own module for the same reason
# test/routers is one, and the reason is the requirement itself: making a
# sampling decision needs go.opentelemetry.io/otel/sdk, the library depends on
# the OTel API submodules only (instrumentation §3.4, L9-1 D1), and Go resolves
# requirements at module granularity — so a test-only import of the SDK would
# put it in every consumer's build list.
#
# It runs here because a falsifier nothing invokes is the same defect one
# directory out: C-30 was found by running the tracer under the project's own
# default sampler, which nothing in CI had ever done.
step "test/sampling (FR-36 clause 4)"
if (cd test/sampling && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("FR-36 clause 4 sampling falsifier")
fi

# The G2 idle-memory harness. Its own module because the server under test
# wires the OpenTelemetry metric and trace SDKs — equivalence-spec §5.6 puts
# default-on observability inside the headline configuration — and a benchmark's
# dependencies must not reach a consumer's go.mod.
#
# What runs here is the ARITHMETIC: §3.6's median, the subtraction, the
# division, and the refusal to compute a figure from a window that is not 60
# samples at 1 Hz. The measurement itself does not run in CI and cannot: it
# needs two containers, a pinned cpuset and twenty-two minutes per cell, and its
# numbers are host-dependent by construction. Building memsrv and memdrv here is
# still the point — a harness that stops compiling is a baseline nobody can
# re-take, and the last time this project let a gate's own tooling rot it took a
# checkpoint to notice.
step "test/memory (G2 baseline harness)"
if (cd test/memory && go build ./... && go vet ./... && go test -race -count=1 ./...); then
  echo "clean"
else
  failures+=("G2 memory harness (RFC-0001 §6.1, equivalence-spec §3.6)")
fi

# The three gotth-live benchmark applications. Each was its own module until the
# single-module fold, for the same reason test/memory was — equivalence-spec
# §5.3 quarantines the bench tree under FR-74 — and each was therefore invisible
# to every step above, `./...` included. They are packages of the one module
# now, and this step is what keeps them visible: the scope every step above uses
# is named rather than `./...`, so bench/ is still outside it by choice. They are
# the artefacts the Phase 5 comparison MEASURES: an app that stops compiling
# invalidates a benchmark cell, and an app whose feature table quietly stops
# holding invalidates the equivalence claim itself, which is worse because it
# still produces numbers.
#
# What runs here is construction only. Not one timing is taken in CI: §5.2 puts
# every measurement in a pinned, cgroup-limited container on a quiet host, and a
# number collected on a shared runner is a number this project may not quote.
#
# Some specs here skip when `bench/fixtures/*/ticks.jsonl` is absent, because
# the fixture is derived (~13 MB of JSONL, generated by `npm run fixtures` in
# the bench image, gitignored by design with only the generator and the digest
# committed). A skip that nobody sees is this repository's oldest failure mode,
# so the step reports the skips rather than printing "clean" either way.
#
# C-33(b). This used to be a sentence chosen from `[ -f … ]` before the suite
# ran — "fixtures ABSENT — the §2.5 digest specs skipped" — and L9-1 blocked it
# on two counts that are worth keeping written down, because the first is the
# one this script exists to prevent:
#
#   1. It was a PREDICTION announced as an observation. It said what the run
#      was going to do, computed from the presence of two files, and nothing
#      compared it against what the run did. That is the same shape as C-33(a)
#      itself, where a guard that had never once taken its branch printed a
#      reassuring sentence for months. D-19 settled the general rule on `gofmt`:
#      let the tool report its own result.
#   2. Its scope was narrower than the truth. "The §2.5 digest specs" undercounts
#      — `dashboard_test.go`'s "stays inside §2.4's element and SVG bounds at the
#      real shapes" needs the same fixture and is not a digest spec — so a reader
#      who trusted the sentence would have believed fewer specs were missing than
#      were actually missing.
#
# So the count now comes from Ginkgo's own JSON report, written by the run being
# described. `go test` without `-v` discards a passing package's output, which is
# why the number cannot simply be scraped from stdout, and why the report file is
# the mechanism rather than a parse of the log.
bench_report_dir="$(mktemp -d)"
trap 'rm -rf "${bench_report_dir}"' EXIT

# D-6, and it runs BEFORE the loop below because it is about the bytes that
# loop is going to compile. `bench/harness/ready.js` is the source of truth and
# the three `bench/apps/*/gotth/bench/ready.js` are copies of it.
#
# The copies are git-TRACKED, unlike `harness/shim.js`'s, and that asymmetry is
# forced rather than chosen: `//go:embed bench/ready.js` is resolved by the
# compiler, so a gitignored copy makes a clean checkout fail with `pattern
# bench/ready.js: no matching files found` — in this very image, which has no
# node and therefore cannot run `sync-ready.mjs` to repair itself. REV-DUP's
# D-6 specification said to gitignore them; that was written before anyone
# tried it, and the deviation is recorded in bench/README.md.
#
# Tracked copies can drift, and the reason that needs a step of its own is that
# a drifted copy still COMPILES: the loop below would stay green over three
# files that no longer agree, which is the shape of every other silent pass
# this script exists to close. The verifier is `sh` + `cmp` for the same
# no-node reason, and it is one script with two callers — `npm run
# verify:ready` and this line — because answering a duplication finding with a
# second copy of its own check is not an answer.
step "bench/apps/*/gotth/bench/ready.js is harness/ready.js (D-6)"
if sh bench/scripts/verify-ready.sh; then
  echo "clean"
else
  failures+=("bench/apps/*/gotth/bench/ready.js drifted from harness/ready.js (D-6)")
fi

# C-40 / FR-70. The bench applications are half of this project's fairness
# argument, and the amendment that ratified Q-E paid for them with a checkable
# obligation: the gotth-live side uses only what a consumer can reach. §5.4's
# pessimization audit protects the Next.js side; nothing audited ours, so a
# gotth-live bench app tuned through `internal/` or behind a build tag would be
# measuring a library nobody can install. L9-1 filed it as a condition with the
# note that it is checkable and nothing checks it.
#
# Two of FR-70's four clauses are mechanical and they are the two below.
#
#   1. No `internal/` import. Computed from the modules' OWN import lists and
#      deliberately not from `go list -deps`, which reports the library's
#      internal packages — imported by the library, not by the bench app — and
#      would therefore fail on every tree that has ever existed. That is the
#      shape of check this file has twice had to delete for asserting something
#      other than what it claimed.
#   2. No build tag. `//go:build` and the pre-1.17 `// +build` both, over the
#      module's own files, because a constrained file is a program whose
#      measured behaviour a reader cannot reproduce from the source they see.
#
# The other two — unexported hooks and undocumented configuration — are NOT
# checked here and are not checkable by grep. They stay where the amendment put
# them: with QA-2 at the Phase-5 gate, reading the three apps against the
# declared method. Saying so is the point; a step named "FR-70" that quietly
# covered half of it would be worse than this one, which covers half and says
# which half.
step "bench/apps/*/gotth use consumer-reachable API only (FR-70, C-40)"
fr70_violations=""
for bench_module in bench/apps/counter/gotth bench/apps/chat/gotth bench/apps/dashboard/gotth; do
  fr70_internal="$( (cd "${bench_module}" && go list -f \
      '{{range .Imports}}{{println .}}{{end}}{{range .TestImports}}{{println .}}{{end}}{{range .XTestImports}}{{println .}}{{end}}' \
      ./... 2>/dev/null) | grep '/pkg/gotth/internal/' | sort -u)"
  if [ -n "${fr70_internal}" ]; then
    fr70_violations="${fr70_violations}${bench_module}: imports $(echo "${fr70_internal}" | tr '\n' ' ')
"
  fi
  fr70_tagged="$( (cd "${bench_module}" && grep -rlE '^//go:build|^// \+build' --include='*.go' .) )"
  if [ -n "${fr70_tagged}" ]; then
    fr70_violations="${fr70_violations}${bench_module}: build tag in $(echo "${fr70_tagged}" | tr '\n' ' ')
"
  fi
done
if [ -z "${fr70_violations}" ]; then
  echo "clean: 3 modules, no internal/ import and no build tag"
else
  printf '%s' "${fr70_violations}" >&2
  failures+=("bench/apps/*/gotth reach past the consumer-reachable API (FR-70, C-40)")
fi

# The loop iterates whole module paths rather than the three app names it used
# to, and the printed output is unchanged by that. The reason is the D-5 guard
# above: its third check asks whether each listed module is named anywhere else
# in this file, and with the paths assembled from a bare `counter chat
# dashboard` the strings `bench/apps/counter/gotth` and its two siblings existed
# nowhere in ci.sh except inside ci_trees itself — so the three entries most
# recently missing from CI would have been the three the guard could not vouch
# for.
for bench_module in bench/apps/counter/gotth bench/apps/chat/gotth bench/apps/dashboard/gotth; do
  step "${bench_module} (equivalence-spec §2)"
  bench_report="${bench_report_dir}/$(echo "${bench_module}" | tr / -).json"
  if (cd "${bench_module}" && go build ./... && go vet ./... \
        && go test -race -count=1 ./... -args -ginkgo.json-report="${bench_report}"); then
    # Ginkgo writes one array entry per suite and OVERWRITES the file, so a
    # module that grows a second test package would silently report only the
    # last one. That is checked rather than assumed: `go list` counts the
    # packages that have tests, and a second one prints a notice instead of a
    # quietly partial count.
    bench_suites="$(cd "${bench_module}" \
      && go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... \
      | grep -c .)"
    if [ -f "${bench_report}" ]; then
      bench_skipped="$(grep -o '"State": *"skipped"' "${bench_report}" | grep -c .)"
      bench_total="$(grep -o '"TotalSpecs": *[0-9]*' "${bench_report}" \
        | head -1 | tr -cd '0-9')"
      if [ "${bench_skipped}" -gt 0 ]; then
        echo "  ${bench_skipped} of ${bench_total:-?} specs SKIPPED, per Ginkgo's own report"
        echo "  most likely the fixture: bench/fixtures/*/ticks.jsonl is derived and"
        echo "  gitignored — regenerate with 'npm run fixtures' in bench/ (needs node,"
        echo "  so the bench image). Run with -v to see which specs and why"
      else
        echo "  0 of ${bench_total:-?} specs skipped, per Ginkgo's own report"
      fi
      if [ "${bench_suites}" -gt 1 ]; then
        echo "  NOTE: ${bench_suites} test packages in this module; the count above covers"
        echo "  only the last suite to write the report. Split the step or the count lies"
      fi
    else
      echo "  NOTE: Ginkgo wrote no JSON report, so this step cannot say what it skipped"
    fi
    echo "clean"
  else
    failures+=("${bench_module} (equivalence-spec §2)")
  fi
done

# --- FR-65, FR-66, FR-68: the exported surface and its documentation ---------

# The gates' own readers are tested here rather than nowhere. apisurface reads
# api-surface.md §0's counts table and fails the build on a delta, so a reader
# that silently ignores part of that table is a gate that passes for the wrong
# reason — which is what it did until L9-1 wrote 9001 into an unread column and
# watched CI agree. doccheck walks the tree for exported symbols with no doc
# comment, which has the same failure available to it one shape out: a walk that
# finds nothing prints an empty table and exits 0.
#
# The label names three requirements because this one invocation runs the tests
# of both gates. `go test ./...` over the whole module rather than two
# per-package invocations, deliberately: the alternative is the D-5 defect one
# directory down, where a third tool lands with a suite and nothing names it.
# It is renamed from "the gate's own tests (FR-65)" for the reason C-33(b)
# settled below — a label whose scope is narrower than what it runs lets a
# reader believe less was checked than was, and this file has now made that
# mistake twice.
step "the gates' own tests (FR-65, FR-66, FR-68)"
if (cd tools && go vet ./... && go test -count=1 ./...); then
  echo "clean"
else
  failures+=("tools tests (FR-65, FR-66, FR-68)")
fi

step "exported-identifier delta (FR-65)"
if (cd tools && go run ./apisurface); then
  :
else
  failures+=("exported surface (FR-65)")
fi

# FR-66 and FR-68, both of which named CI as their gate while nothing ran.
#
# doccheck holds every exported symbol of the PUBLISHED module — live,
# live/livetest, all of internal/** — to a doc comment, every package to a
# package comment, the two consumer-reachable packages to a runnable overview
# (a package-level `func Example()` with an `// Output:` comment, which is the
# block godoc renders under the overview and the only kind go test executes),
# and every Example* function ANYWHERE in the tree to an `// Output:` comment.
#
# That last rule is the whole of FR-68 that a static check can carry, and it is
# the half that matters: an Example without an output comment compiles and is
# never run, so it can assert a behaviour the library lost a year ago and no
# suite will say so. The other half — the examples actually running — is the
# `go test` steps above and the per-module steps, which is why FR-68's row in
# the header names both.
#
# The satellite trees — bench, test, tools, the guide's samples — are measured
# and PRINTED but not enforced, and the count appears in this step's output on
# every run. That is a scope decision and the argument for it is in doccheck's
# own doc comment rather than here. It is written down where the code that acts
# on it lives, and `-report` lists the unenforced findings, so widening the rule
# is a one-line change rather than an archaeology exercise.
#
# THE EXAMPLES ARE THE SECOND ROOT, and they need naming here because the
# failure they had was silent. They used to be three modules inside this tree,
# so one walk found them and scoped them "reported"; they are now three packages
# at ${export_root}/examples/gotth, outside this tree, and for one landing the
# walk simply did not reach them — rule 4 stopped covering them and nothing said
# so. `-reported-root` walks them at the scope they always had: an Example with
# no `// Output:` fails, and their JSON payload fields are measured rather than
# demanded. doccheck refuses a reported root that resolves to no package, so a
# wrong path here fails loudly instead of quietly covering nothing.
#
# No `command -v` assertion here, for the same reason the two steps above have
# none: `go run` reports its own absence with a non-zero exit rather than with
# silence, which is the property D-19 was about. What doccheck could do
# silently is find nothing to check — from the wrong directory, or a root that
# no longer names the tree — so it refuses an empty walk itself and exits 2.
step "godoc on every exported symbol, and every example runs (FR-66, FR-68)"
if (cd tools && go run ./doccheck -reported-root "${export_root}/examples/gotth"); then
  :
else
  failures+=("godoc coverage (FR-66, FR-68)")
fi

# --- NFR-2, NFR-3, NFR-8, FR-57: the three client artifacts ------------------

step "client size gates and subsystem ledger (NFR-2, NFR-3, NFR-8, FR-57)"
if (cd tools && go run ./minify -check); then
  :
else
  failures+=("client size (NFR-2, NFR-3, NFR-8, FR-57)")
fi

# --- FR-7: byte-reproducible codegen ----------------------------------------

step "generated code is byte-reproducible (FR-7)"
if [ -d "${export_root}/pkg/liquidproto/cmd/protoc-gen-liquidproto" ]; then
  if bash gen.sh --check; then
    :
  else
    failures+=("codegen reproducibility (FR-7)")
  fi
else
  echo "SKIPPED: needs the export root candace/ mounted, not just this module." >&2
  echo "  From the REPOSITORY ROOT (a superset of what it needs):" >&2
  echo "  docker run --rm -v \"\$PWD:/workspace\" -w /workspace/candace/pkg/gotth \\" >&2
  echo "      dis-gotth-live:latest bash gen.sh --check" >&2
  echo "  The missing dependency is canonical candace/pkg/liquidproto. gen.sh --check covers" >&2
  echo "  templ outputs as well as the protobuf ones, and the templ half needs" >&2
  echo "  only templ and go — both present here, neither needing the plugin source. The" >&2
  echo "  step is not split, so a .templ edit that was never regenerated passes" >&2
  echo "  this invocation. Run the command above before trusting FR-7." >&2
  skipped+=("codegen reproducibility (FR-7) — including the templ half, which did not need the skip")
fi

# --- NFR-4: the no-eval scan, and the client's own suite ---------------------

step "client runtime suite, including the no-eval scan (NFR-4)"
if command -v node >/dev/null; then
  # D-19's shape, one step down: bash leaves an unmatched glob as its own
  # literal text, node 24 expands glob arguments itself, and node running zero
  # tests exits 0. So a client/test that had been emptied or renamed produced
  # `tests 0 ... fail 0` and a green NFR-4. Count the files first; the suite
  # existing is part of what this step asserts.
  bundle_specs=()
  for spec in client/test/*.test.mjs; do
    [ -f "${spec}" ] && bundle_specs+=("${spec}")
  done
  if [ "${#bundle_specs[@]}" -eq 0 ]; then
    echo "no client/test/*.test.mjs files matched: the no-eval scan asserts nothing" >&2
    failures+=("client runtime suite (NFR-4)")
  else
    bundle_failed=0
    for spec in "${bundle_specs[@]}"; do
      node --test "${spec}" || bundle_failed=1
    done
    if [ "${bundle_failed}" -ne 0 ]; then
      failures+=("client runtime suite (NFR-4)")
    fi
  fi
else
  # The counter in the printed command is not decoration, and it is the same
  # defect the step above guards against, one indirection out: this recipe
  # stands in for a gate that did not run, so it must not be able to pass
  # without running anything. Measured, with the recipe as it used to read:
  # from the repository root it matched no files, node ran `tests 0 ... fail 0`
  # and exited 0 — a green NFR-4 from a container that never loaded the bundle.
  # Fixing the working directory alone would not have closed that, because
  # `docker run` CREATES a missing -w rather than refusing, so the same paste
  # from this directory instead of the root still yields an empty directory, a
  # zero-spec pass, and a root-owned fossil. The count is what fails.
  echo "SKIPPED: node is absent from the library image by design." >&2
  echo "  From the REPOSITORY ROOT:" >&2
  echo "  docker run --rm -v \"\$PWD:/workspace\" -w /workspace/candace/pkg/gotth \\" >&2
  echo "      dis-gotth-live-bench:latest bash -c 'set -e; n=0" >&2
  echo "          for f in client/test/*.test.mjs; do test -f \"\$f\" || continue; n=\$((n+1)); node --test \"\$f\"; done" >&2
  echo "          test \"\$n\" -gt 0 || { echo \"no client/test/*.test.mjs matched\" >&2; exit 1; }'" >&2
  skipped+=("client runtime suite (NFR-4)")
fi

# --- equivalence-spec §3.6: the bench harness's own suite ---------------------

# The gates are code, so the gates need a gate. `bench/harness/*.test.mjs`
# holds §6's statistics, §2's interaction registry, §3.6's synthetic session
# driver — whose frames are round-tripped against the SHIPPED client codec
# rather than a copy of it — the 10 % driver-validation comparison including
# its failing side, the TLS-boundary refusals, and G-DRIVER's three ways of
# saying no. Nothing here starts a container, launches a browser or records a
# measurement; it is the part a reviewer can check by hand.
#
# The same shape as the client runtime suite above, for the same two reasons:
# node lives only in the bench image (that absence is what makes the no-node
# property structural), and a glob that matched nothing would let node exit 0
# over zero tests — so the file count is part of what this step asserts.
step "bench harness suite: the gates, the driver and the §6 statistics"
if command -v node >/dev/null; then
  harness_specs=()
  for spec in bench/harness/*.test.mjs; do
    [ -f "${spec}" ] && harness_specs+=("${spec}")
  done
  if [ "${#harness_specs[@]}" -eq 0 ]; then
    echo "no bench/harness/*.test.mjs files matched: the bench gates assert nothing" >&2
    failures+=("bench harness suite (equivalence-spec §3.6)")
  else
    harness_failed=0
    for spec in "${harness_specs[@]}"; do
      node --test "${spec}" || harness_failed=1
    done
    if [ "${harness_failed}" -ne 0 ]; then
      failures+=("bench harness suite (equivalence-spec §3.6)")
    fi
  fi
else
  echo "SKIPPED: node is absent from the library image by design." >&2
  echo "  From the REPOSITORY ROOT:" >&2
  echo "  docker run --rm -v \"\$PWD:/workspace\" -w /workspace/candace/pkg/gotth \\" >&2
  echo "      dis-gotth-live-bench:latest bash -c 'set -e; n=0" >&2
  echo "          for f in bench/harness/*.test.mjs; do test -f \"\$f\" || continue; n=\$((n+1)); node --test \"\$f\"; done" >&2
  echo "          test \"\$n\" -gt 0 || { echo \"no bench/harness/*.test.mjs matched\" >&2; exit 1; }'" >&2
  skipped+=("bench harness suite (equivalence-spec §3.6)")
fi

# --- FR-25…FR-28, FR-30…FR-32, NFR-7, FR-44, FR-57: the browser-labelled specs

# D-20. The step above already knows how to say "this invocation cannot run
# that"; the browser specs said nothing at all. `go test ./...` above runs the
# conformance package, browserOnly() calls Skip when CHROME_BIN is unset, and
# `go test` swallows a skip into exit 0 — so 30 of 154 specs did not run, 19 of
# them the DOM-preservation and HTMX evidence, and this script printed "every
# gate this invocation could run is green" over the top of it. Measured: 123 of
# 154 in dis-gotth-live:latest, 142 of 154 in dis-gotth-live-bench:latest.
#
# So: run them where a browser exists, announce them where one does not, and
# name the command either way. -ginkgo.fail-on-empty is there because a label
# filter that matches nothing is the same silent pass one level further out.
#
# The step's NAME grew two requirements on 2026-08-05, and that is not
# decoration. FR-44's inspector specs and FR-57's dev-reload specs landed in
# this package to discharge the oldest condition in docs/gates/phase-4.md §6 —
# "DEV-2's browser loop is not in CI", carried unaddressed through four
# revisions. A step whose name enumerates requirements is a claim about what a
# green line covers, so adding coverage without adding the numbers would make
# the label the same kind of stale claim this file exists to catch.
step "browser conformance specs (FR-25…FR-28, FR-30…FR-32, NFR-7, FR-44, FR-57)"
if [ -n "${CHROME_BIN:-}" ] && [ -x "${CHROME_BIN}" ]; then
  echo "browser: ${CHROME_BIN} ($("${CHROME_BIN}" --version 2>/dev/null || echo 'version unavailable'))"
  # GOTTHLIVE_E2E=1: the label selects 43 specs that pass without it and 50
  # with it. The extra seven are the ones that compile a second module or drive
  # one: the counter's own browser, latency and CSP specs, the inspector
  # against examples/gotth/counter, and FR-57's three dev-reload specs. Excluding
  # them would leave seven browser specs that no invocation of anything runs.
  #
  # Those two counts are re-derived when this file is touched, not carried.
  # They were 19/22 at checkpoint 3 and 22/25 after it, and BOTH were stale by
  # the time FR-44 and FR-57 arrived — measured here, at this tree, by running
  # the command below twice, once with GOTTHLIVE_E2E and once without, and
  # reading "N Passed" off each. -ginkgo.fail-on-empty is what actually guards
  # the step; the numbers are for the reader.
  #
  # FR-57's three specs need more than a browser. They copy the counter example
  # out of the checkout into a temporary directory and drive
  # internal/cmd/gotth-live-dev over it, so the image must also carry go and
  # templ. dis-gotth-live-bench:latest does, because it derives FROM the
  # library image rather than beside it — checked in the image rather than
  # assumed from the Dockerfile: go1.26.5 and templ v0.3.1020, and then a full
  # green run of the three specs.
  if GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ -count=1 \
      -args -ginkgo.label-filter=browser -ginkgo.fail-on-empty; then
    :
  else
    failures+=("browser conformance specs (FR-25…FR-28, FR-30…FR-32, NFR-7, FR-44, FR-57)")
  fi
else
  echo "SKIPPED: CHROME_BIN is unset or not executable; the library image has no browser." >&2
  echo "  This is 43 specs — every DOM-preservation and HTMX-coexistence spec in" >&2
  echo "  the suite, plus FR-44's inspector panel — and 7 more behind" >&2
  echo "  GOTTHLIVE_E2E: the counter's browser, latency and CSP specs, the" >&2
  echo "  inspector against examples/gotth/counter, and FR-57's three dev-reload specs" >&2
  echo "  (templ change, Go change, and the negative control that must NOT" >&2
  echo "  reload). They are NOT covered by the race-detector step above: it ran" >&2
  echo "  them and they skipped." >&2
  echo "  From the REPOSITORY ROOT:" >&2
  echo "  docker run --rm -v \"\$PWD:/workspace\" -w /workspace/candace/pkg/gotth \\" >&2
  echo "      dis-gotth-live-bench:latest bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ \\" >&2
  echo "          -count=1 -args -ginkgo.label-filter=browser -ginkgo.fail-on-empty'" >&2
  skipped+=("browser conformance specs (43 + 7: FR-25…FR-28 and FR-30…FR-32 in a browser, FR-44's inspector, FR-57's dev-reload loop)")
fi

# --- G11: consumable from a clean clone --------------------------------------

# `grep -n "G11" ci.sh` returned nothing until this step, and that was the whole
# of the Phase 4 gate's finding against it: docs/README.md asserted the property,
# the three example steps above ran in an image chosen for the toolchain it HAS,
# and no artifact recorded the criterion's own invocation on a machine without
# node, npm, protoc or protoc-gen-liquidproto. The mechanism that would make it true is real —
# FR-7's reproducibility gate and the committed *_templ.go and *.pb.go — but a
# criterion whose evidence is a sentence in a README is the shape this file has
# now caught six times.
#
# This is the one step that skips OUTWARD. Every other skip above sends a reader
# into a bigger image; this one sends them out of the containers entirely, and
# for a reason worth being blunt about: dis-gotth-live:latest is not a valid G11
# environment and cannot be made into one, because it ships templ and protoc.
# The gate has to start a container of its own, in an image chosen for what it
# does NOT have, and the gate images have no docker socket. So inside them this
# step skips, loudly, with the command that runs it.
#
# The runner's exit codes are a contract, so that "could not run" and "ran and
# failed" cannot be confused with each other: 0 is the property holding, 1 is
# G11 failing, 2 is a prerequisite the runner itself refused to work around.
# Only 1 is a failure of this gate.
step "consumable from a clean clone, in an image with no node, npm, protoc or protoc-gen-liquidproto (G11)"
if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
  bash tools/g11/run.sh
  g11_status=$?
  case "${g11_status}" in
    0) : ;;
    2)
      echo "SKIPPED: the runner reported a missing prerequisite; its own output above says which." >&2
      skipped+=("clean-clone consumability (G11) — the runner could not start")
      ;;
    *) failures+=("clean-clone consumability (G11)") ;;
  esac
else
  echo "SKIPPED: no usable docker daemon, and this gate has to start a container." >&2
  echo "  Not from a different image — from the HOST, outside all of them:" >&2
  echo "      bash tools/g11/run.sh          # from this directory, on the host" >&2
  echo "  It clones the repository over file://, runs the three examples in stock" >&2
  echo "  golang:1.26-bookworm, proves node, npm, protoc and protoc-gen-liquidproto are absent" >&2
  echo "  there, and fetches each example's page and client runtime over HTTP." >&2
  echo "  Do NOT substitute dis-gotth-live:latest: it ships templ and protoc, and" >&2
  echo "  two of the four tools G11 names being present is the whole of what the" >&2
  echo "  Phase 4 gate found wrong with the examples steps above." >&2
  echo "  The recorded result is docs/qa/g11-clean-clone.md." >&2
  skipped+=("clean-clone consumability (G11) — three examples served from a real clone, in an image without the four tools")
fi

# --- verdict -----------------------------------------------------------------

printf '\n\033[1m==> verdict\033[0m\n'

if [ "${#skipped[@]}" -ne 0 ]; then
  echo "skipped (needs a context this invocation does not have):"
  for name in "${skipped[@]}"; do
    echo "  - ${name}"
  done
fi

if [ "${#failures[@]}" -ne 0 ]; then
  echo "FAILED:"
  for name in "${failures[@]}"; do
    echo "  - ${name}"
  done
  exit 1
fi

echo "every gate this invocation could run is green"
