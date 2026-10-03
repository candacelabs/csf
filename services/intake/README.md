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

## Why a poller

Webhooks need a public ingress route to the receiving process; that is a
trust-model change this [service](../../csf/docs/generated/ontology_cgen.md#term-service) does not make. The intake dials out to the
GitHub REST API instead and needs no listener at all:

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
CSF_INTAKE_GITHUB_TOKEN=… go run ./app/intake/cmd --routes 'owner/name#12=reviewer,owner/name=triage'
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

- The queue and deduplication memory are in memory. Events older than the
  [service](../../csf/docs/generated/ontology_cgen.md#term-service)'s start are ignored by default (`WithBacklogSince`), so a restart
  does not replay the feed — and events that arrived while it was down are
  not delivered either.
- Delivery is at most once: an event whose [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) is not registered when it is
  dispatched is counted in `Status().Undeliverable` and dropped.
- Only the first page of each feed is read per round; a repository busier than
  100 events per poll interval loses the overflow.
- The counters are exposed through `Status()` only; there are no Prometheus
  series or dashboard yet.
