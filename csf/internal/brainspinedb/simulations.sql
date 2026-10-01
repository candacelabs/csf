-- name: LockSimulationBudget :one
SELECT * FROM brainspine_runs WHERE run_id = $1 FOR UPDATE;

-- name: ReserveSimulationBudget :execrows
UPDATE brainspine_runs SET reserved_usd_micros = reserved_usd_micros + sqlc.arg(amount)::BIGINT
WHERE run_id = sqlc.arg(campaign_id)
AND budget_limit_usd_micros - reserved_usd_micros - charged_usd_micros >= sqlc.arg(amount)::BIGINT;

-- name: InsertSimulation :one
INSERT INTO brainspine_simulations
 (run_id, campaign_id, simulator, executor, steps, job_queue, job_definition, artifact_uri, reservation_usd_micros, timeout_seconds, managed, capture_every, artifact_volume)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING *;

-- name: GetSimulation :one
SELECT * FROM brainspine_simulations WHERE run_id = $1;

-- name: LockSimulation :one
SELECT * FROM brainspine_simulations WHERE run_id = $1 FOR UPDATE;

-- name: ListSimulations :many
SELECT * FROM brainspine_simulations ORDER BY created_at DESC, run_id LIMIT $1;

-- name: ClaimSimulation :one
UPDATE brainspine_simulations SET state = 'submitting', updated_at = now()
WHERE run_id = (SELECT run_id FROM brainspine_simulations
 WHERE state = 'pending' AND executor = 2 ORDER BY created_at, run_id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;

-- name: MarkInterruptedSubmissions :exec
UPDATE brainspine_simulations SET state = 'submission_unknown', reason = 'Submission interrupted; inspect AWS by csf-<run_id> before retrying', updated_at = now()
WHERE state = 'submitting' AND updated_at < now() - interval '2 minutes';

-- name: FinishSimulationSubmission :execrows
UPDATE brainspine_simulations SET state = $2, job_id = $3, reason = $4, updated_at = now()
WHERE run_id = $1 AND state IN ('submitting', 'submission_unknown');

-- name: PollableSimulations :many
SELECT * FROM brainspine_simulations WHERE executor = 2 AND job_id <> ''
AND (state IN ('queued','running','cancelling') OR (state IN ('succeeded','failed','cancelled') AND updated_at > now() - interval '10 minutes'))
ORDER BY updated_at, run_id LIMIT 32;

-- name: UpdateSimulationProvider :exec
UPDATE brainspine_simulations SET state = $2, reason = $3, log_stream = $4,
 cleanup_confirmed = $5, inspection_error = '',
 updated_at = CASE WHEN state <> $2 THEN now() ELSE updated_at END
WHERE run_id = $1;

-- name: SetSimulationInspectionError :exec
UPDATE brainspine_simulations SET inspection_error = $2 WHERE run_id = $1;

-- name: UpdateSimulationLogToken :exec
UPDATE brainspine_simulations SET log_token = $2 WHERE run_id = $1;

-- name: RequestSimulationCancellation :one
UPDATE brainspine_simulations SET cancellation_requested = true,
 state = CASE WHEN state = 'pending' THEN 'cancelled'::brainspine_simulation_state ELSE state END,
 cleanup_confirmed = CASE WHEN state = 'pending' AND (executor = 2 OR managed) THEN true ELSE cleanup_confirmed END,
 updated_at = now()
WHERE run_id = $1 RETURNING *;

-- name: CountSimulationStates :many
SELECT simulator, executor, state, count(*)::BIGINT AS jobs
FROM brainspine_simulations GROUP BY simulator, executor, state;

-- name: LatestSimulationProgress :many
SELECT DISTINCT ON (simulator, executor) simulator, executor, steps, completed_steps, updated_at
FROM brainspine_simulations ORDER BY simulator, executor, created_at DESC, run_id;

-- name: SimulationBudget :one
SELECT budget_limit_usd_micros, reserved_usd_micros FROM brainspine_runs WHERE run_id = $1;

-- name: ListSimulationDefinitions :many
SELECT * FROM brainspine_simulation_definitions WHERE run_id = $1;

-- name: InsertSimulationDefinition :execrows
INSERT INTO brainspine_simulation_definitions(run_id,name,unit,description) VALUES ($1,$2,$3,$4)
ON CONFLICT (run_id,name) DO UPDATE SET name = EXCLUDED.name
WHERE brainspine_simulation_definitions.unit = EXCLUDED.unit AND brainspine_simulation_definitions.description = EXCLUDED.description;

-- name: InsertSimulationMeasurement :execrows
INSERT INTO brainspine_simulation_measurements(run_id,metric,step,value,recorded_at,evidence_hash) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (run_id,metric,step) DO UPDATE SET metric = EXCLUDED.metric
WHERE brainspine_simulation_measurements.value = EXCLUDED.value;

-- name: LatestSimulationMeasurements :many
SELECT DISTINCT ON (metric) * FROM brainspine_simulation_measurements
WHERE run_id = $1 ORDER BY metric, step DESC;

-- name: RecordSimulationProgress :exec
UPDATE brainspine_simulations SET completed_steps = GREATEST(completed_steps, $2),
state = $3, reason = $4, updated_at = now() WHERE run_id = $1;

-- One transaction in the host holds this lock for its bounded Docker iteration.
-- name: LockLocalSimulationWorker :one
SELECT pg_try_advisory_xact_lock(67545231) AS acquired;

-- name: NextLocalSimulation :one
SELECT * FROM brainspine_simulations
WHERE executor = 1 AND managed AND NOT cleanup_confirmed
ORDER BY (state = 'pending'), created_at, run_id LIMIT 1 FOR UPDATE;

-- name: UpdateLocalSimulation :exec
UPDATE brainspine_simulations SET state = $2, job_id = $3, reason = $4,
 cleanup_confirmed = $5, log_stream = $6, inspection_error = '',
 updated_at = CASE WHEN state <> $2 THEN now() ELSE updated_at END
WHERE run_id = $1;

-- name: NextSimulationTrace :one
SELECT * FROM brainspine_simulations WHERE executor = 1
 AND state IN ('succeeded', 'failed', 'cancelled')
 AND (NOT managed OR cleanup_confirmed)
 AND trace_url = '' AND trace_retry_at <= now()
ORDER BY created_at DESC LIMIT 1;

-- name: SetSimulationTrace :exec
UPDATE brainspine_simulations SET trace_url = $2, trace_export_error = $3,
 trace_retry_at = now() + interval '1 minute' WHERE run_id = $1;

-- name: NextSimulationLog :one
SELECT * FROM brainspine_simulations WHERE executor = 1
 AND state IN ('succeeded', 'failed', 'cancelled')
 AND cleanup_confirmed
 AND log_document_id = '' AND log_retry_at <= now()
ORDER BY created_at DESC LIMIT 1;

-- name: SetSimulationLog :exec
UPDATE brainspine_simulations SET log_document_id = $2,
 log_projection_error = $3, log_indexed_at = $4,
 log_retry_at = now() + interval '1 minute' WHERE run_id = $1;

-- name: ClaimSimulationTraceDelivery :one
INSERT INTO brainspine_simulation_trace_deliveries(destination, run_id, trace_id)
VALUES (sqlc.arg(destination), sqlc.arg(run_id), sqlc.arg(trace_id))
ON CONFLICT (destination, trace_id) DO NOTHING
RETURNING trace_id;

-- name: GetSimulationTraceDelivery :one
SELECT * FROM brainspine_simulation_trace_deliveries
WHERE destination = sqlc.arg(destination) AND trace_id = sqlc.arg(trace_id);

-- name: FinishSimulationTraceDelivery :one
UPDATE brainspine_simulation_trace_deliveries
SET state = sqlc.arg(state)::brainspine_trace_delivery_state,
 error = sqlc.arg(error), finished_at = now()
WHERE destination = sqlc.arg(destination) AND trace_id = sqlc.arg(trace_id)
 AND state = 'attempted'
 AND sqlc.arg(state)::brainspine_trace_delivery_state IN
     ('succeeded'::brainspine_trace_delivery_state, 'ambiguous'::brainspine_trace_delivery_state)
RETURNING trace_id;
