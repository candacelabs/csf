// Copyright 2026 Candace Labs

// Package intake turns GitHub activity into the typed candace.intake.v1.Event
// and takes each to whoever acts on it. It has two sources.
//
// The [WebhookReceiver] is the one csf serve mounts: GitHub delivers to
// [WebhookPath] as things happen. Each delivery's X-Hub-Signature-256 is
// checked against the webhook secret in constant time; a verified delivery is
// kept once by its X-GitHub-Delivery GUID (its raw body is the corpus copy),
// recorded with its typed events on the GitHub event stream ([Stream]), and
// every event is offered to the [EventRoute]s: [SessionRoute] queues pull
// request feedback in the session that owns the branch, [MergeRoute] marks a
// merge into main on the slice dispatcher. At Start, recovery asks GitHub to
// redeliver every delivery since the last one kept that never arrived. The
// receiver needs public ingress to its one route, which is the operator's
// decision; until then it is proven with signed replays.
//
// The [EventIntake] is a tailnet-side poller for a host without ingress: it
// polls the GitHub REST API with conditional requests (an unchanged feed
// answers 304 and costs no rate limit), honours the X-Poll-Interval GitHub
// asks for, and backs off until the rate-limit window resets when it is
// exhausted. The rest of this comment describes the poller.
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
// The GitHub API is reached only through the [iohttp.IHTTPClient] the binary
// grants — normally one built by iohttp.NewHTTPClient from an ipc/net dialer.
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
