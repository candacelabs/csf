# Bench task `candaws-roundabout-v1` — Roundabout, a managed load balancer

This is the brief the implementer receives. It is the same in both A/B
conditions: it describes a service and a card, and it is deliberately silent
about how either is produced.

Two fixed files come with it — `engine_spec_test.go` and `fragments_test.go`.
They must pass unmodified. Editing, deleting or skipping any test in either one
voids the run.

---

## The product

Roundabout distributes traffic evenly across your backends, including the ones
that are down, in the interest of fairness. Health checks run on a timer and are
billed as requests, so a thoroughly monitored pool is also a busy one. An
ejected backend is returned to the rotation after a cool-down period the console
labels *optimism*.

You are building two things:

1. **The engine** — a running load balancer: a request/response round trip
   across a pool, health checking with ejection and restoration, and a retry
   budget.
2. **The card** — the live region the console draws for it, rendered from the
   state the engine publishes.

Both live in package `roundabout`, in a module named `roundabout`.

---

## 1. The engine

### The API, fixed

```go
// Backend is one member of the pool as the balancer sees it.
type Backend struct {
	Name  string
	Serve func(ctx context.Context, payload string) (string, error)
	Probe func(ctx context.Context) error
}

type Config struct {
	Backends         []Backend
	FailureThreshold int
	CheckInterval    time.Duration
	Cooldown         time.Duration
	RetryBudget      int
	PolicyName       string
}

type Response struct {
	Payload  string
	Backend  string
	Attempts int
}

type RotationView struct {
	Sequence   uint64
	PolicyName string
	Healthy    int
	Ejected    int
	Inflight   int
	Up         bool
	Draining   bool
}

var (
	ErrNoBackend error
	ErrDraining  error
	ErrClosed    error
)

func Start(config Config) (*Balancer, error)

func (balancer *Balancer) Do(ctx context.Context, payload string) (Response, error)
func (balancer *Balancer) SetDraining(draining bool)
func (balancer *Balancer) Observe() RotationView
func (balancer *Balancer) Close()
```

`Serve` and `Probe` are supplied by the caller. The balancer calls them; it does
not implement them and does not interpret what they return beyond success and
failure.

### The contract, in full

**`Start(config Config) (*Balancer, error)`**

- Returns a balancer that is accepting requests and has begun health checking.
- Rejects a configuration it cannot honour, with a non-nil error and a nil
  balancer: no backends, a backend with an empty name, two backends with the
  same name, a backend with a nil `Serve` or a nil `Probe`,
  `FailureThreshold < 1`, `CheckInterval <= 0`, `Cooldown < 0`,
  `RetryBudget < 0`.
- Every backend starts in the rotation. Nothing is ejected until a health check
  says so.
- A rejected configuration starts no goroutine.

**`Do(ctx context.Context, payload string) (Response, error)`**

- Performs one round trip and returns exactly one of a response or an error.
- Dispatches to a backend the balancer currently believes healthy, chosen by
  advancing a rotation cursor over the healthy members in `Config.Backends`
  order. Over a run in which nothing is ejected, sequential requests are
  distributed evenly: with three healthy backends, nine requests are three each.
- `Response.Payload` is what the chosen backend's `Serve` returned.
  `Response.Backend` is that backend's `Name`. `Response.Attempts` is how many
  backends were asked, counting from 1.
- **Retry.** If `Serve` returns an error, the request is re-dispatched to a
  *different* healthy backend. A request is re-dispatched at most
  `RetryBudget` times, so at most `1 + RetryBudget` backends are ever asked for
  one request, and no backend is asked twice for it. When the budget runs out,
  `Do` returns the last error from a `Serve`.
- **Never dispatches to an ejected backend**, and never has one request in
  flight at two backends at once.
- With no healthy backend, returns an error wrapping `ErrNoBackend` promptly.
  It does not block waiting for one, and it does not pick an ejected backend
  because it is the only one left.
- While draining, returns an error wrapping `ErrDraining` and dispatches
  nothing.
- After `Close`, returns an error wrapping `ErrClosed`. It must not block
  forever or panic.
- Checks `ctx` before dispatching. An already-done context produces an error
  wrapping `ctx.Err()` and no dispatch at all.
- Safe to call from many goroutines at once.

**Health checking**

- Every backend is probed on its own schedule, roughly every `CheckInterval`.
- `FailureThreshold` **consecutive** probe failures eject that backend. Any
  probe success resets its count to zero, so failures that are not consecutive
  never eject.
- Ejection happens at the threshold and never before: with a threshold of 3, two
  consecutive failures leave the backend in the rotation.
- An ejected backend stays out for at least `Cooldown`. After that it is probed
  again, and one success returns it to the rotation.
- Each backend's consecutive-failure count is its own. One backend failing never
  ejects another.

**`SetDraining(draining bool)`**

- `true` stops the balancer accepting new requests. Requests already in flight
  still complete.
- `false` resumes.
- Idempotent, and safe from any goroutine.

**`Observe() RotationView`**

- A snapshot of what the balancer currently believes, safe to call from any
  goroutine and concurrently with `Do`.
- `Healthy + Ejected` always equals the number of configured backends.
- `Inflight` is how many `Do` calls are currently dispatched. It is never
  negative and returns to zero once every call has returned.
- `Sequence` is how many requests the balancer has accepted, from zero, and
  never decreases.
- `PolicyName` is `Config.PolicyName`, reported and not interpreted.
- `Up` is whether the balancer is accepting: true from `Start` until `Close`.
- `Draining` is the last value given to `SetDraining`.
- Legal before, during and after `Close`.

**`Close()`**

- Returns only after every goroutine the balancer started has stopped and every
  in-flight `Do` has returned.
- Idempotent, and safe to call concurrently from several goroutines. Repeated
  calls must not panic or hang.
- After it returns, `Observe` still answers and reports `Up: false`.

---

## 2. The card

The card is a widget: one live region, rendered from state, patched over a live
connection. It implements the widget SDK's `IWidget[S]` contract — see the SDK
README — and is mounted through `widgettest.Mount` by the fixture.

### Identity

| | |
|---|---|
| Widget name | `Roundabout` |
| Region | `widget.candaws.roundabout` |
| Palette | `fieldStation` |
| Title element id | `widget.candaws.roundabout-title` |

### State

Eight fields. Six arrive on a report event, one is toggled by a control, one is
set by the live runtime.

| Field | Kind | Written by |
|---|---|---|
| `dispatchSequence` | counter | the report event, wire field `dispatch_sequence` |
| `policyName` | text | the report event, wire field `policy_name` |
| `healthyBackends` | count | the report event, wire field `healthy_backends` |
| `ejectedBackends` | count | the report event, wire field `ejected_backends` |
| `inflight` | count | the report event, wire field `inflight` |
| `balancerUp` | flag | the report event, wire field `balancer_up` |
| `draining` | flag | toggled by the drain control |
| `clientBehind` | flag | the runtime's slow-client signal: set on `live.SlowClientEvent`, cleared on `live.ClientRecoveredEvent` |

The report event's wire name is `widget.candaws.roundabout.report`. The drain
control's event wire name is `widget.candaws.roundabout.drain`, and it carries
no fields — it toggles `draining`.

### The three conditions the card reads

| Name used below | Holds when |
|---|---|
| *rotation full* | `ejectedBackends` is at most 0 |
| *capacity* | `healthyBackends` is at least 2 |
| *routing* | `balancerUp` **and** *capacity* **and not** `clientBehind` |

### The texts

Every list is ordered, and the **first** clause that holds wins.

**Scene description** — the accessible name of the drawing.

| when | text |
|---|---|
| *routing* | `A client, a balancer and three backends; requests are distributed across the rotation and answered back along the same path.` |
| not `balancerUp` | `A client, a balancer and three backends; the balancer is not accepting requests.` |
| not *capacity* | `A client, a balancer and three backends; too few backends are passing their health checks.` |
| otherwise | `A client, a balancer and three backends; the browser is behind the request stream.` |

**Balancer caption**

| when | text |
|---|---|
| `draining` | `draining` |
| *routing* | `rotating` |
| otherwise | `closed` |

**Backend caption** — the same text on all three backends.

| when | text |
|---|---|
| *rotation full* | `in rotation` |
| otherwise | `partly ejected` |

**Rotation status** — the indicator's text.

| when | text |
|---|---|
| not `balancerUp` | `balancer down` |
| `draining` | `draining by operator` |
| `clientBehind` | `browser catching up` |
| *rotation full* | `every backend in rotation` |
| otherwise | `{ejectedBackends} backends ejected` |

**The three stat lines**, in this order:

1. `policy {policyName}`
2. *routing* → `{inflight} requests in flight`; otherwise → `no requests in flight`
3. *capacity* → `{healthyBackends} of 3 backends healthy`; otherwise →
   `{healthyBackends} of 3 backends healthy · below the floor`
   (the separator is U+00B7, a middle dot, with a space either side)

**Drain control caption**

| when | text |
|---|---|
| `draining` | `Return the pool to the rotation` |
| otherwise | `Drain the pool` |

**Fixed text**: the title is `Roundabout`; the source line is
`Read-only balancer stream`; the two legend entries are `request` then
`response`; the five node titles are `client`, `balancer`, `backend-a`,
`backend-b`, `backend-c`.

### The picture

A client on the left, a balancer in the middle, three backends stacked on the
right, inside one orbit ring.

- **Five nodes.** `client` carries a title and **no caption**. The other four
  carry a title and a caption: the balancer's is the balancer caption, and each
  backend's is the backend caption.
- **Four edges**: client→balancer, and balancer→each backend. Every edge carries
  both channels.
- **Two channels**, in this order: `request`, drawn forward in the accent token;
  `response`, drawn in reverse in the positive token. Each contributes one
  legend entry, in that order.
- **Eight pulses**: one per channel per edge, four requests outward and four
  responses back.
- **One emphasis** on the balancer node.
- **One indicator**, carrying the rotation-status text, toned *positive* when
  *rotation full* holds and *warning* when it does not.
- **One control**, the drain toggle: it carries the drain caption, emits the
  drain event on click, and reports `draining` as its pressed state.
- **Motion is gated**: the gate is open when *routing* holds and `draining` does
  not, and shut otherwise. It restarts on `dispatchSequence`.

### The markup contract

The fixture asserts on rendered output, so the region's markup is part of the
contract. Everything here is the widget stylesheet's own vocabulary — the SDK
ships `widget.css` and it is the authority on what these classes mean.

| Element | Requirement |
|---|---|
| root | an `<aside class="widget">` carrying `data-gotth-region="widget.candaws.roundabout"` exactly once, `aria-labelledby` naming the title element's id, `data-widget="Roundabout"`, `data-palette="fieldStation"`, and `data-motion="true"` or `"false"` for the gate |
| title | carries class `widget-title` and the id `aria-labelledby` names |
| source | carries class `widget-source` |
| scene | one element with class `widget-scene`, whose `aria-label` is the scene description |
| orbit | one element with class `widget-orbit` |
| node | one element with class `widget-node` per node; its title carries class `widget-node-title` and its caption, when it has one, `widget-node-caption` |
| legend | one element with class `widget-legend-entry` per channel, in channel order |
| stats | one element with class `widget-stat` per stat line, in order |
| indicator | one element with class `widget-indicator` carrying `data-tone="positive"` or `data-tone="warning"` |
| control | a `<button class="widget-control">` carrying `aria-pressed="true"` or `"false"` |
| pulse | one element with class `widget-pulse` per pulse |

`data-motion` is the whole of the motion gate: pulses are the scene's declared
motion and are drawn whether or not the gate is open. Nothing is required to
disappear when the gate shuts.

---

## 3. The seam between them

The fixtures drive both halves through this, and nothing else. It is fixed:

```go
package roundabout

const (
	Region      = "widget.candaws.roundabout"
	EventReport = "widget.candaws.roundabout.report"
	EventDrain  = "widget.candaws.roundabout.drain"
)

// MountCard mounts the Roundabout card alone in a registry of its own.
func MountCard(ctx context.Context) (*widgettest.Card, error)

// ReportFields is one rotation view as the report event carries it: the six
// wire field names above, mapped to their string forms.
func ReportFields(view RotationView) map[string]string
```

`ReportFields` fills all six wire fields every time. Counters and counts are
base-ten integers; flags are `true` or `false`.

---

## 4. File layout

Fixed, because the grader measures where lines landed:

| What | Files |
|---|---|
| engine | `engine*.go` |
| card | `card*.go`, `card*.templ`, `*.css`, and a generator's own output names (`view.templ`, `view_templ.go`, `widget.gen.go`) |
| the seam above | `seam.go` |
| the two fixtures | `engine_spec_test.go`, `fragments_test.go` — **do not edit** |

Any other Go file you want is fine as long as it is not one of those names.

---

## 5. How this is graded

```bash
go vet ./...
go test -race ./...
```

Both must pass with the two fixtures exactly as provided.

There is no style requirement in this brief and no partial credit for elegance.
What is recorded is: did it compile, did the engine specs pass, did the card
fragments pass, was it race-clean, and what it cost in tokens, tool calls and
wall-clock time.
