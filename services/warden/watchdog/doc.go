// Package watchdog turns cluster views into operator alerts.
//
// A Watchdog runs on every node but only acts while the local node is the
// authoritative leader. It observes ClusterView snapshots from a
// warden.IViewSource, tracks per-peer dead/recovery "episodes", records an
// Incident log for the dashboard (implementing warden.IIncidentLog), and
// delivers each incident to a warden.INotifier exactly once per episode with
// a per-(peer, incident-type) cooldown so a flapping node cannot spam the
// operator.
//
// # Concurrency model
//
// Run is a single event-loop goroutine that owns all watchdog decision state
// (open episodes, cooldown timestamps, leadership-epoch tracking, and the
// notification retry queue). That state lives in loop-local variables and is
// never touched by any other goroutine, so the core state machine needs no
// locks and cannot race.
//
// The loop selects over four things: the ViewSource subscription channel, the
// CheckInterval ticker (both from warden.IClock, never a sleep-and-check), a
// results channel carrying notification outcomes, and ctx.Done(). Delivery is
// performed by short-lived goroutines the loop spawns; each reports its result
// back on the results channel, and only the loop mutates dedup/retry
// bookkeeping. On shutdown (ctx cancellation) the loop joins every goroutine
// it spawned before returning, so nothing leaks.
//
// The single exception to loop ownership is the append-only incident log
// itself, which external dashboard handler goroutines read via Incidents().
// It is kept behind a tiny dedicated mutex (see incidentRing) rather than the
// loop's select, so a query never depends on the loop's liveness and can
// never hang before Run starts, while it runs, or after it exits.
package watchdog
