# Standalone CSF onboarding

Install CSF from a published public release with one command:

```sh
curl -fsSL https://raw.githubusercontent.com/candacelabs/csf/main/bootstrap.sh | sh
csf up
```

The installer supports Linux x86_64 and requires `curl` and a running local
Docker daemon with the Compose plugin that this user can access. Other host platforms are rejected.
Docker supplies the bootstrap interpreter
and Bazel's build tools; no host Rust, C compiler, Python package, or separate
editor plugin installation is required. The same install provides the `csf`
command and [CSF syntax highlighting](../editor/README.md) for Neovim.

The bootstrap resolves the latest semantic release in `candacelabs/csf`, reads
its tagged export provenance, downloads the existing
`csf-<source-sha12>.tar.gz` release asset and `.sha256` sidecar, and checks
the checksum before validating and extracting the archive. It then runs that
release's `install.sh`, which builds through the repository's Docker-backed
Bazel toolchain. A missing public release fails explicitly; the installer never
falls back to private staging repositories or a moving source branch.

Release sources remain under
`${CSF_RELEASES_DIR:-${XDG_DATA_HOME:-~/.local/share}/csf/releases}/vMAJOR.MINOR.PATCH/source`.
The launcher needs that retained source tree to find its runtime definitions.
Its default path is `~/.local/bin/csf`; `CSF_INSTALL_PATH` selects another
absolute path. Add `~/.local/bin` to `PATH` if your shell does not include it.

Rerun the same command to update. To select or roll back to a specific published
release, pass its immutable semantic tag (replace the example below with an
existing release):

```sh
curl -fsSL https://raw.githubusercontent.com/candacelabs/csf/main/bootstrap.sh | sh -s -- --version v1.2.3
```

Previous release directories are retained. Reusing a version verifies its
source files against a freshly verified archive; unrelated directories and
modified source files are never overwritten. Download or verification failures
leave the current launcher untouched. Build failures retain the downloaded
source for diagnosis and leave the previous launcher selected. A rollback
selects the earlier CLI and editor runtime; it does not roll back application
data or start, stop, or migrate running containers. A stale `.install-lock`
after an abrupt interruption requires inspection before retrying.

For a release archive, Workbench imports the verified source into a private Git
repository using Git from the built runtime image. The import retains the source
provenance marker and creates a new local commit; it does not claim that commit
is the upstream revision or add an upstream remote. Existing Workbench changes
are retained when updating CSF. No host Git installation is required.

Bare `csf` is the same as `csf up --dry`: it validates the retained checkout
and prints a startup plan without creating state or calling Docker or HTTP.
Use `csf --help` for help. `csf up` builds the pinned runtime image and starts
CSF and its required containers. Use `csf status`, `csf logs`, and `csf down`
to manage the same app. `down` keeps its persistent data.

For a consumer that already owns a Bazel workspace, the archive also provides
the same application as `@csf//app/csf/cmd:cmd`. Run it with `serve` and set
the `CSF_*` startup environment described in
[the configuration contract](configuration.md); explicit flags override those
environment defaults. The minimal runtime needs no database or provider, and
knowledge remains unavailable until PostgreSQL, OpenSearch and the existing
ingestion operations are explicitly configured.

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
COPILOT_GITHUB_TOKEN=... csf up
```

`GH_TOKEN` and `GITHUB_TOKEN` are also accepted. The operator saves the token
in its private state directory. Without a provider token, the core runtime
starts and reports why Workbench scheduling is disabled.

`csf key` creates the private agent-MCP signing key if needed and
prints it. The file lives at `$CANDACE_CSF_STATE_DIR/agent-mcp-key` (or
`~/.local/state/csf/agent-mcp-key`) with mode `0600`. Workbench keeps this key
host-side and attaches a derived HMAC-SHA256 bearer credential bound to each
agent ID and session ID. Changing either identity header invalidates the
credential; the signing key itself is not accepted as a bearer credential.
Existing sessions using the old shared bearer key must be recreated through
Workbench. Credentials have no independent expiry or revocation and remain
valid for that identity tuple until signing-key rotation. Do not put the key
or session credentials in source, prompts, logs or receipts.

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
