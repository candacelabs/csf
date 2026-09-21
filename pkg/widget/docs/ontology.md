# Widget ontology v0

[`inventory.md`](inventory.md) harvested 135 concepts from shipped code. This
document types them. It is the contract the dialect
([`dialect.md`](dialect.md)) is a surface syntax for, and the contract the
validator's error catalogue ([`errors.md`](errors.md)) enforces.

## The four-layer test

An entry is in this ontology only if all four layers are present and specific.
Anything missing a layer is **taxonomy** — a name with nothing behind it — and
is merged into an entry that has all four, or cut with the reason written down.

| Layer | What it must state | The failure it prevents |
|---|---|---|
| **1. Declared type** | What the thing *is*, declared and never inferred from context or from a value's shape | An author and a generator disagreeing about what a construct means |
| **2. Relations** | Every relation it participates in, each with a cardinality (`1`, `0..1`, `1..n`, `0..n`) | A generator reaching a `nil` it has no rule for |
| **3. Invariants** | At least one property that is true of every valid instance, stated so a validator can check it | A document that parses, generates, and is wrong |
| **4. Verbs and events** | The operations that may be performed on it and the events it participates in | Behaviour arriving as an undocumented side effect of the generator |

Layer 3 is the one that does the work. "A scene has nodes" is a taxonomy. "A
scene with an edge has at least two nodes, and its description must be bound
whenever the widget has any predicate" is an ontology, because a validator can
fail on it.

The result: **25 typed entries**, from 135 inventory concepts. 104 concepts
merged into an entry; 31 were cut, with the disposition of every one recorded
in [§ What was cut](#what-was-cut).

## How each claim is proven

A cardinality without a gate is aspirational. So every relation cardinality
below carries an **Enforced by** column, and every Layer-3 invariant carries an
**Enforced by** line, and each names one of three kinds of proof. A claim with
none of them is the failure state — `tools/tests/test_ontology_enforcement.py`
reads this file and fails if any survives.

- **A Liquid Proto refinement**, written `refinement `Message.field``. The
  invariant is a local field property a per-message refinement can express — a
  pattern, a bound, a closed enum — and
  [`refinement/v1/refinement.proto`](../refinement/v1/refinement.proto)
  compiles it to a generated `Validate…` that proves it holds. This is the
  layered evolution of [`dialect.md § 10.3`](dialect.md#103-who-owns-the-irs-definition):
  that ruling kept the IR hand-written, and stands; the refinement is the
  machine-checked contract added beside it, not a reversal. The hand-written
  validator remains the author-facing diagnostic, and
  [`refinement_agreement_test.go`](../internal/validate/refinement_agreement_test.go)
  proves the two reject exactly the same values.
- **A validator class plus its proving spec**, written ``W…`` and ``spec
  `…``. The invariant is whole-graph or structural — a scene with an edge has
  two nodes, the predicate graph is acyclic, a role classifies some node — which
  no per-message refinement can state. It stays validator-enforced, and the
  cited spec proves the class fires.
- **`ASPIRATIONAL`**, with the reason. The claim is a runtime guarantee
  (`live/core.go`), a generator obligation, a palette-owner property, or a thing
  v0 does not yet model. It is honestly not gated by the widget validator, and
  says so rather than reading as enforced.

The `spec` paths are relative to [`../`](..); the W-class ids are the catalogue
of [`errors.md`](errors.md).

---

## Index

Structure: [Widget](#widget) · [Scene](#scene) · [Node](#node) ·
[Edge](#edge) · [Orbit](#orbit) · [Placement](#placement) · [Role](#role) ·
[Channel](#channel)

Motion: [Motion](#motion) · [Pulse](#pulse) · [Emphasis](#emphasis) ·
[Tick](#tick)

Data: [StateField](#statefield) · [Predicate](#predicate) ·
[Binding](#binding) · [Label](#label) · [Slot](#slot) ·
[Indicator](#indicator)

Interaction: [Control](#control) · [EventDeclaration](#eventdeclaration) ·
[EventField](#eventfield) · [Stream](#stream)

Appearance: [Palette](#palette) · [Token](#token)

Runtime: [LifecyclePhase](#lifecyclephase)

---

## Widget

**1. Declared type.** The whole document, and one deployable unit: a named
composition that a host registers once and mounts per session, occupying
exactly one server-owned live region.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Widget → Scene | exactly 1 | `W011` · `W006` — a scene is a required, singular block |
| Widget → Palette | exactly 1 (referenced, never declared) | `W002` · `W208` — the palette directive is required and must name a known palette |
| Widget → region identity | exactly 1 | refinement `RegionIdentity.value` · `W002` · `W108` · spec `internal/validate/refinement_agreement_test.go` |
| Widget → StateField | 1..n | `W011` · `W007` — the state block is required and may not be empty |
| Widget → Predicate, Binding, Label, Role, Placement, Channel, Control, EventDeclaration, EventField, Stream, Indicator | 0..n each | `W301` · `W410` — 0..n has no count to violate; each member is uniquely named and, if unreferenced, an error |
| Widget → Motion | 0..1 | `W006` — at most one motion block |
| Widget → Tick | 0..1 | `W405` · `W414` — the tick exists exactly when motion holds an animation |
| Widget → Slot | exactly 1 `title`, 0..1 `source`, 0..n `stat` | `W601` · `W302` — title required, source singular, stat unbounded |

**3. Invariants.**

- The region identity matches `^[A-Za-z0-9_:.-]{1,64}$` and is **stable across
  releases**: a patch names it on the wire, so changing it is a client-visible
  change (`live/core.go:34-37`).
  Enforced by: refinement `RegionIdentity.value` · `W108` · spec `internal/validate/refinement_agreement_test.go` · ASPIRATIONAL (stability across releases has no cross-release state in one document)
- Rendering is a pure function of state and byte-identical for equal state
  (`live/core.go:39-43`). Therefore **every collection in this ontology is an
  ordered sequence, never a set**, and its order is declaration order. A
  construct whose rendering could depend on map iteration order is not
  expressible.
  Enforced by: ASPIRATIONAL (render purity and byte-determinism are a gotth-live runtime guarantee, live/core.go:39-43; the IR carries every collection as an ordered slice by construction)
- Every identifier is unique **across the whole document**, with exactly one
  deliberate sharing: a `flag` [StateField](#statefield) and a composed
  [Predicate](#predicate) occupy one namespace, because a reader must never
  have to ask which kind a name in a `when` clause is. One flat namespace was
  chosen over per-construct namespaces because a name that means two things in
  one file is a name an author will mix up, and because it lets every "unknown
  identifier" diagnostic say *what* the name was declared as.
  Enforced by: `W301` · spec `internal/validate/validate_test.go`
- A widget document names **no host, no address, no filesystem path, no
  credential, and no operator identifier**. It is portable by construction,
  which is also what makes it publishable.
  Enforced by: `W605` · spec `internal/validate/validate_test.go`
- A widget with any [Pulse](#pulse) or [Emphasis](#emphasis) declares a
  [Tick](#tick).
  Enforced by: `W405` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `register`, `mount`, `reduce`, `tick`, `render`,
`effect`, `unmount`. It emits no event of its own; its
[Controls](#control) and [Streams](#stream) do.

---

## Scene

**1. Declared type.** The single bounded, normalized drawing area a widget
owns, and the single accessibility boundary within it: it renders as one image
with one text alternative.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Scene → Widget | exactly 1 (owner) | `W001` · `W006` — one widget owns the one scene block |
| Scene → Node | 1..n | `W303` · `W007` · spec `internal/validate/validate_test.go` |
| Scene → Edge | 0..n | `W301` · `W304` — 0..n; edges are unique per unordered pair |
| Scene → Orbit | 0..n | `W301` · `W013` — 0..n; orbits are uniquely named, valid members |
| Scene → description Slot | exactly 1 | `W602` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- A scene containing an [Edge](#edge) contains at least 2 [Nodes](#node).
  Enforced by: `W303` · spec `internal/validate/validate_test.go`
- The description slot is **filled**, and its [Label](#label) is bound to a
  [Binding](#binding) whenever the widget declares any
  [Predicate](#predicate). A static description of a picture that changes is
  wrong the moment it changes; the shipped card's description is a three-way
  decision for exactly this reason (`app.go:61-69`).
  Enforced by: `W602` · `W603` · spec `internal/validate/validate_test.go`
- Nothing inside the scene contributes its own accessible name. Node titles and
  captions are visible text inside an image, not accessible content, so removing
  the description does not degrade to "some" accessibility — it degrades to
  none.
  Enforced by: ASPIRATIONAL (generator obligation — the grammar has no spelling for a scene-internal accessible name, so nothing inside the scene can carry one)
- The scene box is normalized: positions are percentages of the box, never
  absolute lengths, so the scene is resolution- and container-independent.
  Enforced by: refinement `Placement.left` · refinement `Placement.top` · `W104` · spec `internal/validate/refinement_agreement_test.go`

**4. Verbs and events.** `render`, `describe`. Participates in `render`.

---

## Node

**1. Declared type.** A named participant drawn in a scene, classified by
exactly one [Role](#role) and located at exactly one [Placement](#placement).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Node → Scene | exactly 1 | `W006` — the one scene block is the owner |
| Node → Role | exactly 1 | `W106` · `W201` · `W203` |
| Node → Placement | exactly 1 | `W106` · `W201` · `W306` |
| Node → title Label | exactly 1 | `W106` · `W201` · `W203` |
| Node → caption Label | 0..1 | `W201` · `W203` · `W107` — optional, resolves, not repeated |
| Node → incident Edge | 0..n | `W301` · `W304` — 0..n incident edges, unique per pair |
| Node → Emphasis | 0..1 | `W307` — at most one emphasis per node |

**3. Invariants.**

- Node identifiers are unique within the scene.
  Enforced by: `W301` · spec `internal/validate/validate_test.go`
- Two nodes may not share a [Placement](#placement): one point holds one node,
  so a scene never renders two nodes on top of each other.
  Enforced by: `W306` · spec `internal/validate/validate_test.go`
- A node's appearance — marker size, marker token — comes entirely from its
  [Role](#role). A node carries no appearance of its own, which is what makes
  a role change one edit rather than *n*.
  Enforced by: `W502` · spec `internal/validate/validate_test.go` — the only way to author node appearance is a mermaid shape bracket, which W502 refuses
- A node with no incident edge is legal (the minimal status widget is exactly
  one such node); a node with an [Emphasis](#emphasis) whose role forbids
  emphasis is not.
  Enforced by: `W407` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `render`, `emphasize`. Participates in `render`.

---

## Edge

**1. Declared type.** An ordered pair of distinct nodes in one scene, carrying
one or more [Channels](#channel). It is the only path a [Pulse](#pulse) can
travel.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Edge → Scene | exactly 1 | `W006` — the one scene block is the owner |
| Edge → `from` Node | exactly 1 | `W106` · `W201` · `W203` |
| Edge → `to` Node | exactly 1 | `W106` · `W201` · `W203` |
| Edge → Channel | 1..n | `W106` · `W201` · `W410` — carries required, resolves, channel referenced |
| Edge → Pulse | 0..n | `W301` · `W305` — 0..n pulses, unique per (edge, channel) |

**3. Invariants.**

- `from` and `to` are distinct: **no self-edge in v0.** A self-edge has no
  derivable geometry in a point-placement scene, and no shipped caller.
  Enforced by: `W403` · spec `internal/validate/validate_test.go`
- At most one edge per **unordered** node pair. Two edges between the same two
  nodes would occupy the same line; direction belongs to the
  [Channel](#channel), not to a second edge.
  Enforced by: `W304` · spec `internal/validate/validate_test.go`
- Edge geometry — length and angle — is **derived** from the two nodes'
  placements and is never authored. The shipped card hand-computes
  `width: 36%; transform: rotate(-137deg)` (`home.css:435-436`), which is a
  copy of the endpoints that silently goes stale when a node moves.
  Enforced by: `W503` · spec `internal/validate/validate_test.go` — EdgeGeometry is computed; the grammar has no geometry clause
- An edge is decorative in the accessibility tree: it carries no label, because
  the scene's description is the single text alternative.
  Enforced by: ASPIRATIONAL (the grammar has no spelling for an edge label, so an edge is decorative in the accessibility tree by construction)

**4. Verbs and events.** `render`, `carry`. Participates in `render`.

---

## Orbit

**1. Declared type.** A decorative ambient ellipse belonging to the scene
rather than to any node — the shipped card's two dashed rings
(`view.templ:289-290`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Orbit → Scene | exactly 1 | `W006` — the one scene block is the owner |
| Orbit → Token | exactly 1 | refinement `TokenValue.value` · `W106` · `W204` · spec `internal/validate/refinement_agreement_test.go` |
| Orbit → Node | exactly 0 — an orbit never attaches to a node | `W013` — the grammar has no spelling to attach an orbit to a node |

**3. Invariants.**

- Always hidden from the accessibility tree. An orbit is never given a label,
  and there is no spelling that would give it one.
  Enforced by: ASPIRATIONAL (the grammar has no spelling that gives an orbit a label, so it is hidden from the accessibility tree by construction)
- Never carries a pulse, and never participates in an edge.
  Enforced by: `W201` · `W203` — a pulse names an edge and an edge names nodes; an orbit can be neither
- Its ambient drift is gated by the widget's [Motion](#motion) block like every
  other animation. There is no ungated animation in this language.
  Enforced by: ASPIRATIONAL (generator obligation — every animation is behind the one motion gate; there is no ungated-animation spelling)

**4. Verbs and events.** `render`, `drift`. Participates in `render`.

---

## Placement

**1. Declared type.** A named point in the scene's normalized box, declared once
and referenced by exactly one node.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Placement → Widget | exactly 1 | `W006` — the placements block is singular |
| Placement → Node | exactly 1 (a placement referenced by no node is an error) | `W410` · `W306` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- `left` and `top` are integers in `[0, 100]`, read as percentages of the scene
  box.
  Enforced by: refinement `Placement.left` · refinement `Placement.top` · `W104` · spec `internal/validate/refinement_agreement_test.go`
- No arithmetic, no relative offsets, no anchors in v0. A placement is a
  literal point, which is what makes edge geometry derivable by one formula
  with no evaluation order.
  Enforced by: `W110` · `W013` — the grammar has only `left <int> top <int>`; no arithmetic, offset or anchor is spellable
- A declared placement that no node references is an error, not dead
  decoration: unreferenced vocabulary is the form drift takes.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `locate`; supplies the two endpoints `derive edge
geometry` reads. Participates in `render`.

---

## Role

**1. Declared type.** A named node kind that fixes appearance and animation
eligibility for every node classified by it.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Role → Widget | exactly 1 | `W006` — the roles block is singular |
| Role → Token | exactly 1 | refinement `TokenValue.value` · `W106` · `W204` · spec `internal/validate/refinement_agreement_test.go` |
| Role → Node | 1..n (a role classifying no node is an error) | `W410` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- A role fixes exactly one token and exactly one marker size. Both are
  required: a role with a defaulted appearance is a role whose appearance is
  decided somewhere the author cannot see.
  Enforced by: refinement `TokenValue.value` · refinement `MarkerValue.value` · `W106` · `W204` · spec `internal/validate/refinement_agreement_test.go`
- Emphasis eligibility is **declared** (`emphasis allowed` / `emphasis
  forbidden`), never inferred from whether an [Emphasis](#emphasis) happens to
  exist. Inference makes deleting the last emphasis silently change what is
  permitted.
  Enforced by: `W106` · `W407` · spec `internal/validate/validate_test.go` — the emphasis clause is required and W407 reads it
- A role classifying no node is an error.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `classify`, `select appearance`. Participates in
`render`.

---

## Channel

**1. Declared type.** A named kind of traffic an edge can carry — the shipped
card's `heartbeat` and `ack` (`view.templ:299`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Channel → Widget | exactly 1 | `W006` — the channels block is singular |
| Channel → Token | exactly 1 | refinement `TokenValue.value` · `W106` · `W204` · spec `internal/validate/refinement_agreement_test.go` |
| Channel → legend Label | exactly 1 | `W106` · `W201` · `W203` |
| Channel → Edge | 1..n (a channel carried by no edge is an error) | `W410` · spec `internal/validate/validate_test.go` |
| Channel → Pulse | 0..n | `W301` · `W305` — 0..n pulses, unique per (edge, channel) |

**3. Invariants.**

- A channel has **exactly one direction** — `forward` (`from` → `to`) or
  `reverse` (`to` → `from`). A channel is never bidirectional: two directions
  are two channels, which is exactly how the shipped card spells heartbeat and
  ack (`home.css:929-941`).
  Enforced by: refinement `DirectionValue.value` · `W106` · `W105` · spec `internal/validate/refinement_agreement_test.go`
- A channel declared and carried by no edge is an error.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`
- The footer legend is a **projection** of the declared channels in declaration
  order. It is never authored, so it cannot disagree with the picture.
  Enforced by: ASPIRATIONAL (generator obligation — the Legend is computed from the declared channels; the grammar has no legend clause to disagree with)

**4. Verbs and events.** `carry`, `legend`. Participates in `render`.

---

## Motion

**1. Declared type.** The single block that gates and schedules every finite
animation a widget has. A widget with no motion block has no animation at all.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Motion → Widget | exactly 1 | `W006` — at most one motion block |
| Motion → `requires` Predicate | 0..n | `W201` · `W203` — resolves to a predicate |
| Motion → `forbids` Predicate | 0..n | `W201` · `W203` — resolves to a predicate |
| Motion → Pulse | 0..n | `W301` · `W305` — 0..n pulses, unique per (edge, channel) |
| Motion → Emphasis | 0..n | `W301` · `W307` — 0..n emphases, unique per node |
| Motion → Tick | exactly 1 (when it holds any pulse or emphasis) | `W405` · `W406` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- **Motion-paused implies no pulse animation.** The gate is the conjunction of
  every `requires` predicate with the negation of every `forbids` predicate,
  and *nothing animates outside the gate* — there is no per-pulse override, no
  `force`, and no animation declared outside this block. This is the shipped
  three-part selector made structural
  (`html[data-gotth-status="live"] .system-card[data-live="true"][data-paused="false"]`,
  `home.css:438`).
  Enforced by: ASPIRATIONAL (generator obligation — the compiled gate; no per-pulse override, force or out-of-block animation is spellable)
- The host's own connection status is **always** part of the compiled gate,
  whether or not the author mentions it. A widget cannot animate while its
  connection is dead, because an animation running against stale state is a lie
  about liveness.
  Enforced by: spec `internal/validate/validate_test.go` · ASPIRATIONAL (the interpreter sets ir.Motion.HostStatusGate true by construction; the generator compiles it into the gate)
- Absence is not default-on: no motion block means no animation.
  Enforced by: ASPIRATIONAL (by construction — no motion block leaves ir.Document.Motion nil, so absence is off, never default-on)
- Every animation is **finite**. A duration is required, and there is no
  `repeat` in v0: the shipped pulses are re-armed by the tick rather than
  looped, so the picture moves exactly as often as the data does
  (`feed.go:48-52`).
  Enforced by: refinement `PulseTiming.duration_milliseconds` · refinement `EmphasisTiming.duration_milliseconds` · `W106` · `W013` · spec `internal/validate/refinement_agreement_test.go` — duration required, positive, and there is no `repeat`
- Reduced-motion is respected unconditionally. There is no spelling in this
  language that overrides a viewer's `prefers-reduced-motion` setting
  (`home.css:1123-1135`).
  Enforced by: ASPIRATIONAL (generator obligation — no grammar spelling overrides a viewer prefers-reduced-motion setting, home.css:1123-1135)

**4. Verbs and events.** `gate`, `arm`, `rearm`. Participates in `tick` and
`render`.

---

## Pulse

**1. Declared type.** One finite animation instance of exactly one channel on
exactly one edge.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Pulse → Motion | exactly 1 | `W013` — a pulse is only a member of the motion block |
| Pulse → Edge | exactly 1 | `W106` · `W201` · `W203` |
| Pulse → Channel | exactly 1 | `W106` · `W201` · `W404` |

**3. Invariants.**

- **A pulse travels exactly one edge.** There is no multi-hop pulse, no pulse
  without an edge, and no pulse across a path.
  Enforced by: `W106` · `W201` · spec `internal/validate/validate_test.go` — the edge is required and resolves; no multi-hop pulse is spellable
- The named channel must be carried by the named edge.
  Enforced by: `W404` · spec `internal/validate/validate_test.go`
- At most one pulse per `(edge, channel)` pair; a second is a duplicate, not a
  second particle.
  Enforced by: `W305` · spec `internal/validate/validate_test.go`
- Travel direction is the channel's, never the pulse's. An author cannot make
  an `ack` travel forwards.
  Enforced by: refinement `DirectionValue.value` · `W013` · spec `internal/validate/refinement_agreement_test.go` — travel direction is the channel's; a pulse has no direction clause
- `duration` > 0 and `delay` >= 0, both in milliseconds. The shipped values are
  820 ms with staggers of 0, 80, 180 and 260 ms (`home.css:439-451`).
  Enforced by: refinement `PulseTiming.duration_milliseconds` · refinement `PulseTiming.delay_milliseconds` · `W104` · spec `internal/validate/refinement_agreement_test.go`
- It runs only under [Motion](#motion)'s gate and is re-armed only by the
  [Tick](#tick).
  Enforced by: `W405` · ASPIRATIONAL (generator obligation — armed under the gate, re-armed only by the tick)

**4. Verbs and events.** `arm`, `travel`, `expire`. Participates in `tick`.

---

## Emphasis

**1. Declared type.** One finite animation on exactly one node — the shipped
card's expanding ring on the distinguished node (`home.css:943-946`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Emphasis → Motion | exactly 1 | `W013` — an emphasis is only a member of the motion block |
| Emphasis → Node | exactly 1 | `W106` · `W201` · `W203` |
| Node → Emphasis | 0..1 | `W307` — at most one emphasis per node |

**3. Invariants.**

- The node's [Role](#role) must declare `emphasis allowed`.
  Enforced by: `W407` · spec `internal/validate/validate_test.go`
- At most one emphasis per node.
  Enforced by: `W307` · spec `internal/validate/validate_test.go`
- Same gate, same tick, same finiteness rule as [Pulse](#pulse). `duration` > 0,
  `delay` >= 0.
  Enforced by: refinement `EmphasisTiming.duration_milliseconds` · refinement `EmphasisTiming.delay_milliseconds` · `W104` · spec `internal/validate/refinement_agreement_test.go`

**4. Verbs and events.** `arm`, `pulse`, `expire`. Participates in `tick`.

---

## Tick

**1. Declared type.** The single monotonic counter field that re-arms a
widget's finite motion — the shipped card's `Sequence`, which the markup turns
into a per-tick DOM identity so a finished animation starts again
(`app.go:73-75`, `view.templ:284`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Tick → Widget | 0..1 | `W405` · `W414` — the tick exists exactly when motion holds an animation |
| Tick → StateField | exactly 1, of type `counter` | `W406` · `W201` — restartOn resolves and must be a counter |
| Tick → Motion | exactly 1 (a tick with no motion is an error) | `W414` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- Strictly increasing. An inbound value less than or equal to the current one is
  **discarded, not applied** (`app.go:186-188`) — a widget never animates
  backwards because a retransmission arrived.
  Enforced by: ASPIRATIONAL (runtime guarantee — a value less than or equal to the current one is discarded at reduce, app.go:186-188)
- A tick's field is written only by an [EventField](#eventfield), never by a
  [Control](#control). A user cannot advance the clock.
  Enforced by: `W417` — a control emits an event, and only an event field or a signal writes state; the grammar has no control-writes-field spelling
- A widget with any pulse or emphasis has exactly one tick; a widget with a
  tick and no motion is an error.
  Enforced by: `W405` · `W414` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `advance`, `rearm`. Is the `tick` phase.

---

## StateField

**1. Declared type.** One named, typed field of the widget's state. The type is
declared, one of `flag`, `counter`, `count`, `text`.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| StateField → Widget | exactly 1 | `W006` — the state block is singular |
| StateField → writer | 1..n, each an [EventField](#eventfield), an event `toggles` clause, or a runtime signal | `W409` · spec `internal/validate/validate_test.go` |
| StateField ← Predicate reference | 0..n (`flag` only) | `W301` · `W416` — 0..n; a flag used as a predicate resolves and bounds a numeric only |
| StateField ← Tick | 0..1 (`counter` only) | `W406` · `W418` — a counter re-arms the tick and orders a stream |

**3. Invariants.**

- A field written by nothing is an error: state nothing can reach is state
  that renders one value forever. There are exactly three writers — an
  [EventField](#eventfield) assigning it, an [EventDeclaration](#eventdeclaration)
  flipping it, and a runtime signal. The third exists because a field like
  "the client is behind" has a real writer that is neither the browser nor the
  data stream (`live/core.go:293-294`), and without it that field would be
  unwritable and therefore unexpressible.
  Enforced by: `W409` · spec `internal/validate/validate_test.go`
- A `counter` is monotonic non-decreasing; a `count` is not.
  Enforced by: refinement `FieldTypeValue.value` · spec `internal/validate/refinement_agreement_test.go` · ASPIRATIONAL (monotonicity of a counter is a runtime reduce guarantee, app.go)
- Only `counter`, `count` and `text` fields may be interpolated into a
  [Label](#label)'s text. A `flag` may not: rendering a boolean as text is a
  decision an author should make with a [Binding](#binding) clause, not one a
  formatter should make for them.
  Enforced by: `W206` · `W207` · spec `internal/validate/validate_test.go`
- A renderer never writes state. The only writers are the `event` phase and
  nothing else, which is what makes `render` a pure function of state
  (`live/core.go:39-43`).
  Enforced by: ASPIRATIONAL (runtime guarantee — render reads state and writes none, live/core.go:39-43)
- Field names are `lowerCamelCase` and unique within the widget, sharing a
  namespace with [Predicate](#predicate).
  Enforced by: refinement `Identifier.value` · `W101` · `W301` · spec `internal/validate/refinement_agreement_test.go`

**4. Verbs and events.** `read`, `write`. Participates in `event` and `render`.

---

## Predicate

**1. Declared type.** A named boolean over state. It arises from exactly two
**declaration** forms, both explicit: a [StateField](#statefield) of type
`flag` is an atomic predicate under its own name, and a `predicate` block is a
composition of other predicates.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Predicate → Widget | exactly 1 | `W006` — the predicates block is singular |
| composed Predicate → `requires` Predicate | 0..n | `W201` · `W203` · `W408` |
| composed Predicate → `forbids` Predicate | 0..n | `W201` · `W203` · `W408` |
| composed Predicate → numeric bound on a `counter` or `count` field | 0..n | `W416` · `W201` |
| Predicate ← Binding `when` clause | 0..n | `W201` · `W410` |
| Predicate ← Motion gate, Indicator tone, Control pressed-state | 0..n | `W201` · `W410` |

**3. Invariants.**

- The two forms share one namespace, and a composed predicate may not take the
  name of a flag field. A reader must never have to ask which kind a name is
  in order to know what it means.
  Enforced by: `W301` · spec `internal/validate/validate_test.go` — a composed predicate may not take a flag field's name
- A composed predicate has at least one clause; an empty composition is
  vacuously true, which is a way of writing "always" that does not look like
  one.
  Enforced by: `W303` · spec `internal/validate/validate_test.go` — an empty composition is refused as underfilled
- The composition is a **conjunction only**: `requires` are ANDed, `forbids`
  are ANDed after negation, numeric bounds are ANDed. There is no disjunction
  and no general negation operator in v0 — the shipped widget's `live()` is
  exactly this shape (`app.go:92-94`), and adding `anyOf` later is additive
  rather than breaking.
  Enforced by: `W013` · spec `internal/validate/validate_test.go` — the grammar has only requires/forbids/bounds; no disjunction or general negation
- A numeric bound is a comparison against an integer literal only. It never
  compares two fields, so no evaluation order exists to get wrong. `atLeast`
  and `atMost` are both available even though only one has a shipped caller:
  half of a symmetric pair is the kind of gap an author fills by inventing a
  spelling.
  Enforced by: `W416` · `W110` — a bound compares a numeric field against an integer literal; the grammar has no field-to-field comparison
- The reference graph is acyclic.
  Enforced by: `W408` · spec `internal/validate/validate_test.go`
- A predicate referenced by nothing is an error.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `evaluate`. Participates in `render` and `tick`.

---

## Binding

**1. Declared type.** A named, ordered, **total** decision from widget state to
one rendered text value — the shape every shipped label function already has
(`app.go:41-49`, `103-118`, `120-131`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Binding → Widget | exactly 1 | `W006` — the bindings block is singular |
| Binding → guard clause | 1..n, each with a polarity (`when` / `whenNot`), naming exactly 1 Predicate, carrying exactly 1 text template | `W303` · `W201` · spec `internal/validate/validate_test.go` |
| Binding → `otherwise` clause | exactly 1 | `W401` · spec `internal/validate/validate_test.go` |
| Binding ← Label | 1..n (a binding no label names is an error) | `W410` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- **Totality.** Exactly one `otherwise` is required, so a binding always
  produces a value and a renderer never has to invent one. This is the single
  most load-bearing invariant in the ontology: it is what makes a generated
  render total without a runtime fallback path.
  Enforced by: `W401` · spec `internal/validate/validate_test.go`
- Clause order is significant and is declaration order; the first matching
  clause wins. There is no priority number and no "most specific match".
  Enforced by: ASPIRATIONAL (generator obligation — the IR is an ordered sequence, so the first-match-wins order is declaration order by construction)
- No clause may be unreachable given the predicates already matched above it
  — a `when` whose predicate is implied by an earlier clause's predicate is an
  error, because it is dead text an author believes is live.
  Enforced by: `W415` · spec `internal/validate/validate_test.go`
- A binding referenced by no label is an error.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`
- A guard has a **polarity**, and the two polarities are two constructs rather
  than two spellings of one. Negative guards were first modelled as
  `forbids`-only predicates; tried against the shipped five-way status decision
  (`app.go:103-118`) that needed four ceremonial predicates whose names an
  author has no canonical way to choose, which is exactly the inconsistency the
  design exists to remove.
  Enforced by: `W013` · spec `internal/validate/validate_test.go` — when and whenNot are two keywords, so polarity is two constructs
- A clause's text is a **template**: a sequence of literal runs and
  interpolations of `counter`, `count` or `text` state fields. Every
  interpolated name must resolve, and a template that interpolates a `flag` is
  an error.
  Enforced by: `W206` · `W207` · `W103` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `evaluate`. Participates in `render`.

---

## Label

**1. Declared type.** A named text slot holding **exactly one** source, which
is either a literal string or a reference to a [Binding](#binding).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Label → Widget | exactly 1 | `W006` — the labels block is singular |
| Label → source | exactly 1 (literal *xor* Binding) | `W402` · spec `internal/validate/validate_test.go` |
| Label → Slot / Node title / Node caption / Channel legend / Control caption / Indicator text | 1..n (a label filling nothing is an error) | `W410` · spec `internal/validate/validate_test.go` |

**3. Invariants.**

- Exactly one source. A label is never both literal and bound, and never
  neither.
  Enforced by: `W402` · spec `internal/validate/validate_test.go`
- Literal text is a **template** and is **data**: it is escaped at render, after
  interpolation, and never interpolated as markup or as CSS. This is deliberately the inverse of the
  [Token](#token) rule, and the reason is where the value lands — a label's
  value reaches a text node, a token's reaches a CSS declaration
  (`palette.go:23-27`).
  Enforced by: `W103` · ASPIRATIONAL (generator obligation — literal text is escaped at render, after interpolation, palette.go:23-27)
- A label filling no slot is an error.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`
- Literal text carries no host name, address, path or operator identifier —
  the same rule as the widget, restated here because a label is the one place
  free text enters the language.
  Enforced by: `W605` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `resolve`, `escape`, `render`. Participates in
`render`.

---

## Slot

**1. Declared type.** A named, fixed-arity position in the widget's chrome that
a [Label](#label) is placed into. The set of slots is **closed by the dialect
version** and is not author-extensible: `title`, `source`, `description`,
`stat`.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Widget → `title` slot | exactly 1 | `W601` · spec `internal/validate/validate_test.go` |
| Widget → `source` slot | 0..1 | `W302` — a singular slot, not repeated |
| Scene → `description` slot | exactly 1 | `W602` · spec `internal/validate/validate_test.go` |
| Widget → `stat` slot | 0..n, ordered | `W201` · ASPIRATIONAL (declaration order preserved by construction — the IR slot list is an ordered sequence) |
| Slot → Label | exactly 1 | `W106` · `W201` · `W203` |
| Label → Slot | 0..n | `W410` — a label filling nothing is an error |

**3. Invariants.**

- `title` and `description` must be filled. Both are accessibility surfaces;
  an unfilled one renders a widget with no accessible name, which is a defect
  that no browser reports.
  Enforced by: `W601` · `W602` · spec `internal/validate/validate_test.go`
- `stat` slots render in declaration order — the byte-determinism rule
  (`live/core.go:39-43`) applied to the one collection an author can reorder
  freely.
  Enforced by: ASPIRATIONAL (generator obligation — stat slots render in declaration order, which the IR slot list preserves by construction)
- A slot holds exactly one label. There is no multi-label slot, and no slot
  that concatenates.
  Enforced by: `W302` · `W110` — a slot clause names exactly one label; there is no multi-label or concatenating slot

**4. Verbs and events.** `fill`, `render`. Participates in `render`.

---

## Indicator

**1. Declared type.** A named status mark whose tone is selected by exactly one
predicate — the shipped card's connection chip, whose dot is one colour when
live and another when not (`view.templ:273`, `home.css:371-374`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Indicator → Widget | exactly 1 | `W006` — the indicators block is singular |
| Indicator → Label | exactly 1 | `W106` · `W201` · `W203` |
| Indicator → Predicate | exactly 1 | `W106` · `W201` · `W203` |

**3. Invariants.**

- Tone is **binary in v0**: the predicate holding selects the palette's positive
  token, not holding selects its warning token. There is no third state and no
  authored token, which is exactly the shipped rule and is what stops an
  indicator from drifting into a general-purpose coloured dot.
  Enforced by: `W013` — an indicator names one predicate; there is no third-state or authored-token spelling
- The label is the accessible carrier and may not be empty. An indicator is
  **never the only carrier of a status**: colour alone is not a signal a
  colour-blind or screen-reader user receives, so the language has no
  label-less indicator.
  Enforced by: `W604` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `evaluate`, `render`. Participates in `render`.

---

## Control

**1. Declared type.** A named interactive element that turns exactly one DOM
interaction into exactly one declared event.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Control → Widget | exactly 1 | `W006` — the controls block is singular |
| Control → caption Label | exactly 1 | `W106` · `W201` · `W203` |
| Control → DOM trigger | exactly 1, from the closed set `click`, `change`, `input`, `submit` | refinement `TriggerValue.value` · `W106` · `W105` · spec `internal/validate/refinement_agreement_test.go` |
| Control → EventDeclaration | exactly 1 | `W106` · `W201` · `W411` |
| Control → pressed-state Predicate | 0..1 | `W201` · `W203` · `W107` — optional, resolves, not repeated |

**3. Invariants.**

- **A widget declares every event it emits.** A control naming an undeclared
  event is an error at validation, not a runtime refusal — the runtime is
  default-deny and would refuse it with `UNKNOWN_EVENT`
  (`live/config.go:85-91`), which is a correct but very late way to find out.
  Enforced by: `W411` · spec `internal/validate/validate_test.go`
- Neither the trigger nor the event name may contain `:` or `;`, and the event
  name may not be empty. These are structural in the runtime's binding grammar:
  a stray `:` shifts every component behind it and silently turns a declared
  debounce into a throttle, and an empty name silences every binding behind it
  on the same DOM event. The runtime panics rather than degrading
  (`live/templ.go:104-118`); the validator's job is to make sure it never has
  to.
  Enforced by: refinement `WireName.value` · `W109` · spec `internal/validate/refinement_agreement_test.go`
- At most one control per `(element, trigger)` pair. Composing several bindings
  on one element is a real runtime capability (`OnAll`) and is deliberately not
  in v0 — see [What was cut](#what-was-cut).
  Enforced by: ASPIRATIONAL (not yet modelled — v0 controls have no shared element, so (element, trigger) uniqueness has nothing to key on; OnAll is deferred, dialect.md §11)
- A control with a pressed-state predicate renders `aria-pressed`; one without
  does not. There is no "sometimes pressed" control.
  Enforced by: ASPIRATIONAL (generator obligation — pressedWhen is optional, so a pressed control renders aria-pressed and one without does not, by construction)

**4. Verbs and events.** `bind`, `emit`. Is a source of the `event` phase.

---

## EventDeclaration

**1. Declared type.** A named inbound event this widget accepts, together with
the wire name the host registers for it.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| EventDeclaration → Widget | exactly 1 | `W006` — the events block is singular |
| EventDeclaration → EventField | 0..n | refinement `EventFieldWireName.value` · `W101` · spec `internal/validate/refinement_agreement_test.go` |
| EventDeclaration → `toggles` StateField | 0..1, of type `flag` | `W417` · `W201` |
| EventDeclaration ← Control | 0..n | `W411` · `W410` |
| EventDeclaration ← Stream | 0..n | `W201` · `W410` |

**3. Invariants.**

- The declared set is **exhaustive and default-deny**. An event not declared
  here is refused by the runtime, never dispatched and never ignored
  (`live/config.go:85-91`).
  Enforced by: `W411` · spec `internal/validate/validate_test.go`
- The wire name is unique within the widget and matches the runtime's
  identifier shape.
  Enforced by: refinement `WireName.value` · `W109` · `W301` · spec `internal/validate/refinement_agreement_test.go`
- An event declared but named by neither a control nor a stream is an error:
  nothing can ever send it.
  Enforced by: `W410` · spec `internal/validate/validate_test.go`
- Runtime-minted events — effect failure, slow client, client recovered
  (`live/core.go:219`, `293-294`) — are **never** declared here. They are not
  browser-sendable, and declaring one would put a name in the accept-list that
  the runtime mints itself.
  Enforced by: `W413` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `accept`, `authorize`, `reduce`. Is the `event` phase.

---

## EventField

**1. Declared type.** A named, typed field carried by exactly one event
declaration, which writes exactly one state field.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| EventField → EventDeclaration | exactly 1 | `W013` — a field line lives only in an event block |
| EventField → StateField | exactly 1 | `W106` · `W201` · `W203` |

**3. Invariants.**

- The field's declared type matches the state field it writes.
  Enforced by: `W412` · ASPIRATIONAL (W412 is unreachable in dialect 0 — a field takes the state field type, so the two cannot disagree; errors.md keeps it for the typed-payload version)
- A field that writes no state field is an error; a payload nothing reads is a
  payload that will drift from what the sender sends.
  Enforced by: `W106` · `W201` · spec `internal/validate/validate_test.go` — the writes clause is required and resolves
- Wire field names are `lower_snake_case` and unique within their event,
  matching the shipped snapshot vocabulary (`feed.go:17-24`).
  Enforced by: refinement `EventFieldWireName.value` · `W101` · `W301` · spec `internal/validate/refinement_agreement_test.go`
- **A whole event is applied or none of it is.** The shipped reducer parses
  every field, checks every parse *and* the cross-field relation
  (`aliveVoters > voters`), and returns the previous state untouched if
  anything fails (`app.go:196-200`). Per-field partial application would render
  a state that never existed upstream.
  Enforced by: ASPIRATIONAL (runtime guarantee — the reducer applies a whole event or none of it, app.go:196-200)

**4. Verbs and events.** `parse`, `validate`, `write`. Participates in `event`.

---

## Stream

**1. Declared type.** A named long-running external subscription that delivers
one event declaration. It is a **declaration, never a connection**: the widget
names what it wants, the host owns the transport (`live/core.go:191-196`).

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Stream → Widget | exactly 1 | `W006` — the data block is singular |
| Stream → EventDeclaration | exactly 1 | `W106` · `W201` · `W203` |
| Stream → ordering StateField | 0..1, of type `counter` | `W418` · `W201` |

**3. Invariants.**

- Started at `mount`, stopped at `unmount`. There is no spelling that starts a
  stream from a control.
  Enforced by: ASPIRATIONAL (runtime guarantee — the host starts a stream at mount and stops it at unmount, live/core.go:191-196; no grammar spelling starts one from a control)
- A widget that declares a stream **must tolerate its failure**, because a
  failed effect comes back as an ordinary event rather than as silence
  (`live/core.go:219`, handled at `app.go:173-179`). Concretely: every binding
  is total, so a widget renders correctly with no delivery ever having
  happened.
  Enforced by: `W401` · ASPIRATIONAL (a stream failure returns as an ordinary event, and every binding is total, so a widget renders with no delivery)
- The ordering field, when present, is a `counter`, and it is the widget's
  [Tick](#tick) if it has one.
  Enforced by: `W418` · spec `internal/validate/validate_test.go`
- A stream carries no address, no credential and no topic string that encodes
  one — only a source name.
  Enforced by: `W605` — a stream names a source string only; no address, credential or topic

**4. Verbs and events.** `subscribe`, `deliver`, `fail`, `unsubscribe`. Sources
the `effect` and `tick` phases.

---

## Palette

**1. Declared type.** A named, **externally owned** namespace of validated
design-token values. The widget language references a palette; it never
declares one and never carries a token's value.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Widget → Palette | exactly 1 | `W002` · `W208` — the palette directive is required and names a known palette |
| Palette → Token | 1..n | refinement `TokenValue.value` · `W204` · spec `internal/validate/refinement_agreement_test.go` |

**3. Invariants.**

- The palette is validated by its owner before any widget renders, and
  validation reports **every** failing entry rather than the first
  (`palette.go:119-127`). A widget document may therefore assume that a token
  that resolves is safe to substitute into a stylesheet.
  Enforced by: ASPIRATIONAL (palette-owner guarantee — the palette validator reports every failing entry, palette.go:119-127; not a widget-document property)
- Unset means "keep the shipped value", not "empty" (`palette.go:20-21`). A
  widget referencing an unset token gets the shipped one, never a blank.
  Enforced by: ASPIRATIONAL (palette-owner behaviour — unset keeps the shipped value, palette.go:20-21)
- One inventory serves both validation and rendering (`palette.go:82-115`). The
  widget validator resolves against the same list the renderer reads, so
  "validated" and "rendered" cannot mean different sets.
  Enforced by: ASPIRATIONAL (palette-owner invariant — one inventory serves validation and rendering, palette.go:82-115)
- The set of palettes is not extensible from a widget document. This is the
  ontology's main application of *centralize the primitive at the layer that
  owns its semantics*: colour policy belongs to the design system, and a widget
  that could mint a colour would be a widget that could leave the design
  system.
  Enforced by: `W208` · spec `internal/validate/validate_test.go`

**4. Verbs and events.** `resolve`, `validate`. Participates in `render`.

---

## Token

**1. Declared type.** A named entry of the widget's palette, resolving to one
validated CSS value.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| Token → Palette | exactly 1 | refinement `TokenValue.value` · `W204` · spec `internal/validate/refinement_agreement_test.go` |
| Token ← Role, Channel, Orbit | 0..n each | refinement `TokenValue.value` · `W204` · `W203` · spec `internal/validate/refinement_agreement_test.go` |

**3. Invariants.**

- The token namespace is **closed at seven semantic names** — `surface`, `ink`,
  `muted`, `rule`, `accent`, `positive`, `warning` — rather than exposing a
  palette's own field spellings. This is what makes the reuse claim real: a
  widget renders under any palette that maps the seven, so a document is not
  coupled to the palette it was written against.
  Enforced by: refinement `TokenValue.value` · `W204` · spec `internal/validate/refinement_agreement_test.go`
- A widget writes a token **name only, never a value**. There is no inline
  colour, no hex literal, and no `var(--…)` escape hatch in the language.
  Enforced by: `W505` · spec `internal/validate/validate_test.go`
- An unresolved token name is an **error, never a fallback**. A fallback here
  would render a plausible-looking widget in the wrong colours, which is the
  failure mode hardest to notice in review.
  Enforced by: `W204` · spec `internal/validate/validate_test.go`
- Token values are validated rather than escaped, because a custom-property
  value is substituted into a stylesheet as CSS rather than as text
  (`palette.go:23-27`). The widget language inherits that guarantee rather than
  re-implementing it.
  Enforced by: ASPIRATIONAL (palette-owner guarantee — token values are validated as CSS, not escaped, palette.go:23-27)

**4. Verbs and events.** `resolve`. Participates in `render`.

---

## LifecyclePhase

**1. Declared type.** The closed set of phases a host drives a widget through.
It is not author-extensible: a widget cannot invent a phase, and the generator
emits code for these and no others.

The set: `register`, `mount`, `event`, `tick`, `render`, `effect`, `unmount`.

**2. Relations.**

| Relation | Cardinality | Enforced by |
|---|---|---|
| `register` → process | exactly 1 per widget per process, before any session | ASPIRATIONAL (runtime lifecycle, live/core.go; register runs once per process, not a document property) |
| `mount` → session | exactly 1, first | ASPIRATIONAL (runtime lifecycle, live/core.go; mount is the first session phase) |
| `unmount` → session | exactly 1, last | ASPIRATIONAL (runtime lifecycle, live/core.go; unmount is the last session phase) |
| `event`, `tick`, `render`, `effect` → session | 0..n each | ASPIRATIONAL (runtime lifecycle, live/core.go; these phases recur per session) |
| `render` → Slot, Scene, Motion | reads all, writes none | ASPIRATIONAL (runtime lifecycle, live/core.go; render reads all, writes none) |

**3. Invariants.**

- No `event`, `tick`, `render` or `effect` occurs before `mount` or after
  `unmount`.
  Enforced by: ASPIRATIONAL (runtime guarantee — no event, tick, render or effect before mount or after unmount, live/core.go)
- `register` happens once per process, before any session exists. A widget
  whose identity depended on a session could not be registered.
  Enforced by: ASPIRATIONAL (runtime guarantee — register happens once per process before any session, live/core.go)
- **A failure is not a phase.** A failed effect re-enters `event` carrying the
  failure, so a reducer sees it in the same switch as everything else
  (`live/core.go:219`, `app.go:173-179`). A failure that arrives as an event is
  replayable; one that arrives as a log line is not.
  Enforced by: ASPIRATIONAL (runtime guarantee — a failed effect re-enters the event phase, live/core.go:219, app.go:173-179)
- `render` is total and pure: it reads state and writes none, and for equal
  state it produces equal bytes.
  Enforced by: ASPIRATIONAL (runtime guarantee — render is total and pure, live/core.go:39-43)
- `tick` is the only phase that re-arms motion, and the only phase a
  [Stream](#stream) can drive without a user.
  Enforced by: `W405` · ASPIRATIONAL (runtime guarantee — the tick is the only phase that re-arms motion; W405 gates the tick existing at all)

**4. Verbs and events.** The phase names are the verbs. The events crossing
them: an inbound [EventDeclaration](#eventdeclaration), a
[Stream](#stream) delivery, an effect failure, a slow-client or
client-recovered notice from the runtime (`live/core.go:293-294`).

---

## What was cut

135 inventory concepts → 25 entries. 104 concepts merged; **31 were cut**. Each
cut is one of five kinds.

### Host concerns — 13 concepts

Real, but not the widget's to declare. A widget document that named any of
these could not be mounted twice, or could not be published.

| Inventory | Concept | Why it is the host's |
|---|---|---|
| 1.29, 5.8, 5.9 | The document shell, `Document`, `NoRuntime` | One document hosts many widgets |
| 2.1 | Mount path | An address; also the kind of string the export gate would rather never see in a widget document |
| 2.29, 5.22 | `Config` / `live.Config[S]` | Host wiring: the widget declares streams, the host decides transport |
| 2.34, 5.26 | Session limits | Host capacity policy |
| 3.4, 3.6 | Fan-out, latest-value-wins delivery | Transport semantics the host owns; the widget only needs the monotonicity guarantee, which [Tick](#tick) carries |
| 5.10 | `data-gotth-status` | A host fact. Recorded instead as a **generator obligation**: it is compiled into every motion gate whether or not an author mentions it |
| 5.21 | Session identity | A per-identity widget is a different program |
| 5.25 | `Anonymous` / `AllowAll` / `NoCSRFCheck` | Host security posture |

### Derived, so authoring it invites drift — 4 concepts

| Inventory | Concept | What it is derived from |
|---|---|---|
| 1.23 | Channel legend | The declared [Channels](#channel), in order |
| 4.9 | Edge geometry (`width`, `rotate`) | The two endpoints' [Placements](#placement) |
| 2.14, 5.13 | The dirty projection | The set of state fields a widget's bindings and predicates read, together with the [Tick](#tick) its motion re-arms on, whose value the markup carries. Under-declaring it is a correctness bug (`live/core.go:45-49`), so it is exactly the kind of thing a human should not be asked to maintain |

### Deferred to a later dialect version — 5 concepts

Not typed badly now. Each has either no invariant yet or exactly one caller,
and *one caller is not two*.

| Inventory | Concept | Why not yet |
|---|---|---|
| 1.27 | `aria-live` politeness | No invariant distinguishes polite from assertive in v0 |
| 1.28 | A hidden control reporting a browser measurement | Not a user interaction; [Control](#control) requires a user-facing caption. Needs its own type, not a weakened one |
| 2.23 | Write-once state fields | One shipped instance |
| 5.5 | `OnWith` — per-binding debounce, throttle, static fields | No shipped caller |
| 5.6 | `OnAll` — several bindings on one element | No shipped caller |

### Out of scope by design — 3 concepts

| Inventory | Concept | Reason |
|---|---|---|
| 1.26 | "A second widget on the page" | Not a construct: it is a second document, which the language already expresses |
| 4.5 | The page entrance animation | Page chrome. It is not behind the widget's motion gate in the shipped CSS (`home.css:170`, `299`), and pulling it in would put page layout inside a widget |
| 5.7 | `Preserve()` — third-party-owned DOM inside a live region | A widget that hosts foreign DOM is not a widget in this language's sense. The escape hatch stays available at the gotth-live layer for the code that needs it |

### Carried into the error catalogue instead — 6 concepts

The palette's validator is a *method*, not a vocabulary. These shape
[`errors.md`](errors.md) rather than becoming language constructs.

| Inventory | Concept | Where it lands |
|---|---|---|
| 6.2 | One sentinel error class callers match on | The `W` identifier scheme |
| 6.6 | Report every failure, not the first | The validator's whole-document pass |
| 6.8 | A forbidden thing carries its own human reason | The message template's *reason* clause |
| 6.10 | Structural balance (an unclosed group swallows what follows) | The block-nesting error class |
| 6.11 | Messages name the subject and what disqualified it | The message template |
| 6.12 | Validation is separate from rendering, and says so | "No document generates before it validates" |

### Candidate types that failed the four-layer test

Seven types were drafted and did not survive as entries:

| Candidate | Layer it failed | Disposition |
|---|---|---|
| **Legend** | 3 — no invariant beyond "shows the channels" | Cut; it is a projection of [Channel](#channel) |
| **Stat** | 2, 3 — no relations or invariants distinct from a label in a position | Merged into [Slot](#slot) |
| **Region** | 3 — 1:1 with the widget, no invariant of its own | Merged into [Widget](#widget) as its identity |
| **Card chrome / header / footer** | 2, 3 — pure layout, no relations | Merged into [Slot](#slot) |
| **Marker** (a node's dot) | 4 — no verbs of its own; appearance is fixed by the role | Merged into [Role](#role) |
| **Animation** (one general type) | 2 — relations differ irreconcilably (edge + channel vs node), so a merged type has optional halves | **Split** into [Pulse](#pulse) and [Emphasis](#emphasis) |
| **Session** | 1 — it is a host type; a widget document never names one | Rejected as a host concern |
