-- SCOREBOARD (#517): every program meter the `csf scoreboard` verb writes, one
-- row per meter plus the consistency headline's per-invariant rows. The CSF
-- artifact declares both rows (csf/observability/scoreboard.csf); this
-- migration is their csfpg home, migrations only and sqlc only. A new file
-- rather than an edit of 001_init.sql or 002_eval_suite.sql, because a live
-- database records those as applied and would never see the edit. Every
-- instant is a parameter the service read from its clock, so a measurement is
-- keyed by the day and the commit it was taken at rather than a timestamp.
-- The column is commit_sha, not commit: commit is a reserved word.

-- One row per meter: what it measures, its latest value and target, the query
-- that computes them, and the slices that move it, in the order the verb
-- prints them.
CREATE TABLE csf_meter_measurements (
    measured_on TEXT NOT NULL CHECK (measured_on ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    commit_sha TEXT NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{7,40}$'),
    name TEXT NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    measures TEXT NOT NULL CHECK (length(measures) BETWEEN 1 AND 256),
    now TEXT NOT NULL CHECK (length(now) BETWEEN 1 AND 256),
    target TEXT NOT NULL CHECK (length(target) BETWEEN 1 AND 256),
    source TEXT NOT NULL CHECK (source ~ '^[a-z][a-z0-9_]*$'),
    moved_by JSONB NOT NULL,
    PRIMARY KEY (measured_on, commit_sha, name)
);

-- One row per invariant behind the consistency headline: the instances it is
-- satisfied over and checked over, the gate mode that holds it, and the slice
-- that owns raising it.
CREATE TABLE csf_meter_invariants (
    measured_on TEXT NOT NULL CHECK (measured_on ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    commit_sha TEXT NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{7,40}$'),
    name TEXT NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    satisfied INTEGER NOT NULL CHECK (satisfied >= 0),
    checked INTEGER NOT NULL CHECK (checked > 0),
    mode TEXT NOT NULL CHECK (mode IN ('ratchet', 'hard_gate', 'advisory_until_zero')),
    owner_slice TEXT NOT NULL CHECK (owner_slice ~ '^[a-z][a-z0-9_]*$'),
    CHECK (satisfied <= checked),
    PRIMARY KEY (measured_on, commit_sha, name)
);
