#!/usr/bin/env bash
#
# Prepare one isolated run of bench task candaws-roundabout-v1.
#
# What it builds, under a scratch directory the caller never has to name:
#
#   <work>/sdk/          the widget SDK module, WITH the CandaWS tree removed
#   <work>/run/          the scratch Go module the implementer works in
#   <work>/run/task.md   the brief, and the only prose the implementer is given
#   <work>/bin/templ     the templ CLI, for BOTH conditions
#   <work>/dialect/      condition B only: the dialect documents it may read
#   <work>/bin/widgetc   condition B only: the generator, as a built binary
#
# The pruning is the point. bench.md § "Isolation" makes "the implementer read
# a CandaWS document" a void condition, and a procedural rule that a mechanical
# one could enforce is a rule that will eventually be broken by accident. The
# shipped Roundabout document, the five built fleet services and the probe
# design are all under examples/widget/candaws, and none of them is in the tree
# the run can see. What survives is the SDK, its README and raftdemo — which
# both conditions are entitled to.
#
# It does NOT hand the implementer anything else from this repository, and in
# particular not this directory: the fixtures are copied in under the names the
# brief promises, and README.md stays here.
#
# Usage:
#
#     bootstrap.sh <with-dialect|without-dialect> [work directory]
#
# It prints the work directory on the last line, so a caller can capture it.

set -euo pipefail

kit_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${kit_dir}/../../../../../.." && pwd)"
export_root="${repo_root}/candace"

condition="${1:-}"
case "${condition}" in
  with-dialect|without-dialect) ;;
  *)
    echo "bootstrap.sh: usage: bootstrap.sh <with-dialect|without-dialect> [work directory]" >&2
    exit 2
    ;;
esac

work="${2:-}"
if [ -z "${work}" ]; then
  work="$(mktemp -d -t roundabout-XXXXXXXX)"
else
  mkdir -p "${work}"
fi

[ -d "${export_root}/pkg/widget" ] || {
  echo "bootstrap.sh: ${export_root} does not look like the widget SDK's export root" >&2
  exit 2
}

# ---------------------------------------------------------------------------
# 1. The SDK the run builds against, minus every CandaWS file.
# ---------------------------------------------------------------------------
echo "==> copying the SDK into ${work}/sdk (without the CandaWS tree)"
rm -rf "${work}/sdk"
mkdir -p "${work}/sdk"
rsync -a --delete \
  --exclude 'examples/widget/candaws' \
  "${export_root}/" "${work}/sdk/"

if [ -e "${work}/sdk/examples/widget/candaws" ]; then
  echo "bootstrap.sh: the CandaWS tree survived the prune; refusing to hand it to a run" >&2
  exit 1
fi

# Harness revision 2 (2026-09-02, recorded in bench.md §10.7): run A1 proved
# the brief is not the boundary — the SDK copy carried the generator, the
# dialect corpus and worked .widget documents, and CS-4 (held on in both arms
# by design) led the run straight to them. A without-dialect tree therefore
# strips the treatment from the WORLD, not only from the brief: the dialect
# corpus, the generator, its CLI, and every .widget document. The generated
# projections (view_templ.go, widget.gen.go) stay, because they are committed
# build inputs the SDK's examples cannot compile without.
if [ "${condition}" = "without-dialect" ]; then
  rm -rf "${work}/sdk/pkg/widget/docs"          "${work}/sdk/pkg/widget/internal/uigen"          "${work}/sdk/pkg/widget/internal/cmd/widgetc"
  find "${work}/sdk" -name '*.widget' -delete
fi

# ---------------------------------------------------------------------------
# 2. The scratch module.
# ---------------------------------------------------------------------------
echo "==> writing the scratch module into ${work}/run"
rm -rf "${work}/run"
mkdir -p "${work}/run"

cat >"${work}/run/go.mod" <<'MODULE'
module roundabout

go 1.26.0

require github.com/candacelabs/candace v0.0.0

replace github.com/candacelabs/candace => ../sdk
MODULE

cp "${kit_dir}/task.md" "${work}/run/task.md"
cp "${kit_dir}/engine_spec_test.go.txt" "${work}/run/engine_spec_test.go"
cp "${kit_dir}/fragments_test.go.txt" "${work}/run/fragments_test.go"

# The fixtures' own checksums, so grading can tell whether they were edited
# without diffing them against a copy the implementer could also have reached.
(cd "${kit_dir}" && sha256sum engine_spec_test.go.txt fragments_test.go.txt) \
  >"${work}/fixtures.sha256"

# ---------------------------------------------------------------------------
# 3. Condition B's two extras.
# ---------------------------------------------------------------------------
if [ "${condition}" = "with-dialect" ]; then
  echo "==> condition B: the dialect documents and a built widgetc"
  mkdir -p "${work}/dialect"
  cp "${export_root}/pkg/widget/docs/dialect.md" "${work}/dialect/dialect.md"
  cp "${export_root}/pkg/widget/docs/errors.md" "${work}/dialect/errors.md"
  cp "${export_root}/pkg/widget/docs/examples/01-cluster-heartbeats.widget" "${work}/dialect/"
  cp "${export_root}/pkg/widget/docs/examples/03-relay-pipeline.widget" "${work}/dialect/"

  mkdir -p "${work}/bin"
  docker run --rm \
    -v "${work}:/work" -v "${HOME}/go:/gopath" -w /work/sdk \
    -e GOPATH=/gopath -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
    golang:1.26 bash -c 'go build -o /work/bin/widgetc ./pkg/widget/internal/cmd/widgetc'
  chown --reference="${work}" "${work}/bin/widgetc" 2>/dev/null || true
fi

# ---------------------------------------------------------------------------
# 3b. The templ CLI, for BOTH conditions.
#
# It is not part of the varied thing. templ is how the SDK renders — the README
# documents it and raftdemo uses it — so a condition A that could not compile a
# hand-written .templ would be missing a tool the SDK expects rather than the
# one this task withholds. It comes from the image that pins it rather than
# from a fetch, so the CLI matches the module's templ runtime.
# ---------------------------------------------------------------------------
mkdir -p "${work}/bin"
docker run --rm -v "${work}/bin:/out" --entrypoint /bin/bash \
  dis-gotth-live:latest -c 'cp "$(command -v templ)" /out/templ'
chown --reference="${work}" "${work}/bin/templ" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4. The baseline commit.
#
# diff_in_source_pct counts the lines the RUN produced, so everything handed to
# it is committed first and the metric reads the diff against that.
# ---------------------------------------------------------------------------
echo "==> baselining ${work}/run"
git -C "${work}/run" init -q
git -C "${work}/run" -c user.email=bench@example.invalid -c user.name=bench \
  add -A >/dev/null
git -C "${work}/run" -c user.email=bench@example.invalid -c user.name=bench \
  commit -q -m "bench fixtures and module, as handed to the run"

# ---------------------------------------------------------------------------
# 5. Prove the fixtures compile against the SDK before anybody is handed them.
#
# They must fail to BUILD, because nothing implements the seam yet — but they
# must fail on the missing implementation and nothing else. A fixture that
# cannot resolve the SDK would waste an implementer's whole run.
# ---------------------------------------------------------------------------
echo "==> checking the fixtures resolve against the SDK"
resolution="$(docker run --rm \
  -v "${work}:/work" -v "${HOME}/go:/gopath" -w /work/run \
  -e GOPATH=/gopath -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
  golang:1.26 bash -c 'go mod tidy >/dev/null 2>&1; go vet ./... 2>&1' || true)"
if echo "${resolution}" | grep -qE 'no required module|cannot find module|missing go.sum'; then
  echo "bootstrap.sh: the fixtures do not resolve against the SDK copy:" >&2
  echo "${resolution}" >&2
  exit 1
fi
chown -R --reference="${work}" "${work}/run" 2>/dev/null || true

echo
echo "condition:  ${condition}"
echo "brief:      ${work}/run/task.md"
echo "fixtures:   ${work}/run/engine_spec_test.go ${work}/run/fragments_test.go"
echo "templ:      ${work}/bin/templ"
[ "${condition}" = "with-dialect" ] && echo "dialect:    ${work}/dialect  widgetc: ${work}/bin/widgetc"
echo "${work}"
