CREATE TABLE parents (session_id TEXT NOT NULL, id INTEGER NOT NULL);
CREATE UNIQUE INDEX parents_session_id_id ON parents (session_id, id);
CREATE TABLE children (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    parent_id INTEGER NOT NULL,
    FOREIGN KEY (session_id, parent_id) REFERENCES parents(session_id, id)
);
CREATE TABLE deliveries (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    lease_until INTEGER,
    next_attempt_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX deliveries_pending_name ON deliveries (name)
    WHERE status = 'pending';
CREATE INDEX deliveries_due ON deliveries (COALESCE(lease_until, next_attempt_at), id DESC)
    WHERE status IN ('pending', 'running');
