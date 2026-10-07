-- EVAL-SUITE: the held-out suite and the scores of csf builds on it;
-- candace/services/ouroboros/evaluate owns the rules. Every instant is a
-- parameter the service read from its clock.

-- name: InsertEvalSuite :exec
INSERT INTO csf_eval_suites (version, selected_at, suite)
VALUES (sqlc.arg(version), sqlc.arg(selected_at), sqlc.arg(suite));

-- name: GetLatestEvalSuite :one
SELECT * FROM csf_eval_suites ORDER BY version DESC LIMIT 1;

-- name: ListEvalSuites :many
SELECT * FROM csf_eval_suites ORDER BY version;

-- A replay recorded again (a shard rerun) replaces the earlier reading.
-- name: UpsertEvalReplay :exec
INSERT INTO csf_eval_replays (build, suite_version, ticket, node, assignment_id, tool_calls, episodes, recall, cost_usd_micros, seconds, recorded_at)
VALUES (sqlc.arg(build), sqlc.arg(suite_version), sqlc.arg(ticket), sqlc.arg(node), sqlc.arg(assignment_id), sqlc.arg(tool_calls), sqlc.arg(episodes), sqlc.arg(recall), sqlc.arg(cost_usd_micros), sqlc.arg(seconds), sqlc.arg(recorded_at))
ON CONFLICT (build, suite_version, ticket) DO UPDATE SET
    node = EXCLUDED.node,
    assignment_id = EXCLUDED.assignment_id,
    tool_calls = EXCLUDED.tool_calls,
    episodes = EXCLUDED.episodes,
    recall = EXCLUDED.recall,
    cost_usd_micros = EXCLUDED.cost_usd_micros,
    seconds = EXCLUDED.seconds,
    recorded_at = EXCLUDED.recorded_at;

-- name: ListEvalReplays :many
SELECT * FROM csf_eval_replays
WHERE build = sqlc.arg(build) AND suite_version = sqlc.arg(suite_version)
ORDER BY ticket;

-- name: UpsertEvalScore :exec
INSERT INTO csf_eval_scores (build, suite_version, started_at, finished_at)
VALUES (sqlc.arg(build), sqlc.arg(suite_version), sqlc.arg(started_at), sqlc.arg(finished_at))
ON CONFLICT (build, suite_version) DO UPDATE SET
    started_at = EXCLUDED.started_at,
    finished_at = EXCLUDED.finished_at;

-- name: ListEvalScores :many
SELECT * FROM csf_eval_scores ORDER BY finished_at, build, suite_version;
