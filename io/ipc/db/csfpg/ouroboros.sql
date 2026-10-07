-- Ouroboros: the mining loop's ledger; candace/services/ouroboros owns the
-- rules. Every instant is a parameter the service read from its clock.

-- name: GetOuroborosItem :one
SELECT * FROM csf_ouroboros_items WHERE miner = sqlc.arg(miner) AND item = sqlc.arg(item);

-- name: ListOuroborosItems :many
SELECT * FROM csf_ouroboros_items WHERE miner = sqlc.arg(miner) ORDER BY item;

-- name: ListOuroborosItemsSince :many
SELECT * FROM csf_ouroboros_items WHERE mined_at >= sqlc.arg(since) ORDER BY mined_at, miner, item;

-- name: UpsertOuroborosItem :exec
INSERT INTO csf_ouroboros_items (miner, item, byte_size, mined_at)
VALUES (sqlc.arg(miner), sqlc.arg(item), sqlc.arg(byte_size), sqlc.arg(mined_at))
ON CONFLICT (miner, item) DO UPDATE SET
    byte_size = EXCLUDED.byte_size,
    mined_at = EXCLUDED.mined_at;

-- A finding already recorded is not recorded again: the detector mines a
-- grown item whole, so repeats are expected and count no row.
-- name: InsertOuroborosFinding :execrows
INSERT INTO csf_ouroboros_findings (miner, rule, subject, item, severity, scope, finding, found_at)
VALUES (sqlc.arg(miner), sqlc.arg(rule), sqlc.arg(subject), sqlc.arg(item), sqlc.arg(severity), sqlc.arg(scope), sqlc.arg(finding), sqlc.arg(found_at))
ON CONFLICT (miner, rule, subject, item) DO NOTHING;

-- name: ListOuroborosFindings :many
SELECT * FROM csf_ouroboros_findings ORDER BY found_at DESC, miner, rule, subject, item LIMIT sqlc.arg(row_limit);

-- name: ListOuroborosFindingsSince :many
SELECT * FROM csf_ouroboros_findings WHERE found_at >= sqlc.arg(since) ORDER BY found_at, miner, rule, subject, item;

-- name: GetOuroborosProposal :one
SELECT * FROM csf_ouroboros_proposals WHERE ticket = sqlc.arg(ticket) AND proposal = sqlc.arg(proposal);

-- name: UpsertOuroborosProposal :one
INSERT INTO csf_ouroboros_proposals (ticket, proposal, miner, corpus, instances, verdict, reason, checked_at)
VALUES (sqlc.arg(ticket), sqlc.arg(proposal), sqlc.arg(miner), sqlc.arg(corpus), sqlc.arg(instances), sqlc.arg(verdict), sqlc.arg(reason), sqlc.arg(checked_at))
ON CONFLICT (ticket, proposal) DO UPDATE SET
    miner = EXCLUDED.miner,
    corpus = EXCLUDED.corpus,
    instances = EXCLUDED.instances,
    verdict = EXCLUDED.verdict,
    reason = EXCLUDED.reason,
    checked_at = EXCLUDED.checked_at
RETURNING *;

-- The labeler's queue: proposals whose ticket named no instance in the corpus.
-- name: ListOuroborosProposalsNeedingLabels :many
SELECT * FROM csf_ouroboros_proposals WHERE verdict = 'needs_labels' ORDER BY checked_at, ticket, proposal;

-- name: ListOuroborosProposals :many
SELECT * FROM csf_ouroboros_proposals ORDER BY checked_at DESC, ticket, proposal LIMIT sqlc.arg(row_limit);

-- name: InsertOuroborosFixer :one
INSERT INTO csf_ouroboros_fixers (assignment_id, ticket, proposal, miner, model, started_at, outcome)
VALUES (sqlc.arg(assignment_id), sqlc.arg(ticket), sqlc.arg(proposal), sqlc.arg(miner), sqlc.arg(model), sqlc.arg(started_at), 'running')
RETURNING *;

-- name: UpdateOuroborosFixer :execrows
UPDATE csf_ouroboros_fixers
SET finished_at = sqlc.narg(finished_at),
    outcome = sqlc.arg(outcome),
    pull_request_url = sqlc.arg(pull_request_url),
    cost_usd_micros = sqlc.arg(cost_usd_micros),
    input_tokens = sqlc.arg(input_tokens),
    output_tokens = sqlc.arg(output_tokens),
    seconds = sqlc.arg(seconds)
WHERE assignment_id = sqlc.arg(assignment_id);

-- name: ListOuroborosFixersRunning :many
SELECT * FROM csf_ouroboros_fixers WHERE outcome = 'running' ORDER BY started_at, assignment_id;

-- name: ListOuroborosFixersStartedSince :many
SELECT * FROM csf_ouroboros_fixers WHERE started_at >= sqlc.arg(since) ORDER BY started_at, assignment_id;

-- name: ListOuroborosFixersForTicket :many
SELECT * FROM csf_ouroboros_fixers WHERE ticket = sqlc.arg(ticket) ORDER BY started_at, assignment_id;

-- name: ListOuroborosFixers :many
SELECT * FROM csf_ouroboros_fixers ORDER BY started_at DESC, assignment_id LIMIT sqlc.arg(row_limit);

-- name: GetOuroborosMerge :one
SELECT * FROM csf_ouroboros_merges WHERE pull_request = sqlc.arg(pull_request) AND head_sha = sqlc.arg(head_sha);

-- name: InsertOuroborosMerge :one
INSERT INTO csf_ouroboros_merges (pull_request, head_sha, author_sessions, merger, merged, exit_code, reason, recorded_at)
VALUES (sqlc.arg(pull_request), sqlc.arg(head_sha), sqlc.arg(author_sessions), sqlc.arg(merger), sqlc.arg(merged), sqlc.arg(exit_code), sqlc.arg(reason), sqlc.arg(recorded_at))
RETURNING *;

-- name: ListOuroborosMerges :many
SELECT * FROM csf_ouroboros_merges ORDER BY recorded_at DESC, pull_request, head_sha LIMIT sqlc.arg(row_limit);

-- name: UpsertOuroborosSeries :exec
INSERT INTO csf_ouroboros_series (series, day, value, low, high, numerator, denominator, computed_at)
VALUES (sqlc.arg(series), sqlc.arg(day), sqlc.narg(value), sqlc.narg(low), sqlc.narg(high), sqlc.arg(numerator), sqlc.arg(denominator), sqlc.arg(computed_at))
ON CONFLICT (series, day) DO UPDATE SET
    value = EXCLUDED.value,
    low = EXCLUDED.low,
    high = EXCLUDED.high,
    numerator = EXCLUDED.numerator,
    denominator = EXCLUDED.denominator,
    computed_at = EXCLUDED.computed_at;

-- name: ListOuroborosSeries :many
SELECT * FROM csf_ouroboros_series WHERE series = sqlc.arg(series) ORDER BY day;
