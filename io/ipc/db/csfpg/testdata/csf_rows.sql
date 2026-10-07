-- Copyright 2026 Candace Labs
--
-- One valid row in each table the csfpg specs pin. The specs write it on pgmem
-- after ApplySchema, so every CHECK constraint, foreign key, enum column and
-- default on those tables evaluates at least once.
INSERT INTO csf_documents (content_hash, byte_size, artifact_ref)
VALUES (repeat('b', 64), 42, 'documents/fixture.txt');

INSERT INTO csf_source_revisions (source_id, revision, content_hash, source_uri, title, media_type, license, retrieved_at)
VALUES ('source-fixture', 'v1', repeat('b', 64), 'urn:csf:fixture', 'Fixture source', 'text/plain', 'test-fixture',
    '2026-10-01T00:00:00Z');

INSERT INTO csf_projection_tasks (source_id, revision, status, attempts, lease_generation, last_error)
VALUES ('source-fixture', 'v1', 'failed', 3, 3, 'projection backend unavailable');

INSERT INTO csf_nodes (node_id, kind, symbol_key, title, statement, author_kind, author_ref,
    citation_content_hash, citation_source_id, citation_revision)
VALUES ('node-root', 'evidence', 'fixture.root', 'Root evidence', 'The root is retained.', 'source', 'fixture-fixture',
    repeat('b', 64), 'source-fixture', 'v1');

INSERT INTO csf_nodes (node_id, kind, symbol_key, title, statement, parent_node_id, author_kind, author_ref)
VALUES ('node-child', 'claim', 'fixture.child', 'Child claim', 'The child is retained.', 'node-root', 'model', 'fixture-model');

INSERT INTO csf_edges (from_node_id, to_node_id, relation, author_kind, author_ref, rationale)
VALUES ('node-root', 'node-child', 'supports', 'checker', 'fixture-checker', 'The root supports the child.');

INSERT INTO csf_job_budgets (account, limit_usd_micros, reserved_usd_micros)
VALUES ('budget-fixture', 5000, 2000);

INSERT INTO csf_jobs (job_id, kind, executor, budget_account, request, request_sha256, state, total_units,
    completed_units, reservation_usd_micros, timeout_seconds, managed)
VALUES ('job-fixture', 'overnight.compute', 'aws_batch', 'budget-fixture', '{"shards":4}', repeat('b', 64),
    'cancelling', 50000, 4, 2000, 600, TRUE);

INSERT INTO csf_job_metric_definitions (job_id, name, unit, description)
VALUES ('job-fixture', 'reward', 'points', 'Episode reward');

INSERT INTO csf_job_measurements (job_id, metric, step, value, recorded_at, evidence_hash)
VALUES ('job-fixture', 'reward', 4, 1.5, '2026-10-01T00:00:02Z', repeat('c', 64));

INSERT INTO csf_job_trace_deliveries (destination, trace_id, job_id, state, finished_at)
VALUES ('langfuse', repeat('d', 32), 'job-fixture', 'ambiguous', '2026-10-01T00:00:03Z');

INSERT INTO csf_agent_configurations (agent_id, revision, opensearch_index)
VALUES ('fixture-agent', 2, 'agent-events');

INSERT INTO csf_meter_measurements (measured_on, commit_sha, name, ordinal, measures, now, target, source, moved_by)
VALUES ('2026-10-06', '369d12d', 'consistency', 0, 'mean of satisfied over checked', '0.590', '1.000',
    'consistency', '["#517"]');

INSERT INTO csf_meter_invariants (measured_on, commit_sha, name, satisfied, checked, mode, owner_slice)
VALUES ('2026-10-06', '369d12d', 'code_compiles', 249, 250, 'hard_gate', 'green_main');
