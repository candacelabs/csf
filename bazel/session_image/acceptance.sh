#!/usr/bin/env bash
# Acceptance for container sessions: start a real session in a container on
# this host through csf serve, then verify the container's configuration, the
# work a session does from inside it, and that cancel leaves nothing behind.
#
# It runs a second csf serve, built from this checkout, on its own state
# directory and port, so the host's running harness is untouched. The session
# runs a cheap model on a one-line task; the work checks run inside its
# container with docker exec, as the session's own user. The draft pull request
# it opens is closed and its branch deleted at the end.
#
# Usage: bazel/session_image/acceptance.sh REPOSITORY CSF_BINARY [TAILNET_ADDRESS]
#   REPOSITORY       a clone of this repository whose origin the session pushes to
#   CSF_BINARY       csf built from this checkout (go build ./app/harness/cmd)
#   TAILNET_ADDRESS  host:port answering HTTP on the tailnet; default: this
#                    host's `tailscale ip -4` at port 14120
set -Eeuo pipefail

die() {
  printf 'session acceptance: %s\n' "$*" >&2
  exit 1
}

pass=0
fail=0
check() {
  local name=$1 outcome=$2 detail=${3:-}
  if [[ "$outcome" == ok ]]; then
    pass=$((pass + 1))
    printf 'PASS  %s  %s\n' "$name" "$detail"
  else
    fail=$((fail + 1))
    printf 'FAIL  %s  %s\n' "$name" "$detail"
  fi
}

[[ $# -ge 2 ]] || die 'usage: acceptance.sh REPOSITORY CSF_BINARY [TAILNET_ADDRESS]'
repository=$(cd -- "$1" && pwd -P)
csf=$(cd -- "$(dirname -- "$2")" && pwd -P)/$(basename -- "$2")
tailnet=${3:-$(tailscale ip -4 | head -n 1):14120}
module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
image=$(<"$module_root/bazel/session_image.txt")
docker image inspect "$image" >/dev/null || die "session image $image is not built; run bazel/session_image/build.sh"
limits_source="$module_root/services/harness/session/agent_session_runner_container.go"
# The session branches from this checkout's commit, so it runs this checkout's
# scripts and the image this checkout pins.
base=$(git -C "$module_root" rev-parse HEAD)
git -C "$repository" cat-file -e "$base^{commit}" || die "commit $base is not in $repository; fetch it there first"

state=$(mktemp -d "${TMPDIR:-/tmp}/csf-session-acceptance.XXXXXX")
port=$((14200 + RANDOM % 500))
endpoint="http://127.0.0.1:$port"
assignment=$(</proc/sys/kernel/random/uuid)
branch="acceptance/session-container-${assignment:0:8}"
run_directory="$state/$assignment"
worktree="$run_directory/worktree"
serve_pid=
pull_request=

cleanup() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    printf '\n--- csf serve log (last 20 lines) ---\n' >&2
    tail -n 20 -- "$state/serve.log" >&2 2>/dev/null || true
    printf -- '--- run events (last 10, truncated) ---\n' >&2
    tail -n 10 -- "$run_directory/events.jsonl" 2>/dev/null | cut -c1-400 >&2 || true
  fi
  if [[ -n "$pull_request" ]]; then
    gh pr close "$pull_request" --delete-branch >/dev/null 2>&1 || true
  fi
  git -C "$repository" push origin --delete "$branch" >/dev/null 2>&1 || true
  if [[ -n "$serve_pid" ]]; then
    kill "$serve_pid" 2>/dev/null || true
    wait "$serve_pid" 2>/dev/null || true
  fi
  git -C "$repository" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  git -C "$repository" branch -D "$branch" >/dev/null 2>&1 || true
  chmod -R u+w -- "$state" 2>/dev/null || true
  rm -rf -- "$state"
}
trap cleanup EXIT

# wait_for runs its arguments until they succeed, up to the given seconds.
wait_for() {
  local seconds=$1
  shift
  local deadline=$((SECONDS + seconds))
  until "$@" >/dev/null 2>&1; do
    ((SECONDS < deadline)) || return 1
    sleep 0.2
  done
}

session_container() { docker ps -q --no-trunc --filter "label=csf.assignment=$assignment"; }
has_container() { [[ -n "$(session_container)" ]]; }
container_gone() { [[ -z "$(docker ps -aq --filter "label=csf.assignment=$assignment")" ]]; }
turn_finished() { grep -q '"event_type":"harness_run_finished"\|"msg":"turn finished"' "$run_directory/events.jsonl"; }
# cgroup_processes counts the host processes in the container's cgroup.
cgroup_processes() {
  python3 - "$1" <<'PY'
import os, sys
scope = f"docker-{sys.argv[1]}.scope"
count = 0
for pid in filter(str.isdigit, os.listdir("/proc")):
    try:
        with open(f"/proc/{pid}/cgroup") as f:
            count += scope in f.read()
    except OSError:
        pass
print(count)
PY
}

"$csf" serve -state "$state" -listen "127.0.0.1:$port" -launch container -session-image "$image" \
  >"$state/serve.log" 2>&1 &
serve_pid=$!
wait_for 30 test -f "$state/harness.json" || die "csf serve did not start; see $state/serve.log"

cat >"$state/recipe.json" <<JSON
{
  "assignment_id": "$assignment",
  "agent": {"id": "session-container-acceptance", "revision": 1, "display_name": "Container session acceptance",
            "instructions": "You are a CSF acceptance probe. Answer in one word."},
  "ticket_url": "https://github.com/candacelabs/csf_staging/issues/325",
  "model": "claude-haiku-4-5-20251001",
  "repository_id": "csf_staging",
  "task": "Reply with the single word READY. Run no tools.",
  "workspace": {
    "repository_path": "$repository",
    "base_branch": "$base",
    "branch": "$branch",
    "allowed_tools": ["Bash"],
    "pull_request_title": "Container session acceptance probe (closed automatically)"
  }
}
JSON
"$csf" submit -endpoint "$endpoint" -recipe "$state/recipe.json" >/dev/null
wait_for 120 has_container || die "no session container appeared; see $state/serve.log"
container=$(session_container)
printf 'session container %s\n' "${container:0:12}"

# Container start time, as the harness measured it.
start_ms=$(python3 - "$run_directory/events.jsonl" <<'PY'
import json, sys
for line in open(sys.argv[1]):
    record = json.loads(line)
    if record.get("event_type") == "harness_container_started":
        print(record["start_ms"])
        break
PY
)
check start-time "$([[ -n "$start_ms" ]] && echo ok)" "${start_ms} ms"

# Configuration, from docker inspect.
docker inspect "$container" >"$state/inspect.json"
while IFS='|' read -r name outcome detail; do
  check "$name" "$outcome" "$detail"
done < <(python3 - "$state/inspect.json" "$limits_source" "$run_directory" "$repository" "$state" "$HOME" <<'PY'
import json, os, re, sys
inspect = json.load(open(sys.argv[1]))[0]
source = open(sys.argv[2]).read()
run, repository, state, home = sys.argv[3:7]
# The session's own home, under its run directory: the harness's
# containerHomeDirectory, which it sets as the executor's HOME.
session_home = os.path.join(run, "home")
config, host = inspect["Config"], inspect["HostConfig"]

def derived(name):
    return eval(re.search(name + r"\s+int64\s*=\s*([0-9_<> ]+)", source).group(1))

def line(name, ok, detail):
    print(f"{name}|{'ok' if ok else 'no'}|{detail}")

line("user", config["User"] == "1000:1000", config["User"])
line("cap-drop", host["CapDrop"] == ["ALL"], host["CapDrop"])
line("no-new-privileges", "no-new-privileges" in (host["SecurityOpt"] or []), host["SecurityOpt"])
line("not-privileged", host["Privileged"] is False, host["Privileged"])
line("namespaces", host["PidMode"] != "host" and host["IpcMode"] != "host" and host["NetworkMode"] != "host",
     f"pid={host['PidMode'] or 'private'} ipc={host['IpcMode']} net={host['NetworkMode']}")
# The harness asks for "default"; Docker records the default network by its
# name on Linux, bridge.
line("network", host["NetworkMode"] in ("default", "bridge"), "default -> " + host["NetworkMode"])
mounts = {(m["Source"], m["Destination"], m["RW"]) for m in inspect["Mounts"]}
line("no-docker-socket", not any("docker.sock" in s or "docker.sock" in d for s, d, _ in mounts), f"{len(mounts)} mounts")
key = re.sub(r"[^A-Za-z0-9]", "-", f"{run}/worktree")
expected = {
    (run, run, True),
    (f"{repository}/.git", f"{repository}/.git", True),
    (f"{state}/bazel-disk-cache", f"{state}/bazel-disk-cache", True),
    (f"{state}/ocaml-toolchain", f"{state}/ocaml-toolchain", True),
    (f"{home}/.claude/.credentials.json", f"{session_home}/.claude/.credentials.json", False),
    (f"{home}/.config/gh", f"{session_home}/.config/gh", False),
    # The worktree's directory of the executor's conversation store.
    (f"{home}/.claude/projects/{key}", f"{session_home}/.claude/projects/{key}", True),
}
line("mounts-exact", mounts == expected,
     "extra=" + str(sorted(mounts - expected)) + " missing=" + str(sorted(expected - mounts)))
for field, name in (("PidsLimit", "DefaultContainerPidsLimit"), ("Memory", "DefaultContainerMemoryBytes"),
                    ("NanoCpus", "DefaultContainerNanoCPUs")):
    line(f"limit-{field}", host[field] == derived(name), f"{host[field]} (derived {derived(name)})")
PY
)

# Work, from inside the session container as its own user.
inside() { docker exec --user 1000:1000 --workdir "$worktree" "$container" bash -c "$1"; }
wait_for 180 turn_finished || printf 'note: the first turn has not finished; the work checks run beside it\n'
check executor "$(inside 'claude --version' >/dev/null && echo ok)" "$(inside 'claude --version')"
check no-docker "$(inside '! command -v docker && ! test -S /var/run/docker.sock' >/dev/null && echo ok)" "no docker CLI, no socket"
check no-sudo "$(inside '! sudo -n true' >/dev/null 2>&1 && echo ok)" "sudo -n true refused"
bazel_output=$(inside 'tools/bazel.sh test //pkg/argv:argv_test 2>&1 | tail -n 3') || true
check bazel-test "$(grep -q 'PASSED\|1 test passes\|Executed 1 out of 1 test' <<<"$bazel_output" && echo ok)" "$(tr '\n' ' ' <<<"$bazel_output")"
race_output=$(inside 'go test -race ./pkg/argv/ 2>&1 | tail -n 1') || true
check go-test-race "$(grep -q '^ok' <<<"$race_output" && echo ok)" "$race_output"
inside "printf 'container session acceptance\n' > ACCEPTANCE_PROBE.md && git add ACCEPTANCE_PROBE.md && git commit -q -m 'acceptance: container session probe' && git push -q origin HEAD:refs/heads/$branch" \
  && check git-commit-push ok "$branch" || check git-commit-push no "$branch"
pull_request=$(inside "gh pr create --repo \$(git remote get-url origin) --draft --base main --head $branch --title 'Container session acceptance probe (closed automatically)' --body 'Opened from inside a session container by bazel/session_image/acceptance.sh; closed by it.'" 2>&1 | tail -n 1) || true
check draft-pr "$([[ "$pull_request" == https://* ]] && echo ok)" "$pull_request"
https_status=$(inside 'curl -sS -o /dev/null -w "%{http_code}" https://api.github.com/zen') || true
check https "$([[ "$https_status" == 200 ]] && echo ok)" "api.github.com -> $https_status"
tailnet_status=$(inside "curl -sS -o /dev/null -w '%{http_code}' --max-time 10 http://$tailnet/") || true
check tailnet "$([[ "$tailnet_status" =~ ^[1-5][0-9][0-9]$ ]] && echo ok)" "csf serve on the tailnet -> HTTP $tailnet_status"

# Lifecycle: a double-forked process inside, then cancel.
docker exec --user 1000:1000 --detach "$container" setsid bash -c 'sleep 86400 & disown' >/dev/null
wait_for 10 test "$(cgroup_processes "$container")" -ge 3 || true
before=$(cgroup_processes "$container")
cancel_started=$SECONDS
"$csf" cancel -endpoint "$endpoint" -assignment "$assignment" >/dev/null
wait_for 120 container_gone && gone=ok || gone=no
check cancel-removes-container "$gone" "$((SECONDS - cancel_started)) s"
after=$(cgroup_processes "$container")
check no-process-left "$([[ "$before" -gt 0 && "$after" -eq 0 ]] && echo ok)" "session processes on the host: before cancel $before, after $after"
wait_for 30 grep -q '"event_type":"harness_launch_receipt"' "$run_directory/events.jsonl" || true
receipt=$(python3 - "$run_directory/events.jsonl" <<'PY'
import json, sys
for line in open(sys.argv[1]):
    record = json.loads(line)
    if record.get("event_type") == "harness_launch_receipt":
        print(f"launch={record['launch']} cpu_usec={record.get('cpu_usec')} memory_peak_bytes={record.get('memory_peak_bytes')}")
PY
)
check receipt "$([[ "$receipt" == launch=container\ cpu_usec=[0-9]* && "$receipt" != *None* ]] && echo ok)" "$receipt"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
