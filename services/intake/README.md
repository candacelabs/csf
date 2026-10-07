# intake — GitHub events wake the owning agent

`services/intake` turns actionable GitHub activity on a pull request or issue
into a typed `candace.intake.v1.Event` and delivers it through the
[relay](../relay/README.md) to the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) that owns that pull request or issue.
An idle in-process [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) blocked in `Messenger.Receive` wakes when the event
arrives; a host or network [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) finds it at its next inbox fetch.

| Kind | GitHub source |
|---|---|
| `EVENT_KIND_COMMENT` | `IssueCommentEvent` (created) on a pull request or an issue |
| `EVENT_KIND_REVIEW` | `PullRequestReviewEvent` (submitted review, any state) |
| `EVENT_KIND_REVIEW_COMMENT` | `PullRequestReviewCommentEvent` (inline diff comment) |
| `EVENT_KIND_CHECK_FAILURE` | a workflow run whose conclusion is `failure`, one event per attempt |

## The webhook receiver

`csf serve` [mounts](../../csf/docs/generated/ontology_cgen.md#term-mount) `WebhookReceiver` at `POST /api/intake/github`. GitHub
delivers there as things happen; nothing polls.

1. Verify: `X-Hub-Signature-256` must be the HMAC-SHA256 of the body under
   `github.webhook_secret` in `<state>/providers.json` (owner-only),
   compared in constant time.
2. Keep once: the raw body is kept as `deliveries/<X-GitHub-Delivery>.json`;
   a redelivery of a kept GUID is a duplicate.
3. Record: each delivery and each typed event is a record of the GitHub event
   stream.
4. Route: `SessionRoute` and `MergeRoute` take each event to whoever acts on
   it.
5. Recover: at start, every delivery since the last kept one that never
   arrived is redelivered.

A missing secret refuses every delivery with `no_webhook_secret`; a missing,
malformed or wrong signature is `bad_signature`.

The GitHub event stream is the run directory
`<state>/00000000-0000-0000-0000-000000000401`: an `events.jsonl` and the kept
deliveries, and no `run.json`, so it is never resumed or shown as a
[session](../../csf/docs/generated/ontology_cgen.md#term-session). `csf events -assignment 00000000-0000-0000-0000-000000000401`
prints it, and the [mining loop](../../csf/docs/generated/ontology_cgen.md#term-ouroboros) mines it with every other run's log.

| Kind | Delivery | [Action](../../csf/docs/generated/ontology_cgen.md#term-action) | Routed to |
|---|---|---|---|
| `EVENT_KIND_PULL_REQUEST_OPENED` | [`pull_request`](../../csf/docs/generated/ontology_cgen.md#term-pull_request) | opened | |
| `EVENT_KIND_PULL_REQUEST_MERGED` | [`pull_request`](../../csf/docs/generated/ontology_cgen.md#term-pull_request) | closed, merged | dispatcher |
| `EVENT_KIND_PULL_REQUEST_CLOSED` | [`pull_request`](../../csf/docs/generated/ontology_cgen.md#term-pull_request) | closed | |
| `EVENT_KIND_COMMENT` | `issue_comment` | created | owning session |
| `EVENT_KIND_REVIEW` | `pull_request_review` | submitted | owning session |
| `EVENT_KIND_REVIEW_COMMENT` | `pull_request_review_comment` | created | owning session |
| `EVENT_KIND_CHECK_RUN_COMPLETED` | `check_run` | completed | owning session |
| `EVENT_KIND_CHECK_SUITE_COMPLETED` | `check_suite` | completed | owning session |
| `EVENT_KIND_PUSH` | `push` | | |
| `EVENT_KIND_ISSUE_OPENED` | `issues` | opened | |
| `EVENT_KIND_ISSUE_CLOSED` | `issues` | closed | |
| `EVENT_KIND_RELEASE_PUBLISHED` | `release` | published | [Workbench](../../csf/docs/generated/ontology_cgen.md#term-bench) |

The dispatcher takes a merge only when its base is main, and the owning
session takes a comment only on a pull request and a check only when it
failed. The owning session is the live session whose branch is the event's
head branch, or whose recorded pull request is the event's. A csf release
published after the host started shows on the [Workbench](../../csf/docs/generated/ontology_cgen.md#term-bench) as the notice that
`csf upgrade` installs it. `/metrics` exports `csf_github_deliveries_total`,
`csf_github_events_total` and `csf_github_event_latency_seconds`, each with a
panel in the CSF dashboard's GitHub row.

## Why a poller as well

Webhooks need a public ingress route to the receiving process; that is a
trust-model change the operator makes, not this [service](../../csf/docs/generated/ontology_cgen.md#term-service). On a host without
it, the poller dials out to the GitHub REST API instead and needs no listener
at all:

- each feed (`/repos/{owner}/{name}/events`, `/repos/{owner}/{name}/actions/runs?status=failure`)
  is requested with `If-None-Match`, so an unchanged feed answers `304` and
  does not count against the rate limit;
- a round never runs sooner than GitHub's `X-Poll-Interval`;
- a spent primary limit (`X-RateLimit-Remaining: 0`) backs off until
  `X-RateLimit-Reset`; a secondary limit backs off for `Retry-After`; no wait
  exceeds `WithMaxBackoff` (default one hour).

## Mounting it (bare binary first)

```go
client, err := ipchttp.NewHTTPClient(ipcnet.NewHostNetwork()) // the only way out
core, err := relay.NewRelay[*intakev1.Event]()
messenger, err := relay.NewMessenger[*intakev1.Event](core)
routes, err := intake.NewStaticRoutes(intake.Route{Repository: "owner/name", Number: 12, Agent: "reviewer"})
service, err := intake.NewEventIntake(
    intake.WithHTTPClient(client),
    intake.WithRelay(core, messenger),
    intake.WithRoutes(routes),
    intake.WithRepositories(routes.Repositories()...),
    intake.WithToken(token), // read by the binary, never by the service
)
host.Mount("relay", core)     // before the intake: it registers on Start
host.Mount("intake", service) // starts the poller and the dispatcher
```

[`app/intake`](../../app/intake/cmd/main.go) is the runnable composition:

```sh
CSF_INTAKE_GITHUB_TOKEN=… go run ./app/intake/cmd --routes 'candacelabs/example#12=reviewer,candacelabs/example=triage'
```

With no [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) attached it registers a journal in each routed [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)'s place and
logs one JSON line per delivered event. It is not deployed anywhere.

## Contracts

- `IEventQueue` — deduplication and hand-off between poller and dispatcher.
  `MemoryEventQueue` is the implementation; a durable one in [csfpg](../../csf/docs/generated/ontology_cgen.md#term-csfpg) must keep
  the semantics written on the interface.
- `IRoutes` — which [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) owns a subject. `StaticRoutes` is the configured
  table until [agents](../../csf/docs/generated/ontology_cgen.md#term-agent)' ownership claims are stored.
- `IAgentDirectory` — the relay's `Register`/`Resolve`; `*relay.Relay[*intakev1.Event]`
  satisfies it.

## Limits

- The queue and deduplication are kept in [memory](../../csf/docs/generated/ontology_cgen.md#term-memory). Events older than the
  [service](../../csf/docs/generated/ontology_cgen.md#term-service)'s start are ignored by default (`WithBacklogSince`), so a restart
  does not replay the feed — and events that arrived while it was down are
  not delivered either.
- Delivery is at most once: an event whose [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) is not registered when it is
  dispatched is counted in `Status().Undeliverable` and dropped.
- Only the first page of each feed is read per round; a repository busier than
  100 events per poll interval loses the overflow.
- The counters are exposed through `Status()` only; there are no Prometheus
  series or dashboard yet.
