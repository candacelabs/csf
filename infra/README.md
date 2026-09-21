# CSF standalone infrastructure

This Compose project supplies local Langfuse, OpenSearch and PostgreSQL for the
CSF runtime. The native HTTP MCP servers provide traces and retrieval for an
inspectable agent loop. It does not deploy a robot or capture this desktop
agent's internal model calls automatically.

Install the standalone Rust operator with `./install.sh`, inspect its dry
startup plan with `candace csf`, and start the complete composition with
`candace csf up`. Use `candace csf --help` for the command reference. The
runtime is built in its pinned container; private
credentials, database schema and evidence state are provisioned automatically.
See the [CSF operator guide](../tools/csf-operator/README.md).

```sh
candace csf
candace csf up --dry
candace csf up
candace csf status
candace csf logs --service runtime --tail 80
candace csf down
```

`down` retains PostgreSQL and other Compose volumes. Startup creates a private
local state directory, joins no external Docker network, and starts only the
services needed by CSF. Dashboards and MLflow are present in the Compose source
for research compatibility, but they are not part of the CSF core start path.

| Local endpoint | Purpose |
| --- | --- |
| `http://127.0.0.1:14300` | Langfuse UI and API |
| `http://127.0.0.1:14300/api/public/mcp` | Langfuse native MCP, project Basic Auth |
| `http://127.0.0.1:19200/_plugins/_ml/mcp` | OpenSearch native MCP |
| `http://127.0.0.1:19200` | OpenSearch API |
| `127.0.0.1:15432` | PostgreSQL; separate `brain` and `langfuse` databases/users |

Search has no application authentication in this local profile, and its host
socket binds only to loopback. Langfuse project keys and UI credentials are
generated on first start. The Compose network uses a private subnet configurable
through `CSF_SUBNET` (default `10.231.75.0/24`).

## Credentials and clients

First startup creates `credentials.env`, `csf-database.json`, `evidence/`, and
the embedding model record under the private state directory with restrictive
permissions. The default is `~/.local/state/csf`; set `CANDACE_CSF_STATE_DIR` to
keep it elsewhere. Never commit these files or expose generated
credentials in receipts or chat output. The Langfuse UI account is
`operator@example.invalid`; its password is stored as `LANGFUSE_USER_PASSWORD`.

The private database JSON supplies the retained internal Go schema account;
Langfuse uses its own database and user. Startup refuses to generate new
credentials when it finds existing `csf` volumes without their original state.

## Training results

MLflow is included for research compatibility but is not started by `candace csf up`;
the CSF core path does not depend on training services. The notes below describe
the retained training setup and its evidence, not services started by the core
command.

Set `MLFLOW_TRACKING_URI=http://127.0.0.1:14500` for the official MLflow client
(the server is pinned to 3.16.0). Log objective versions, seed splits, candidate
hashes, accepted/rejected decisions, metrics and output files as real runs.
MLflow is the comparison and artifact interface; the campaign's append-only
receipts remain the source of execution provenance. The trainer decides whether
a candidate is accepted, using its fixed objective, not an MLflow dashboard.

MLflow proxies uploads into its named `mlflow-artifacts` volume. The doctor uses
the upstream REST API to create a run, log a metric and parameter, upload an
artifact, download identical bytes, finish the run and retrieve the stored
metric. The probe experiment is `brain-infrastructure-probes` and its runs are
explicitly marked synthetic; they are not training results. No MLflow MCP server
is invented by this deployment.

## Retrieval projection

The Go protobuf and PostgreSQL records own source identity and revision.
OpenSearch is their query projection, not the durable authority.

- `brain-logs`: `recorded_at`, `run_id`, `kind`, `level`, `message`,
  `evidence_path`, `controller_hash`, and unindexed `metadata`.
- `brain-knowledge`: source metadata (`source_id`, `revision`, `content_hash`,
  `source_uri`, `title`, `media_type`, `license`, `retrieved_at`, `artifact_ref`,
  `size_bytes`) plus `text` and the 384-dimensional `embedding` vector.
- `brain-embedding`: default ingest pipeline runs the local MiniLM model on
  `text`. The model identifier is recorded in the private `embedding-model.json`
  under the CSF state directory.
- `brain-infra-probes`: synthetic doctor documents using that same pipeline,
  kept separate from source records returned by the knowledge store.
- MCP exposes upstream `ListIndexTool`, `IndexMappingTool`, `SearchIndexTool`.
  The search tool accepts `{"index":"brain-knowledge","query":"<JSON DSL>"}`;
  use the same neural query DSL as the direct API.

Code indexing can use a separate index and the same pipeline. Split documents
into short chunks before embedding: the upstream model truncates long input,
so a whole repository or large document is not a sound embedding unit. The
model's semantic ranking is empirical; the two-document doctor probe establishes
connectivity and inference only, not retrieval quality or code understanding.

## Trace boundary

The doctor sends a real OTLP/HTTP JSON span with Langfuse's v4 ingestion header
and retrieves it with native `listObservations` MCP. Its `operator-tool-receipt`
label distinguishes this explicit service check from a model generation. Real
runtime/agent instrumentation should preserve trace/span IDs, task/run/source
IDs, tool inputs/results, elapsed time and outcome. Never invent token usage,
model spans, or hidden reasoning for an upstream process that was not traced.

## Pinned ingredients and portability

The Compose file pins observed `linux/amd64` manifests by digest, including
Langfuse 4.36.1, OpenSearch/OpenSearch Dashboards 3.8.0, and MLflow 3.16.0's
official `full` image (includes its PostgreSQL driver). It adapts Langfuse's
[official recipe at commit 5c528311125d0cfe629b1bcf7b6ca8ef4ba57148](https://github.com/langfuse/langfuse/blob/5c528311125d0cfe629b1bcf7b6ca8ef4ba57148/docker-compose.yml).
Dependencies are PostgreSQL 17, ClickHouse 25.12, Redis 7, and the upstream
Chainguard MinIO image, each digest-pinned. This profile needs Docker Compose,
Rust 1.91 to build the operator; no Python runtime, GPU or provider key.
Container memory limits in the CSF core total about 16 GiB. Image pulls and the model require
internet on first start; subsequent operation uses local volumes. MiniLM's
TorchScript artifact is 91,789,778 bytes, pinned by its upstream SHA-256; the
first deployment also downloads CPU inference libraries inside OpenSearch.
Exact observed image sizes are retained in `receipts/image-inventory.json`;
those uncompressed sizes do not measure unique disk use or network transfer.
Native ARM
images are not selected by these pins.

Licenses are upstream-owned: Langfuse's main code is MIT (its enterprise subtree
has separate terms); OpenSearch, Dashboards, ClickHouse, MLflow and MiniLM are Apache-2.0;
PostgreSQL uses the PostgreSQL License; MinIO is AGPL-3.0; Redis 7.4 has the
upstream RSALv2/SSPLv1 dual license. Redis is an unmodified internal dependency
from the official Langfuse recipe. The archive includes our configuration,
not third-party container images or model weights.

Primary references:

- [Langfuse Docker Compose](https://langfuse.com/self-hosting/deployment/docker-compose)
  and [headless initialization](https://langfuse.com/self-hosting/administration/headless-initialization).
- [Langfuse native MCP](https://langfuse.com/docs/api-and-data-platform/features/mcp-server)
  and [OTLP JSON ingestion](https://langfuse.com/docs/observability/get-started).
- [OpenSearch native MCP](https://docs.opensearch.org/latest/ml-commons-plugin/api/mcp-server-apis/mcp-server/)
  and [native tool registration](https://docs.opensearch.org/latest/ml-commons-plugin/api/mcp-server-apis/register-mcp-tools/).
- [OpenSearch pretrained models](https://docs.opensearch.org/latest/ml-commons-plugin/pretrained-models/)
  and [semantic search setup](https://docs.opensearch.org/latest/tutorials/vector-search/neural-search-tutorial/).
- [MiniLM model card and license](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2)
  and [Langfuse license](https://github.com/langfuse/langfuse/blob/v4.36.1/LICENSE).
- [MLflow tracking server](https://mlflow.org/docs/latest/self-hosting/architecture/tracking-server/),
  [REST API](https://mlflow.org/docs/latest/api_reference/rest-api.html), and
  [official full image recipe](https://github.com/mlflow/mlflow/blob/v3.16.0/docker/Dockerfile.full).
- [Redis 7.4 declared license](https://github.com/redis/redis/blob/7.4/LICENSE.txt).

The source monorepo retains redacted historical acceptance receipts separately
from this standalone application. Those recorded checks are neither shipped
runtime state nor continuous uptime evidence.

## Runtime boundary

The `runtime` container builds `candace/app/csf/cmd` from the checked-out Go
module, initializes and migrates the durable PostgreSQL schema, mounts persistent
work/evidence state, and connects to the CSF-owned OpenSearch service. It binds
HTTP/MCP to loopback by default. Its PostgreSQL and artifact state survives
`candace csf down` and later starts.

Workbench session scheduling remains disabled until the standalone runtime
image includes the Copilot CLI and an operator supplies GitHub Copilot
authorization. The `candace csf up` startup output states this boundary; core CSF
services do not wait for provider credentials.
