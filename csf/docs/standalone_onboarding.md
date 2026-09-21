# Standalone CSF onboarding

Use this guide from a fresh clone of the private `candacelabs/csf` repository.

```sh
git clone git@github.com:candacelabs/csf.git
cd csf
./install.sh
candace csf up
```

The install step provides the `candace` command. Bare `candace csf` is the
same as `candace csf up --dry`: it validates the checkout and prints a startup
plan without creating state or calling Docker or HTTP. Use `candace csf --help`
for help. `candace csf up` builds the pinned runtime image and starts CSF and
its required containers. Use `candace csf status`, `candace csf logs` and
`candace csf down` to manage the same app. `down` keeps its persistent data.

The runtime state root is `~/.local/state/csf`; set
`CANDACE_CSF_STATE_DIR` to move it. Private operator configuration stays in
this directory. Evidence is written to `<state>/evidence`; Workbench
repositories and agent worktrees are under `<state>/workbench`. Durable
sessions and schedules live in the Compose PostgreSQL volume. Preserve the
state directory and Compose volumes when keeping an installation.

## Agent configuration

When Workbench is enabled, its agent sessions receive CSF's authenticated MCP
server automatically. Discover the current contract with MCP `tools/list`,
then use the generated `GetOwnAgentConfiguration` operation to read that
agent's configuration and `UpdateOwnAgentConfiguration` to replace it. The
transport supplies the agent and session identity; do not add an agent ID to
the request or try to select another agent.

The update is a complete replacement and uses `expected_revision` for
compare-and-swap: send zero when creating the first record, then use the
revision returned by the latest read or update. The fields are:

- Langfuse: endpoint URL, public-key secret reference and secret-key secret
  reference.
- OpenSearch: endpoint URL, index, embedding model and credentials secret
  reference.

These operations store and return configuration references, never secret
values. Secret references must stay under `agent/<agent-id>/`. The config API
does not provision or update secret values; those values must already be
managed outside the agent configuration API. Do not send raw credentials
through agent prompts, chat or MCP. The generated HTTP projections are
`POST /api/agents/configuration/get` and
`POST /api/agents/configuration/update`; use the authenticated agent MCP path
for agent-owned requests.

Run the Workbench scheduler by setting one provider credential before startup:

```sh
COPILOT_GITHUB_TOKEN=... candace csf up
```

`GH_TOKEN` and `GITHUB_TOKEN` are also accepted. The operator saves the token
in its private state directory. Without a provider token, the core runtime
starts and reports why Workbench scheduling is disabled.

`candace csf key` creates the one opaque agent-MCP bearer key if needed and
prints it. The file lives at `$CANDACE_CSF_STATE_DIR/agent-mcp-key` (or
`~/.local/state/csf/agent-mcp-key`) with mode `0600`. Workbench attaches it to
agent sessions. Do not put its output in source, prompts, logs or receipts.

## Generated documentation and architecture checks

`csf/compiler/language/architecture.csf` owns the CSF vocabulary, diagrams and
generated documentation. The generated dictionaries and diagrams are under
`csf/docs/generated/`. Run both compiler suites and both drift checks from the
standalone root. The language compiler owns `csf/README.md` and generated CSF
docs; the architecture compiler checks its declared source boundaries. These
checks do not prove deployed behavior or physical safety.

```sh
bash tools/bazel.sh test //csf/compiler/language:tests \
  --lockfile_mode=error --test_output=errors
bazel-bin/csf/compiler/language/generate.exe --root "$PWD" write
bazel-bin/csf/compiler/language/generate.exe --root "$PWD" check

bash tools/bazel.sh test \
  //csf/compiler/architecture:all \
  //tools/gorilla_mux_lint:checker_test \
  //csf/architecture/generated:typed_projection \
  --lockfile_mode=error --test_output=errors --jobs=2 --repo_env=OPAMJOBS=2
bazel-bin/csf/compiler/architecture/csfc.exe check-generated
```

The protobuf and API declarations remain authoritative for agent operations.
Use their generated projections and discover live MCP schemas with
`tools/list`; do not copy operation schemas into a second handwritten
contract. See `tools/codegen/api/adapter.proto` and
`proto/candace/brainspine/v1/brainspine.proto` when changing those contracts.
