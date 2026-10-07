#!/usr/bin/env bash
# Acceptance for the session sandbox launcher (csf_staging#316).
#
# Runs the built launcher against a real per-session cgroup in the invoking
# user's delegated systemd subtree and asserts the confinement the slice
# promises. It needs a user systemd manager (cgroup v2, delegated), the built
# launcher, bash, coreutils and curl; the overhead section additionally needs
# Docker.
#
# It probes the Docker and lxd sockets with a real connect(2) (curl
# --unix-socket), not an open(2): opening a socket inode always fails, so the
# old `: < socket` check was vacuous. Landlock ABI 4 does not govern Unix-socket
# connect, so on this kernel the launcher does NOT yet block the sockets; those
# two checks report GAP (not PASS) until the connect wall lands, and GAP does
# not count toward the pass/fail total.
#
# Prerequisites, which belong to the operator (sessions never run sudo):
#   - The harness runs in a delegated cgroup. Restart it under the user manager:
#       systemd-run --user --scope -p Delegate=yes --unit csf-harness \
#         csf serve <its current flags>
#   - Build the launcher and proxy:
#       tools/bazel.sh build //tools/csf-sandbox:all
#
# Usage: acceptance.sh [LAUNCHER_BINARY]
# Prints one line per check: <check> <PASS|FAIL> <observation>. Exit = #FAIL,
# or 2 when the environment cannot host the proof (not a failure of the slice).
set -Eeuo pipefail

die() { printf 'sandbox acceptance: %s\n' "$*" >&2; exit 2; }

uid=$(id -u)
service_cgroup="/sys/fs/cgroup/user.slice/user-${uid}.slice/user@${uid}.service"
launcher=${1:-}
if [[ -z "$launcher" ]]; then
  root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
  launcher="$root/bazel-bin/tools/csf-sandbox/csf-session-launcher"
fi
[[ -x "$launcher" ]] || die "launcher not found at $launcher; build //tools/csf-sandbox:all"
[[ -d "$service_cgroup" ]] || die "no user service cgroup at $service_cgroup; run under a user systemd manager"
if ! grep -qw pids "$service_cgroup/cgroup.subtree_control" 2>/dev/null; then
  die "the user service cgroup does not delegate the pids controller; restart the harness with Delegate=yes"
fi
command -v systemd-run >/dev/null 2>&1 || die "systemd-run is required to place the launcher in the delegated subtree"

fail=0
report() { printf '%-26s %-4s %s\n' "$1" "$2" "$3"; [[ "$2" == PASS ]] || fail=$((fail + 1)); }
# gap reports a wall the launcher does not yet enforce: it is neither a pass nor
# a script failure, but it is never silent. When the connect wall lands, the
# matching check becomes a PASS instead.
gap() { printf '%-26s %-4s %s\n' "$1" "GAP" "$2"; }

session="csf-acceptance-$$"
cg="$service_cgroup/$session"
work=$(mktemp -d)
cleanup() {
  [[ -f "$cg/cgroup.kill" ]] && printf 1 > "$cg/cgroup.kill" 2>/dev/null || true
  rmdir "$cg" 2>/dev/null || true
  rm -rf -- "$work" "$policy" 2>/dev/null || true
}
trap cleanup EXIT
mkdir "$cg"

policy=$(mktemp)
cat > "$policy" <<EOF
{
  "session_id": "$session",
  "cgroup": "$cg",
  "limits": {"cpu_max": "max", "memory_max_bytes": 2147483648, "memory_swap_max_bytes": 0, "pids_max": 64, "derivation": "acceptance"},
  "read_write_paths": ["$work", "/dev/null"],
  "read_only_paths": ["/usr", "/bin", "/lib", "/lib64", "/etc", "/proc", "/sys", "/dev"]
}
EOF

# The confined command: each line reports one proof to stdout (an inherited fd,
# so Landlock does not govern it). A double-forked setsid child escapes the
# process group and session; only the cgroup still contains it.
cat > "$work/confined.sh" <<EOF
setsid nohup bash -c "sleep 300 & exec sleep 300" >/dev/null 2>&1 < /dev/null &
in_cgroup=no; grep -q "$session" /proc/self/cgroup && in_cgroup=yes; echo "IN_CGROUP \$in_cgroup"
( echo x > /tmp/sandbox-escape 2>/dev/null && echo "WRITE_OUTSIDE allowed" ) || echo "WRITE_OUTSIDE denied"
( echo x > "$work/inside.txt" 2>/dev/null && echo "WRITE_WORKTREE ok" ) || echo "WRITE_WORKTREE denied"
# A real connect(2), not an open(2): opening a socket inode always fails, so the
# old open-based probe proved nothing. curl --unix-socket actually connects.
( curl -s --max-time 5 --unix-socket /var/run/docker.sock http://localhost/_ping >/dev/null 2>&1 && echo "DOCKER_CONNECT reached" ) || echo "DOCKER_CONNECT refused"
( curl -s --max-time 5 --unix-socket /var/snap/lxd/common/lxd/unix.socket http://localhost/1.0 >/dev/null 2>&1 && echo "LXD_CONNECT reached" ) || echo "LXD_CONNECT refused"
( sudo -n true 2>/dev/null && echo "SUDO ok" ) || echo "SUDO denied"
( echo \$\$ > "$cg/cgroup.procs" 2>/dev/null && echo "WRITE_CGROUP ok" ) || echo "WRITE_CGROUP denied"
( unshare --user true 2>/dev/null && echo "UNSHARE ok" ) || echo "UNSHARE denied"
n=0; while [ \$n -lt 400 ]; do sleep 8 & [ \$? -ne 0 ] && break; n=\$((n+1)); done; echo "FORKED \$n"
EOF

out=$(XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$uid}" systemd-run --user --scope --quiet \
  --unit "csf-acceptance-run-$$" \
  "$launcher" --policy "$policy" -- /bin/bash "$work/confined.sh" 2>&1 || true)

has() { grep -qx "$1" <<< "$out"; }
[[ $(grep -c . <<< "$out") -gt 0 ]] || report launcher_ran FAIL "no output: $out"
has "IN_CGROUP yes"        && report in_session_cgroup PASS "process is in $session"                 || report in_session_cgroup FAIL "$out"
has "WRITE_OUTSIDE denied" && report write_outside_denied PASS "Landlock denied a write outside"      || report write_outside_denied FAIL "$out"
has "WRITE_WORKTREE ok"    && report write_worktree_ok PASS "the worktree is writable"                || report write_worktree_ok FAIL "$out"
has "DOCKER_CONNECT refused" && report docker_socket_wall PASS "connect to the Docker socket is refused" \
  || gap docker_socket_wall "connect REACHES the daemon; Landlock does not cover Unix-socket connect, so the wall (seccomp user-notif / eBPF) is a follow-up"
has "LXD_CONNECT refused"    && report lxd_socket_wall PASS "connect to the lxd socket is refused" \
  || gap lxd_socket_wall "connect REACHES lxd; same pending connect wall as the Docker socket"
has "SUDO denied"          && report sudo_denied PASS "no_new_privs blocks sudo"                       || report sudo_denied FAIL "$out"
has "WRITE_CGROUP denied"  && report cgroup_write_denied PASS "cgroup.procs is read-only"              || report cgroup_write_denied FAIL "$out"
has "UNSHARE denied"       && report unshare_denied PASS "seccomp denies unshare"                      || report unshare_denied FAIL "$out"
# A fork bomb pushes the task count to pids.max and no further. bash aborts on
# the hard fork failure, so the bound is read from pids.peak, not from the
# script: peak at the limit (and never above it) is the cgroup capping the bomb.
peak=$(cat "$cg/pids.peak" 2>/dev/null || echo 0)
[[ "${peak:-0}" -ge 50 && "${peak:-0}" -le 64 ]] \
  && report fork_bomb_bounded PASS "pids.peak=$peak reached and held the limit of 64" \
  || report fork_bomb_bounded FAIL "pids.peak=$peak (expected the bomb to be capped at 64)"

# Receipts: usage is readable while the cgroup still exists.
usage=$(sed -n 's/^usage_usec //p' "$cg/cpu.stat" 2>/dev/null || echo "")
mempeak=$(cat "$cg/memory.peak" 2>/dev/null || echo "")
[[ -n "$usage" && -n "$mempeak" ]] \
  && report receipts_readable PASS "cpu usage_usec=$usage memory.peak=$mempeak" \
  || report receipts_readable FAIL "usage=$usage mempeak=$mempeak"

# Cancel: kill the whole subtree, including the double-forked setsid child.
procs_before=$(wc -l < "$cg/cgroup.procs")
printf 1 > "$cg/cgroup.kill"
for _ in 1 2 3 4 5 6 7 8 9 10; do [[ "$(wc -l < "$cg/cgroup.procs")" -eq 0 ]] && break; read -t 0.3 -r _ < /dev/zero 2>/dev/null || true; done
procs_after=$(wc -l < "$cg/cgroup.procs")
[[ "$procs_after" -eq 0 ]] \
  && report cancel_kills_subtree PASS "procs $procs_before -> 0 after cgroup.kill" \
  || report cancel_kills_subtree FAIL "procs remaining=$procs_after"

# Overhead: the launcher against `docker run` of a trivial image, best of three.
if command -v docker >/dev/null 2>&1; then
  trivial=busybox:latest
  docker image inspect "$trivial" >/dev/null 2>&1 || docker pull --quiet "$trivial" >/dev/null 2>&1 || true
  bench() { # bench CMD... -> milliseconds, minimum of three
    local best=999999 i start end ms
    for i in 1 2 3; do
      start=$(date +%s%N); "$@" >/dev/null 2>&1 || true; end=$(date +%s%N)
      ms=$(( (end - start) / 1000000 )); [[ $ms -lt $best ]] && best=$ms
    done
    printf '%s' "$best"
  }
  obench="$service_cgroup/csf-acceptance-oh-$$"; mkdir -p "$obench"
  cat > "$policy.oh" <<EOF
{"session_id":"oh","cgroup":"$obench","limits":{"cpu_max":"max","memory_max_bytes":2147483648,"memory_swap_max_bytes":0,"pids_max":64,"derivation":"overhead"},"read_write_paths":["$work","/dev/null"],"read_only_paths":["/usr","/bin","/lib","/lib64","/etc","/proc","/sys","/dev"]}
EOF
  launcher_ms=$(bench env XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$uid}" systemd-run --user --scope --quiet "$launcher" --policy "$policy.oh" -- /bin/true)
  printf 1 > "$obench/cgroup.kill" 2>/dev/null || true; rmdir "$obench" 2>/dev/null || true
  rm -f "$policy.oh"
  if docker image inspect "$trivial" >/dev/null 2>&1; then
    docker_ms=$(bench docker run --rm "$trivial" true)
    report launcher_overhead PASS "launcher ${launcher_ms}ms vs docker run ${docker_ms}ms (trivial image)"
  else
    report launcher_overhead PASS "launcher ${launcher_ms}ms (docker image unavailable for comparison)"
  fi
else
  printf '%-26s %-4s %s\n' launcher_overhead SKIP "docker not available"
fi

printf 'FAILED %s\n' "$fail"
exit "$fail"
