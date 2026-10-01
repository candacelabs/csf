# CandaWS

Five parody cloud services, five CSP engines, five generated widgets, one
binary. "Monolithic microservices" taken literally.

```
go run ./examples/widget/candaws                 # http://127.0.0.1:8081
go run ./examples/widget/candaws -pace 0.25      # everything four times faster
go run ./examples/widget/candaws -trouble 0      # nothing fails anywhere
go run ./examples/widget/candaws -seed 7         # a different fleet, reproducibly
go run ./examples/widget/candaws -goroutines 5s  # the scheduler's own census
```

`-pace` is accepted in `[0.001, 1000]` and refused by name outside it, rather
than passed on to become an engine complaining about an interval nobody chose.
`-trouble` reads a negative value as zero, and the banner prints the pace and
trouble the fleet is actually running at.

| Service | Category parodied | Engine shape |
|---|---|---|
| [Yakshave](yakshave) | continuous-delivery pipeline | a chain of stage goroutines, closed from the head |
| [Queuecumber](queuecumber) | managed message queue | a broker answering lease requests on their own reply channels |
| [Blobfish](blobfish) | object store | a coordinator that stops counting at the write quorum |
| [Coldstart](coldstart) | function-as-a-service | a dispatcher that spawns and reaps instance goroutines |
| [Dashbored](dashbored) | metrics and dashboards | a fan-in with a counted shutdown |

[`docs/fleet.md`](docs/fleet.md) is the design — the roster, the build order and
the ontology table behind each service. A sixth, Roundabout, is deliberately not
built: it is the reserved task for the agentic A/B benchmark, and
[`docs/bench.md`](docs/bench.md) is grader-side.

## What was written by hand, and what was not

Every card is generated, from the five `.widget` documents in
[`docs/`](docs) through `bash pkg/widget/gen.sh`. The state, the reducers, the
bindings, the labels, the SVG scenes, the motion gates, the legends and every
word on every card come out of those documents; each service directory's
`view.templ`, `view_templ.go` and `widget.gen.go` open with a generated marker
and are overwritten on the next run.

What is hand-written is each service's **engine** and the ten-line **seam**
between it and its card. `seam.go` in each package is the only file that knows
both halves exist: it maps one view onto the wire field names the document
declared, using the generated constants, so renaming a field in a document is a
compile error rather than a card that silently stops updating. No engine file
names a region, a wire name or a field spelling; no generated file knows there
are goroutines behind it.

`main.go` and `fleet.go` are this host: they build the five engines, register
the five widgets, resolve the six sources their declared streams name — six and
not five, because Yakshave declares two — and serve the result.
[`../hosting`](../hosting) is the plumbing both this host and the SDK's smaller
one share: the Origin allowlist, the palette resolution, the region rendering
and the router. Neither host builds an `http.ServeMux`; there is exactly one
correct arrangement of those two routes, and two copies of it was two chances to
get the mount path or the ordering wrong.

## What a `ps`-eye view shows

One process. One port. No container, no sidecar, no service mesh, and no
inter-service call that is not a channel send.

Inside it, measured with `-goroutines`:

```
candaws: 47 goroutines at steady state, 6s in
```

That is the whole fleet with **no browser connected**: five engines, the HTTP
listener, the signal watcher and the Go runtime's own. It breaks down as the
sum of what each engine documents, plus one goroutine per engine holding its
`Run`:

| Service | Goroutines | What they are |
|---|---|---|
| Yakshave | 11 | two feeds, an observer, a meter, the head, a collector, four stages, `Run` |
| Queuecumber | 8 | a feed, the broker, the expiry sweep, the producer, three workers, `Run` |
| Blobfish | 9 | a feed, an observer, the coordinator, the repairer, the bucket, three zones, `Run` |
| Coldstart | 4 + n | a feed, the dispatcher, the caller, `Run` — plus one per live instance and one per in-flight call, which come and go by design |
| Dashbored | 8 | a feed, an observer, the aggregator, the alerter, three collectors, `Run` |

A browser session adds one session goroutine plus one per effect, and this host
opens six. The end-to-end specification measures it rather than asserting it
from a table: against a bare test binary's six goroutines, the fleet plus one
live session runs at **58** — the same 40-odd, plus the session, plus its six
sources, plus whatever Coldstart happens to have warm.

Registering a sixth service would add its engine's goroutines, one fragment,
some event names and a slice index. It would not add a port, a process or a
deployment.

## What is actually checked

- **Engine specifications, `-race` green, one suite per service.** Every one of
  the five has a file that starts no goroutine at all, because the interesting
  half of each engine is a pure function: Yakshave's stage registry and its view
  fold, Queuecumber's conservation law, Blobfish's replica rule table,
  Coldstart's temperature ladder, Dashbored's reservoir, histogram and merge.
  The running specifications are then about the concurrency and nothing else.
- **Card assertions on rendered output only.** Every service has a `card_test.go`
  that mounts its widget through [`pkg/widget/widgettest`](../../../pkg/widget/widgettest)
  and asserts substrings, counts, declaration order and the landmark — never
  source. A generated card and a hand-written one share no file names and no
  formatting, and what comes out of `Render` is the only thing both can be held
  to. The BEN probe's own fragment fixture needs exactly that shape.
- **`live_test.go` opens a real WebSocket** against the real handler — a real
  handshake, real protobuf frames, all five engines running — and asserts that
  the pipeline card carries a stage the chain is actually in, that the console
  card carries an ingest rate the collectors actually produced, that a browser's
  own toggle comes back on the same connection, and that the prewarm command,
  which changes no widget state at all, reaches an engine and warms something.
- **`gen.sh --check`** asserts every committed generated file is byte-identical
  to a fresh generation, so a document edited without regenerating fails.

## The one thing that is not here

Roundabout. Building any part of it voids the benchmark probe.
