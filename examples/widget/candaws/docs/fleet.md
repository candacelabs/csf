# The CandaWS fleet

A tongue-in-cheek re-creation of a cloud provider's product line as widgets.
Six parody services, each one goroutine-scheduled in-process widget in one
binary — "monolithic microservices" taken literally — and each also one
repeatable task for the agentic A/B benchmark
the Widget Foundry program calls workstream BEN.

**This document is a design.** Nothing here is built. The six `.widget`
documents beside it validate against dialect v0 with zero findings; no engine,
no generated widget and no host registration exists yet, and none is written
in this stage. The precedent is P1's: an agent that can see the generator
shapes the language to suit the generator, and the dependency has to run the
other way. The fleet is the language's first real consumer, so it is designed
before it is allowed to touch anything.

The names are jokes about **categories**, not about products. Every one is an
original coinage; none is a misspelling of anything.

---

## The roster, in build order

| # | Service | Category parodied | Topology | Built in P4? |
|---|---|---|---|---|
| 1 | [Yakshave](#1-yakshave) | continuous-delivery pipeline | chain | yes |
| 2 | [Queuecumber](#2-queuecumber) | managed message queue | fan-out | yes |
| 3 | [Blobfish](#3-blobfish) | object (blob) store | storage | yes |
| 4 | [Coldstart](#4-coldstart) | function-as-a-service runtime | request/response | yes |
| 5 | [Dashbored](#5-dashbored) | managed metrics and dashboards | fan-in | yes |
| — | [Roundabout](#reserved-roundabout) | managed load balancer | request/response | **no — reserved** |

### Why this order

**1. Yakshave, because the chain is already proved.**
`03-relay-pipeline.widget` generates and compiles today, so a chain-shaped
scene is the one shape the generator is known to handle end to end. The first
service exists to establish the whole loop — engine, document, generation, host
registration, spec tests, page — and a first service that is also a first
generator bug is two failures wearing one coat.

**2. Queuecumber, because it is the widest.**
Six nodes, five edges, four channels, four roles, two indicators, two controls,
seven pulses. Every construct the generator might refuse is in it. Second,
because the loop has to exist before it is stressed, and because a refusal
found in week one is a dialect conversation while a refusal found in week four
is a schedule problem.

**3. Blobfish, because it is furthest from anything drawn.**
No shipped widget is storage-shaped, and two of the fleet's four highest-ranked
v1 asks — a node that shows a level, and appearance that depends on state —
were found by trying to draw it. Third, so those asks reach
[`dialect-v1-asks.md`](dialect-v1-asks.md) while the agenda is still being
ranked rather than after.

**4. Coldstart, because it is the probe's twin.**
Its topology is a request/response round trip, which is also
[Roundabout's](#reserved-roundabout). Building it fourth means both bench
conditions inherit exactly one worked round-trip precedent and neither
inherits two, so the probe measures the language rather than novelty.

**5. Dashbored, because it watches the other four.**
Its stream resolves against observations of the four engines built before it.
Building it earlier means writing a host seam against services that do not
exist. It is also the fan-in shape of `csp-fanin-v1`, which is the one place
the two workstreams touch.

**Roundabout is not built.** It is the BEN probe task; see
[`bench.md`](bench.md).

---

## 1. Yakshave

**Category:** a managed continuous-delivery pipeline.

**The joke.** Yakshave runs your build in four stages, each of which exists to
satisfy a prerequisite discovered while working on the stage before it. Runs
are billed by the minute including the minutes spent waiting for a runner,
which is why the runner pool is described in the console as *right-sized*. A
red build is retried automatically until it is green or until the quota is,
whichever arrives first. The deploy stage is called a gate because it is the
one that can be closed.

**Engine sketch (CS-5 native).** The textbook CSP pipeline and nothing
cleverer. Four stage goroutines connected by three channels, each stage
`func(in <-chan artifact, out chan<- artifact)` owning its own `busy` and
`lastResult` as locals and closing `out` when `in` closes — so shutdown is a
single `close` at the head that drains the chain in order, which is the whole
demonstration. **A stage at runtime is a goroutine; an artifact is a value
moving down a chain of channels, owned by exactly one stage at a time.** A
failed stage sends on `failures chan failure` to a retry goroutine that
re-injects at the head, and a `reports chan stageReport` feeds one observer
goroutine that owns the published view. Two tickers — one for run time, one for
quota — are the two streams. No mutex, because no datum has two owners.

**Widget surface.** Nodes (4), edges (4), channels (2: `artifact` forward,
`rollback` reverse), roles (2: `step`, `gate`), placements (4), one bound
scene description, one indicator, four stats, no controls, two events with
fields, **two streams**. Motion: three staggered artifact pulses and one
emphasis on the gate.

**What it shows that the raft card does not.** Two streams rather than one, and
therefore the first document in which only one of a widget's sources can be the
tick. Two field-carrying events. A `text` state field (`currentStage`)
interpolated into a literal label — no shipped exemplar carries a string
through state. A chrome with no `source` slot. An edge carrying exactly one
channel, and that channel `reverse`: the rollback runs deploy → checkout while
every other edge in the repository carries a forward channel somewhere.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Stage | A goroutine owning one inbound channel and its own last result | Stage → inbound channel `1`; Stage → downstream Stage `0..1`; Stage → Artifact in hand `0..1` | A stage holds at most one artifact at a time, and closes its outbound channel exactly once, only after its inbound channel closed | `accept`, `run`, `forward`, `close` |
| Artifact | A value carrying a run identity and the stages it has cleared | Artifact → Stage (current owner) `1`; Artifact → Run `1` | An artifact is owned by exactly one goroutine at every instant; it never appears in two stages | `enter`, `advance`, `fail` |
| Run | One traversal of the chain, identified by a monotonic sequence | Run → Artifact `1`; Run → retry count `1` | The sequence is monotonic and never reused, which is what makes it the tick | `start`, `retry`, `finish` |
| Quota | A minute budget consumed by queue time and run time alike | Quota → Run `0..n` | Consumed minutes never decrease within a billing window; a run may not start on a zero balance | `charge`, `refuse` |

---

## 2. Queuecumber

**Category:** a managed message queue.

**The joke.** Queuecumber delivers your messages at least once and, on the
premium tier, considerably more than once. Billing is per message *looked at*,
so a redelivery is a fresh transaction. The dead-letter queue is sold as an
add-on on the grounds that messages arriving there have been tried the hardest,
and the console's largest and most prominently rendered number is the redrive
count. Visibility timeouts are configurable between one second and one second.

**Engine sketch (CS-5 native).** One broker goroutine owns the ready ring and
the in-flight table as locals; nothing else can see either. Producers send on
`submissions chan message`. A worker asks for work by sending a
`leaseRequest` carrying its own `reply chan lease` — snapshot-by-reply-channel,
the same shape `csp-fanin-v1`'s reference implementation uses — and the broker
answers on that channel or not at all. Three worker goroutines each own their
own jittered PCG stream and their own in-hand message; they ack on
`acks chan ack`. One expiry goroutine drives `timeouts chan messageID` from a
ticker, and an expired lease returns the message to the ring with
`attempts + 1`; past the attempt ceiling it moves to the broker's dead-letter
slice. **A message at runtime is a value that is in exactly one of two places:
the broker's own slice, or one worker's goroutine stack.** That is the whole
reason there is no lock — a queue is a hand-off, and a hand-off is a channel.

**Widget surface.** Nodes (6), edges (5), channels (4: `enqueue`, `lease`,
`ack`, `redrive`), roles (4, one of which classifies three nodes), placements
(6), two indicators, two controls, three events, one stream, seven pulses and
one emphasis. The longest legend and the most motion in the fleet.

**What it shows that the raft card does not.** A role classifying three nodes
at once. A four-entry legend — nothing until now had rendered one long enough
to wrap. A second indicator, so the question of whether indicators stack or
replace is answered. A second control, and deliberately of the other kind: one
toggles a flag the widget owns, the other emits an event that changes no widget
state at all and is acted on by the host. The `atMost` bound, which shipped
with `atLeast` and had no caller anywhere until this fleet. And a channel that
is carried by an edge and animates never, which is where the per-pulse gate ask
comes from.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Message | An immutable payload plus a mutable attempt count | Message → Lease `0..1`; Message → Queue `0..1`; Message → DeadLetter `0..1` | A message is in exactly one of {ready, leased, dead-lettered} at every instant, and its attempt count only increases | `enqueue`, `lease`, `ack`, `expire`, `redrive` |
| Queue | The broker goroutine's own ordered ready list | Queue → Message `0..n`; Queue → Broker goroutine `1` | Only the broker goroutine reads or writes it; depth is the length and is never negative | `push`, `pop`, `measure` |
| Lease | A time-bounded grant of one message to one worker | Lease → Message `1`; Lease → Worker `1`; Lease → deadline `1` | A message has at most one live lease; an expired lease returns the message rather than dropping it | `grant`, `renew`, `expire`, `release` |
| Worker | A goroutine holding at most one leased message | Worker → Lease `0..1` | A worker never holds two leases, and its ack names the lease it was granted | `request`, `handle`, `ack`, `nack` |
| DeadLetter | The terminal store for messages past the attempt ceiling | DeadLetter → Message `0..n` | Every message in it has an attempt count strictly greater than the ceiling; entry is one-way until a redrive | `admit`, `count`, `redrive` |

---

## 3. Blobfish

**Category:** an object (blob) store.

**The joke.** Blobfish offers eleven nines of durability and approximately one
nine of findability. Buckets are free; naming them is where the money is.
Every object is replicated to three zones, one of which is always described as
*eventually consistent* so that the other two do not have to be. The storage
class named Glacial is cheaper because retrieving from it is billed separately,
and the storage class named Deep Glacial is cheaper still.

**Engine sketch (CS-5 native).** **A bucket at runtime is a goroutine owning a
map that no other goroutine can reach.** A put is a `writeRequest` value
carrying its own `reply chan writeResult`, sent to a coordinator goroutine; the
coordinator fans the write out to three replica goroutines over their own
inbound channels and collects on one shared `acks chan replicaAck` until W = 2
arrive, then replies and *stops waiting*. The third acknowledgement arrives
later, or never — which is not a simplification of eventual consistency, it is
eventual consistency, made out of a `select` that stopped counting. A repair
goroutine periodically asks each replica for its generation on a reply channel
and issues a repair write to whichever is behind. Reads take R = 2 the same
way. Every map has exactly one owner, so the whole engine has no mutex and
nothing for one to guard.

**Widget surface.** Nodes (4), edges (3, one of which carries three channels),
channels (3: `write` forward, `ack` reverse, `repair` forward), roles (2),
placements (4), two orbits, one indicator, four stats, no controls, one event,
one stream, six pulses and one emphasis. Chrome with no `source` slot on a
widget that does have a stream.

**What it shows that the raft card does not.** A predicate graph two levels
deep: `durable` requires `serving`, `quorumWrites` and `consistent`, each of
which is itself composed, where example 01's `live` composes flag fields only.
Two orbits used as tiers rather than as decoration. `atMost` as a consistency
bound. Four stats. And the demonstration that the absence of a `source` slot is
independent of the absence of a source.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Object | An immutable blob identified by a key and a generation | Object → Bucket `1`; Object → Replica copy `1..3` | A generation is never reused for a key; a read never returns a generation older than one a quorum acknowledged | `put`, `get`, `replicate` |
| Bucket | A goroutine owning one key-to-object map | Bucket → Object `0..n`; Bucket → goroutine `1` | Exactly one goroutine reads or writes the map; no reference to it leaves the goroutine | `accept`, `serve`, `list` |
| Replica | One zone's copy, a goroutine with its own generation counter | Replica → Coordinator `1`; Replica → Object copy `0..n` | A replica's generation is monotonic; it accepts a repair write only for a generation greater than its own | `write`, `acknowledge`, `repair` |
| WriteQuorum | The count of acknowledgements a write waits for | WriteQuorum → Replica `2..3`; WriteQuorum → write `1` | The quorum is a strict majority of the replica set, and the coordinator replies on reaching it and never un-replies | `collect`, `satisfy`, `abandon` |

---

## 4. Coldstart

**Category:** a function-as-a-service runtime.

**The joke.** Coldstart scales to zero instantly and back up eventually. It
bills per hundred milliseconds, of which the first eight hundred are the
platform getting ready. The premium tier keeps one instance permanently warm
and is called, on the pricing page and without any apparent discomfort,
*Serverful*. Its dashboard reports a p50 latency measured over the invocations
that did not cold-start.

**Engine sketch (CS-5 native).** **A function instance at runtime is a
goroutine that exists only while somebody is calling it**, holding its own
temperature — cold, warming, warm, idle — as a local and reading its own
`invocations chan invocation`. **An invocation is a value carrying its own
`reply chan result`**, which is what makes the round trip a round trip: the
caller blocks in a `select` on reply-or-context and nothing shared carries the
answer back. One dispatcher goroutine owns the routing table as locals and
selects over `arrivals`, `instanceStates chan stateChange` and a reaper ticker.
On a miss it spawns a goroutine, which sleeps the start-up budget, reports
`warm`, and serves. The reaper closes an idle instance's channel and that
goroutine returns — goroutine lifecycle *is* the product, which is the joke
made mechanical rather than illustrated.

**Widget surface.** Nodes (4, one with no caption), edges (3, one carrying
three channels), channels (3: `request`, `response`, `thaw`), roles (3),
placements (4), one orbit, two indicators, one control, two events, one stream,
six pulses and one emphasis.

**What it shows that the raft card does not.** A single edge carrying three
channels. A node with no caption — `caption` is the one optional clause in a
`node` block and nothing omits it today. A control whose event changes no state
and which carries no `pressedWhen`, so the control/event pair is fully
decoupled from the widget's own flags. Two indicators reading two different
predicates rather than two views of one.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Invocation | A request value carrying its own reply channel | Invocation → reply channel `1`; Invocation → Instance `0..1` | Exactly one value is ever sent on its reply channel, and the channel is closed after; a dropped invocation closes it with an error rather than leaking the caller | `arrive`, `dispatch`, `answer` |
| Instance | A goroutine with a temperature and one inbound channel | Instance → Pool `1`; Instance → Invocation in hand `0..1` | Temperature moves cold → warming → warm → idle and never skips warming; a frozen instance's goroutine has returned | `spawn`, `warm`, `serve`, `freeze` |
| Dispatcher | The single goroutine owning the routing table | Dispatcher → Instance `0..n`; Dispatcher → Invocation `0..n` queued | It is the only writer of the routing table, and it never routes to an instance whose channel it has closed | `route`, `spawn`, `reap`, `queue` |
| Pool | The set of live instances and the warm floor | Pool → Instance `0..n` | The warm count never exceeds the live count; a warm floor of zero is legal and is what "scales to zero" means | `size`, `prewarm`, `drain` |

---

## 5. Dashbored

**Category:** a managed metrics and dashboard service.

**The joke.** Dashbored ingests everything you emit and surfaces the p50.
Retention is thirteen months, of which the queryable window is two hours; the
remainder is described as *retained*, which is true. Its flagship feature is an
alert that fires when the dashboard has not been opened recently. It is also
the fleet's own console: one of the six widgets in this binary watches the
other five, which is what "monolithic microservices" looks like when the
monitoring is a goroutine too.

**Engine sketch (CS-5 native).** Fan-in, and deliberately the same fan-in as
`csp-fanin-v1`: three collector goroutines, each owning its own reservoir
sample as locals, all sending on one shared `ingest chan sample`. One
aggregator goroutine owns the histogram buckets and selects over `ingest`,
`queries chan queryRequest` (answered on the request's own reply channel) and a
flush ticker. Breaches leave on `alerts chan breach` to one alerter goroutine
that owns the firing set and debounces it. Shutdown is counted rather than
locked: each collector's departure is a send on `departures chan int`, and the
aggregator closes when the count reaches zero — so "have all producers
finished" is a message, not a shared integer.

**Widget surface.** Nodes (5), edges (4), channels (2, **both forward**), roles
(3), placements (5), one indicator, one control, five stats, two events, one
stream, four pulses and one emphasis.

**What it shows that the raft card does not.** Five stats, the most in the
fleet, two of which are literal templates side by side — the retention joke
rendered as data rather than written as a sentence. A five-node fan-in. A scene
with **no reverse channel anywhere**: every other document here and both shipped
exemplars with edges carry one, and a metrics pipeline answers nobody. And a
`forbids`-only predicate with no `requires` clause at all, which is the natural
spelling for "nothing is wrong" and which nothing had used.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Sample | One timestamped observation from one collector | Sample → Collector `1`; Sample → Series `1` | A sample is sent exactly once and read by exactly one aggregator; it is never mutated after being sent | `observe`, `emit`, `fold` |
| Collector | A goroutine owning one reservoir and one series | Collector → Series `1`; Collector → ingest channel `1` (shared) | Its reservoir never leaves the goroutine; on departure it sends exactly one departure notice | `scrape`, `sample`, `depart` |
| Aggregator | The single goroutine owning every histogram bucket | Aggregator → Collector `0..n`; Aggregator → Alert `0..n` | It is the only reader and writer of the buckets; a query is answered from a copy, never from the buckets themselves | `ingest`, `roll up`, `answer`, `flush` |
| Alert | A named breach with a firing state and a debounce | Alert → Aggregator `1`; Alert → threshold `1` | An alert transitions firing ↔ resolved only after the debounce window; a silenced alert still transitions and is only not notified | `evaluate`, `fire`, `resolve`, `silence` |

---

## Reserved: Roundabout

**Category:** a managed load balancer.

**The joke.** Roundabout distributes traffic evenly across your backends,
including the ones that are down, in the interest of fairness. Health checks
run every thirty seconds and are billed as requests, so a thoroughly monitored
pool is also a busy one. An ejected backend is returned to the rotation after a
cool-down period the console labels *optimism*. The routing policy named
Least Connections selects the backend with the fewest connections, which after
an ejection is reliably the one that just failed.

**Engine sketch (CS-5 native).** One balancer goroutine owns the rotation
cursor and the pool's health state as locals, selecting over
`arrivals chan request`, `health chan healthReport` and context. One health
checker goroutine per backend, each with its own ticker and its own consecutive
failure count as a local, all reporting into that one shared channel — fan-in
beside the round trip. One backend goroutine per member, each owning its
latency profile and in-flight count and serving requests that carry their own
`reply chan response`. Three consecutive failures and the balancer skips that
index; a `time.After` in the checker's select restores it. A failed reply is
re-dispatched to the next backend at most once, and the retry budget is the
balancer's own local.

**Widget surface.** Nodes (5, one with no caption), edges (4), channels (2),
roles (3), placements (5), one orbit, one indicator, one control, three stats,
two events, one stream, eight pulses and one emphasis.

**Why this one is the probe.** It is deliberately *mid-width*: wider than the
node-status card, narrower than Queuecumber, and its topology is Coldstart's,
which is built. Both bench conditions therefore have one worked precedent for
the shape and neither has a second, so the run measures the language rather
than how novel the picture is. Its engine is fresh — health checking, ejection
and a retry budget appear nowhere else in the fleet — so the work is real.
[`bench.md`](bench.md) is the probe design and states how the shipped document
is kept out of a run's reach.

| Concept | 1. Declared type | 2. Relations (cardinality) | 3. Invariant | 4. Verbs / events |
|---|---|---|---|---|
| Request | A value carrying a reply channel and a retry budget | Request → reply channel `1`; Request → Backend `0..1`; Request → attempts `1` | Exactly one response is sent on its reply channel; the retry budget only decreases and a request is never dispatched twice concurrently | `arrive`, `dispatch`, `retry`, `answer` |
| Backend | A goroutine with a latency profile and an in-flight count | Backend → Pool `1`; Backend → Request `0..n` in flight | Its in-flight count is only read by itself; it is never sent a request while ejected | `serve`, `delay`, `fail` |
| HealthCheck | A periodic probe owning a consecutive-failure count | HealthCheck → Backend `1`; HealthCheck → health channel `1` (shared) | The count resets to zero on any success; ejection happens at the threshold and never before | `probe`, `succeed`, `fail`, `eject` |
| Rotation | The balancer's cursor over the healthy members | Rotation → Backend `0..n`; Rotation → cursor `1` | The cursor only ever names a member the balancer currently believes healthy; an empty rotation refuses rather than picking arbitrarily | `advance`, `skip`, `restore`, `refuse` |

---

## What the fleet is evidence for

Six documents in, three things are measurable rather than argued.

**The dialect covered five shapes it was not designed against.** Every one of
the six validates with zero findings, and the constructs the fleet needed that
nothing had exercised — `atMost`, a `forbids`-only predicate, a two-level
predicate graph, a `text` state field, a command event, a reverse-only edge, a
second control, a second indicator, a second stream, a node with no caption, a
chrome with no source — were all already there. None required a language
change. That is the strongest available evidence that v0's block set was drawn
from the ontology rather than from the raft card.

**Amplification is real but smaller than the pitch implies.** Generated from
these documents, `widget.gen.go` plus `view.templ` is about 1.8× the
non-comment source. The three widgets already committed, whose `view_templ.go`
has also been compiled, run 3.7× to 5.9×. Both numbers matter, and
[`bench.md`](bench.md) settles which one the ledger records.

**The flat namespace costs a naming convention.** Roles, placements, nodes,
indicators, controls, events, streams, labels and bindings share one identifier
space (W301), and drafting six documents in one sitting collided on it four
separate times. This fleet spells placements with a `Spot` suffix, gives roles
a vocabulary distinct from the nodes they classify, and suffixes indicators.
That is not a style preference — it is what a flat namespace costs, and it is
written down here so that a seventh author does not invent a different one.

The gaps the fleet found are ranked in [`dialect-v1-asks.md`](dialect-v1-asks.md),
each one anchored to the `%%` GAP comment in the document that hit it.
