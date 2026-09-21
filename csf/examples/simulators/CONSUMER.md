# CSF simulator consumer contract

| Owner | Provides |
|---|---|
| CSF | One shared Go host; durable job admission/inspection/cancellation; generated HTTP/CLI/MCP contracts; PostgreSQL state; bounded retained logs; artifact links; optional OpenSearch archive and Langfuse trace projections. |
| Consumer | Simulator image and compatible entrypoint; scenario, controller and assets; model/data licenses; infrastructure credentials; resource and spending limits; evaluation objectives and acceptance checks. |
| Consumer's agent harness | An HTTP MCP connection to the host's `/mcp`, plus the existing repository tools needed to edit/build/test the consumer's code. A separate simulator-specific MCP server is not required. |

## Replace an example with your simulator fork

| Boundary | Existing contract |
|---|---|
| Local execution policy | `LocalSimulationConfig` / `LocalSimulationProfile` in [brainspine.proto](../../../proto/candace/brainspine/v1/brainspine.proto). Configure an immutable local image ID, named artifact volume, host-mounted artifact directory, browser artifact URL, container network, progress URL and wall timeout. [Example](local-policy.example.json). |
| Entrypoint environment | `CSF_RUN_ID`, `CSF_STEPS`, `CSF_OUTPUT=/artifacts/<run-id>`. The Isaac profile also sets `ACCEPT_EULA=Y`; the operator must accept the vendor's terms before enabling it. |
| Local command arguments | `--progress-url`, `--max-wall-seconds`, `--capture-every`; the CARLA profile additionally passes `--timeout-seconds`. A fork can provide a compatible entrypoint around its own simulator/controller. Arbitrary per-job commands, mounts and images are not accepted from agents. |
| Progress | Generated `ResearchEvent` JSON: define metrics, then report measurements and status with UTC `recorded_at` and the admitted run ID. POST batches using `RecordSimulationEvents`; print `CSF_EVENT <json>` for the Batch log reader. |
| Simulation steps | Measurement/frame step numbers count completed physics steps starting at 1. The trajectory's first JSONL row represents completed step 1 even when the vendor labels its internal iteration 0. UTC observation time and simulator time remain separate. |
| Retained source | `events.jsonl`, one JSON row per physics step in `trace.jsonl`, and `manifest.json`. Camera-enabled workers additionally write generated `SimulationFrames` JSON in `frames.json` and hash-referenced PNGs. See [workers](README.md) and [canonical types](../../../proto/candace/brainspine/v1/brainspine.proto). |
| Terminal result | Report success after the simulator's cleanup and configured artifact uploads succeed. CSF separately confirms managed-container removal; a completed progress counter alone does not prove cleanup. |
| AWS Batch | Consumer supplies region, credentials, queue ARN, revision-pinned job definitions, S3 prefix, CloudWatch group and conservative per-job reservation/timeout. [Policy example](aws-policy.example.json), [job definition](aws-job-definition.example.json). Native AWS execution remains unverified. |

The current host profiles select CARLA or Isaac. Forks fitting those entrypoint and artifact contracts can replace the images without changing CSF's Go orchestration. A new simulator family or different artifact protocol needs an explicit adapter/contract extension. Automatic repository adaptation is future work, not implemented by this example.

## Agent tools and optional MCP connections

| Consumer need | CSF-provided tools / connection | Consumer supplies |
|---|---|---|
| Run and inspect | `SubmitSimulation`, `InspectSimulation`, `ListSimulations`, `CancelSimulation` through the shared `/mcp` | An operator-configured local or Batch profile and a bounded request. |
| Read local worker output | `ReadSimulationLogs` | A host-managed local run; retained output is bounded. |
| Recover a trace | `RebuildSimulationTrace` | `logIndex`, the shared OpenSearch client, Langfuse export configuration, and an archived terminal local run. Spans are derived from OpenSearch source bytes; PostgreSQL still owns admission/state. |
| Search source evidence | `IngestDocument`, `GetDocument`, `Search` | Source material and, for semantic search, a configured embedding model; the response identifies lexical/semantic mode. |
| Inspect symbolic relationships | `PutNode`, `PutEdge`, `GetGraph` | Domain meanings and useful relationships. |
| Inspect the current controller contract | `Compile`, `GetSnapshot` | A recipe accepted by the existing small driving-controller grammar. General compiler verification is separate work. |
| Browse aggregated logs | Optional native OpenSearch MCP (`SearchIndexTool`) | Endpoint/authentication and allowed indexes. Indexing is automatic Go-worker work, not an agent tool. |
| Inspect agent/simulator traces | Optional Langfuse MCP; browser trace links | Endpoint/project credentials and explicit sharing policy. No Langfuse token is needed by the simulator image. |
| Inspect training runs | Consumer's MLflow API/MCP when training is added | Experiment, metrics, model/dataset provenance and artifact storage. A complete training loop is not delivered by these workers. |
| Modify consumer code | Consumer's existing repository/filesystem/build/test tools | Scoped checkout/worktree and its instructions. CSF does not require source rewriting or an extra agent runtime to schedule jobs. |

The [generated OpenAPI](../../tools/codegen/generated/openapi/adapter.openapi.json) and [RPC definitions](../../tools/codegen/api/adapter.proto) own operation shapes. `csf call --endpoint <host> <Operation>` uses those same operations with protobuf JSON on stdin. The embedding host owns the router, configuration and lifecycle; simulator coordination and log projection run in its existing Go worker.

## Remaining consumer work

| Outcome | Needed next | Evidence required |
|---|---|---|
| Neural training | Training implementation, datasets, model objective, compute and checkpoint/resume policy | Retained training/evaluation results and repeatable improvement against a fixed baseline. |
| Perception/control from sensors | Sensor adapters, observation/action contract, calibration and trained policy | Measured perception/controller performance on held-out scenarios. |
| Cross-simulator equivalence | Shared units/coordinates, scenario meaning and tolerances | Compared trajectories and metrics under explicit equivalence criteria. |
| AWS execution | Consumer account resources, permissions, artifact/log configuration and approved reservation | Actual submission, progress, logs, artifacts, cleanup and cost receipt. |
| ROS bags | ROS1/ROS2 bridges, topic/schema/time contract and recording configuration | A produced bag, indexed metadata and successful playback/inspection. No bag has been produced by these examples. |
| Physical hardware | Hardware interfaces, operating envelope, interlocks and independent validation | Hardware-specific evidence; simulator completion is insufficient. |

OpenSearch retains typed run/source records, original event and trajectory text, bounded stdout/stderr and artifact references. Langfuse is a rebuildable observation projection. Hash checks detect changed source bytes; they are not a formal proof of physical correctness. Camera bytes stay in artifact storage, so preserve that storage if the consumer needs to retrieve images after replay.

Langfuse v4 observations are immutable. A corrected projector version uses new trace/span IDs and links the previous trace; recorded UTC times remain unchanged. The PostgreSQL delivery ledger suppresses repeat successful exports and retains uncertain responses as ambiguous. Ambiguous delivery requires sink inspection before recovery; CSF does not blindly retry it. See the [upstream v4 contract](https://langfuse.com/integrations/native/opentelemetry/migration-to-v4).
