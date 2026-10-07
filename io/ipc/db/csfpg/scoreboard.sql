-- SCOREBOARD (#517): the rows `csf scoreboard` writes into csfpg. One reading is
-- (measured_on, commit_sha): the verb deletes the reading's rows and inserts
-- them again, so a rerun replaces the measurement rather than colliding with it
-- (no ON CONFLICT; the delete and the insert are the whole write path).

-- name: DeleteMeterMeasurements :execrows
DELETE FROM csf_meter_measurements
WHERE measured_on = $1 AND commit_sha = $2;

-- name: InsertMeterMeasurement :one
INSERT INTO csf_meter_measurements (measured_on, commit_sha, name, ordinal, measures, now, target, source, moved_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListMeterMeasurements :many
SELECT * FROM csf_meter_measurements
WHERE measured_on = $1 AND commit_sha = $2
ORDER BY ordinal;

-- name: DeleteMeterInvariants :execrows
DELETE FROM csf_meter_invariants
WHERE measured_on = $1 AND commit_sha = $2;

-- name: InsertMeterInvariant :one
INSERT INTO csf_meter_invariants (measured_on, commit_sha, name, satisfied, checked, mode, owner_slice)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListMeterInvariants :many
SELECT * FROM csf_meter_invariants
WHERE measured_on = $1 AND commit_sha = $2
ORDER BY name;
