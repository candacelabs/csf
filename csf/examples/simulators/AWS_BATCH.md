# Simulator runs through CSF

| Operation | Generated command | Result |
| --- | --- | --- |
| Submit | `csf call --endpoint http://127.0.0.1:14111 SubmitSimulation` | Durable run identity and reserved admission budget |
| Inspect | `csf call --endpoint http://127.0.0.1:14111 InspectSimulation` | Provider state, completed steps, latest measurements, artifact prefix, collection errors |
| List | `csf call --endpoint http://127.0.0.1:14111 ListSimulations` | Recent runs |
| Cancel | `csf call --endpoint http://127.0.0.1:14111 CancelSimulation` | Cancellation request; inspect until cleanup is confirmed |

Requests arrive on stdin. The identical named tools are available through the
shared `/mcp` endpoint, already configured in Copilot Workbench.

```json
{"runId":"carla-example-001","simulator":"SIMULATOR_CARLA","executor":"SIMULATION_EXECUTOR_AWS_BATCH","steps":120}
```

Use `SIMULATOR_ISAAC` for the procedural rigid-body example. Inspection and
cancellation take `{"runId":"carla-example-001"}`; listing takes `{"limit":20}`.
A repeated submission with identical inputs returns the existing run. Reusing
an identity with different inputs fails.

## Operator configuration

The existing shared host enables local observation when PostgreSQL and artifact
storage are configured. AWS submission additionally requires `--simulation-config`
with protobuf JSON shaped like `aws-policy.example.json`. The example contains
placeholder resources, not a deployed cloud stack. The upstream Go AWS SDK owns
authentication through its standard chain. A profile must be accessible **inside
the host container**; a profile installed only on the workstation does not make
it available there. Do not put credentials in the policy or checked-in files.
For the supplied host, pass `--simulation-config /path/to/policy.json` and make
your AWS SDK profile or role available to that host. Consumers mounting CSF in
their own Go host can supply the same typed policy through its service options.

| Required resource | Owner / check |
| --- | --- |
| AWS region and profile/role | Configured AWS SDK chain |
| GPU EC2 Batch queue | Existing operator-owned queue, with reviewed capacity and idle scaling |
| Revision-pinned job definitions | One per built CARLA/Isaac image; same region and account as queue |
| Worker artifact bucket/prefix | Job task role permits `s3:PutObject` only on that prefix |
| CloudWatch log group | Job definition uses `awslogs`; CSF role permits `logs:GetLogEvents` |
| CSF cloud permissions | `batch:SubmitJob` on selected queue/definitions, `batch:DescribeJobs`, `batch:TerminateJob` on owned jobs |

`aws-job-definition.example.json` shows the upstream Batch request shape for
one ECS/EC2 GPU job. Register an equivalent Isaac definition with its own
pinned image and the NVIDIA-required license configuration. The examples use
the image entrypoint; CSF supplies only `CSF_RUN_ID`, `CSF_STEPS`, and
`CSF_ARTIFACT_URI`. No agent-supplied shell command, image, queue, price, or
credential crosses the submission API. Publish the locally built images to a
registry reachable by the Batch execution role before registering definitions.

## Execution and evidence

1. PostgreSQL reserves the configured amount and enqueues the run atomically.
2. One goroutine in the existing Go host claims a job and invokes the generated
   AWS SDK. Job attempts and SDK submission attempts are each limited to one.
3. The worker emits `CSF_EVENT` protobuf JSON to stdout. AWS owns log shipping;
   CSF reads the recorded job's CloudWatch stream and stores typed measurements
   plus hash-addressed original event envelopes.
4. Agents inspect step counts and measurements using `InspectSimulation`.
   Grafana panels 30–35 show queue state, progress, freshness and reservations.
5. The worker retains step traces and a manifest, uploads artifacts, and writes
   `upload-receipt.json` last. The S3 prefix in the initial response is a planned
   destination; it is not evidence that those objects already exist.
6. Cancellation acknowledgement is not cleanup proof. `cleanupConfirmed` becomes
   true when AWS reports the job terminal. This does not mean the shared Batch
   compute environment or its instances have been deleted.

The configured reservation limit caps **admitted reservations**, not measured AWS billing.
Reservations are not refunded automatically, including after completion or an
ambiguous response. Configure them conservatively; validate queue capacity,
image startup, idle instances, storage and logs separately before paid execution.
AWS job timeouts start after the job starts; they do not bound every possible
shared infrastructure charge. If a submission result is lost, the run remains
`SUBMISSION_UNKNOWN`, keeps its reservation, and is never automatically
resubmitted. Locate `csf-<runId>` in AWS before reconciling or cancelling that
ambiguous operation; deleting a database row is not a cleanup procedure.

Native simulator physics, mocked AWS contract tests, and real cloud execution
are separate evidence classes. Neither example trains a model or proves
physical safety. The Isaac example is physics instrumentation, not autonomous
driving.

Sources: [AWS SubmitJob](https://docs.aws.amazon.com/batch/latest/APIReference/API_SubmitJob.html),
[AWS job timeouts](https://docs.aws.amazon.com/batch/latest/userguide/job_timeouts.html),
[AWS GPU jobs](https://docs.aws.amazon.com/batch/latest/userguide/gpu-jobs.html).
