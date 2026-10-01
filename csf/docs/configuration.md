# CSF application configuration

The public archive contains one ready-made CSF application binary at
`app/csf/cmd`. A consumer can pin the archive in Bazel and run that binary
without copying CSF capability wiring into its own repository:

```sh
bazel run @csf//app/csf/cmd:cmd -- serve
```

The binary owns its listener, signals and process lifetime. CSF services remain
in-process libraries. `CSF_*` environment variables provide startup defaults
for the `serve` and `initialize` commands. An explicit command-line flag wins
over its environment value, which keeps existing invocations compatible.

## Minimal runtime

No environment variable is required for the dashboard-only runtime. With no
database configuration, the binary starts the CSF HTTP and MCP endpoints and
does not construct the durable knowledge store, artifact store or OpenSearch
index. Knowledge operations return an explicit capability-unavailable error;
they do not claim that a document was indexed.

The defaults are:

| Setting | Environment variable | Default |
| --- | --- | --- |
| HTTP listen address | `CSF_LISTEN` | `127.0.0.1:14111` |
| Browser origin | `CSF_ORIGIN` | `http://127.0.0.1:14111` |
| Dashboard event log | `CSF_EVENTS` | `events.jsonl` |
| Content-addressed artifacts | `CSF_ARTIFACTS` | `artifacts/cas` |
| OpenSearch endpoint | `CSF_SEARCH_URL` | `http://127.0.0.1:19200` |
| OpenSearch index | `CSF_SEARCH_INDEX` | `brain-knowledge` |
| Embedding model | `CSF_EMBEDDING_MODEL` | empty; lexical search |

`CSF_EVENTS` is the path read by the dashboard. It may not exist at startup;
the snapshot then reports the missing observation source. The process still
starts. `CSF_WORKBENCH_THEME_DIR` can point at a directory containing the
fixed filename `workbench-theme.css`; a missing file means the default theme.

For a same-binary smoke test using only optional settings:

```sh
CSF_LISTEN=127.0.0.1:14111 \
CSF_WORKBENCH_THEME_DIR=/path/to/consumer-theme \
bazel run @csf//app/csf/cmd:cmd -- serve
```

The HTTP `GetWorkbenchTheme` operation reads the configured stylesheet. A
running process can reload that file through `ReloadWorkbenchTheme`; changing
environment values requires a process restart.

## Durable knowledge

Set `CSF_DATABASE_CONFIG` to a private JSON file containing the PostgreSQL
connection URL:

```json
{"url":"postgres://user:password@host/database?sslmode=require"}
```

When it is set, startup connects to PostgreSQL, runs the existing migrations,
opens the content-addressed artifact directory, and connects to OpenSearch.
`CSF_SEARCH_URL` selects the OpenSearch endpoint and `CSF_SEARCH_INDEX`
selects its index. PostgreSQL and OpenSearch are both required for this mode;
the default OpenSearch endpoint is only useful when a local instance is
already listening there. The embedding model is optional: an empty value keeps
search lexical, while a configured model enables the existing semantic query
path.

The binary does not create PostgreSQL, OpenSearch, credentials, or provider
accounts. Keep connection files and credentials outside source control with
owner-only permissions. The process reads the JSON file at startup and does
not log its contents. A database configuration change, search endpoint or
index change takes effect after restart.

Knowledge ingestion is an explicit operation over the configured store. A
consumer repository is not indexed merely because it is configured as a
Workbench repository or because the binary starts. An importer must submit
bounded documents through the existing generated ingestion operation and
retain its source and revision metadata.

`LearnAboutCSF` is the onboarding entry point. Calling it with `{}` returns
guidance and, when knowledge is configured, queues the documentation embedded
in this exact binary. Set `CSF_CONSUMER_ROOT` to authorize a checkout, then pass
relative `consumer_paths` in batches of up to 64 to queue its source files too.
The tool rejects symlinks and excludes `.git`, `.env*`, `secrets`, dependencies,
database files and private-key files; callers still own source selection and
review. Merely setting the root does not ingest anything.

The optional `CSF_CONSUMER_REVISION` identifies a reviewed source revision.
When omitted, each file's content hash is its revision, including uncommitted
edits. Receipts distinguish queued work from indexed work. Inspect a receipt
with `GetDocument`, or repeat `LearnAboutCSF` after projection to retrieve
matching indexed sources. An empty retrieval result does not claim that a
source was found. Custom library hosts may also override the embedded CSF
documentation root and revision with `WithOnboarding`.

### Optional Copilot history onboarding

Set `CSF_COPILOT_HISTORY_SOURCE` to an authorized native Copilot history
directory to enable the optional read-only bridge. At startup the bridge
rejects a symlinked source root, copies the source into a disposable
wrapper-owned SDK home, and reads only that point-in-time copy. A process
restart takes a new snapshot; changing the source directory while a process is
running does not change the existing copy. The source must be explicitly
configured, and `LearnAboutCSF` reads no history when `copilot_session_ids` is
empty.

Pass at most 16 selected session IDs in `copilot_session_ids`. When the source
is configured, the same MCP server exposes `ListCopilotHistorySessions`; list
IDs there, then pass selected IDs to onboarding. Library hosts can call the
bridge's typed `ListHistorySessions` method directly. Either listing path reads
metadata only and does not resume sessions or read their events. The app's
bridge uses the SDK's typed metadata and event APIs; it does not reconstruct or
expose the native storage format.

Each selected session is retained through the existing `IngestDocument` queue
as one `application/json` snapshot containing the decoded SDK metadata and
events. The document revision and content hash are both derived from those
retained bytes, so the revision can be reproduced from the artifact. A receipt
with `queued=true` and `indexed=false` is durable ingestion evidence, not
retrieval evidence. Projection workers must finish before `Search` can return
the session. SDK transport tests cover typed resume/read/disconnect behavior.
Native acceptance with Copilot CLI 1.0.85 and SDK 1.0.11 reads a persisted
synthetic conversation from a disposable copy, preserves the source bytes and
requires no additional model request. Separate PostgreSQL/OpenSearch acceptance
verifies durable ingestion and lexical retrieval. These fixtures use no user
history or credentials.

## Optional capabilities

| Capability | Environment variables | Requirements and behavior |
| --- | --- | --- |
| Source watch | `CSF_WATCH_ROOT`, `CSF_RECEIPTS` | `CSF_WATCH_ROOT` requires `CSF_RECEIPTS`; receipts alone are harmless. The watcher writes source-check receipts below the configured directory. |
| Consumer onboarding | `CSF_CONSUMER_ROOT`, `CSF_CONSUMER_REVISION` | Authorizes selected source files for `LearnAboutCSF`. A revision requires a root; indexing requires durable knowledge. |
| Copilot history onboarding | `CSF_COPILOT_HISTORY_SOURCE` | Copies an authorized native history directory into a disposable SDK home at startup. Adds the metadata-only `ListCopilotHistorySessions` MCP tool; `LearnAboutCSF` imports only explicitly selected session IDs, up to 16 per call. |
| Workbench | `CSF_WORKBENCH_DATABASE_CONFIG`, `CSF_WORKBENCH_REPOSITORY`, `CSF_WORKBENCH_WORKTREES` | `CSF_WORKBENCH_DATABASE_CONFIG` requires `CSF_WORKBENCH_REPOSITORY`; worktree storage is optional and otherwise derives beside the repository. Workbench-only UI and token settings require the Workbench database setting. A trace configuration can also be used by local simulations when `CSF_LOCAL_SIMULATION_CONFIG` and `CSF_DATABASE_CONFIG` are configured. |
| Workbench theme | `CSF_WORKBENCH_THEME_DIR` | Reads `workbench-theme.css`; file edits can be reloaded through the generated operation. |
| Agent MCP auth | `CSF_AGENT_MCP_KEY_FILE` | Reads the private signing key file at startup. It is required for authenticated agent MCP routes and operator email. |
| Operator email | `CSF_OPERATOR_EMAIL_CONFIG` | Requires the agent MCP key and a separate owner-only protobuf JSON file; SMTP password is referenced by that file, never placed in an environment value. |
| Local simulations | `CSF_LOCAL_SIMULATION_CONFIG` | Requires `CSF_DATABASE_CONFIG`; reads the existing protobuf JSON Docker profile and uses the caller's Docker boundary. |
| AWS Batch simulations | `CSF_SIMULATION_CONFIG` | Requires `CSF_DATABASE_CONFIG`; reads the existing protobuf JSON profile; AWS SDK credentials and region are supplied by the configured runtime. |

`CSF_WORKBENCH_TOKEN_FILE` points to the private provider token file. The
binary never accepts a raw provider token through a `CSF_*` value. If the file
is omitted, the configured Copilot SDK environment and credential discovery
remain the source of provider authentication; the minimal runtime does not
construct Workbench at all. Optional providers, simulations and Workbench
execution need their own credentials and runtime acceptance; this
configuration guide does not claim those external integrations are live.

The `initialize` command accepts the same database setting and requires
`CSF_DATABASE_CONFIG` (or `--database-config`). It performs database
initialization and exits; it does not start an HTTP listener. Invalid paths,
malformed JSON, missing configuration combinations, failed database
connections and invalid provider configuration fail startup with an error.

## Flags and restart behavior

Every setting above also has the existing kebab-case flag. The environment is
read once before flag parsing, so flags override environment values. The full
mapping is:

| Environment variable | Flag |
| --- | --- |
| `CSF_WATCH_ROOT` | `--watch-root` |
| `CSF_DATABASE_CONFIG` | `--database-config` |
| `CSF_ARTIFACTS` | `--artifacts` |
| `CSF_EVENTS` | `--events` |
| `CSF_LISTEN` | `--listen` |
| `CSF_WORK_STATE` | `--work-state` |
| `CSF_RECEIPTS` | `--receipts` |
| `CSF_ORIGIN` | `--origin` |
| `CSF_SEARCH_URL` | `--search-url` |
| `CSF_SEARCH_INDEX` | `--search-index` |
| `CSF_EMBEDDING_MODEL` | `--embedding-model` |
| `CSF_CONSUMER_ROOT` | `--consumer-root` |
| `CSF_CONSUMER_REVISION` | `--consumer-revision` |
| `CSF_COPILOT_HISTORY_SOURCE` | `--copilot-history-source` |
| `CSF_WORKBENCH_DATABASE_CONFIG` | `--workbench-database-config` |
| `CSF_WORKBENCH_REPOSITORY` | `--workbench-repository` |
| `CSF_WORKBENCH_WORKTREES` | `--workbench-worktrees` |
| `CSF_WORKBENCH_UI` | `--workbench-ui` |
| `CSF_WORKBENCH_THEME_DIR` | `--workbench-theme-dir` |
| `CSF_WORKBENCH_TRACE_CONFIG` | `--workbench-trace-config` |
| `CSF_WORKBENCH_TOKEN_FILE` | `--workbench-token-file` |
| `CSF_AGENT_MCP_KEY_FILE` | `--agent-mcp-key-file` |
| `CSF_OPERATOR_EMAIL_CONFIG` | `--operator-email-config` |
| `CSF_LOCAL_SIMULATION_CONFIG` | `--local-simulation-config` |
| `CSF_SIMULATION_CONFIG` | `--simulation-config` |

Environment, flag and file changes are startup configuration and require a
restart. The one reload boundary is the theme stylesheet operation described
above. The process owns shutdown through its signal context; stopping it
closes its worker and listener resources, while PostgreSQL, OpenSearch,
provider and simulator processes remain caller-managed dependencies.
