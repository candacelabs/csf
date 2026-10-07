#!/usr/bin/env bash
# Executable checks for the lemmas behind G (cancel bounds session lifetime).
# Prints one line per check: <check> <PASS|FAIL> <observation>. Exit = #FAIL.
set -uo pipefail
unit="csf-proof-$$"
cg="/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/$unit.scope"
label="csf.session=proof-$$"
fail=0
report() { printf '%s %s %s\n' "$1" "$2" "$3"; [[ "$2" == PASS ]] || fail=$((fail + 1)); }

# A session leader whose child escapes with setsid + nohup + double fork.
systemd-run --user --scope --quiet --unit="$unit" \
  bash -c 'setsid nohup bash -c "sleep 600 & exec sleep 600" >/dev/null 2>&1 < /dev/null & exec sleep 600' &
runner=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do [[ -s "$cg/cgroup.procs" ]] && [[ $(wc -l < "$cg/cgroup.procs") -ge 3 ]] && break; read -t 0.3 -r _ < /dev/zero 2>/dev/null || true; done

members=$(cat "$cg/cgroup.procs")
procs=$(printf '%s\n' $members | wc -l)
sids=$(for p in $(cat "$cg/cgroup.procs"); do ps -o sid= -p "$p"; done | sort -u | wc -l)
# L1: fork/clone inherits the cgroup; setsid/nohup/double fork do not leave it.
[[ $procs -ge 3 && $sids -ge 2 ]] && report L1 PASS "procs=$procs distinct_sessions=$sids all in $unit.scope" \
                                  || report L1 FAIL "procs=$procs distinct_sessions=$sids"

# L3 setup: a labelled container, started from inside the session.
cid=$(docker run -d --rm --label "$label" golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 sleep 600)
cpid=$(docker inspect -f '{{.State.Pid}}' "$cid")
ccg=$(cut -d: -f3 "/proc/$cpid/cgroup")
# C3 (dockerd path): the container is NOT in the session's cgroup.
[[ "$ccg" != *"$unit"* ]] && report C3-observed PASS "container cgroup=$ccg (outside $unit.scope)" \
                         || report C3-observed FAIL "container in session cgroup"

# L2: cgroup.kill is transitive over the subtree.
printf 1 > "$cg/cgroup.kill"
wait "$runner" 2>/dev/null
# running = /proc/<pid> exists and its state is not Z (a zombie has exited
# and only awaits reaping, per the cgroup v2 doc's own clause). Bounded by
# beta = 3 s, checked every 0.3 s.
running() {
  local count=0 state
  for p in $members; do
    state=$(awk '{print $3}' "/proc/$p/stat" 2>/dev/null) || continue
    [[ -n "$state" && "$state" != Z ]] && count=$((count + 1))
  done
  printf '%s' "$count"
}
for _ in 1 2 3 4 5 6 7 8 9 10; do [[ $(running) -eq 0 ]] && break; read -t 0.3 -r _ < /dev/zero 2>/dev/null || true; done
left=$(running)
[[ $left -eq 0 ]] && report L2 PASS "recorded pids running after cgroup.kill=0 of $procs within beta=3s" || report L2 FAIL "recorded pids running=$left"
alive=$(docker ps -q --filter "label=$label" | wc -l)
[[ $alive -eq 1 ]] && report C3-survives-kill PASS "labelled container still running after cgroup.kill" \
                   || report C3-survives-kill FAIL "alive=$alive"

# L3: the label reaches the container; removal by label ends it.
docker ps -q --filter "label=$label" | xargs -r docker rm -f > /dev/null
alive=$(docker ps -q --filter "label=$label" | wc -l)
[[ $alive -eq 0 ]] && report L3 PASS "containers with $label after rm -f by label=0" || report L3 FAIL "alive=$alive"

printf 'FAILED %s\n' "$fail"
exit "$fail"
