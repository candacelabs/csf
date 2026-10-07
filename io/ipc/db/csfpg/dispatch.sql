-- The slice graph: the dispatch service's persisted work queue. The service
-- owns the graph in process and writes every change through; on start it
-- reads the whole graph back. No read filters by caller: one dispatch
-- service owns one harness process.

-- name: InsertSlice :one
INSERT INTO csf_slices
 (slice_id, sequence, title, recipe, touch_set, provenance, state, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) RETURNING *;

-- name: UpdateSliceState :one
UPDATE csf_slices SET state = $2, assignment_id = $3, pull_request_url = $4,
 attempts = $5, checkpoint = $6, error = $7, updated_at = $8
WHERE slice_id = $1 RETURNING *;

-- name: ListSlices :many
SELECT * FROM csf_slices ORDER BY sequence;

-- name: InsertSliceEdge :exec
INSERT INTO csf_slice_edges (from_slice_id, to_slice_id, relation) VALUES ($1, $2, $3)
ON CONFLICT (from_slice_id, to_slice_id, relation) DO NOTHING;

-- name: ListSliceEdges :many
SELECT * FROM csf_slice_edges ORDER BY from_slice_id, to_slice_id, relation;

-- name: InsertIntent :one
INSERT INTO csf_intents (intent_id, slice_id, intent, created_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (intent_id) DO UPDATE SET slice_id = EXCLUDED.slice_id, intent = EXCLUDED.intent
RETURNING *;

-- name: ListIntents :many
SELECT * FROM csf_intents ORDER BY created_at, intent_id;

-- name: InsertDispatchControl :one
INSERT INTO csf_dispatch_controls (sequence, action, slice_id, reason, recorded_at)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: ListDispatchControls :many
SELECT * FROM csf_dispatch_controls ORDER BY sequence;
