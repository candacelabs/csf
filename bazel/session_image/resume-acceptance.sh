#!/usr/bin/env bash
# Acceptance for resuming across the switch to container sessions: a session
# launched on the host, from a worktree that pins no session image, survives a
# restart with container launch on and resumes its own conversation.
#
# It runs a second csf serve, built from this checkout, on its own state
# directory and port, twice over the same state: first with -launch host, then
# with -launch container and no -session-image, as a production restart does.
# The session runs a cheap model; its first turn is given a random code word,
# and the turn after the restart must answer with it. Nothing is pushed.
#
# Checks, each one line: the run reopens in a container of the fallback image
# and stays OPEN (not FAILED), and the resumed turn answers with the code word.
#
# Usage: bazel/session_image/resume-acceptance.sh REPOSITORY CSF_BINARY EXECUTOR [BASE_COMMIT]
#   REPOSITORY   a clone of this repository
#   CSF_BINARY   csf built from this checkout (go build ./app/harness/cmd)
#   EXECUTOR     the executor CLI on the host, for the host-launched half
#   BASE_COMMIT  a commit from before bazel/session_image.txt existed
#                (default: the parent of the commit that added it)
set -Eeuo pipefail

die() {
  printf 'resume acceptance: %s\n' "$*" >&2
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

[[ $# -ge 3 ]] || die 'usage: resume-acceptance.sh REPOSITORY CSF_BINARY EXECUTOR [BASE_COMMIT]'
repository=$(cd -- "$1" && pwd -P)
csf=$(cd -- "$(dirname -- "$2")" && pwd -P)/$(basename -- "$2")
executor=$3
module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
base=${4:-$(git -C "$module_root" log --format=%H --diff-filter=A -n 1 HEAD -- bazel/session_image.txt)^}
base=$(git -C "$repository" rev-parse "$base")
git -C "$repository" cat-file -e "$base:bazel/session_image.txt" 2>/dev/null && die "$base already pins a session image"
docker image inspect csf-session:latest >/dev/null || die 'no csf-session:latest; run bazel/session_image/build.sh'

state=$(mktemp -d "${TMPDIR:-/tmp}/csf-resume-acceptance.XXXXXX")
port=$((14700 + RANDOM % 200))
endpoint="http://127.0.0.1:$port"
assignment=$(</proc/sys/kernel/random/uuid)
branch="acceptance/session-resume-${assignment:0:8}"
run_directory="$state/$assignment"
worktree="$run_directory/worktree"
word="kestrel-${assignment:9:4}"
serve_pid=

cleanup() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    printf '\n--- csf serve log (last 20 lines) ---\n' >&2
    tail -n 20 -- "$state/serve.log" >&2 2>/dev/null || true
  fi
  "$csf" cancel -endpoint "$endpoint" -assignment "$assignment" >/dev/null 2>&1 || true
  stop_serve
  docker rm -f $(docker ps -aq --filter "label=csf.assignment=$assignment") >/dev/null 2>&1 || true
  git -C "$repository" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  git -C "$repository" branch -D "$branch" >/dev/null 2>&1 || true
  chmod -R u+w -- "$state" 2>/dev/null || true
  rm -rf -- "$state"
}
trap cleanup EXIT

wait_for() {
  local seconds=$1
  shift
  local deadline=$((SECONDS + seconds))
  until "$@" >/dev/null 2>&1; do
    ((SECONDS < deadline)) || return 1
    sleep 0.5
  done
}

start_serve() {
  "$csf" serve -state "$state" -listen "127.0.0.1:$port" -claude "$executor" "$@" >>"$state/serve.log" 2>&1 &
  serve_pid=$!
  wait_for 30 test -f "$state/harness.json" || die "csf serve did not start; see $state/serve.log"
}
stop_serve() {
  if [[ -n "$serve_pid" ]]; then
    "$csf" stop -endpoint "$endpoint" >/dev/null 2>&1 || kill "$serve_pid" 2>/dev/null || true
    wait "$serve_pid" 2>/dev/null || true
    serve_pid=
  fi
}
# finished_turns counts the run's finished turns.
finished_turns() { grep -c '"event_type":"harness_run_finished"' "$run_directory/events.jsonl" 2>/dev/null || true; }
turns_at_least() { [[ "$(finished_turns)" -ge "$1" ]]; }
has_container() { [[ -n "$(docker ps -q --filter "label=csf.assignment=$assignment")" ]]; }
phase() { "$csf" get -endpoint "$endpoint" -assignment "$assignment" | python3 -c 'import json,sys; print(json.load(sys.stdin)["session"]["phase"])'; }
# last_reply is the text of the run's last result.
last_reply() {
  python3 - "$run_directory/events.jsonl" <<'PY'
import json, sys
reply = ""
for line in open(sys.argv[1]):
    record = json.loads(line)
    if record.get("event_type") == "result":
        reply = (record.get("event") or {}).get("result") or ""
print(reply)
PY
}

cat >"$state/recipe.json" <<JSON
{
  "assignment_id": "$assignment",
  "agent": {"id": "session-resume-acceptance", "revision": 1, "display_name": "Session resume acceptance",
            "instructions": "You are a CSF acceptance probe. Answer in as few words as possible and run no tools."},
  "ticket_url": "https://github.com/candacelabs/csf_staging/issues/325",
  "model": "claude-haiku-4-5-20251001",
  "repository_id": "csf_staging",
  "task": "Remember this code word for later: $word. Reply with only OK.",
  "workspace": {
    "repository_path": "$repository",
    "base_branch": "$base",
    "branch": "$branch",
    "allowed_tools": ["Bash"],
    "pull_request_title": "Session resume acceptance probe (never pushed)"
  }
}
JSON

printf 'host half: base %s pins no session image\n' "${base:0:12}"
start_serve -launch host
"$csf" submit -endpoint "$endpoint" -recipe "$state/recipe.json" >/dev/null
wait_for 180 turns_at_least 1 || die 'the host-launched first turn did not finish'
check host-turn "$([[ "$(finished_turns)" -ge 1 ]] && echo ok)" "reply: $(last_reply)"
stop_serve

printf 'container half: restart with -launch container and no -session-image\n'
start_serve -launch container
wait_for 120 has_container && reopened=ok || reopened=no
image=$(python3 - "$run_directory/events.jsonl" <<'PY'
import json, sys
image = ""
for line in open(sys.argv[1]):
    record = json.loads(line)
    if record.get("event_type") == "harness_container_started":
        image = record.get("image", "")
print(image)
PY
)
check reopened-in-container "$reopened" "image $image"
check fallback-image "$([[ "$image" == csf-session:latest ]] && echo ok)" "$image"
is_open() { [[ "$(phase)" == AGENT_SESSION_PHASE_OPEN ]]; }
wait_for 60 is_open || true
check stays-open "$([[ "$(phase)" == AGENT_SESSION_PHASE_OPEN ]] && echo ok)" "$(phase)"
before=$(finished_turns)
"$csf" send -endpoint "$endpoint" -assignment "$assignment" -message "What was the code word I gave you? Reply with only the word." >/dev/null
wait_for 180 turns_at_least $((before + 1)) || true
reply=$(last_reply)
check resumed-context "$([[ "$reply" == *"$word"* ]] && echo ok)" "expected $word, reply: $reply"
check no-lost-conversation "$(grep -q 'No conversation found' "$state/serve.log" "$run_directory/events.jsonl" && echo no || echo ok)" "no 'No conversation found' line"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
