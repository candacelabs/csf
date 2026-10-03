// Copyright 2026 Candace Labs

// Package intake wakes the agent that owns a pull request or issue when
// something actionable happens to it: a comment, a review, an inline review
// comment, or a failed CI run.
//
// It is a tailnet-side poller, not a webhook receiver. Accepting GitHub's
// webhooks would need public ingress, which is a trust-model change this
// service deliberately does not make; instead it polls the GitHub REST API
// with conditional requests (an unchanged feed answers 304 and costs no rate
// limit), honours the X-Poll-Interval GitHub asks for, and backs off until the
// rate-limit window resets when it is exhausted.
//
// # Flow
//
// poll → normalize → deduplicate → queue → route → relay.
//
// Each feed (CS-6: a registry in data, see feeds.go) turns one GitHub
// response into typed candace.intake.v1 Events. The [IEventQueue] drops an
// event whose identifier it has already accepted and holds the rest; the
// [IRoutes] table names the agent registered for the event's pull request or
// issue; the relay delivers the event into that agent's inbox. An in-process
// agent is woken by its blocked Receive returning; a host or network agent
// finds it at its next inbox fetch.
//
// # Capabilities and goroutines
//
// The GitHub API is reached only through the [ipchttp.IHTTPClient] the binary
// grants — normally one built by ipchttp.NewHTTPClient from an ipc/net dialer.
// The service reads no environment and opens no socket of its own. It is a
// runtime service: [EventIntake.Start] starts exactly two goroutines on the
// scope it is given, the poller and the dispatcher, and both return when that
// scope is canceled.
//
// # Limits
//
// The queue and the deduplication memory are in memory (a durable queue in
// csfdb is a follow-up), so a restart forgets what was delivered; the
// default backlog cut-off — events older than the service's start are
// ignored — is what keeps a restart from re-waking every agent. Routing is a
// static table until agents' ownership claims are stored. Delivery is at most
// once: an event whose agent is not registered when it is dispatched is
// counted and dropped.
package intake
