#!/usr/bin/env bash
# One burst job: a whole harness inside a disposable cloud container. The job
# container is the sandbox: it reaches nothing of the operator's host, and it
# is gone when the job ends.
#
# It starts its own PostgreSQL and `csf serve -launch host` with its own
# state, clones the repository, submits the recipes in BURST_RECIPES (a JSON
# array of agent.json recipes), waits for every session to finish its turn,
# merges each pull request on content (the operator's ruling: no checks), and
# uploads the run logs to the corpus dataset before it exits.
#
# Environment (set by csf burst sessions; the secrets are job secrets, never
# part of the image): BURST_NAME, BURST_RECIPES, BURST_REPOSITORY,
# CORPUS_DATASET, GH_TOKEN, HF_TOKEN and the executor's model credential.
#
# With BURST_MODE=suite the job is one shard of an evaluation suite (#416):
# BURST_BUILD names the csf build to replay on, whose released binary
# replaces the image's; BURST_SUITE and BURST_JOBS are the suite and the
# replay jobs; csf eval replay runs them at the suite's budget; every push
# stays inside the job and nothing is merged; and the replays are uploaded
# beside the run logs for csf eval record.
set -Eeuo pipefail

die() {
  printf 'burst: %s\n' "$*" >&2
  exit 1
}

mode=${BURST_MODE:-sessions}
required=(BURST_NAME BURST_REPOSITORY CORPUS_DATASET GH_TOKEN HF_TOKEN)
if [[ "$mode" == suite ]]; then
  required+=(BURST_BUILD BURST_SUITE BURST_JOBS)
else
  required+=(BURST_RECIPES)
fi
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || die "$name is not set"
done

work=/tmp/burst
state="$work/state"
repository="$work/repository"
mkdir -p -- "$state" "$work/recipes" "$work/bin"

# A suite shard replays on the build it scores: its released binary, checked
# against the published checksum, first on PATH.
if [[ "$mode" == suite ]]; then
  tag=$(gh release list --repo "$BURST_REPOSITORY" --limit 500 --json tagName --jq ".[].tagName | select(startswith(\"csf-$BURST_BUILD\"))" | head -n 1)
  [[ -n "$tag" ]] || die "no release of build $BURST_BUILD"
  gh release download "$tag" --repo "$BURST_REPOSITORY" --dir "$work/bin" --pattern csf-linux-amd64 --pattern csf-linux-amd64.sha256
  (cd "$work/bin" && sha256sum --check --quiet csf-linux-amd64.sha256) || die "checksum of $tag does not match"
  install -m 0755 "$work/bin/csf-linux-amd64" "$work/bin/csf"
  export PATH="$work/bin:$PATH"
fi

# The harness's database, owned by this job.
pg_bin=$(ls -d /usr/lib/postgresql/*/bin | tail -n 1)
"$pg_bin/initdb" --pgdata="$work/postgres" --username=csf --auth=trust >/dev/null
"$pg_bin/pg_ctl" --pgdata="$work/postgres" --wait --log="$work/postgres.log" \
  --options="-c listen_addresses=127.0.0.1 -c port=5432 -c unix_socket_directories=$work" start >/dev/null
printf '{"url":"postgres://csf@127.0.0.1:5432/postgres?sslmode=disable"}\n' >"$work/database.json"

git config --global user.name "csf burst $BURST_NAME"
git config --global user.email "burst@csf.invalid"
gh auth setup-git
git clone --quiet "https://github.com/$BURST_REPOSITORY.git" "$repository"

csf serve -launch host -state "$state" -database-config "$work/database.json" -listen 127.0.0.1:14120 >"$work/serve.log" 2>&1 &
serve=$!
for _ in $(seq 1 120); do
  [[ -f "$state/harness.json" ]] && break
  kill -0 "$serve" 2>/dev/null || die "csf serve exited: $(tail -n 20 "$work/serve.log")"
  sleep 1
done
[[ -f "$state/harness.json" ]] || die 'csf serve did not start within 120 s'

if [[ "$mode" == suite ]]; then
  # Pushes go to a repository inside the job, so a replay opens no pull
  # request anywhere.
  git init --quiet --bare "$work/pushes.git"
  git -C "$repository" remote set-url --push origin "$work/pushes.git"
  printf '%s' "$BURST_SUITE" >"$work/suite.json"
  printf '%s' "$BURST_JOBS" >"$work/jobs.json"
  csf eval replay -state "$state" -suite-file "$work/suite.json" -jobs-file "$work/jobs.json" \
    -build "$BURST_BUILD" -node burst -repository "$repository" -out "$state/replays.jsonl" ||
    printf 'burst: csf eval replay failed; uploading what was read\n' >&2
else

# Each recipe runs on this job's own clone.
assignments=()
count=$(jq 'length' <<<"$BURST_RECIPES")
for index in $(seq 0 $((count - 1))); do
  recipe="$work/recipes/$index.json"
  jq --arg repository "$repository" ".[$index] | .workspace.repositoryPath = \$repository" <<<"$BURST_RECIPES" >"$recipe"
  csf submit -state "$state" -recipe "$recipe" >"$recipe.receipt"
  assignments+=("$(jq -r '.assignmentId' "$recipe")")
done

# A session has finished its turn when it is open again, or ended.
finished() {
  local phase
  phase=$(csf get -state "$state" -assignment "$1" | jq -r '.session.phase // empty')
  [[ "$phase" != AGENT_SESSION_PHASE_STARTING && "$phase" != AGENT_SESSION_PHASE_RUNNING && -n "$phase" ]]
}
for assignment in "${assignments[@]}"; do
  until finished "$assignment"; do
    sleep 30
  done
done

# Merge on content: every pull request a session opened.
for assignment in "${assignments[@]}"; do
  url=$(csf get -state "$state" -assignment "$assignment" | jq -r '.session.pullRequestUrl // empty')
  if [[ -n "$url" ]]; then
    gh pr ready "$url" || true
    gh pr merge "$url" --squash --admin || printf 'burst: %s not merged\n' "$url" >&2
  fi
done

fi

kill "$serve" 2>/dev/null || true
wait "$serve" 2>/dev/null || true

# The run logs, so the mining loop sees burst sessions too.
python3 - "$state" "$CORPUS_DATASET" "$BURST_NAME" <<'PYTHON'
import base64, json, os, sys, urllib.request

state, dataset, burst = sys.argv[1:4]
lines = [json.dumps({"key": "header", "value": {"summary": f"burst {burst}: run logs"}})]
def upload(path, name):
    with open(path, "rb") as handle:
        content = base64.b64encode(handle.read()).decode()
    lines.append(json.dumps({"key": "file", "value": {"path": f"bursts/{burst}/{name}", "encoding": "base64", "content": content}}))
for assignment in sorted(os.listdir(state)):
    for name in ("events.jsonl", "run.json", "recipe.json"):
        path = os.path.join(state, assignment, name)
        if os.path.isfile(path):
            upload(path, f"{assignment}/{name}")
replays = os.path.join(state, "replays.jsonl")
if os.path.isfile(replays):
    upload(replays, "replays.jsonl")
request = urllib.request.Request(
    f"https://huggingface.co/api/datasets/{dataset}/commit/main",
    data="\n".join(lines).encode(),
    headers={"Authorization": "Bearer " + os.environ["HF_TOKEN"], "Content-Type": "application/x-ndjson"},
)
with urllib.request.urlopen(request, timeout=300) as response:
    print("burst: run logs uploaded:", response.status, json.loads(response.read()).get("commitUrl"))
PYTHON
