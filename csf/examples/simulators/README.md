# Native simulator worker examples

[Consumer integration contract, MCP tools and remaining work](CONSUMER.md).

These are bounded external workers for CARLA 0.9.16 and NVIDIA Isaac Sim 6.0.
They write the generated protobuf `ResearchEvent` JSON shape to `events.jsonl`,
print every event as `CSF_EVENT <compact-json>` for CloudWatch ingestion, retain
one raw record per physics step, and write a hash-bearing manifest. With
`--progress-url`, metric definitions are sent as one batch and each physics
step's five measurements are sent as one
`{"runId":"...","events":[<ResearchEvent>,...]}` request. The immutable request
body is attempted at most three times with a ten-second timeout per attempt. An
exhausted POST is visible once in stderr and `progress-errors.jsonl` and fails
the run; retries do not duplicate retained JSONL or `CSF_EVENT` records.

The scripts refuse to reuse an output directory. They always close Isaac Sim;
the CARLA worker also destroys its vehicle and restores the world's original
settings. An optional `--artifact-uri s3://bucket/prefix/run-id` uses the
upstream `boto3` SDK to upload retained files. Upload failure fails the run and
writes a failed terminal artifact receipt. A successful terminal event is
announced only after simulator cleanup and configured artifact upload succeed.

## CARLA 0.9.16

Start a separately installed 0.9.16 server, then use the matching Python API:

```sh
python3 carla_waypoint.py --run-id carla-local-1 --steps 120 \
  --output /absolute/path/to/carla-local-1
```

The worker enables synchronous mode with a fixed 0.05-second step, selects a
spawn point from the explicit seed, and drives one vehicle toward successive map
waypoints with a small fixed feedback controller. It requires the server and
client to both report exactly `0.9.16`. Use the emitted manifest to verify the completed step count for your run.

Primary sources: [CARLA 0.9.16 release](https://github.com/carla-simulator/carla/releases/tag/0.9.16),
[synchronous fixed stepping](https://carla.readthedocs.io/en/latest/adv_synchrony_timestep/),
and [Waypoint/WorldSettings APIs](https://carla.readthedocs.io/en/latest/python_api/).

## Isaac Sim 6.0

Run with the Python launcher from an existing Isaac Sim 6.0 installation:

```sh
./python.sh /absolute/path/to/isaac_rigidbody.py \
  --run-id isaac-local-1 --steps 120 --output /absolute/path/to/isaac-local-1
```

The worker creates a ground plane and dynamic cuboid with no downloaded USD
assets, applies a fixed initial velocity, and records 120 explicit 1/60-second
physics steps. Installation and asset sizes remain substantial.

Registry manifests report 8,642,319,304 compressed bytes for
`carlasim/carla:0.9.16` and 10,706,674,165 compressed bytes for the amd64
`nvcr.io/nvidia/isaac-sim:6.0.0` image. On the inspected host, pulling both would
consume 19.35 GB before expanded layers, caches, and run output.

Primary sources: [Isaac Sim 6.0 standalone workflow](https://docs.isaacsim.omniverse.nvidia.com/6.0.0/introduction/workflows.html),
[Core World and physics-step API](https://docs.isaacsim.omniverse.nvidia.com/6.0.0/py/source/deprecated/isaacsim.core.api/config/python_api.html),
and [procedural physics example](https://docs.isaacsim.omniverse.nvidia.com/6.0.0/core_api_tutorials/tutorial_core_hello_world.html).

AWS Batch can pass `CSF_RUN_ID`, `CSF_STEPS`, `CSF_ARTIFACT_URI`, and optionally
`CSF_OUTPUT`; CLI flags override those values. These examples never submit cloud
jobs themselves. Install `protobuf` in either vendor Python environment; install
`boto3` only when S3 artifact upload is configured.

## Container jobs

`Dockerfile.carla` pins `carlasim/carla:0.9.16`. Its Python entrypoint starts the
CARLA server inside the job container, waits at most `CSF_SERVER_TIMEOUT`
seconds for its owned listener, runs the worker, then terminates only that server
process group. The job therefore has no undeclared external CARLA daemon.

`Dockerfile.isaac` pins `nvcr.io/nvidia/isaac-sim:6.0.0`; its entrypoint execs
the upstream `/isaac-sim/python.sh` launcher. Both images pin `protobuf==7.35.1`
and `boto3==1.43.94` through `requirements.txt`. Build contexts are the repository
root so the generated protobuf owner is copied into the image. Use your deployment's image-build workflow and configure CSF with the resulting image IDs.

Build from the public repository root, then use the immutable image ID in your
operator-owned local policy. GPU images and their terms are supplied by their
respective vendors; they are not embedded in the CSF archive.

```sh
docker build -f csf/examples/simulators/Dockerfile.carla -t csf-carla:local .
docker image inspect csf-carla:local --format '{{.Id}}'
docker build -f csf/examples/simulators/Dockerfile.isaac -t csf-isaac:local .
docker image inspect csf-isaac:local --format '{{.Id}}'
```

Check at least 30 GiB free before a cold build. The shared Go host coordinates
jobs through its Docker SDK queue; vendor Python runs inside simulator containers.
Configure `--local-simulation-config` and the host's artifact storage explicitly.

Use `csf`, the Go binary, to submit and inspect jobs directly. Requests are
protobuf JSON on stdin; the generated dispatcher requires `--endpoint` before
the operation. Vendor Python remains inside the simulator job container.

```sh
printf '%s\n' '{"runId":"carla-camera-1","simulator":"SIMULATOR_CARLA","executor":"SIMULATION_EXECUTOR_LOCAL","steps":120,"captureEvery":20}' \
  | csf call --endpoint http://127.0.0.1:14111 SubmitSimulation
printf '%s\n' '{"runId":"carla-camera-1"}' \
  | csf call --endpoint http://127.0.0.1:14111 InspectSimulation
printf '%s\n' '{"limit":20}' \
  | csf call --endpoint http://127.0.0.1:14111 ListSimulations
printf '%s\n' '{"runId":"carla-camera-1","maxBytes":65536}' \
  | csf call --endpoint http://127.0.0.1:14111 ReadSimulationLogs
printf '%s\n' '{"runId":"carla-camera-1"}' \
  | csf call --endpoint http://127.0.0.1:14111 CancelSimulation
```

`captureEvery: 20` asks the worker to retain a real 640x360 vendor RGB frame
after every twentieth completed physics step. Zero or omission disables camera
capture. `frames.json` records each PNG's 1-based step, simulated seconds, UTC
wall timestamp, dimensions, relative path, and SHA-256. CARLA also records its
vendor frame and simulator timestamp. Isaac camera capture requires its
Replicator/RTX extensions: the worker preloads its pinned complete botocore
dependency before Kit can shadow it with the incomplete SimReady bundle.
Camera receipts are emitted into your run directory.

The upstream CARLA image metadata declares Ubuntu 20.04, user `carla`, and
working directory `/workspace`. Its derivative therefore returns to that user
after installing dependencies, launches `/workspace/CarlaUE4.sh`, and supplies a
pinned Python 3.11 runtime because generated protobuf 7.35.1 requires Python
3.10 or newer. The image build fails unless the pinned CARLA release contains
its matching cp311 wheel. Retain the image ID alongside your run manifest.

The Isaac example measures procedural rigid-body physics only. Its progress
does not represent perception, navigation, policy learning, or autonomous
behavior.

## Verification boundary

```sh
python3 -m pytest -q tests
```

The tests execute event serialization, HTTP envelopes, retry identity, cleanup,
bounded loops, and manifests against fakes at the vendor boundary.

This public package includes no historical simulator receipts. Run acceptance
on your configured GPU and retain its manifests. AWS execution requires your
account resources and credentials; it is not established by the local tests.
These examples do not establish neural training, perception, cross-simulator
equivalence or physical-robot safety.

`local-policy.example.json` is the generated `LocalSimulationConfig` JSON shape.
Replace its placeholder image ID with the built image's immutable ID and its
volume with an existing owned artifact volume. The runtime mounts those volumes
read-only inside the Workbench UI directory, so `InspectSimulation.artifacts`
contains camera and source links. It alone mounts the Docker socket; simulator
jobs receive only their artifact volume and a GPU. The shared Go queue serializes
local jobs and retains logs before removing the owned container.

When the existing Workbench OTLP configuration and `traceBaseUrl` are present,
the same worker projects completed native runs into Langfuse. UTC event times
own spans; simulator seconds remain separate output values. Camera references
are checked against their recorded hashes. `traceExportError` preserves failures
and retries; a trace URL records an accepted OTLP upload, not browser verification.
`publicTraces: true` explicitly enables read-only sharing for these simulator
traces. Workbench sessions and project APIs retain their existing authentication.
