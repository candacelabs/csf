#!/usr/bin/env bash
set -Eeuo pipefail

consumer_binary=$1
mkdir -p /workspace/theme
printf '%s\n' 'body { color: #123456; }' >/workspace/theme/workbench-theme.css
printf '%s\n' 'agent-mcp-test-key' >/workspace/agent-mcp-key

CSF_LISTEN=127.0.0.1:14111 \
CSF_WORKBENCH_THEME_DIR=/workspace/theme \
CSF_AGENT_MCP_KEY_FILE=/workspace/agent-mcp-key \
  "$consumer_binary" serve >/workspace/runtime.log 2>&1 &
consumer_pid=$!
trap 'kill -TERM "$consumer_pid" 2>/dev/null || true; wait "$consumer_pid" || true' EXIT

curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 1 \
  --max-time 5 http://127.0.0.1:14111/api/snapshot >/workspace/snapshot.json
grep -Fq '"snapshot"' /workspace/snapshot.json
grep -Fq '"issues"' /workspace/snapshot.json
grep -Fq 'No observations yet' /workspace/snapshot.json

theme_status=$(curl --silent --show-error --output /workspace/theme.json --write-out '%{http_code}' \
  --max-time 5 \
  -H 'Content-Type: application/json' -d '{}' \
  http://127.0.0.1:14111/api/workbench/theme/get)
printf '%s\n' "$theme_status" >/workspace/theme-status.txt
[[ "$theme_status" == 200 ]]
if grep -Fq 'body { color: #123456; }' /workspace/theme.json; then
  printf '%s\n' adopted >/workspace/theme-adoption.txt
else
  printf '%s\n' default >/workspace/theme-adoption.txt
fi

request=$(printf '%s' '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')
raw_status=$(curl --silent --show-error --output /workspace/agent-raw.json --write-out '%{http_code}' \
  --max-time 5 -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer agent-mcp-test-key' \
  -H 'X-CSF-Agent-ID: agent-mcp-test' \
  -H 'X-CSF-Session-ID: 2d6d5a52-ae7a-4f8d-b89f-e58beaa4a735' \
  -d "$request" http://127.0.0.1:14111/mcp/agent)
printf '%s\n' "$raw_status" >/workspace/raw-key-status.txt

kill -TERM "$consumer_pid"
wait "$consumer_pid"
trap - EXIT
if curl --silent --max-time 1 http://127.0.0.1:14111/api/snapshot; then
  printf '%s\n' 'listener survived process shutdown' >&2
  exit 1
fi
