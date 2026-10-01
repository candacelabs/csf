// Package opencode implements the built-in OpenCode agent runtime behind the
// public CandaceOS harness seam.
//
// # What OpenCode is
//
// OpenCode is a separate server process (pinned to [PinnedServerVersion]) that
// owns the agent loop: the model, the tool calls, and the transcript. This
// package never runs a model or a tool. It speaks to that server over HTTP
// with the official generated SDK, attaches to exactly one OpenCode session
// scoped to one workspace directory, and projects that session's provider
// transcript into the normalized Liquid Proto HarnessEvent stream that
// CandaceOS Core consumes.
//
// # What the runtime owns
//
// The runtime owns only what the provider does not:
//
//   - Run correlation. Core submits a prompt carrying a run ID. The runtime
//     remembers which provider message a run ID belongs to and fences every
//     projected event to the run of the turn that is currently active, so a
//     late or historical provider message can never be attributed to a newer
//     run.
//   - Admission. Follow-up guidance is admitted through a bounded FIFO queue
//     whose capacity comes from configuration; an overflow is rejected with
//     [ErrQueueFull] rather than dropped.
//   - Delivery semantics. See "Steering" below.
//   - Publication retry. Host.Publish is allowed to fail. A projected event is
//     recorded as delivered only after the host accepts it, so a failed
//     publication is retried on the next reconciliation instead of being lost,
//     and an idle transition is never published ahead of the terminal event it
//     concludes.
//
// The runtime owns none of Core's authority: approvals, fleet truth,
// reconciliation, durable run state, and UI projection stay in Core. Session
// state here is in memory only; a restart re-attaches and re-hydrates rather
// than replaying history.
//
// # Steering
//
// A prompt carries a delivery mode. HARNESS_DELIVERY_ENQUEUE appends to the
// bounded queue and is drained in FIFO order as each turn completes, with no
// idle event published between queued turns. HARNESS_DELIVERY_IMMEDIATE steers
// the active turn: when the session is busy the runtime aborts the in-flight
// provider turn, marks that abort as operator-intentional so the resulting
// provider "aborted" error is suppressed rather than surfaced as a failure,
// and submits the replacement prompt in its place. This is the capability the
// factory advertises as HARNESS_CAPABILITY_ACTIVE_TURN_STEERING.
//
// Abort clears queued guidance, keeps the aborting run's fence so the idle
// event that concludes it carries the right run ID, and leaves the session
// ready for new work.
//
// # Concurrency model
//
// Every mutation of session state happens on one command goroutine reached
// through an unbuffered mailbox, so no session field needs a lock and no
// operation observes a torn state. Start, Activate, Send, Abort, and Close are
// safe to call concurrently; each blocks until the command goroutine has run
// it. Provider HTTP calls that must be ordered against session state (prompt
// submission, abort, reconciliation) deliberately run on that goroutine under
// the configured request timeout, which is what makes "publish the terminal
// event before accepting newer guidance" observable rather than racy.
//
// Two background goroutines are started by Activate and stopped by Close: a
// poller that reconciles the transcript on the configured interval, and an
// event-stream watcher that collapses provider server-sent events into
// reconciliation wakeups. Close cancels both, waits for them, and is
// idempotent.
package opencode
