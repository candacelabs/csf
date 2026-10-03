// Copyright 2026 Candace Labs

// Package dispatch is the intent-ranked work queue of one harness process:
// a slice graph, its frontier, and the list scheduling that dispatches the
// frontier onto agent sessions.
//
// # Contract
//
// Work is a slice graph, not a heap. A slice is one unit of work with the
// recipe its session runs. Its depends_on edges form a DAG and are the only
// serial relation: a slice enters the frontier once every slice it depends
// on has merged. Its contends edges are undirected mutual exclusion, declared
// or computed from overlapping touch-sets (path globs and named hotspots):
// two contending slices never run at once. No edge means parallel.
//
// Dispatch is list scheduling (pkg/listsched) of the frontier onto sessions
// within a concurrency cap derived from the host's measured idle cores
// (harness.WorkerCap). Rank is lexicographic: the upward critical-path
// length of the slice in the depends_on graph (HEFT upward rank, unit
// weights until durations have been measured), then the strongest urgency of
// the intents attached to it, then enqueue order. No weight is hand-set; the
// learned term is zero until data exists. A blocked higher-ranked slice does
// not hold back a lower-ranked one that may run (bucketed, not strictly
// serial, order).
//
// An intent is a typed record: statement, scope, urgency, optional deadline
// and the intent it supersedes, with term slots. Route sends an operator
// message to the slice whose touch-set covers the intent's terms: into its
// session when it runs (steer), onto the slice when it does not (queue), as a
// new slice when none covers the terms, or back as already done when the
// intent compiles to no change against main. An URGENT intent preempts: when
// its slice cannot start because the cap is full or a contending slice runs,
// the lowest-ranked running session that ranks below it is asked to cancel
// at its next safepoint; once that session reports CANCELED the slice it ran
// is checkpointed and re-enqueued.
//
// # Ownership
//
// The graph is an in-process store owned by one goroutine, which pops
// commands and session events from one io/inproc.Queue. Every change is
// written through to csfpg when a database was granted, and the whole graph
// is read back on Start, so the queue survives a restart: slices that were
// running in the previous process return to the frontier. The service owns
// its goroutines through its runtime scope; it opens no listener and no pool.
//
// Sessions are reached through ISessionHost, which services/harness's
// AgentSessionService satisfies, and session completion arrives through
// ObserveSession, which the host wires as a harness session observer. There
// is no polling.
package dispatch
