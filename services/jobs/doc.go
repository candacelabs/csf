// Copyright 2026 Candace Labs

// Package jobs is the harness job ledger: the durable record of requests
// admitted to external executors, kept in the tables csf_jobs,
// csf_job_budgets, csf_job_metric_definitions, csf_job_measurements and
// csf_job_trace_deliveries. Their DDL is part of CSF's schema in
// ipc/db/csfpg/schema/001_init.sql; ipc/db/csfpg/jobs.sql holds the queries.
//
// # Contract
//
// A job is an admitted request to an external executor. The ledger records,
// for every job:
//
//   - its identity, kind and executor name, chosen by the consumer;
//   - the consumer's typed request (Spec), stored as JSON with its SHA-256;
//   - a budget reservation taken from one immutable budget account at
//     admission, never released by the ledger;
//   - progress as completed units out of an admitted total, which only grows;
//   - the executor's external identity, log stream and log cursor;
//   - whether cancellation was requested and whether cleanup of the external
//     resource was confirmed;
//   - measurements of declared metrics, each carrying the SHA-256 of the
//     evidence it was read from;
//   - trace and log projection state, and a delivery ledger that refuses to
//     resend an immutable trace after an ambiguous outcome.
//
// A consumer builds one JobLedger[Spec] per request type. Spec is any
// JSON-encodable type; the ledger is restricted to the kinds it was built
// with, so it never decodes another consumer's request with the wrong type.
// Several ledgers over different Spec types may share one database.
//
// Admission (Submit) is idempotent per job identity: repeating an admission
// with the same kind, executor, total units and encoded request returns the
// recorded job; any other reuse fails with ErrIdentityReused. Admission
// fails with ErrBudgetExhausted when the account cannot cover the
// reservation and with ErrBudgetChanged when the account exists with
// another limit.
//
// # Executors
//
// Executors are capabilities granted to the ledger through its constructor;
// the ledger owns no goroutine, listener or client. Reconcile performs one
// bounded pass over every registered executor in registration order, and
// the caller decides how often to call it.
//
//   - IRemoteExecutor serves providers without an idempotency key, such as
//     AWS Batch. A pending job is claimed and committed as submitting
//     before Submit is called, so a lost reply leaves the job in
//     submission_unknown with its reservation held; it is never resubmitted.
//     Observe and ReadLog then follow the external job until it is terminal.
//   - IHostExecutor serves executors on this host whose operations are
//     idempotent by a durable name, such as local Docker. One job at a time
//     is reconciled inside a transaction holding an advisory lock for that
//     executor, so overlapping processes never drive the same job.
//
// AWSBatchExecutor and LocalDockerExecutor are the two executors this
// package ships. The local Docker executor consumes the Docker Engine SDK
// through IDockerEngine; the binary owns the client.
//
// # Observations
//
// Record applies an Observation — metric definitions, measurements and phase
// reports in order — atomically under the job's row lock. A measurement must
// name a declared metric and a step within the admitted total; the progress
// metric must equal its step. A completed phase requires every admitted
// unit; a failed phase overrides an earlier success; a cancelled phase is
// accepted only after cancellation was requested. A host executor that sees
// its external resource exit without a complete success report fails the job.
//
// The ledger never closes the database it was given; the binary that opened
// the pool through ipc/db/csfpg closes it after the ledger's callers stop.
package jobs
