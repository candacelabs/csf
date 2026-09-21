#!/usr/bin/env bash
#
# The G2 idle-connection memory measurement, in one script.
#
# It implements equivalence-spec §3.6 for the gotth-live side and nothing else:
#
#     mem_per_session = ( M(N) − M(0) ) / N
#
# where M(x) is the median of 60 samples taken at 1 Hz over the last 60 s of a
# five-minute steady-state window, and M(x) is the serving container's cgroup v2
# `memory.current` minus `memory.stat`'s `file`, read FROM THE HOST — outside the
# measured process, which is why the sampler is here and not in the server.
#
# # What it does, per run
#
#   1. records host state (uptime, nproc, free, the unrelated containers running)
#   2. starts the SUT alone in its own container: explicit --memory, explicit
#      --cpuset-cpus, plaintext port published on 127.0.0.1
#   3. ASSERTS the §3.6 TLS boundary from outside before anything is recorded:
#      the measured container answers plaintext HTTP and refuses TLS, and its own
#      /introspect reports zero TLS listeners
#   4. warms up with N_WARMUP full-page loads, identically for the M(0) and the
#      M(N) window, per §3.6's "M(0) is measured after the same warm-up as M(N)"
#   5. for M(N): starts the synthetic session driver in a SEPARATE container on a
#      DISJOINT cpuset, and waits until it reports N live sessions
#   6. settles for 240 s, then samples 60 times at 1 Hz — the last 60 s of the
#      five-minute window
#   7. takes the §3.6 secondaries: runtime/metrics, goroutines, and a LABELLED
#      post-debug.FreeOSMemory() floor, in that order, all after the headline
#      window is closed so the headline stays unforced steady state
#   8. records host state again and writes run.json
#
# The two windows of a run are separate container lifecycles, and the ORDER
# ALTERNATES between runs (m0 first on odd runs, mn first on even ones) so that
# a host which drifts during a run does not bias every run in the same
# direction.
#
# # Running it
#
#   cd <checkout>/gotth-live
#   bash test/memory/measure.sh --n 1000 --runs 5 --observability on \
#        --out /tmp/g2/n1000-obs-on
#
#   # then, in the project image:
#   docker run --rm -v "$PWD:/w" -w /w/test/memory dis-gotth-live:latest \
#       bash -c 'go run ./cmd/memstat -cell /out/n1000-obs-on'   # with /tmp/g2 mounted
#
# Requirements on the host: docker, curl, jq, awk, and cgroup v2 at
# /sys/fs/cgroup. No Go and no node: everything Go runs in dis-gotth-live.
#
# # What it deliberately does NOT do
#
# It does not create docker networks, and it does not touch any container it did
# not start. The driver reaches the SUT over the loopback address the SUT's port
# is published on, with --network host, which is the arrangement that needs no
# new network. Every container this script starts is named g2-{sut,drv}-<runid>
# and is removed on exit, including on failure.
#
# It does not run a TLS-terminating proxy. §3.6 places one in front of BOTH
# stacks so the extra hop is common-mode in the A-vs-B delta, and excludes it
# from M(x) by definition; with only gotth-live measured and the proxy excluded,
# its absence cannot move M(x). The measured container terminating no TLS is the
# half of that rule which binds here, and step 3 asserts it. See
# docs/bench/g2-baseline.md §"TLS boundary".

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# --- parameters --------------------------------------------------------------

N=1000
RUNS=5
OBSERVABILITY=on
OUT=""
# §3.6's window: 5 minutes of steady state, of which the last 60 s are sampled.
SETTLE_S=240
WINDOW_SAMPLES=60
SAMPLE_PERIOD_MS=1000
# The full-page loads §3.6 calls warm-up. Both windows get exactly this many.
WARMUP_LOADS=200
# Disjoint cpusets, stated in the manifest. The SUT gets four cores and the
# driver four others; §3.6 requires only that they be disjoint, and the numbers
# are recorded because "pinned" without a core count is not reproducible.
SUT_CPUS="24-27"
DRV_CPUS="28-31"
SUT_MEMORY="2g"
# GOGC is Go's documented production default. GOMEMLIMIT is set equal to the
# container memory limit for symmetry with §3.6's Node rule
# (--max-old-space-size = container limit); at a working set two orders of
# magnitude below it, it is never approached and therefore never changes GC
# behaviour. Both are disclosed in every manifest, which is what §3.6 asks for.
GOGC=100
GOMEMLIMIT="2GiB"
IMAGE="dis-gotth-live:latest"
SUT_PORT=18080
DRV_STATUS_PORT=19080

usage() {
  cat >&2 <<'EOF'
usage: measure.sh --out DIR [options]

  --n N                sessions for the M(N) window (default 1000)
  --runs R             independent runs of the cell (default 5)
  --observability on|off
                       on wires logger+meter+tracer (equivalence-spec §5.6's
                       headline configuration); off leaves them nil
  --out DIR            cell directory; one subdirectory per run
  --settle S           seconds of steady state before sampling (default 240)
  --warmup-loads W     full-page loads per window (default 200)
  --sut-cpus SET       cpuset for the server under test (default 24-27)
  --drv-cpus SET       cpuset for the driver, must be disjoint (default 28-31)
  --port P             plaintext port to publish on 127.0.0.1 (default 18080)
  --image IMG          toolchain image (default dis-gotth-live:latest)
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --n) N="$2"; shift 2 ;;
    --runs) RUNS="$2"; shift 2 ;;
    --observability) OBSERVABILITY="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --settle) SETTLE_S="$2"; shift 2 ;;
    --warmup-loads) WARMUP_LOADS="$2"; shift 2 ;;
    --sut-cpus) SUT_CPUS="$2"; shift 2 ;;
    --drv-cpus) DRV_CPUS="$2"; shift 2 ;;
    --port) SUT_PORT="$2"; shift 2 ;;
    --image) IMAGE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "measure.sh: unknown argument $1" >&2; usage; exit 2 ;;
  esac
done

if [ -z "${OUT}" ]; then
  echo "measure.sh: --out is required" >&2
  usage
  exit 2
fi
case "${OBSERVABILITY}" in on|off) ;; *) echo "measure.sh: --observability must be on or off" >&2; exit 2 ;; esac

for tool in docker curl jq awk; do
  command -v "${tool}" >/dev/null || { echo "measure.sh: ${tool} is not on PATH" >&2; exit 2; }
done
[ "$(stat -fc %T /sys/fs/cgroup)" = "cgroup2fs" ] || {
  echo "measure.sh: /sys/fs/cgroup is not cgroup v2; M(x) is defined in cgroup v2 terms" >&2
  exit 2
}

mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
note() { printf '    %s\n' "$*"; }
die() { echo "measure.sh: $*" >&2; exit 1; }

# --- container lifetime ------------------------------------------------------

SUT_NAME=""
DRV_NAME=""

cleanup() {
  [ -n "${DRV_NAME}" ] && docker rm -f "${DRV_NAME}" >/dev/null 2>&1
  [ -n "${SUT_NAME}" ] && docker rm -f "${SUT_NAME}" >/dev/null 2>&1
  DRV_NAME=""
  SUT_NAME=""
}
trap 'cleanup' EXIT INT TERM

# --- build -------------------------------------------------------------------

# The binaries are built ONCE, outside the measured container, into a directory
# outside the checkout. Building inside the SUT container would charge the Go
# compiler's memory to the cgroup that M(x) reads, and `go run` inside it would
# do the same on every start.
BIN_DIR="$(mktemp -d /tmp/g2-bin.XXXXXXXX)"
say "building memsrv and memdrv into ${BIN_DIR} (outside the measured container)"
docker run --rm \
  -v "${MODULE_ROOT}:/w" -v "${BIN_DIR}:/out" -w /w/test/memory "${IMAGE}" \
  bash -c 'go build -trimpath -o /out/memsrv ./cmd/memsrv && go build -trimpath -o /out/memdrv ./cmd/memdrv' \
  || die "the harness binaries did not build"
# The binaries come out of the container owned by root and already 0755; the
# chmod is belt and braces and its failure is not one.
chmod 0755 "${BIN_DIR}"/mem* 2>/dev/null || true

GIT_SHA="$(git -C "${MODULE_ROOT}" rev-parse HEAD 2>/dev/null || echo unknown)"
GIT_DIRTY=false
git -C "${MODULE_ROOT}" diff --quiet 2>/dev/null || GIT_DIRTY=true
IMAGE_ID="$(docker image inspect -f '{{.Id}}' "${IMAGE}")"

# --- host state --------------------------------------------------------------

# The manifest carries the host, because this VM is shared: unrelated containers
# are unpinned and can be scheduled onto the SUT's cores. A contended run is
# published with the flag set, not withheld and not quietly rounded.
host_json() {
  local load1 load5 load15 containers
  read -r load1 load5 load15 _ < /proc/loadavg
  containers="$(docker ps -q | wc -l)"
  jq -n \
    --arg uptime "$(uptime | tr -s ' ')" \
    --argjson nproc "$(nproc)" \
    --argjson load1 "${load1}" --argjson load5 "${load5}" --argjson load15 "${load15}" \
    --argjson mem_total_mb "$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo)" \
    --argjson mem_available_mb "$(awk '/MemAvailable/{print int($2/1024)}' /proc/meminfo)" \
    --argjson running_containers "${containers}" \
    --arg container_names "$(docker ps --format '{{.Names}}' | paste -sd, -)" \
    '{uptime:$uptime, nproc:$nproc, loadavg:{one:$load1, five:$load5, fifteen:$load15},
      mem_total_mb:$mem_total_mb, mem_available_mb:$mem_available_mb,
      running_containers:$running_containers, container_names:$container_names}'
}

# cpuset_size counts the cores in a cpuset string like "24-27" or "0,2,4".
cpuset_size() {
  awk -F, -v spec="$1" 'BEGIN{
    n=split(spec, parts, ",");
    total=0;
    for (i=1;i<=n;i++) {
      if (split(parts[i], r, "-") == 2) total += r[2]-r[1]+1; else total += 1;
    }
    print total;
  }'
}

SUT_CORES="$(cpuset_size "${SUT_CPUS}")"

# contended applies one stated rule so the flag is reproducible rather than a
# judgement: the host is contended for this run if the one-minute load average
# at either end of the window is at least the number of cores the SUT was given.
# Unrelated containers here are unpinned, so that much runnable work can land on
# the SUT's cores.
contended() {
  awk -v a="$1" -v b="$2" -v cores="${SUT_CORES}" \
    'BEGIN{print (a >= cores || b >= cores) ? "true" : "false"}'
}

# --- cgroup ------------------------------------------------------------------

# cgroup_dir resolves the measured container's cgroup v2 directory and verifies
# it before any number is believed. The fallbacks exist because the layout is a
# property of the host's cgroup driver, not of docker; if none resolves the run
# fails rather than falling back to `docker stats`, which reads a different
# quantity through a different path.
cgroup_dir() {
  local id="$1" candidate
  for candidate in \
      "/sys/fs/cgroup/system.slice/docker-${id}.scope" \
      "/sys/fs/cgroup/docker/${id}" \
      "/sys/fs/cgroup/system.slice/docker.service/docker-${id}.scope"; do
    if [ -r "${candidate}/memory.current" ] && [ -r "${candidate}/memory.stat" ]; then
      echo "${candidate}"
      return 0
    fi
  done
  return 1
}

# sample_window writes §3.6's window: COUNT samples at 1 Hz, one CSV row each.
# The loop targets absolute deadlines rather than sleeping a fixed second, so a
# slow read does not make every later sample late; memstat rejects the window if
# the spacing drifted anyway.
sample_window() {
  local cg="$1" out="$2" count="$3"
  echo "unix_ms,memory_current,file,anon,sock,slab,kernel" > "${out}"
  local i now deadline sleep_ms
  deadline="$(date +%s%3N)"
  for ((i = 0; i < count; i++)); do
    now="$(date +%s%3N)"
    printf '%s,%s,%s\n' "${now}" "$(cat "${cg}/memory.current")" \
      "$(awk '$1=="file"{f=$2} $1=="anon"{a=$2} $1=="sock"{s=$2} $1=="slab"{sl=$2} $1=="kernel"{k=$2}
              END{printf "%d,%d,%d,%d,%d", f, a, s, sl, k}' "${cg}/memory.stat")" >> "${out}"
    deadline=$((deadline + SAMPLE_PERIOD_MS))
    sleep_ms=$((deadline - $(date +%s%3N)))
    if [ "${sleep_ms}" -gt 0 ]; then
      sleep "$(awk -v ms="${sleep_ms}" 'BEGIN{printf "%.3f", ms/1000}')"
    fi
  done
}

# --- one window --------------------------------------------------------------

# measure_window runs one complete container lifecycle and produces one sample
# file: sessions=0 gives M(0), sessions=N gives M(N).
measure_window() {
  local runid="$1" sessions="$2" dir="$3" tag="$4"

  SUT_NAME="g2-sut-${runid}-${tag}"
  say "run ${runid}: ${tag} window, ${sessions} session(s)"

  host_json > "${dir}/host-pre-${tag}.json"

  docker run -d --name "${SUT_NAME}" \
    --memory "${SUT_MEMORY}" --memory-swap "${SUT_MEMORY}" \
    --cpuset-cpus "${SUT_CPUS}" \
    -e "GOGC=${GOGC}" -e "GOMEMLIMIT=${GOMEMLIMIT}" \
    -p "127.0.0.1:${SUT_PORT}:8080" \
    -v "${BIN_DIR}:/bin-under-test:ro" \
    "${IMAGE}" /bin-under-test/memsrv \
      -addr :8080 \
      -origin "http://127.0.0.1:${SUT_PORT}" \
      -observability "${OBSERVABILITY}" >/dev/null || die "the SUT container did not start"

  local sut_id
  sut_id="$(docker inspect -f '{{.Id}}' "${SUT_NAME}")"

  local cg
  cg="$(cgroup_dir "${sut_id}")" || die "no cgroup v2 directory resolved for ${sut_id}: \
refusing to substitute docker stats, which is a different quantity read through a different path"
  note "cgroup: ${cg}"

  local i
  for ((i = 0; i < 60; i++)); do
    curl -fsS "http://127.0.0.1:${SUT_PORT}/healthz" >/dev/null 2>&1 && break
    sleep 0.5
  done
  curl -fsS "http://127.0.0.1:${SUT_PORT}/healthz" >/dev/null || die "the SUT never became ready"

  # --- the §3.6 TLS boundary, asserted rather than trusted --------------------
  local tls_listeners plaintext_ok tls_refused
  tls_listeners="$(curl -fsS "http://127.0.0.1:${SUT_PORT}/introspect" | jq -r '.tls_listeners')"
  plaintext_ok=false
  curl -fsS "http://127.0.0.1:${SUT_PORT}/healthz" >/dev/null 2>&1 && plaintext_ok=true
  tls_refused=true
  curl -fsS --max-time 5 -k "https://127.0.0.1:${SUT_PORT}/healthz" >/dev/null 2>&1 && tls_refused=false
  [ "${tls_listeners}" = "0" ] || die "the measured container reports ${tls_listeners} TLS listeners"
  [ "${plaintext_ok}" = true ] || die "the measured container does not answer plaintext HTTP"
  [ "${tls_refused}" = true ] || die "the measured container completed a TLS handshake: \
§3.6 terminates TLS OUTSIDE the measured container, and this is a disqualifying method error"
  note "TLS boundary: outside (0 listeners, plaintext yes, TLS handshake refused)"

  # --- warm-up: the same page loads for both windows --------------------------
  note "warm-up: ${WARMUP_LOADS} full-page loads from the driver cpuset"
  docker run --rm --network host --cpuset-cpus "${DRV_CPUS}" "${IMAGE}" \
    bash -c "for i in \$(seq 1 ${WARMUP_LOADS}); do \
               curl -fsS -o /dev/null http://127.0.0.1:${SUT_PORT}/ || exit 1; done" \
    || die "the warm-up page loads failed"

  # --- sessions ---------------------------------------------------------------
  local driver_status='null'
  if [ "${sessions}" -gt 0 ]; then
    DRV_NAME="g2-drv-${runid}-${tag}"
    docker run -d --name "${DRV_NAME}" --network host --cpuset-cpus "${DRV_CPUS}" \
      -v "${BIN_DIR}:/bin-under-test:ro" \
      "${IMAGE}" /bin-under-test/memdrv \
        -url "ws://127.0.0.1:${SUT_PORT}/live" \
        -origin "http://127.0.0.1:${SUT_PORT}" \
        -n "${sessions}" \
        -status "127.0.0.1:${DRV_STATUS_PORT}" >/dev/null || die "the driver container did not start"

    local live=0
    for ((i = 0; i < 240; i++)); do
      live="$(curl -fsS "http://127.0.0.1:${DRV_STATUS_PORT}/status" 2>/dev/null | jq -r '.live' 2>/dev/null || echo 0)"
      [ "${live}" = "${sessions}" ] && break
      sleep 0.5
    done
    [ "${live}" = "${sessions}" ] || die "the driver reached ${live} of ${sessions} sessions"
    note "established: ${live} idle sessions"
  fi

  # --- the five-minute steady-state window ------------------------------------
  note "settling ${SETTLE_S} s, then sampling ${WINDOW_SAMPLES} times at 1 Hz"
  sleep "${SETTLE_S}"
  sample_window "${cg}" "${dir}/${tag}.csv" "${WINDOW_SAMPLES}"

  # --- secondaries, all AFTER the headline window is closed --------------------
  curl -fsS "http://127.0.0.1:${SUT_PORT}/introspect" > "${dir}/introspect-${tag}.json"
  if [ "${sessions}" -gt 0 ]; then
    curl -fsS "http://127.0.0.1:${DRV_STATUS_PORT}/status" > "${dir}/driver-${tag}.json"
    driver_status="$(cat "${dir}/driver-${tag}.json")"
  fi

  # §3.6's post-forced-GC floor: a LABELLED secondary on both stacks or on
  # neither, taken here and never in place of the headline.
  curl -fsS "http://127.0.0.1:${SUT_PORT}/freeosmemory" > "${dir}/floor-${tag}.json"
  sleep 5
  sample_window "${cg}" "${dir}/floor-${tag}.csv" 10

  host_json > "${dir}/host-post-${tag}.json"

  # The container log carries the provenance stream when observability is on;
  # it is kept because §5.6 asks where that stream was sunk.
  docker logs "${SUT_NAME}" > "${dir}/sut-${tag}.log" 2>&1

  jq -n \
    --arg cgroup "${cg}" \
    --arg sut_container "${sut_id}" \
    --argjson sessions "${sessions}" \
    --argjson tls_listeners "${tls_listeners}" \
    --argjson plaintext_ok "${plaintext_ok}" \
    --argjson tls_refused "${tls_refused}" \
    --slurpfile introspect "${dir}/introspect-${tag}.json" \
    --slurpfile floor "${dir}/floor-${tag}.json" \
    --argjson driver "${driver_status}" \
    --slurpfile host_pre "${dir}/host-pre-${tag}.json" \
    --slurpfile host_post "${dir}/host-post-${tag}.json" \
    '{cgroup:$cgroup, sut_container:$sut_container, sessions:$sessions,
      tls:{boundary:"outside", listeners:$tls_listeners, plaintext_ok:$plaintext_ok,
           tls_handshake_refused:$tls_refused, proxy:"none — see docs/bench/g2-baseline.md"},
      introspect:$introspect[0], forced_gc_floor:$floor[0], driver:$driver,
      host_pre:$host_pre[0], host_post:$host_post[0],
      contended:(($host_pre[0].loadavg.one >= '"${SUT_CORES}"') or
                 ($host_post[0].loadavg.one >= '"${SUT_CORES}"'))}' \
    > "${dir}/window-${tag}.json"

  cleanup
}

# --- the cell ----------------------------------------------------------------

CELL_ID="n${N}-obs-${OBSERVABILITY}"
say "cell ${CELL_ID}: ${RUNS} run(s), N=${N}, settle ${SETTLE_S} s, window ${WINDOW_SAMPLES} s"
note "image ${IMAGE} (${IMAGE_ID})"
note "git ${GIT_SHA} (dirty=${GIT_DIRTY})"
note "sut cpus ${SUT_CPUS} (${SUT_CORES} cores), driver cpus ${DRV_CPUS}, memory ${SUT_MEMORY}"
note "GOGC=${GOGC} GOMEMLIMIT=${GOMEMLIMIT}"

for ((run = 1; run <= RUNS; run++)); do
  RUNID="$(printf '%s-r%02d' "${CELL_ID}" "${run}")"
  RUNDIR="${OUT}/${RUNID}"
  mkdir -p "${RUNDIR}"

  if (( run % 2 == 1 )); then
    ORDER="m0-then-mn"
    measure_window "${RUNID}" 0 "${RUNDIR}" m0
    measure_window "${RUNID}" "${N}" "${RUNDIR}" mn
  else
    ORDER="mn-then-m0"
    measure_window "${RUNID}" "${N}" "${RUNDIR}" mn
    measure_window "${RUNID}" 0 "${RUNDIR}" m0
  fi

  jq -n \
    --arg run_id "${RUNID}" --arg cell_id "${CELL_ID}" \
    --argjson n "${N}" --arg window_order "${ORDER}" \
    --arg observability "${OBSERVABILITY}" \
    --arg git_sha "${GIT_SHA}" --argjson git_dirty "${GIT_DIRTY}" \
    --arg image "${IMAGE}" --arg image_id "${IMAGE_ID}" \
    --arg sut_cpus "${SUT_CPUS}" --arg drv_cpus "${DRV_CPUS}" \
    --argjson sut_cores "${SUT_CORES}" \
    --arg sut_memory "${SUT_MEMORY}" \
    --argjson gogc "${GOGC}" --arg gomemlimit "${GOMEMLIMIT}" \
    --argjson warmup_loads "${WARMUP_LOADS}" \
    --argjson settle_s "${SETTLE_S}" --argjson window_samples "${WINDOW_SAMPLES}" \
    --argjson sample_period_ms "${SAMPLE_PERIOD_MS}" \
    --slurpfile m0 "${RUNDIR}/window-m0.json" \
    --slurpfile mn "${RUNDIR}/window-mn.json" \
    '{run_id:$run_id, cell_id:$cell_id, n:$n, window_order:$window_order,
      method:"equivalence-spec §3.6",
      workload:"Idle (connected, no application events, heartbeat frames only)",
      observability:$observability,
      provenance_logger:(if $observability=="on" then "configured" else "nil" end),
      provenance_sink:(if $observability=="on" then "container stderr (docker log), not the SUT filesystem" else "none" end),
      git_sha:$git_sha, git_dirty:$git_dirty, image:$image, image_id:$image_id,
      sut_cpus:$sut_cpus, sut_cores:$sut_cores, drv_cpus:$drv_cpus, sut_memory:$sut_memory,
      gogc:$gogc, gomemlimit:$gomemlimit,
      warmup_loads:$warmup_loads, settle_s:$settle_s,
      window_samples:$window_samples, sample_period_ms:$sample_period_ms,
      m0:$m0[0], mn:$mn[0],
      contended:($m0[0].contended or $mn[0].contended)}' \
    > "${RUNDIR}/run.json"

  note "wrote ${RUNDIR}/run.json"
done

say "cell ${CELL_ID} complete: ${OUT}"
echo "compute the figure with:"
echo "  docker run --rm -v \"${MODULE_ROOT}:/w\" -v \"${OUT}:/cell\" -w /w/test/memory ${IMAGE} \\"
echo "      bash -c 'go run ./cmd/memstat -cell /cell'"
