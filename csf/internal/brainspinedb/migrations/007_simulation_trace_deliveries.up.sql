CREATE TYPE brainspine_trace_delivery_state AS ENUM
    ('attempted', 'succeeded', 'ambiguous');

CREATE TABLE brainspine_simulation_trace_deliveries (
    destination TEXT NOT NULL CHECK (destination <> ''),
    trace_id TEXT NOT NULL CHECK (trace_id ~ '^[0-9a-f]{32}$'),
    run_id TEXT NOT NULL REFERENCES brainspine_simulations(run_id),
    state brainspine_trace_delivery_state NOT NULL DEFAULT 'attempted',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CHECK ((state = 'attempted') = (finished_at IS NULL)),
    PRIMARY KEY (destination, trace_id)
);
