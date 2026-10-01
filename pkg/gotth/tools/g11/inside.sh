#!/usr/bin/env bash
#
# The container half of the G11 gate. run.sh copies this in and invokes it; it
# is not meant to be run by hand, and it assumes the layout run.sh builds:
#
#   /g11/clone          a fresh `git clone` of the repository, at HEAD
#   /g11/summary.txt    written here, read by run.sh for its verdict
#
# Everything in here runs in a stock upstream `golang` image, which is the whole
# point: the four tools G11 names are absent, and the first thing this script
# does is prove it rather than assume it.
#
# It answers TWO questions and keeps them apart, because they have different
# answers and merging them is how a box gets ticked for the wrong reason:
#
#   1. Does the command G11's own wording names — `go run ./examples/<name>` —
#      work? It could not while the examples were separate modules with their
#      own go.mod, and the DISCREPANCY block below is the report that said so.
#      The single-module fold changed the answer: the examples are packages of
#      github.com/candacelabs/csf now, at examples/gotth/<name>, so the
#      literal command works from the export root. The step still measures it
#      rather than asserting it.
#   2. Does the property G11 is ABOUT hold — can a stranger with nothing but Go
#      clone this and get all three examples serving? That is the question the
#      run below actually measures, with the command docs/README.md documents.

set -uo pipefail

clone=/g11/clone
# The clone arrives in one of two layouts and this gate has to work in both, or
# the one gate whose entire subject is a stranger's clone is the one a stranger
# cannot run. Inside the monorepo the export root is a candace/ directory below
# the repository root; the published repository IS that export root, so its
# clone has go.mod at the top. Detect it from the module file rather than from
# the directory name, which is the thing that differs.
if [ -f "${clone}/candace/go.mod" ]; then
  export_root="${clone}/candace"
  # What a reader has to type in front of a path, in the layout under test.
  from_root='candace/'
else
  export_root="${clone}"
  from_root=''
fi
module="${export_root}/pkg/gotth"
summary=/g11/summary.txt
deadline="${G11_DEADLINE:-90}"

: >"${summary}"
record() { printf '%s=%s\n' "$1" "$2" >>"${summary}"; }

failures=()

step() { printf '\n\033[1m--- %s\033[0m\n' "$1"; }

# --- the environment ---------------------------------------------------------

step "the environment"
echo "go:        $(go version)"
echo "GOVERSION: $(go env GOVERSION)"
echo "GOOS/ARCH: $(go env GOOS)/$(go env GOARCH)"
echo "image:     $(grep -m1 PRETTY_NAME /etc/os-release | cut -d= -f2- | tr -d '"')"
record "goversion" "$(go env GOVERSION)"

# --- the precondition G11 is stated against ----------------------------------
#
# "with no node, npm, protoc, or protoc-gen-liquidproto installed" is a condition on the
# machine, and until this ran the three example steps in ci.sh executed in
# dis-gotth-live:latest, an image chosen for the toolchain it HAS. templ and
# protoc are both in it. A gate that does not check its own precondition is
# asserting the thing it was written to measure, so this is checked first and
# any hit is fatal to the run.

step "the four tools G11 names must be absent"
absent_ok=1
for tool in node npm protoc protoc-gen-liquidproto; do
  path="$(command -v "${tool}" 2>/dev/null)"
  if [ -n "${path}" ]; then
    echo "PRESENT: ${tool} -> ${path}   (this is not a G11 environment)"
    absent_ok=0
  else
    echo "absent:  ${tool}"
  fi
done
record "tools_absent" "$([ "${absent_ok}" = 1 ] && echo yes || echo no)"
if [ "${absent_ok}" != 1 ]; then
  failures+=("one of node/npm/protoc/protoc-gen-liquidproto is installed: the run cannot answer G11")
fi

# Beyond G11's four, and reported as beyond them. docs/README.md's sentence says
# "no generator", and templ is a generator: the examples' *_templ.go files are
# committed, so if templ is absent here and the examples still build, that is
# the committed-generated-code claim measured rather than repeated. buf is the
# other way to reach protoc and is checked for the same reason.
echo "also, past what G11 names:"
for tool in templ buf protoc-gen-go go-bindata; do
  path="$(command -v "${tool}" 2>/dev/null)"
  printf '  %-14s %s\n' "${tool}" "${path:-absent}"
done

# --- the toolchain against go.mod --------------------------------------------
#
# The default image tag is a minor tag, so it moves. This is what stops it from
# moving somewhere that answers a different question: if the image's Go is older
# than the module's own `go` directive, the run would fail for a reason that has
# nothing to do with G11 and the failure would be blamed on the tree.

# The go.mod is the EXPORT ROOT's: the single-module fold made this library a
# package of github.com/candacelabs/csf rather than a module of its own, and
# ${module}/go.mod has not existed since. Reading the path that no longer exists
# left `directive` empty, and an empty string sorts below every version, so this
# check reported "clean" no matter which image it ran in — the one failure it
# was written to catch was the one it could not see. An unparseable directive is
# a failure now, so the next path change cannot make it inert again.
step "the toolchain satisfies go.mod"
directive="$(grep -m1 -E '^go [0-9]' "${export_root}/go.mod" | awk '{print $2}')"
have="$(go env GOVERSION)"
have="${have#go}"
lowest="$(printf '%s\n%s\n' "${directive}" "${have}" | sort -V | head -n1)"
echo "go.mod says go ${directive:-(none found)}; this image has go ${have}"
record "go_directive" "${directive}"
if [ -z "${directive}" ]; then
  echo "no 'go' directive found in ${export_root}/go.mod: this check cannot answer." >&2
  failures+=("the module's go directive could not be read")
elif [ "${lowest}" = "${directive}" ]; then
  echo "clean: the image satisfies the module"
else
  echo "the image's Go is OLDER than the module's go directive." >&2
  failures+=("image toolchain go ${have} is older than go.mod's go ${directive}")
fi

# --- an external consumer with both checkout replacements -------------------
#
# The library is a package of github.com/candacelabs/csf, so an unpublished
# checkout consumer names ONE local module where it used to name two. Build a
# module outside the clone to prove that handoff works; the examples below are
# useful applications, but they are inside the module rather than consumers of
# it, so they cannot make this claim on a stranger's behalf.

step "a scratch external consumer resolves candace from the checkout"
consumer=/g11/external-consumer
rm -rf "${consumer}"
mkdir -p "${consumer}"
cat >"${consumer}/go.mod" <<EOF
module example.invalid/g11-consumer

go 1.26.0

require github.com/candacelabs/csf v0.0.0

replace github.com/candacelabs/csf => ${export_root}
EOF
cat >"${consumer}/consumer_test.go" <<'EOF'
package consumer

import "github.com/candacelabs/csf/pkg/gotth/live"

func compilePublicAPI() {
	var _ live.Config[int]
}
EOF
consumer_ok=1
if ! (cd "${consumer}" && GOWORK=off go test -mod=mod ./...); then
	consumer_ok=0
	failures+=("scratch consumer could not build with the checkout replacement")
fi
candace_resolved="$(cd "${consumer}" && GOWORK=off go list -mod=mod -m -f '{{.Dir}}' github.com/candacelabs/csf 2>/dev/null)"
if [ "${candace_resolved}" != "${export_root}" ]; then
	echo "FAIL: the checkout replacement resolved to an unexpected directory:" >&2
	echo "  candace: ${candace_resolved:-unresolved}" >&2
	consumer_ok=0
	failures+=("scratch consumer did not resolve the checkout replacement")
fi
if [ "${consumer_ok}" = 1 ]; then
	echo "PASS: a module outside the clone compiled against the checkout module"
fi
record "external_consumer" "$([ "${consumer_ok}" = 1 ] && echo PASS || echo FAIL)"

# --- question 1: G11 exactly as it is worded ---------------------------------

# The three plausible working directories collapse to two when the clone IS the
# export root, which is what a clone of the published repository looks like.
# Trying the same directory twice would start a second server on a port the
# first already holds and print the bind failure as if the literal command had
# not worked, so the list is deduplicated rather than repeated.
cwds=("${clone}")
[ "${export_root}" = "${clone}" ] || cwds+=("${export_root}")
cwds+=("${module}")

step "G11 as worded: 'go run ./examples/<name>'"
echo "G11 names no working directory, so all ${#cwds[@]} plausible ones are tried."
asworded_any=0
for cwd in "${cwds[@]}"; do
  echo
  echo "from ${cwd}:"
  for ex in counter chat dashboard; do
    # timeout, because a form of this that WORKED would start a server and
    # never return. 124 is timeout's own code for that, and it is the branch
    # that would mean the discrepancy below had been fixed.
    out="$(cd "${cwd}" && timeout 30 go run "./examples/gotth/${ex}" 2>&1)"
    rc=$?
    first="$(printf '%s' "${out}" | head -n1)"
    if [ "${rc}" -eq 124 ]; then
      echo "  \$ go run ./examples/gotth/${ex}   -> STARTED (still running at 30s)"
      asworded_any=1
    else
      echo "  \$ go run ./examples/gotth/${ex}   -> exit ${rc}"
      echo "      ${first:-(no output)}"
    fi
  done
done
pkill -f 'exe/(counter|chat|dashboard)$' >/dev/null 2>&1

if [ "${asworded_any}" = 1 ]; then
  record "as_worded" "works"
  echo
  echo "G11's literal command works from at least one directory."
else
  record "as_worded" "impossible"
  cat <<'DISCREPANCY'

################################ DISCREPANCY ################################
G11's literal command did not work from any directory on this tree.

This block used to explain why it could not: the three examples were separate
Go modules, each with its own go.mod and its own `replace github.com/... =>
../..`, and a separate module is not a package of its parent. The single-module
fold removed that obstacle — the examples are packages of
github.com/candacelabs/csf at examples/gotth/<name> — so reaching this
block now means something else is wrong, and it is worth finding out what
rather than reading this as the expected outcome.

The invocation the tree documents either way is:
DISCREPANCY
  printf '\n      cd %sexamples/gotth/<name> && go run .\n' "${from_root}"
  cat <<'DISCREPANCY'

That is what the section below runs.
#############################################################################
DISCREPANCY
fi

# --- question 2: the property, with the invocation the tree supports ---------

step "the invocation the tree documents: 'cd ${from_root}examples/gotth/<name> && go run .'"

# Ports are the examples' own documented defaults, so this runs `go run .` with
# no arguments — the command a reader of examples/gotth/<name>/README.md types. They
# are bound on 127.0.0.1 INSIDE this container and no host port is published, so
# nothing here can collide with a machine that is serving real traffic.
#
# The third argument is a path to fetch, with a cookie jar, before asking for
# the page. It is empty for two of the three and it is chat's whole sign-in:
# chat's `/` renders a LOGIN page to a caller with no identity cookie, which is
# the example behaving exactly as examples/gotth/chat/README.md documents it, and a
# gate that fetched `/` and looked for live regions would report chat broken. It
# is spelled out here rather than hidden in a curl flag because the first run of
# this script did exactly that and the answer was a defect in the gate.
serve() {
  local name="$1" port="$2" login="${3:-}"
  local dir="${export_root}/examples/gotth/${name}"
  local log="/tmp/${name}.log" page="/tmp/${name}.html" js="/tmp/${name}.js"
  local jar="/tmp/${name}.cookies"

  echo
  echo "=== ${name} ==="
  echo "\$ cd ${from_root}examples/gotth/${name} && go run ."

  ( cd "${dir}" && exec go run . ) >"${log}" 2>&1 &
  local pid=$!

  local served=0 i
  for i in $(seq 1 "${deadline}"); do
    if ! kill -0 "${pid}" 2>/dev/null; then
      echo "  FAIL: the process exited before it served anything. Its output:"
      sed 's/^/    /' "${log}"
      failures+=("examples/gotth/${name} exited before serving")
      record "${name}" "FAIL_exited"
      return 1
    fi
    if curl -sS -f --max-time 5 -o "${page}" "http://127.0.0.1:${port}/" 2>/dev/null; then
      served="${i}"
      break
    fi
    sleep 1
  done

  if [ "${served}" = 0 ]; then
    echo "  FAIL: nothing served on 127.0.0.1:${port} within ${deadline}s. Its output:"
    sed 's/^/    /' "${log}"
    kill "${pid}" 2>/dev/null
    wait "${pid}" 2>/dev/null
    failures+=("examples/gotth/${name} did not serve within ${deadline}s")
    record "${name}" "FAIL_timeout"
    return 1
  fi

  local downloads
  downloads="$(grep -c '^go: downloading ' "${log}")"
  echo "  served after ${served}s; ${downloads} modules downloaded from the proxy first"
  echo "  its own startup output:"
  grep -v '^go: downloading ' "${log}" | sed 's/^/    /'

  local ok=1

  if [ -n "${login}" ]; then
    echo "  \$ curl -c jar -L 'http://127.0.0.1:${port}${login}'   (this example signs in first)"
    if ! curl -sS -f -L --max-time 5 -c "${jar}" -o /dev/null "http://127.0.0.1:${port}${login}"; then
      echo "  FAIL: the documented sign-in path did not answer." >&2
      ok=0
    fi
    curl -sS -f --max-time 5 -b "${jar}" -c "${jar}" -o "${page}" "http://127.0.0.1:${port}/" \
      || { echo "  FAIL: the page after sign-in did not serve." >&2; ok=0; }
  fi

  # Evidence 1: the page is a page. Byte count printed so "it served" cannot
  # mean "it returned 200 and nothing else".
  local bytes
  bytes="$(wc -c <"${page}")"
  echo "  GET /  ->  200, ${bytes} bytes"
  if [ "${bytes}" -lt 200 ]; then
    echo "  FAIL: that is not a rendered page." >&2
    ok=0
  fi

  # Evidence 2: the live-region markup. This is the attribute the client morphs
  # against and the thing a patch names; a document without it is a static page
  # that happens to be served by this binary. Region IDs are printed because a
  # count of zero and a count of two are different findings.
  local regions
  regions="$(grep -o 'data-gotth-region="[^"]*"' "${page}" | sort -u)"
  if [ -z "${regions}" ]; then
    echo "  FAIL: no data-gotth-region in the served document." >&2
    ok=0
  else
    echo "  live regions: $(printf '%s' "${regions}" | tr '\n' ' ')"
  fi

  # Evidence 3, and the load-bearing one for the "no npm" half of G11: the page
  # names a client runtime URL, and that URL serves bytes. Nothing in this
  # container could have built that file — there is no node here — so it came
  # out of the Go binary, which is the committed, embedded artifact.
  #
  # The URL is READ OUT OF THE PAGE rather than hardcoded, and that is not
  # tidiness. live.Script renders the runtime's src under the application's own
  # mount path, and the three examples mount at three different places —
  # /live, /chat/live, /dashboard/live. A gate with /live written into it
  # reported two of the three as serving no runtime, which was false. Taking the
  # page at its word also makes this a stronger check: it asserts that the URL
  # the browser would actually request is the URL that answers.
  local src url
  src="$(grep -o 'src="[^"]*gotth-live\.min\.js"' "${page}" | head -n1)"
  url="${src#src=\"}"; url="${url%\"}"
  if [ -z "${src}" ]; then
    echo "  FAIL: the page does not reference the shipped client runtime." >&2
    ok=0
  elif curl -sS -f --max-time 5 -o "${js}" "http://127.0.0.1:${port}${url}" 2>/dev/null; then
    local jsbytes
    jsbytes="$(wc -c <"${js}")"
    echo "  GET ${url}  ->  200, ${jsbytes} bytes, built by nothing on this machine"
    if [ "${jsbytes}" -lt 1000 ]; then
      echo "  FAIL: the runtime served is too small to be the runtime." >&2
      ok=0
    fi
  else
    echo "  FAIL: the runtime URL the page names does not serve." >&2
    ok=0
  fi

  # Evidence 4: it was still the process we started that answered.
  if kill -0 "${pid}" 2>/dev/null; then
    echo "  the process was still running when all of that was fetched"
  else
    echo "  FAIL: the process had exited by the end of the checks." >&2
    ok=0
  fi

  # `go run` runs the compiled binary as a CHILD, so killing go run alone leaves
  # the server holding the port. Killing it is not cosmetic: the check below is
  # what proves the answers above came from this process and not from a
  # leftover, and a leftover makes every later example's evidence wrong too.
  #
  # Find it by parentage, not by name. The path of that binary has already
  # changed shape once — `go run` used to exec $TMPDIR/go-build*/b001/exe/<name>
  # and Go 1.26 runs the cached artifact at $GOCACHE/xx/<hash>-d/<name> directly
  # once the cache is warm — and the `exe/<name>` pattern that used to be the
  # safety net silently stopped matching anything at that point. Parentage is a
  # fact about this process rather than about the toolchain's internals.
  children=''
  for proc in /proc/[0-9]*; do
    [ -r "${proc}/stat" ] || continue
    # Field 2 of /proc/pid/stat is a parenthesised command that may contain
    # spaces, so cut through the last ')' before counting fields: what follows
    # is the state, then the parent pid.
    parent="$(sed -e 's/^.*) //' "${proc}/stat" 2>/dev/null | awk '{print $2}')"
    [ "${parent}" = "${pid}" ] && children="${children} ${proc#/proc/}"
  done

  kill "${pid}" 2>/dev/null
  wait "${pid}" 2>/dev/null
  # shellcheck disable=SC2086
  [ -z "${children}" ] || kill ${children} 2>/dev/null
  # Belt and braces, for both artifact paths Go has used. Cheap, and it costs
  # nothing when the parentage kill already worked.
  pkill -f "go-build.*/${name}\$" >/dev/null 2>&1
  sleep 1
  if curl -sS -f --max-time 3 -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; then
    echo "  FAIL: 127.0.0.1:${port} still answers after the process was killed." >&2
    # Say WHICH process, because the two ways this fails need opposite repairs:
    # a survivor whose command line the pkill pattern does not match is a bug in
    # this gate, and no survivor at all means something outside the container is
    # answering and the evidence above is about the wrong server.
    echo "  processes still alive whose command line names this example:" >&2
    survivors=0
    for proc in /proc/[0-9]*; do
      cmd="$(tr '\0' ' ' <"${proc}/cmdline" 2>/dev/null)" || continue
      case "${cmd}" in
        *"${name}"*)
          echo "    ${proc#/proc/}: ${cmd}" >&2
          survivors=$((survivors + 1))
          ;;
      esac
    done
    [ "${survivors}" -gt 0 ] || echo "    (none — the answer came from outside this container)" >&2
    ok=0
  else
    echo "  port ${port} stopped answering once the process was killed"
  fi

  if [ "${ok}" = 1 ]; then
    echo "  ${name}: PASS"
    record "${name}" "PASS"
    return 0
  fi
  failures+=("examples/gotth/${name} served, but not the live UI it is supposed to serve")
  record "${name}" "FAIL_content"
  return 1
}

serve counter 8080
serve chat 8081 '/login?user=alice'
serve dashboard 8082

# --- verdict -----------------------------------------------------------------

printf '\n\033[1m--- in-container verdict\033[0m\n'
if [ "${#failures[@]}" -ne 0 ]; then
  echo "FAILED:"
  for name in "${failures[@]}"; do echo "  - ${name}"; done
  record "verdict" "FAIL"
  exit 1
fi
echo "all three examples served a live UI from a clean clone, with no node, npm, protoc or protoc-gen-liquidproto"
record "verdict" "PASS"
