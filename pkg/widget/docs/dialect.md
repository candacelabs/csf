# The widget dialect, v0

A Mermaid dialect for declaring widgets. This document is the surface syntax
for [`ontology.md`](ontology.md); the errors it can produce are catalogued in
[`errors.md`](errors.md); four commented documents in
[`examples/`](examples/) are the worked cases.

This document was written before any parser existed, deliberately: an agent
that can see the generator shapes the language to suit the generator, and the
dependency has to run the other way. The interpreter now exists — it is
[`..`](..), whose entry point is `widget.Interpret` — and it was built against
this spec rather than the spec against it. Where implementation found a defect
in the spec, the spec was corrected in the same commit and the correction is
marked in place; there is no second, truer document.

---

## 1. Why a Mermaid dialect at all

The empirical case is in the program goal, and it is about token cost rather
than taste. An invented DSL starts an agent below 20% accuracy purely from
syntax unfamiliarity, and every token spent teaching brackets and separators is
a token not spent on the widget. Mermaid has enormous training-data presence,
so a dialect of it pays for its custom vocabulary only — the block structure,
the statement-per-line rhythm, the `%%` comment, the `… end` block terminator
and the `keyword Identifier` header all come for free.

That transfer lives in the **body**, not in the header word. A document's first
line naming a diagram type mermaid has never heard of costs an agent nothing;
a body full of unfamiliar punctuation costs it accuracy on every line. So the
dialect keeps mermaid's *shape* aggressively and its *vocabulary* not at all.

---

## 2. Base form: flowchart flavour

Mermaid's two candidate base forms are the state-diagram family
(`stateDiagram-v2`) and the flowchart family (`flowchart`). **Flowchart wins**,
for three reasons in descending weight.

**The subject is a topology, not a machine.** The flagship widget is three
nodes, two lines and traffic travelling along them. Its nodes are *not* states:
they coexist, they are all occupied at once, and nothing transitions between
them. A state diagram would force each scene node into a `state` and each edge
into a transition, which mis-types the central concept — a
[Pulse](ontology.md#pulse) is *traffic on an edge*, not a transition between
configurations — and would leave a node's [Role](ontology.md#role) and
[Placement](ontology.md#placement) with no natural home.

**Flowchart is the denser training corpus.** Of mermaid's diagram types,
`flowchart`/`graph` is the one an agent has seen most, and `A --> B` is the
single most-recognised construct in the language. Building on the densest
region of the corpus is the whole point of the choice.

**Node-then-edge ordering matches how the ontology resolves.** A flowchart
declares participants, then relations between them. So does this dialect:
[Placements](ontology.md#placement) and [Roles](ontology.md#role) before
[Nodes](ontology.md#node), nodes before [Edges](ontology.md#edge), edges before
[Pulses](ontology.md#pulse). Every reference points backwards, which is what
lets the validator resolve in one pass and report an unknown name at the exact
line that used it.

The widget lifecycle *is* a state machine, and that is the obvious counter-
argument. It does not apply, because the lifecycle is fixed by the SDK and
identical for every widget ([LifecyclePhase](ontology.md#lifecyclephase) is
a closed set). A widget document never declares its lifecycle, so the language
never has to express one.

### What flowchart's shape is kept, and what is dropped

| Mermaid construct | Kept? | Why |
|---|---|---|
| Fenced block with a type header | Kept | The recognisable frame |
| One statement per line | Kept | No statement separators to get wrong |
| `%% comment` to end of line | Kept | The one piece of mermaid punctuation with no alias |
| `subgraph … end` block shape | Kept, spelled `scene … end` and used for every container | Familiar terminator, explicit nesting |
| `keyword Identifier` declarations | Kept | Every construct is `keyword id …` |
| `A --> B` and its nine siblings | **Dropped** | See [§ 4.1](#41-one-canonical-form-per-construct) |
| Node shape brackets `[]`, `()`, `{}`, `(())`, … | **Dropped** | Appearance comes from the role, not the punctuation |
| `direction LR` | **Dropped** | The scene is absolutely placed, so a direction would be a keyword that does nothing — worse than absent, because an author would expect it to work |
| `classDef` / `class` / `:::` styling | **Dropped** | Styling is a [Role](ontology.md#role) or a [Token](ontology.md#token) |
| `%%{init: {…}}%%` directives | **Dropped** | Replaced by verbose preamble directives ([§ 5](#5-the-preamble)) |

---

## 3. The container, and what standard mermaid does with it

### 3.1 Two containers, one body

A widget document is either a standalone `.widget` file, or a fenced block in
markdown whose info string is exactly `widget`:

~~~
```widget
widget NodeStatus
dialect 0
…
```
~~~

The body is identical in both. The container is transport, not syntax: there is
no construct that exists in one and not the other, and no directive that
depends on which one is in use.

### 3.2 The ruling: **reject, and never let it come up**

The question is what a standard mermaid renderer does with a dialect file. The
answer has two halves.

**In practice, it never sees one.** The fence info string is `widget`, not
`mermaid`. Markdown renderers — including the ones that render mermaid natively
in this repository's docs site and in published artifacts — dispatch on the
info string, so a `widget` block renders as an ordinary monospace code block.
No mermaid parser is invoked, no error box appears, and the source is legible
to a human reviewer exactly as written. **This is the graceful degradation**,
and it is graceful precisely because it is total: the reader sees all of the
document or none of it, never a picture that omits a third of it.

**If forced, it rejects the whole block, and that is correct.** Fence the same
body as `mermaid` and mermaid fails to match any known diagram type and renders
its syntax error. That is the intended behaviour and the dialect will not be
contorted to avoid it.

The alternative — a body that is valid mermaid, with the widget semantics
smuggled into `%%` comments so a standard renderer draws the topology — was
considered and rejected:

- A renderer that draws the nodes and edges but silently drops the roles, the
  motion gate, the live bindings and the accessibility description produces a
  picture a reviewer believes they have reviewed. Reviewing a third of a widget
  while believing you reviewed all of it is worse than reviewing none of it.
- It would put the semantics in comments, which fights two of the four Anka
  rules at once (explicit named blocks; verbose keywords over sigils) and makes
  the load-bearing half of every document look ignorable.
- It buys a rendering that is available anyway. Compatibility is kept where it
  is cheap by **projection**: the interpreter can emit a standard `flowchart`
  block from the resolved IR for docs and artifacts. A projection is honest
  about being a partial view; a source file that happens to render is not.

---

## 4. The four Anka rules, and where each is applied

### 4.1 One canonical form per construct

Every construct has exactly one spelling. Where mermaid offers a family, the
dialect takes none of them.

| Construct | Mermaid's spellings | The dialect's |
|---|---|---|
| An edge | `-->` `---` `-.->` `==>` `--x` `--o` `<-->` `-- t -->` `-->\|t\|` `~~~` | `edge <id>` block with `from` and `to` |
| A node | 12 bracket shapes | `node <id>` block with `role` |
| An edge label | two spellings | none — edges are decorative, [Channels](ontology.md#channel) carry the meaning |
| Styling | `classDef`, `class`, `:::`, `style` | `role`, `channel`, `token` |
| A comment | `%%` | `%%` (mermaid's only single-form construct, so it is kept unchanged) |

Dropping `-->` is the one place a mermaid-familiarity cost is paid on purpose,
and it is paid because the alias fan-out is ten-to-one. An agent reaching for
`-.->` to mean "a dashed, therefore acknowledging, edge" is exactly the failure
mode: it encodes semantics in punctuation the interpreter would then have to
guess at. There is also nowhere in `A --> B` to put the edge's identifier,
which rule 4.2 requires.

The rule extends past syntax into **document structure**: the fourteen blocks
appear in one fixed order ([§ 6](#6-block-order)), so two documents describing
the same widget are the same document, and a diff between two versions is a
diff of content rather than of arrangement.

It also extends to **form**: whether a construct is a single line or a block is
fixed by the grammar, never chosen by the author. `label` is always a line;
`node` is always a block. There is no "short form".

### 4.2 Every intermediate named

Nothing in a document is anonymous or positional.

- Every [Edge](ontology.md#edge), [Pulse](ontology.md#pulse),
  [Emphasis](ontology.md#emphasis), [Orbit](ontology.md#orbit),
  [Placement](ontology.md#placement), [Role](ontology.md#role),
  [Channel](ontology.md#channel), [Binding](ontology.md#binding),
  [Label](ontology.md#label), [Control](ontology.md#control) and
  [Stream](ontology.md#stream) carries an identifier.
- Text never appears where a reference belongs. A node's title names a
  [Label](ontology.md#label); the label holds the text. So a string that
  appears twice is one label named twice, and changing it is one edit.
- Node placement names a [Placement](ontology.md#placement) rather than
  carrying two numbers, so "where the west node goes" is a thing with a name
  and the numbers live in one place.
- There is no chaining. `a --> b --> c` in mermaid creates an unnamed
  intermediate relation; here it is two named edges.

The payoff is diagnostic: every error in [`errors.md`](errors.md) can name the
thing that is wrong, and every reference error can point at both the use and
the declaration.

### 4.3 Explicit named blocks

Every statement lives inside exactly one named block, closed by `end`. The only
statements at document scope are the four preamble directives.

The block set is closed (fourteen names, [§ 6](#6-block-order)); an author
cannot invent one. A block that would be empty is **omitted**, and writing an
empty block is an error rather than a no-op, because an empty `motion` block
and no `motion` block would otherwise be two spellings of the same thing.

Nesting is at most two deep — container, then declaration — and is never
implicit. `scene` holds `node` and `edge` blocks; nothing holds a `scene`.

### 4.4 Verbose keywords over sigils

The complete punctuation inventory of the language: `%%`, `"`, `{`, `}`, and
the digits and letters of identifiers. That is all.

Every relation is a word, and this is the complete inventory.

| Group | Words |
|---|---|
| Preamble directives | `widget` `dialect` `region` `palette` |
| Block names (14) | `state` `predicates` `bindings` `labels` `chrome` `roles` `channels` `placements` `scene` `motion` `indicators` `controls` `events` `data` |
| Declaration keywords | `field` `predicate` `binding` `label` `title` `source` `stat` `description` `role` `channel` `placement` `orbit` `node` `edge` `pulse` `emphasis` `indicator` `control` `event` `stream` |
| Clause keywords | `type` `signal` `requires` `forbids` `atLeast` `atMost` `when` `whenNot` `then` `otherwise` `text` `binds` `token` `marker` `emphasis` `direction` `legend` `left` `top` `at` `caption` `from` `to` `carries` `restartOn` `duration` `delay` `milliseconds` `node` `edge` `channel` `label` `positiveWhen` `pressedWhen` `trigger` `emits` `wire` `toggles` `writes` `delivers` `ordering` |
| Closed-set values | `flag` `counter` `count` `text` · `forward` `reverse` · `large` `small` · `allowed` `forbidden` · `click` `change` `input` `submit` · `slowClient` · `surface` `ink` `muted` `rule` `accent` `positive` `warning` |

Seven words appear in more than one group: `text`, `emphasis`, `source`,
`node`, `edge`, `channel`, `label`. That is a real cognitive cost and it is
accepted rather than hidden, under a rule that bounds it: **a word's meaning is
fixed by the block it appears in, and no block accepts two meanings of the same
word.** So `text` is a state-field type inside `state` and a label source inside
`labels`, and never both in one place; `emphasis` starts a declaration inside
`motion` and is a role clause inside `roles`. Eliminating the overlap would
mean `roleEmphasis` and `labelText`, which trades a bounded ambiguity for
unbounded prefixing.

Braces survive in exactly one place — `{fieldName}` inside a label's text — and
`{{` escapes a literal brace. Quotes survive because human text needs a
delimiter. `%%` survives because it is mermaid's, and replacing it would cost
familiarity for nothing.

A comment **occupies a whole line**. There are no trailing comments, which is
mermaid's own rule and is kept for a second reason: with no trailing form, a
`%%` inside a quoted string is unambiguously text, and no author has to reason
about where a comment starts. A comment annotating a statement goes on the line
above it, which is how [`examples/04-wrong-on-purpose.widget`](examples/04-wrong-on-purpose.widget)
names each fault.

Time carries its unit as a word: `duration 820 milliseconds`, never
`duration 820` and never `0.82s`. The shipped CSS writes `0.82s` while the
event payloads write integer milliseconds, and an author reading both would
have to guess which one a bare number is.

---

## 5. The preamble

Four directives, at document scope, in this order, all required:

```widget
widget RaftHeartbeats
dialect 0
region "widget.cluster-heartbeats"
palette fieldStation
```

| Directive | Value | Meaning |
|---|---|---|
| `widget` | `PascalCase` identifier | The widget's name, unique within a host |
| `dialect` | integer | The dialect version this document is written against. A parser refuses a version it does not implement rather than guessing |
| `region` | quoted string matching `^[A-Za-z0-9_:.-]{1,64}$` | The live region identity |
| `palette` | `lowerCamelCase` identifier | The token namespace every `token` reference resolves in |

`region` is the language's **one authored-identity escape hatch**, and it earns
it: a region id is named by every patch on the wire, so changing it is a
client-visible change (`live/core.go:34-37`). A widget renamed in source must
be able to keep the identity a deployed client already knows. Everything else
that could have been authored twice — the legend, the edge geometry, the dirty
predicate — is derived instead.

---

## 6. Block order

Fourteen blocks, in exactly this order. Out-of-order is an error, not a style
preference: the order is the resolution order, so every reference points at
something already declared and the validator resolves in one pass.

| # | Block | Required | Holds |
|---|---|---|---|
| 1 | `state` | yes | `field` lines, 1..n |
| 2 | `predicates` | no | `predicate` blocks |
| 3 | `bindings` | no | `binding` blocks |
| 4 | `labels` | yes | `label` lines, 1..n |
| 5 | `chrome` | yes | `title`, `source`, `stat` lines |
| 6 | `roles` | yes | `role` blocks, 1..n |
| 7 | `channels` | no | `channel` blocks |
| 8 | `placements` | yes | `placement` lines, 1..n |
| 9 | `scene` | yes | `description` line, `orbit` lines, `node` and `edge` blocks |
| 10 | `motion` | no | gate lines, `pulse` and `emphasis` blocks |
| 11 | `indicators` | no | `indicator` blocks |
| 12 | `controls` | no | `control` blocks |
| 13 | `events` | no | `event` blocks |
| 14 | `data` | no | `stream` blocks |

An optional block that would be empty is omitted.

---

## 7. The constructs

Identifiers are `lowerCamelCase` except the widget name, which is `PascalCase`,
and wire field names, which are `lower_snake_case`. Every identifier is unique
across the whole document, with one exception: a `field` of type `flag` and a
`predicate` deliberately share one namespace, because a reader must never have
to ask which kind a name in a `when` clause is.

### 7.1 `state` — fields

```widget
state
  field sequence type counter
  field connected type flag
  field term type count
  field degraded type flag signal slowClient
end
```

`type` is one of `flag`, `counter`, `count`, `text`. A `counter` is monotonic
and is the only type a [Tick](ontology.md#tick) may name; a `count` is an
ordinary number.

`signal <name>` binds a field to a runtime-minted signal rather than to an
event. The v0 signal set has exactly one member, `slowClient`, which is true
while the client is behind and false after it recovers
(`live/core.go:293-294`). It exists because every field needs a writer and this
one's writer is the runtime.

The field a `signal` binds is a `flag`. The runtime writes the signal as true
and false, and no other type can hold either, so `field beats type counter
signal slowClient` is `W419` rather than a widget — a document that once passed
the interpreter and produced Go the compiler refused.

### 7.2 `predicates`

```widget
predicates
  predicate live
    requires connected
    requires authoritative
    requires leaderKnown
    requires hasQuorum
    forbids degraded
  end

  predicate quorumKnown
    requires connected
    requires voters atLeast 1
  end
end
```

Three clause forms, each canonical for one thing:

| Clause | Means |
|---|---|
| `requires <predicate>` | the named predicate must hold |
| `forbids <predicate>` | the named predicate must not hold |
| `requires <numericField> atLeast \| atMost <integer>` | a bound on a `counter` or `count` |

`atLeast` and `atMost` ship together even though only one has a shipped caller.
Half of a symmetric pair is the kind of gap an author fills by inventing a
spelling.

A composed predicate is a **conjunction only**. There is no disjunction in v0,
because the shipped composite is exactly this shape (`app.go:92-94`) and
`anyOf` can be added later without breaking a document that does not use it.

### 7.3 `bindings`

```widget
bindings
  binding leaderTitleText
    whenNot connected then "leader unavailable"
    when leaderKnown then "elected leader"
    otherwise "leader pending"
  end

  binding quorumStatText
    when quorumKnown then "{aliveVoters} / {voters} voters alive"
    otherwise "quorum —"
  end
end
```

An ordered decision, first match wins, **`otherwise` required**. Totality is
the single most load-bearing rule in the language: it is what lets a generated
render be total with no runtime fallback path.

`when` and `whenNot` are two constructs, not two spellings of one: a positive
guard and a negative guard. The alternative — negative guards only via a
`forbids`-only predicate — was tried on the shipped five-way status decision
(`app.go:103-118`) and needed four ceremonial predicate declarations whose
names an author has no canonical way to choose (`notConnected`?
`disconnected`? `connectionLost`?). Inconsistent invented names are precisely
the accuracy loss the design exists to avoid.

A clause's text is a **template**. `{fieldName}` interpolates a state field of
type `counter`, `count` or `text`; a `flag` may not be interpolated, because
rendering a boolean as text is a decision the author should make with a `when`.
`{{` is a literal brace. Every name in a template must resolve.

### 7.4 `labels`

```widget
labels
  label widgetTitleLabel text "Raft-style heartbeats"
  label leaderNameLabel binds leaderTitleText
end
```

Exactly one source: `text "…"` or `binds <binding>`, never both and never
neither. Literal text is escaped at render — it is data, never markup and never
CSS.

### 7.5 `chrome` — filling slots

```widget
chrome
  title widgetTitleLabel
  source widgetSourceLabel
  stat termStatLabel
  stat quorumStatLabel
  stat telemetryStatLabel
end
```

`title` is required. `stat` lines render in declaration order.

### 7.6 `roles`

```widget
roles
  role leader
    token accent
    marker large
    emphasis allowed
  end
  role voter
    token positive
    marker small
    emphasis forbidden
  end
end
```

All three clauses are required. `marker` is `large` or `small`; `emphasis` is
`allowed` or `forbidden` and is declared rather than inferred from whether an
emphasis happens to exist.

### 7.7 `channels`

```widget
channels
  channel heartbeat
    direction forward
    token accent
    legend heartbeatLegendLabel
  end
  channel ack
    direction reverse
    token positive
    legend ackLegendLabel
  end
end
```

`direction` is `forward` (`from` → `to`) or `reverse` (`to` → `from`). A
channel is never bidirectional: two directions are two channels, which is how
the shipped card already spells heartbeat and ack.

The footer legend is rendered from these declarations in order. It is never
authored, so it cannot disagree with the picture.

### 7.8 `placements`

```widget
placements
  placement centre left 50 top 50
  placement west left 21 top 17
  placement east left 81 top 20
end
```

Integers in `[0, 100]`, read as percentages of the scene box. No arithmetic, no
relative offsets, no anchors — which is what makes edge geometry derivable by
one formula with no evaluation order.

### 7.9 `scene`

```widget
scene cluster
  description sceneDescriptionLabel

  orbit outerRing token rule
  orbit innerRing token rule

  node nodeB
    role leader
    at centre
    title leaderNameLabel
    caption leaderCaptionLabel
  end

  edge linkWest
    from nodeB
    to nodeA
    carries heartbeat
    carries ack
  end
end
```

`description` is required. Node `caption` is optional; everything else in a
`node` block is required. An `edge` carries one or more channels, `from` and
`to` must differ, and there is at most one edge per unordered node pair.

An edge's length and angle are computed from the two placements. There is no
spelling for them.

### 7.10 `motion`

```widget
motion
  requires live
  forbids paused
  restartOn sequence

  pulse heartbeatWest
    edge linkWest
    channel heartbeat
    duration 820 milliseconds
    delay 0 milliseconds
  end

  emphasis leaderRing
    node nodeB
    duration 720 milliseconds
    delay 0 milliseconds
  end
end
```

The gate is the conjunction of every `requires` with the negation of every
`forbids`, and **nothing animates outside it**: there is no per-pulse override
and no animation declared anywhere else. The host's own connection status is
compiled into the gate whether or not the author mentions it, because an
animation running against a dead connection is a lie about liveness.

`restartOn` names the [Tick](ontology.md#tick) and is required when the block
holds any pulse or emphasis. Every animation is finite — `duration` is required
and there is no `repeat`, so the picture moves exactly as often as the data
does.

`delay` is the language's one optional clause with a materialised default: an
omitted delay is zero, and the IR carries the zero. Both examples above write it
anyway, which is the style to copy — but the IR's totality rule is what decides
the semantics, and "absent means zero" is a default the interpreter writes down
rather than a value a generator has to invent.

A viewer's reduced-motion preference is respected unconditionally. There is no
spelling that overrides it.

### 7.11 `indicators`

```widget
indicators
  indicator connection
    label connectionStatusLabel
    positiveWhen live
  end
end
```

Binary tone: the predicate holding selects `positive`, not holding selects
`warning`. There is no third state and no authored token. The label is the
accessible carrier and may not be empty — colour alone is not a signal every
viewer receives.

### 7.12 `controls`

```widget
controls
  control motionToggle
    caption motionCaptionLabel
    trigger click
    emits toggleMotion
    pressedWhen paused
  end
end
```

`trigger` is one of `click`, `change`, `input`, `submit`. `emits` must name an
event declared in the `events` block — a widget declares every event it emits.
`pressedWhen` is optional and renders `aria-pressed`.

Neither the trigger nor the wire name may contain `:` or `;`, and a wire name
may not be empty. Those are structural in the runtime's binding grammar: a
stray `:` shifts every component behind it and turns a declared debounce into a
throttle, and an empty name silences every binding behind it on the same DOM
event. The runtime panics rather than degrading
(`live/templ.go:104-118`); the validator's job is that it never has to.

### 7.13 `events`

```widget
events
  event toggleMotion
    wire "widget.motion.toggle"
    toggles paused
  end

  event snapshot
    wire "widget.cluster.snapshot"
    field sequence writes sequence
    field leader_known writes leaderKnown
    field alive_voters writes aliveVoters
  end
end
```

`wire` is the name the host registers, and the declared set is exhaustive: an
event not declared here is refused by the runtime, never ignored
(`live/config.go:85-91`). `toggles <flagField>` is the flip transition.
`field <wire_name> writes <stateField>` binds a payload field to state; the
declared types must match.

A whole event applies or none of it does. Runtime-minted events — effect
failure, slow client, client recovered — are never declared here; a widget
reaches the slow-client signal through a `signal` field instead.

### 7.14 `data`

```widget
data
  stream clusterWatch
    source "widget.cluster.watch"
    delivers snapshot
    ordering sequence
  end
end
```

A declaration, never a connection: the widget names what it wants and the host
owns the transport. Started at mount, stopped at unmount. `ordering` is
optional and must name a `counter`. A stream carries no address and no
credential — only a source name.

---

## 8. Tokens: a closed semantic namespace

A widget references **seven** token names and no others:

| Token | Role | Homepage palette | CandaceOS palette |
|---|---|---|---|
| `surface` | the card's own background | `--sheet` | `--card` |
| `ink` | primary text | `--ink` | `--ink` |
| `muted` | secondary text | `--muted-strong` | `--muted` |
| `rule` | hairlines, orbits, separators | `--rule` | `--line` |
| `accent` | the distinguished role and the primary channel | `--archive` | `--forest` |
| `positive` | healthy state and the acknowledging channel | `--lichen` | `--green` |
| `warning` | degraded state | `--signal` | `--amber` |

This is where *centralize the primitive at the layer that owns its semantics*
lands. Colour policy belongs to the design system, not to a widget: a widget
that could mint a colour would be a widget that could leave the design system.
Naming seven **semantic roles** rather than a palette's own field spellings is
what makes the reuse claim real — the same widget document renders under any
palette that maps the seven, and the mapping above is not hypothetical, it is
the shipped homepage vocabulary lining up one-for-one with a palette written
for a different product.

A widget writes a token **name** only. There is no inline colour, no hex
literal and no `var(--…)` escape hatch. An unresolved name is an error, never a
fallback: a fallback renders a plausible-looking widget in the wrong colours,
which is the failure mode hardest to notice in review.

---

## 9. Resolution

One pass, in block order. A reference resolves against declarations already
seen; a forward reference is an error at the line that made it. Then one
whole-document pass for the relations that cannot be checked locally — an
unreferenced role, a channel carried by no edge, a state field written by
nothing, a cycle in the predicate graph.

Two references are exempt from the forward rule, and building the interpreter is
what found them. A **control names an event**, and the canonical order puts
controls at 12 and events at 13: that forward reference is required rather than
tolerated, which is why an undeclared one has its own class (`W411`) instead of
being a reference error. And a **predicate names a predicate** declared later in
its own block: under a strict rule the cycle class `W408` could never fire
alone, because every cycle contains at least one forward reference, and the
cycle is the finding an author needs. Everything else resolves backwards,
which is what lets an unknown name be reported at the exact line that used it.

Both passes run to completion. The validator reports **every** finding, never
the first, which is the palette validator's rule (`palette.go:119-127`) applied
to a language: an author who fixes one error and re-runs to find the next
learns to distrust the tool.

Nothing generates before it validates.

---

## 10. The IR, at document level

The interpreter's output is one resolved, ordered, total record. Typed shapes
only; the implementation slice owns the encoding.

### 10.1 Five properties of the whole IR

1. **Resolved.** Every reference is a handle, not a name. A generator reading
   the IR cannot encounter an unknown identifier, so it needs no error path for
   one and no fallback.
2. **Total.** No field's absence means "work it out". Defaults are materialised
   by the interpreter, so the IR that reaches a generator says the same thing
   whether or not the author wrote the default.
3. **Ordered.** Every collection is a sequence in declaration order. This is
   forced: a render must be byte-identical for equal state
   (`live/core.go:39-43`), so an IR containing a set would make byte-identical
   output depend on iteration order.
4. **Anchored.** Every record carries a `SourceSpan`. This is what makes an
   error message point at a line, and later what makes generated output
   traceable back to the statement that produced it.
5. **Closed.** The IR names no file path, no host, no address and no
   credential. What is not in the document cannot be in the IR.

### 10.2 The records

`SourceSpan { file, startLine, startColumn, endLine, endColumn }` — carried by
every record below; omitted from each row for brevity.

| Record | Fields | Cardinality within its parent |
|---|---|---|
| `WidgetDocument` | `name`, `dialectVersion`, `region`, `palette`, plus one collection per record below | root |
| `StateField` | `name`, `type` ∈ {flag, counter, count, text}, `writers` (resolved handles), `signal?` | 1..n |
| `Predicate` | `name`, `kind` ∈ {atomic, composed}, `field?` (atomic), `requires[]`, `forbids[]`, `bounds[]` | 0..n |
| `NumericBound` | `field`, `comparison` ∈ {atLeast, atMost}, `value` | 0..n per predicate |
| `Binding` | `name`, `clauses[]`, `otherwise` | 0..n |
| `BindingClause` | `polarity` ∈ {when, whenNot}, `predicate`, `template` | 1..n per binding |
| `TextTemplate` | `segments[]`, each a literal run or a resolved `StateField` handle | exactly 1 per clause |
| `Label` | `name`, `source` ∈ {literal `TextTemplate`, `Binding` handle} | 1..n |
| `Slot` | `kind` ∈ {title, source, description, stat}, `label`, `ordinal` | title 1, source 0..1, description 1, stat 0..n |
| `Role` | `name`, `token`, `marker` ∈ {large, small}, `emphasisAllowed` | 1..n |
| `Channel` | `name`, `direction` ∈ {forward, reverse}, `token`, `legendLabel` | 0..n |
| `Placement` | `name`, `left`, `top` | 1..n |
| `Scene` | `name`, `descriptionSlot`, `orbits[]`, `nodes[]`, `edges[]` | exactly 1 |
| `Orbit` | `name`, `token` | 0..n |
| `Node` | `name`, `role`, `placement`, `titleLabel`, `captionLabel?` | 1..n |
| `Edge` | `name`, `from`, `to`, `channels[]`, `geometry` | 0..n |
| `EdgeGeometry` | `lengthPercent`, `angleDegrees` — **computed**, never parsed | exactly 1 per edge |
| `Motion` | `requires[]`, `forbids[]`, `restartOn`, `pulses[]`, `emphases[]`, `hostStatusGate` (always present) | 0..1 |
| `Pulse` | `name`, `edge`, `channel`, `durationMilliseconds`, `delayMilliseconds` | 0..n |
| `Emphasis` | `name`, `node`, `durationMilliseconds`, `delayMilliseconds` | 0..n |
| `Indicator` | `name`, `label`, `predicate` | 0..n |
| `Control` | `name`, `captionLabel`, `trigger`, `event`, `pressedWhen?` | 0..n |
| `EventDeclaration` | `name`, `wire`, `toggles?`, `fields[]` | 0..n |
| `EventField` | `wireName`, `writes` (StateField handle), `type` | 0..n per event |
| `Stream` | `name`, `source`, `delivers`, `ordering?` | 0..n |
| `Legend` | `entries[]`, each `{channel, label}` — **computed** from `Channel[]` | exactly 1 |
| `DirtyProjection` | `fields[]` — **computed**: the state fields the widget's rendered output depends on — every binding guard, every binding template, every literal label's template (since a literal label may carry an interpolation too), and the [Tick](ontology.md#tick) `motion` re-arms on, because a generated view carries the tick as a per-tick identity so a finished animation starts again | exactly 1 |

The three computed records — `EdgeGeometry`, `Legend`, `DirtyProjection` — are
the ontology's derived concepts made explicit at the IR level. They are in the
IR precisely so a generator does not have to re-derive them, and they are
absent from the grammar precisely so an author cannot contradict them.

### 10.3 Who owns the IR's definition

**The design said a Liquid Proto contract. The implementation slice landed
hand-written Go, and the reason is worth recording rather than quietly
reversing.**

The original ruling: define the IR as a Liquid Proto contract under the
repository's proto tree and generate its bindings, because the contract already
has refinement types for the shapes this IR needs — the region-identity
pattern, the `[0,100]` placement bounds, the positive duration, the non-empty
label — and expressing them as refinements means the validator and the type
cannot drift, whereas a hand-written mirror type needs a hand-written validator
beside it and those two do drift.

What building the validator showed:

- **The refinements would be a second, weaker validator, not the same one.** A
  refinement rejects a value; the catalogue owes an author a class, a line, a
  column and a repair. `left 120` is `W104` anchored at the integer, naming the
  range — a `this >= 0 && this <= 100` predicate cannot produce that, so the
  message has to exist anyway, and then the refinement is a duplicate check
  that can only ever fire on a value the message already refused.
- **The drift the contract was meant to prevent runs the other way.** The IR is
  never serialized: it is built by `internal/validate` and read by a generator
  in the same module, in the same process. There is no wire, no second
  implementation, and no consumer that could hold a stale copy — which is the
  situation a contract exists for.
- **Half the IR is pointers into itself.** Every reference is a handle, so
  `Node → Role`, `Edge → Node`, `Pulse → Channel` and the writer graph are
  cyclic object references. Expressing them in protobuf means ids and a
  resolution step, which is exactly the "unknown identifier" a generator was
  promised it could not meet.
- **Every record carries a `SourceSpan`**, which is a compiler concern rather
  than a contract one.

The path back, if a second consumer ever arrives: the moment an IR crosses a
process — a generator in another language, a cached artifact, a service — the
contract becomes the right shape and this section becomes a migration note
rather than a ruling. Until then the IR is `internal/ir`, the interpreter and
the validator's findings are handwritten beside it, and the generator's
templ/SVG emission will be too.

### 10.4 The evolution: a refinement contract *beside* the IR, not *as* it

The ruling above is right and stands: the IR stays hand-written Go. But it was
read too far. It rejected a Liquid Proto contract *as the IR* — one message per
record, resolution replacing handles, a `SourceSpan` the contract has no place
for — and from that it followed, wrongly, that refinements had no place at all.
The [`ontology`](ontology.md) is where that showed: it asserted a hundred
cardinalities and Layer-3 invariants and *proved* almost none of them, and an
operator ruling on 2026-09-03 named the gap — a cardinality without a gate is
aspirational.

The resolution is layered rather than either/or, and it does not touch the four
arguments above:

- A refinement **rejects** a value; the catalogue owes a **class, a line, a
  column and a repair**. Both are still true — so the hand-written validator
  stays the author-facing diagnostic, and the refinement does **not** run in the
  interpreter's path. `left 120` is still `W104`, produced by the validator,
  not by a predicate.
- What the refinement adds is a **proof**, not a second diagnostic. The local
  field invariants — the region pattern, the `[0,100]` placement bounds, the
  positive duration, the wire-name shape, the closed token/marker/direction/
  trigger/field-type enums — are expressed as Liquid Proto refinements on the
  smallest proto surface that carries them,
  [`refinement/v1/refinement.proto`](../refinement/v1/refinement.proto). This is
  **not** the IR: it is a separate contract mirroring the IR's local fields, so
  none of the handle-graph, `SourceSpan`, or no-wire arguments apply to it. The
  IR is still built by `internal/validate` and read by the generator in one
  process, exactly as ruled.
- The two are a layering only if they **agree**, so a spec proves it:
  [`internal/validate/refinement_agreement_test.go`](../internal/validate/refinement_agreement_test.go)
  asserts that, for every invariant carrying both, a value the refinement
  rejects is a value the validator rejects with its class, and a value one
  accepts the other accepts.

So a full proto IR remains the wrong shape, and this section's decision is
unchanged. What changed is the false corollary that the local invariants
therefore go unproven. They are now proven by a generated contract, the
whole-graph ones by their validator class and a spec, and the handful that only
a runtime or a generator can hold are marked aspirational in the ontology rather
than left reading as enforced.

---

## 11. What v0 deliberately does not have

Each of these has a recorded reason and an additive path to v1. None is an
oversight.

| Absent | Why | Path |
|---|---|---|
| Disjunction in predicates (`anyOf`) | The shipped composite is a pure conjunction | Additive block form |
| Node shapes and per-node styling | Appearance is a [Role](ontology.md#role); per-node styling defeats the point of roles | Not planned |
| Self-edges | No derivable geometry in a point-placement scene, no caller | Needs a curve model first |
| Looping animation (`repeat`) | Finite pulses re-armed by the tick are what make the picture honest | Not planned |
| Importing roles or channels from another document | Every reuse mechanism is a versioning problem; v0 has one document per widget | The measured need from the second and third real widget decides the shape |
| Per-binding debounce/throttle (`OnWith`) | No shipped caller. One caller is not two | Additive clause on `control` |
| Several controls on one element (`OnAll`) | No shipped caller | Additive |
| `aria-live` politeness | No invariant distinguishes polite from assertive yet | Additive line in `chrome` |
| Foreign DOM inside a widget (`Preserve`) | A widget hosting third-party DOM is not a widget in this language's sense | Stays available at the gotth-live layer |
| Multiple scenes per widget | A widget is one region with one accessibility boundary | Not planned |
| Layout direction, arithmetic placement, anchors | A keyword that does nothing is worse than an absent one | Needs a layout engine, which is a different program |

`dialect 0` is a hard version pin. A parser that implements version *n* refuses
a document declaring any other version rather than guessing which constructs it
can still handle.
