#!/usr/bin/env bash
# The test-convention gate. It enforces two rules with two different scopes, and
# the scopes are the point rather than an accident of history — a gate that
# walks a subtree draws a line, and an author who reads the line builds on the
# far side of it in good faith (see the note below the RunSpecs check).
#
#   1. RunSpecs bootstrap (candace/pkg + candace/tools). A standard-library
#      TestXxx function is allowed only as the RunSpecs bootstrap of a Ginkgo
#      suite. This walk is deliberately the public module's pkg/ and tools/
#      trees: the convention arrived there, pkg/gotth carries its own gate, and
#      widening the walk is a content decision made one directory at a time.
#
#   2. CS-11 dot import (REPO-WIDE, every first-party module). A *_test.go file
#      imports github.com/onsi/ginkgo/v2 and github.com/onsi/gomega with a dot,
#      so specs read Describe/It/Expect/Eventually unqualified the way this
#      tree's suites already do hundreds of times. This check is repo-wide, not
#      candace-only, because import detection is cheap and the shape it guards
#      against is not confined to one module — the only violators when it landed
#      were in go/. Its one exemption is structural and forced by the language:
#      an in-package test whose own package declares a top-level identifier the
#      library also exports (redis's Entry, which ginkgo's DescribeTable helper
#      is also named) cannot dot-import that library without a redeclaration
#      compile error, so that import stays qualified. Production code that wraps
#      gomega as a library (candace/pkg/patience) is not a test file and is out
#      of scope entirely.
#
# CS-11 is also carried by the Python gate (check_style.py, rule CS-11) so the
# eval corpus and the style census measure it, but the BLOCKING enforcement is
# here: this script is what candace-go-checks.yml runs, and it runs from the
# repository root so the repo-wide walk reaches go/ and github-runner/.
set -euo pipefail

module_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
repo_root="$(cd "${module_root}/.." && pwd)"

# ---------------------------------------------------------------------------
# Check 1 — RunSpecs bootstrap, scoped to candace/pkg and candace/tools.
# ---------------------------------------------------------------------------
walk_roots=("${module_root}/pkg" "${module_root}/tools")
violations=()

# `find` over a path that does not exist reports the error and carries on, and
# its exit status is discarded by the process substitution below — so a root
# renamed out from under this script would silently narrow the walk to whatever
# is left and still print nothing. A gate that cannot read its corpus must never
# be able to render itself as a clean one.
for walk_root in "${walk_roots[@]}"; do
  if [[ ! -d "${walk_root}" ]]; then
    printf 'check-test-style: %s is not a directory; refusing a vacuous pass\n' \
      "${walk_root}" >&2
    exit 2
  fi
done

examined=0
while IFS= read -r -d '' test_file; do
  examined=$((examined + 1))
  test_count="$(grep -Ec '^func Test[[:alnum:]_]*\(t \*testing[.]T\)' "${test_file}" || true)"
  bootstrap_count="$(grep -Ec '^[[:space:]]*(ginkgo[.])?RunSpecs\(t,' "${test_file}" || true)"
  if ((test_count != bootstrap_count)); then
    violations+=(
      "${test_file#"${module_root}/"} (${test_count} Test functions, ${bootstrap_count} RunSpecs bootstraps)"
    )
  fi
done < <(find "${walk_roots[@]}" -path "${module_root}/pkg/gotth" -prune -o -type f -name '*_test.go' -print0)

if ((examined == 0)); then
  printf 'check-test-style: no *_test.go under %s; refusing a vacuous pass\n' \
    "${walk_roots[*]}" >&2
  exit 2
fi

if ((${#violations[@]} > 0)); then
  printf 'Standard-library behavior tests are not allowed; use Ginkgo/Gomega:\n' >&2
  printf '  %s\n' "${violations[@]}" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Check 2 — CS-11 dot import, repo-wide over every tracked *_test.go.
# ---------------------------------------------------------------------------
# The two exact, quote-bounded paths. The gomega/ginkgo sub-packages
# (gomega/gstruct, gomega/types, ginkgo/v2/... helpers) are namespaced helpers
# and stay qualified, so only these two full paths are ever considered.
ginkgo_path='github.com/onsi/ginkgo/v2'
gomega_path='github.com/onsi/gomega'

# The exported names a package declares that would collide with a dot import of
# `alias` (the local name binding ginkgo or gomega). A `alias.Name` reference in
# any file of the directory proves the library exports Name; a top-level
# declaration of that same Name in the directory proves the package declares it;
# the two together are the redeclaration a dot import cannot escape.
dot_import_would_redeclare() {
  local directory="$1" alias="$2" used name
  used="$(grep -hoE "\b${alias}\.[A-Z][A-Za-z0-9_]*" "${directory}"/*.go 2>/dev/null \
    | sed -E "s/^${alias}\.//" | sort -u || true)"
  for name in ${used}; do
    if grep -qhE "^(type|func|var|const)[[:space:]]+${name}\b" "${directory}"/*.go 2>/dev/null; then
      return 0
    fi
  done
  return 1
}

dot_import_violations=()
test_files_examined=0
while IFS= read -r -d '' relative; do
  test_file="${repo_root}/${relative}"
  # Only pay for the line scan on files that import one of the two packages.
  grep -qE "\"(${ginkgo_path}|${gomega_path})\"" "${test_file}" || continue
  test_files_examined=$((test_files_examined + 1))
  directory="$(dirname "${test_file}")"

  while IFS= read -r import_line; do
    lineno="${import_line%%:*}"
    content="${import_line#*:}"
    # The spec text before the quoted path: strip a leading `import`, the
    # surrounding whitespace, and everything from the quote onward. What remains
    # is "" (plain import), "." (the dot import the rule wants), "_" (blank), or
    # an alias.
    before="${content%%\"*}"
    before="$(printf '%s' "${before}" | sed -E 's/^[[:space:]]*(import[[:space:]]+)?//; s/[[:space:]]+$//')"
    [[ "${before}" == "." ]] && continue  # dot import: compliant

    path="$(printf '%s' "${content}" | sed -E 's#.*"(github\.com/onsi/[^"]*)".*#\1#')"
    if [[ "${path}" == "${ginkgo_path}" ]]; then
      alias="${before:-ginkgo}"
    else
      alias="${before:-gomega}"
    fi

    # Structural exemption: a dot import here would redeclare a package
    # identifier, so the qualified import is forced rather than chosen. "_" has
    # no usable qualifier and is never forced.
    if [[ "${before}" != "_" ]] && dot_import_would_redeclare "${directory}" "${alias}"; then
      continue
    fi

    dot_import_violations+=(
      "${relative}:${lineno}: imports ${path} as '${before:-the package name}' rather than dot-importing it"
    )
  done < <(grep -nE "^[[:space:]]*(import[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*|\.|_)?[[:space:]]*\"(${ginkgo_path}|${gomega_path})\"[[:space:]]*\$" "${test_file}" \
    | grep -vE '^[0-9]+:[[:space:]]*//' || true)
done < <(git -C "${repo_root}" ls-files -z -- '*_test.go' ':!:**/vendor/**' ':!:vendor/**')

if ((test_files_examined == 0)); then
  printf 'check-test-style: no tracked *_test.go imports ginkgo/gomega; refusing a vacuous pass\n' >&2
  exit 2
fi

if ((${#dot_import_violations[@]} > 0)); then
  printf 'CS-11: test files must dot-import ginkgo/v2 and gomega:\n' >&2
  printf '  %s\n' "${dot_import_violations[@]}" >&2
  printf 'Use `. "%s"` and `. "%s"`. If an in-package test cannot (its package\n' "${ginkgo_path}" "${gomega_path}" >&2
  printf 'declares a name the library also exports), keep the qualified import.\n' >&2
  exit 1
fi
