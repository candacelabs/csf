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
// Dispatch is list scheduling (pkg/listsched) of the frontier, less the held
// slices, onto sessions within an admission the slice dispatcher derives.
// Rank is lexicographic: the upward critical-path
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
// # The slice dispatcher
//
// The dispatcher is a pass, [DispatchService.Dispatch], that the binary
// runs as a cron trigger every [DispatcherInterval] and after every merge
// the merge train makes, and once on start. A pass marks merged every slice
// whose recorded pull request has merged ([WithMergeCheck]), measures each
// limit ([Limit]) and launches the frontier — less the held slices and the
// slices whose provider's rate limit admits no launches — up to the
// admission: the dispatched sessions running plus the fewest launches any
// unscoped limit allows. The limits are the harness's launch check (the
// worker cap of idle cores and the disk floor; a held admission admits
// none), and those the binary adds: the mining loop's daily budget
// ([DailyBudgetLimit]) and the rate-limit headroom of the provider its
// newest rate_limit_event came from, read from the session logs
// ([RateLimit]). A rate limit is scoped to that provider, so one provider's
// event never gates another's launches: it drops its own provider's slices
// from the frontier rather than lowering the admission. Each limit records
// its derivation, and a limit that cannot be measured admits no launch.
// Between passes, enqueues, merges and session ends launch within the latest
// admission at once.
//
// Pause and resume stop and restart every launch; hold and release keep one
// slice from being launched. Each control carries its reason, is written to
// csf_dispatch_controls and replayed on start. AddSlice, the four controls
// and the snapshot are typed operations, served as MCP tools
// ([DispatchService.Tools]) and HTTP routes ([DispatchService.Register]);
// every pass and control publishes the
// [Snapshot] to the binary's sink, which writes [SnapshotFile].
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
// ObserveSession, which the host wires as a harness session observer. The
// pass reads what has no event: the limits and the pull requests' state.
package dispatch
