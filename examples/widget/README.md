# widget

The widget SDK's smallest end-to-end host: two generated widgets, one registry,
one page, one binary — and, behind one of the widgets, a real consensus
protocol.

```
cd examples/widget
go run .                      # http://127.0.0.1:8080
go run . -nodes 5             # a five-node cluster; quorum becomes three
go run . -heartbeat 400ms     # a faster protocol, and a faster picture
go run . -chaos 0             # elect once and never fail a node again
```

The raft card is drawing an election that is actually happening.
[`raftdemo/`](raftdemo) runs Raft's election half — terms and votes, no log — as
one goroutine per node inside this process, and one heartbeat round of that
protocol is one snapshot is one round of pulses on the card. A pulse crossing an
edge is a heartbeat that crossed a channel.

Every `-chaos` interval the current leader is crashed and, an interval later,
restarted. The motion gate closes while the survivors campaign, the indicator
turns, the scene's text alternative is rewritten and the term in the stat line
goes up when somebody wins — none of it scripted, and all of it identical to what
a real failure would produce. The button pauses the pulses, and a viewer who has
asked for reduced motion never sees them. The node card's caption alternates
between `reachable` and `unreachable` on a timer, because that widget has
nothing behind it yet.

## What was written by hand, and what was not

`clusterheartbeats/` and `nodestatus/` are generated, from
[`01-cluster-heartbeats.widget`](../../pkg/widget/docs/examples/01-cluster-heartbeats.widget)
and [`02-node-status.widget`](../../pkg/widget/docs/examples/02-node-status.widget)
through `bash pkg/widget/gen.sh`. The state, the reducers, the bindings, the
labels, the SVG scenes, the motion gate, the legend and every word on both cards
come from those two documents. Every file in both directories opens with a
generated marker and is overwritten on the next run.

`relaypipeline/` is generated too, from
[`03-relay-pipeline.widget`](../../pkg/widget/docs/examples/03-relay-pipeline.widget),
and this host does not mount it. It is here because `gen.sh` generates every
exemplar that describes a real widget rather than only the ones something
serves: the relay pipeline is the chain-shaped scene — three nodes, two edges,
no `forbids` on the motion gate — and a generator change that broke that shape
would otherwise be found by whoever next read the document. Compiling it is the
assertion; serving it would be a second demo.

What is hand-written is this host: `main.go` registers the two widgets it serves, resolves
the palette they name, hands the registry the four security decisions a library
may not make for a host, and serves the result. `page.templ` is the page shell,
and it knows nothing about what a widget is — it is handed already-rendered
regions in registration order, which is why the second widget cost a
registration rather than a page edit.

`widget.css` is this page's chrome and nothing else. The widgets' own CSS — the
seven token values, the token classes, the scene's structure and the motion gate
— is `widget.Stylesheet(palette)`, served in front of it: a host that kept its
own copy of that mapping would be hand-maintaining the projection the SDK
derives.

## The streams the widgets cannot open

Each document declares a stream — `widget.cluster.watch` and
`widget.node-status.watch` — and the generated `Register` carries it. Nothing in
either widget opens one, and nothing could: a widget document names no host, no
address and no credential by construction, which is what makes one publishable.
Resolving those names against something real is the host's job, and here
`widget.cluster.watch` resolves to a subscription on the election and
`widget.node-status.watch` resolves to a ticker.

The whole seam is `clusterFields` in `main.go`: eight wire field names against a
fleet view, and nothing else. The engine knows no region, no wire name and no
field spelling; the widget knows no cluster. Either could be replaced without
the other noticing, which is the test of whether a seam is a seam.

## Where the goroutines are

There is no per-widget process, port or connection. A session is one goroutine,
its reducer advances one widget at a time, and each effect gets a goroutine of
its own at the actor boundary — which is the whole of what "monolithic
microservices" cashes out to. Registering a third widget adds a fragment, some
event names and a slice index.

The cluster adds `-nodes` + 2 more: one per member, one for the network, one for
the fleet view. It is one cluster per process and one subscription per session,
so every browser watching is watching the same election rather than a private
copy of one.

## What is actually checked

- `dirty_test.go` replays a log of events through the registry and holds each
  generated widget's dirty declaration against its own markup.
- `live_test.go` opens a **real WebSocket** against the real handler — a real
  handshake, real protobuf frames — waits for the election to elect somebody,
  and asserts the patch that arrives carries the new leader, in the raft region
  and no other, with the origin the wire records as the stream rather than the
  mount. It then crashes the leader and asserts the card reports the outage and
  the replacement, and sends the motion toggle back up the same connection.
- `raftdemo/` has 39 specs of its own, green under `-race`: one leader per term
  across a crash, a recovery and a second crash; a minority that stays
  leaderless rather than appointing itself.

## The other host

[`candaws/`](candaws) is the same SDK carrying five services rather than two:
five CSP engines, five generated cards and one binary, which is where the
"monolithic microservices" claim is actually measured. Both hosts share
[`hosting/`](hosting) — the Origin allowlist, the palette resolution, the region
rendering and the router — because there is exactly one correct arrangement of
those and two copies of it was two chances to get the mount path wrong.

## What the demo does not show

A control bound to anything but a click. A control declares a caption, a trigger
and an event, and nothing that says what kind of element it is, so `change`,
`input` and `submit` have nothing to bind to — the generator refuses those three
by name rather than emitting a binding that could never fire.
`uigen.Refusals()` is the whole list.
