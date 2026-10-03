#!/usr/bin/env bash
#
# Grade one run of bench task candaws-roundabout-v1.
#
# Everything here is mechanical, and everything here is derived from RENDERED
# OUTPUT AND TEST RESULTS. Nothing in this script reads a line of the
# implementation's source: the only files it opens are its own two fixtures, to
# recover their test names and their checksums, and the only thing it counts is
# how many lines landed in which file — never what those lines say. That is the
# rule the A/B design rests on. A hand-written card and a generated card share
# no file names and no function names, so a grader that greps source is grading
# one condition on a bar the other cannot reach.
#
# Usage:
#
#     grade.sh <with-dialect|without-dialect> <work directory from bootstrap.sh>
#
# Exit status is 0 when every mechanical outcome passed, 1 when any did not, and
# 2 when the run could not be graded at all. The third is a failure too: an
# ungraded run must never read as a pass.
#
# The last line of stdout is the ledger row's mechanical half, as JSON. It
# carries fixtures_intact, so a voided run cannot be transcribed as a clean pass
# by an operator who copies the row and not the prose above it.

set -uo pipefail

kit_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

condition="${1:-}"
work="${2:-}"
case "${condition}" in
  with-dialect|without-dialect) ;;
  *)
    echo "grade.sh: usage: grade.sh <with-dialect|without-dialect> <work directory>" >&2
    exit 2
    ;;
esac
[ -n "${work}" ] && [ -d "${work}/run" ] || {
  echo "grade.sh: ${work:-<empty>} is not a bootstrapped work directory" >&2
  exit 2
}

# go runs the Go toolchain over the scratch module, pinned and containerised:
# this host has no Go, and a grader whose toolchain drifts between two runs of
# an A/B pair is not comparing them on the same bar.
go_in() {
  local workdir="$1"; shift
  docker run --rm \
    -v "${work}:/work" -v "${HOME}/go:/gopath" -w "/work/${workdir}" \
    -e GOPATH=/gopath -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
    -e GOCACHE=/gopath/bench-build-cache \
    golang:1.26 bash -c "$*"
}

# tests_named prints a Go test-name regex covering every top-level test in one
# fixture. It reads the GRADER's copy, never the run's, so a run that renamed a
# test does not quietly shrink what it is graded on.
tests_named() {
  local fixture="$1"
  local names
  names="$(grep -oE '^func (Test[A-Za-z0-9_]+)\(' "${kit_dir}/${fixture}" \
    | sed -E 's/^func //; s/\($//' | paste -sd '|' -)"
  [ -n "${names}" ] || { echo "grade.sh: no tests found in ${fixture}" >&2; exit 2; }
  printf '^(%s)$' "${names}"
}

pass() { [ "$1" = "true" ] && printf 'PASS' || printf 'FAIL'; }

echo "=== bench candaws-roundabout-v1 — ${condition}"
echo "=== work: ${work}"
echo

# ---------------------------------------------------------------------------
# 0. Void check: were the fixtures edited?
#
# Recomputed against this directory's checksums rather than against the copy in
# the work tree, which the run could also have rewritten.
# ---------------------------------------------------------------------------
fixtures_intact=true
for fixture in engine_spec_test.go fragments_test.go; do
  if [ ! -f "${work}/run/${fixture}" ]; then
    echo "VOID: ${fixture} is missing from the run"
    fixtures_intact=false
    continue
  fi
  expected="$(sha256sum <"${kit_dir}/${fixture}.txt" | cut -d' ' -f1)"
  actual="$(sha256sum <"${work}/run/${fixture}" | cut -d' ' -f1)"
  if [ "${expected}" != "${actual}" ]; then
    echo "VOID: ${fixture} was edited by the run"
    fixtures_intact=false
  fi
done
echo "fixtures unedited:  $(pass "${fixtures_intact}")"

# ---------------------------------------------------------------------------
# 1. compiled
# ---------------------------------------------------------------------------
vet_output="$(go_in run 'go mod tidy >/dev/null 2>&1; go vet ./... 2>&1')"
vet_status=$?
compiled=false
[ "${vet_status}" = 0 ] && compiled=true
echo "compiled:           $(pass "${compiled}")"
[ "${compiled}" = true ] || { echo "--- go vet ---"; echo "${vet_output}"; }

# ---------------------------------------------------------------------------
# 2. engine_specs_pass and card_fragments_pass, recorded separately.
#
# Separately because a run that got the concurrency right and the card wrong is
# a different result from the reverse, and one boolean cannot say which.
# ---------------------------------------------------------------------------
engine_specs_pass=false
card_fragments_pass=false
engine_output=""
fragment_output=""
if [ "${compiled}" = true ]; then
  engine_output="$(go_in run "go test -race -count=1 -run '$(tests_named engine_spec_test.go.txt)' ./... 2>&1")"
  [ $? = 0 ] && engine_specs_pass=true
  fragment_output="$(go_in run "go test -race -count=1 -run '$(tests_named fragments_test.go.txt)' ./... 2>&1")"
  [ $? = 0 ] && card_fragments_pass=true
fi
echo "engine_specs_pass:  $(pass "${engine_specs_pass}")"
[ "${engine_specs_pass}" = true ] || { echo "--- engine specs ---"; echo "${engine_output}" | tail -40; }
echo "card_fragments_pass:$(pass "${card_fragments_pass}")"
[ "${card_fragments_pass}" = true ] || { echo "--- card fragments ---"; echo "${fragment_output}" | tail -40; }

# ---------------------------------------------------------------------------
# 3. tests_pass and race_clean, from one whole-suite run.
#
# Two fields from one command, because a run can fail a test AND be racy and the
# two mean different things.
# ---------------------------------------------------------------------------
tests_pass=false
race_clean=false
suite_output=""
if [ "${compiled}" = true ]; then
  suite_output="$(go_in run 'go test -race -count=1 ./... 2>&1')"
  [ $? = 0 ] && tests_pass=true
  echo "${suite_output}" | grep -q 'WARNING: DATA RACE' || race_clean=true
fi
echo "tests_pass:         $(pass "${tests_pass}")"
echo "race_clean:         $(pass "${race_clean}")"
[ "${tests_pass}" = true ] || { echo "--- go test -race ---"; echo "${suite_output}" | tail -60; }

# ---------------------------------------------------------------------------
# 4. widgetc_clean — condition B only.
#
# Absent for A, which has no document: a field nobody computed stays absent.
# ---------------------------------------------------------------------------
widgetc_clean_json=""
if [ "${condition}" = "with-dialect" ]; then
  widgetc_clean=false
  documents="$(cd "${work}/run" && ls -1 ./*.widget 2>/dev/null)"
  if [ -z "${documents}" ]; then
    echo "widgetc_clean:      FAIL (the run produced no widget document)"
  elif [ ! -x "${work}/bin/widgetc" ]; then
    echo "grade.sh: ${work}/bin/widgetc is missing; re-run bootstrap.sh" >&2
    exit 2
  else
    validate_output="$(docker run --rm \
      -v "${work}:/work" -w /work/run \
      golang:1.26 bash -c '/work/bin/widgetc validate ./*.widget 2>&1')"
    [ $? = 0 ] && widgetc_clean=true
    echo "widgetc_clean:      $(pass "${widgetc_clean}")"
    [ "${widgetc_clean}" = true ] || { echo "--- widgetc ---"; echo "${validate_output}"; }
  fi
  widgetc_clean_json=", \"widgetc_clean\": ${widgetc_clean}"
fi

# ---------------------------------------------------------------------------
# 5. The two derivability fields, per bench.md § 5.
#
# Line counts only. This step never opens a source file: git reports how many
# lines were added to each path and wc reports how long the document is, and
# neither says what any of those lines contain.
# ---------------------------------------------------------------------------
metrics() {
  local run="${work}/run"
  git -C "${run}" -c user.email=bench@example.invalid -c user.name=bench add -A >/dev/null 2>&1
  local src hand
  # The denominator is what the RUN AUTHORED — the document plus hand-written
  # card code — and not what the toolchain emitted. bench.md § 5 carries the
  # measurement that settled it: with emitted output in the denominator a clean
  # generated run scores 1/(1+amplification), which is that other field said
  # twice, and the § 5 reading "a run that hand-edits scores below 100" has no
  # 100 to be below.
  src=$(git -C "${run}" diff --cached --numstat -- '*.widget' | awk '{s+=$1} END {print s+0}')
  hand=$(git -C "${run}" diff --cached --numstat -- 'card*.go' 'card*.templ' '*.css' \
        | grep -v '_templ\.go' | awk '{s+=$1} END {print s+0}')
  local denominator=$(( src + hand ))
  if [ "${denominator}" -eq 0 ]; then
    echo "0.0 "
    return
  fi
  local in_source
  in_source=$(python3 -c "print(round(100*${src}/${denominator}, 1))")
  if [ "${src}" -eq 0 ]; then
    echo "${in_source} "
    return
  fi
  local emitted_lines source_lines
  emitted_lines=$(cd "${run}" && cat view.templ view_templ.go widget.gen.go 2>/dev/null | wc -l)
  source_lines=$(cd "${run}" && cat ./*.widget | wc -l)
  echo "${in_source} $(python3 -c "print(round(${emitted_lines}/${source_lines}, 2))")"
}
read -r diff_in_source_pct amplification <<<"$(metrics)"
echo "diff_in_source_pct: ${diff_in_source_pct}"
amplification_json=""
if [ -n "${amplification}" ]; then
  echo "amplification:      ${amplification}"
  amplification_json=", \"amplification\": ${amplification}"
fi

# ---------------------------------------------------------------------------
# The row's mechanical half.
#
# fixtures_intact leads it because a void invalidates every field after it. Its
# absence was found by the P4 audit: step 0 above printed VOID in English, the
# row printed `"compiled": true, "tests_pass": true, ...` and nothing in it
# could say the run had been voided. § 10.5 tells the operator to record
# everything this script printed, and an operator who copies the row rather than
# the prose above it was recording a clean pass for a voided run. A doctrine
# that "a void run gets a row saying so" cannot rest on somebody reading English.
# ---------------------------------------------------------------------------
echo
printf '{"kind": "bench", "date": "%s", "task": "candaws-roundabout-v1", "condition": "%s", ' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${condition}"
printf '"fixtures_intact": %s, "compiled": %s, "tests_pass": %s, "race_clean": %s, ' \
  "${fixtures_intact}" "${compiled}" "${tests_pass}" "${race_clean}"
printf '"engine_specs_pass": %s, "card_fragments_pass": %s%s, "diff_in_source_pct": %s%s}\n' \
  "${engine_specs_pass}" "${card_fragments_pass}" "${widgetc_clean_json}" \
  "${diff_in_source_pct}" "${amplification_json}"

for outcome in "${fixtures_intact}" "${compiled}" "${tests_pass}" "${race_clean}" \
               "${engine_specs_pass}" "${card_fragments_pass}"; do
  [ "${outcome}" = true ] || exit 1
done
if [ "${condition}" = "with-dialect" ] && [ "${widgetc_clean:-false}" != true ]; then
  exit 1
fi
exit 0
