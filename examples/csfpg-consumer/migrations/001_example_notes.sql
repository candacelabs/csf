-- Copyright 2026 Candace Labs
--
-- An example consumer migration: copy this directory as the starting point
-- for an application that embeds CSF. It is numbered independently of CSF's
-- schema (candace/ipc/db/csfpg/schema/001_init.sql) and recorded in the
-- consumer's own version table, here csfpg_consumer_schema_version, never in
-- CSF's csf_schema_version. Apply it after csfpg.ApplySchema with any
-- migration tool and its own version table.
CREATE TABLE example_notes (
    note_id TEXT PRIMARY KEY CHECK (note_id <> ''),
    body TEXT NOT NULL CHECK (body <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
