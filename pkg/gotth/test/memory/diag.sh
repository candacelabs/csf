#!/usr/bin/env bash
#
# The G2 remediation diagnostic. NOT a measurement, and it produces no number
# that may be quoted as a baseline.
#
# measure.sh implements equivalence-spec §3.6 and answers "how much". This
# answers "where", which is what RFC §6.1.2 asks for before a remedy: it reads
# the SUT's own runtime/metrics through /introspect at a small session count,
# across configurations that isolate one signal at a time, and reports the
# goroutine-stack and heap classes per session.
#
# Nothing here is sampled over a five-minute window, nothing here is read from
# the cgroup, and nothing here goes into a run manifest. It is a mechanism
# probe, and every cell it prints is labelled as one.
#
#   bash test/memory/diag.sh --n 200
#
# The cells:
#
#   off      all three hooks nil            — RFC §6.2's implicit configuration
#   logger   slog JSON handler only
#   metrics  OTel SDK meter only
#   tracer   OTel SDK tracer only
#   on       all three                      — equivalence-spec §5.6's headline
#   on-quiet all three, driver sends NOTHING — isolates the read pump's share of
#            the goroutine-stack line from the actor's, because with no inbound
#            frame the read pump never runs its instrumented parse path
#
# Requirements: docker and the toolchain image. No Go on the host.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

N=200
IMAGE="dis-gotth-live:latest"
HOLD_S=25
OUT=""
CELLS="off logger metrics tracer on on-quiet on-probe"
MEMPROFILERATE=""

usage() {
  cat >&2 <<'EOF'
usage: diag.sh [--n N] [--hold S] [--image IMG] [--out DIR]

  --n N       sessions per cell (default 200)
  --hold S    seconds to hold the sessions before reading (default 25; must
              stay under HeartbeatTimeout for the on-quiet cell)
  --out DIR   write the raw /introspect and /stackprobe JSON here
  --cells "a b"        subset of: off logger metrics tracer on on-quiet on-probe
  --memprofilerate N   runtime.MemProfileRate for the heap profile (diagnostic)
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --n) N="$2"; shift 2 ;;
    --hold) HOLD_S="$2"; shift 2 ;;
    --image) IMAGE="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --cells) CELLS="$2"; shift 2 ;;
    --memprofilerate) MEMPROFILERATE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "diag.sh: unknown argument $1" >&2; usage; exit 2 ;;
  esac
done

if [ -z "${OUT}" ]; then
  OUT="$(mktemp -d /tmp/g2-diag.XXXXXX)"
fi
mkdir -p "${OUT}"

echo "diag.sh: n=${N} hold=${HOLD_S}s out=${OUT}"
echo "diag.sh: host at start: $(cat /proc/loadavg)"

# Everything runs inside one container: no cgroup reading, no pinning, no
# published ports. The two processes talk over the container's own loopback.
docker run --rm \
  -v "${MODULE_ROOT}:/w:ro" \
  -v "${OUT}:/out" \
  -v /tmp/gotth-gomod:/go/pkg/mod \
  -e "N=${N}" -e "HOLD_S=${HOLD_S}" -e "CELLS=${CELLS}" \
  -e "MEMPROFILERATE=${MEMPROFILERATE}" \
  -w /w/test/memory "${IMAGE}" bash -c '
set -uo pipefail
export GOGC=100 GOMEMLIMIT=2GiB
go build -trimpath -o /tmp/memsrv ./cmd/memsrv || exit 1
go build -trimpath -o /tmp/memdrv ./cmd/memdrv || exit 1

cell() {
  obs="$1"; echoflag="$2"; probe="$3"; label="$4"
  rate=""
  [ -n "${MEMPROFILERATE}" ] && rate="-memprofilerate ${MEMPROFILERATE}"
  /tmp/memsrv -addr 127.0.0.1:18080 -origin http://127.0.0.1:18080 \
      -observability "${obs}" ${probe} ${rate} >/tmp/srv.log 2>/tmp/srv.err &
  srv=$!
  for _ in $(seq 1 50); do
    curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break
    sleep 0.2
  done
  # The same warm-up shape measure.sh uses, at a tenth the count: this is a
  # mechanism probe, not a window.
  for _ in $(seq 1 20); do curl -fsS http://127.0.0.1:18080/ >/dev/null 2>&1; done
  # The zero-session reading, after the same warm-up: the subtrahend of every
  # per-session figure below.
  curl -fsS http://127.0.0.1:18080/introspect > "/out/introspect-pre-${label}.json" 2>/dev/null
  curl -fsS http://127.0.0.1:18080/heapprofile > "/out/heap-pre-${label}.pb.gz" 2>/dev/null

  /tmp/memdrv -url ws://127.0.0.1:18080/live -origin http://127.0.0.1:18080 \
      -n "${N}" -status 127.0.0.1:19080 -echo="${echoflag}" >/tmp/drv.log 2>&1 &
  drv=$!
  live=0
  for _ in $(seq 1 120); do
    live=$(curl -fsS http://127.0.0.1:19080/status 2>/dev/null | grep -o "\"live\": *[0-9]*" | grep -o "[0-9]*$")
    [ "${live:-0}" -ge "${N}" ] && break
    sleep 0.5
  done
  sleep "${HOLD_S}"

  curl -fsS http://127.0.0.1:18080/introspect > "/out/introspect-${label}.json" 2>/dev/null
  curl -fsS http://127.0.0.1:19080/status    > "/out/status-${label}.json" 2>/dev/null
  if [ -n "${probe}" ]; then
    curl -fsS http://127.0.0.1:18080/stackprobe > "/out/stackprobe-${label}.json" 2>/dev/null
  fi
  # The per-component heap profile RFC §6.3 asks for. Taken BEFORE the forced
  # floor so the two readings are independent.
  curl -fsS http://127.0.0.1:18080/heapprofile > "/out/heap-${label}.pb.gz" 2>/dev/null
  curl -fsS http://127.0.0.1:18080/freeosmemory > "/out/floor-${label}.json" 2>/dev/null

  kill "${drv}" 2>/dev/null; wait "${drv}" 2>/dev/null
  kill "${srv}" 2>/dev/null; wait "${srv}" 2>/dev/null
  sleep 1
}

for want in ${CELLS}; do
  case "${want}" in
    off)      cell off     true  ""       off ;;
    logger)   cell logger  true  ""       logger ;;
    metrics)  cell metrics true  ""       metrics ;;
    tracer)   cell tracer  true  ""       tracer ;;
    on)       cell on      true  ""       on ;;
    on-quiet) cell on      false ""       on-quiet ;;
    on-probe) cell on      true  "-probe" on-probe ;;
    *) echo "diag.sh: unknown cell ${want}" >&2; exit 2 ;;
  esac
done
'

echo
echo "diag.sh: host at end: $(cat /proc/loadavg)"
echo
docker run --rm -v "${MODULE_ROOT}:/w:ro" -v "${OUT}:/cell:ro" \
  -v /tmp/gotth-gomod:/go/pkg/mod \
  -w /w/test/memory "${IMAGE}" \
  bash -c 'go run ./cmd/memdiag -dir /cell'
echo
echo "diag.sh: raw JSON in ${OUT}"
