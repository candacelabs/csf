// Copyright 2026 Candace Labs

// Package dreamer is the mining loop's dreaming: the service that keeps the
// slice dispatcher's ready frontier as long as its admission can use, with
// the work most likely to bend the struggle rate down. It generates and
// ranks work; it never executes it, and it never launches a session.
//
// # The pass
//
// [Dreamer.Dream] is one pass, which the binary runs as a cron trigger every
// [DreamInterval], after every merge, and once on start. A pass reads the
// dispatcher's snapshot and slice graph, releases the slices it added on the
// pass before (each is added held, so the Workbench queue shows it with its
// source and rank before the dispatcher can launch it), and derives the
// target: the launches the admission allows now plus the sessions expected
// to finish before the next pass, from the measured enqueue-to-merge
// durations. When the ready frontier is shorter, it asks every source for
// candidates, ranks them and adds the best through the dispatcher's
// AddSlice until it is not.
//
// It adds nothing while it is paused, while the dispatcher is paused, or
// while any of the dispatcher's limits (the daily budget, the harness's load
// and disk floors, the provider's rate) admits no launch. An operator's hold
// is never released: the dreamer releases only its own visibility hold.
//
// # Ranking
//
// A source is a named function value registered with [WithSource]. A
// candidate carries how often the problem it addresses occurs per week and
// the measured outcomes of earlier work on the same class. Its score is
// frequency × fixability × exploration: fixability is the Laplace estimate
// (fixed + 1) / (attempted + 2) over those outcomes, pooled with the
// outcomes of the dreamer's own slices of that source; exploration is the
// strategy's weight on classes nothing has worked on before, and 1 for known
// ones. Fallback sources (the harness's own gaps) are asked only when every
// primary source found nothing.
//
// # Strategy and the held-out sessions
//
// [HeldOut] splits the corpus's runs deterministically. The struggle-class
// source chooses work from the in-sample runs only; [Compare] measures the
// weekly struggle factor on both. [Dreamer.Strategize], run daily, sets the
// exploration weight from the held-out series alone: one plus the number of
// trailing complete weeks whose held-out rate did not fall.
//
// Every pass, control and strategy is a typed [Record], handed to the
// binary's record sink; the latest state is a [Snapshot] the Workbench
// shows.
package dreamer
