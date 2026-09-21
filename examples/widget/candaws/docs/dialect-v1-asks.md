# Dialect v1 asks — the ranked agenda

The dialect's deferred items were scattered: eleven rows in
[`dialect.md` § 11](../../../../pkg/widget/docs/dialect.md), five more in the
ontology's own deferred table, and the rest in lab entries, generator doc
comments and one machine-readable backlog file. Nothing joined them, and
nothing ranked them.

This document joins them and ranks them, and it earns the right to rank by
bringing evidence: the [CandaWS fleet](fleet.md) is six documents written
against dialect v0 by an author who wanted things v0 does not have, and every
one of those wants is anchored below to the `%%` GAP comment in the document
that hit it.

**Ranking rule**, stated so a later reader can disagree with the rule rather
than with the list:

1. **Does it block something already shipped?** An ask that keeps working code
   hand-written outranks an ask that makes a future picture nicer.
2. **How many independent witnesses?** This repository's standing bar is *one
   caller is not two*. Two witnesses is an ask; one is a note.
3. **Is it additive?** A change a v0 document survives unchanged is cheaper
   than one that is not, and cheapness breaks ties rather than making the case.

Nothing here is a decision. This is the agenda a decision round would work
through, in the order it should work through it.

---

## Ranked

### 1. Control element kinds — `change`, `input`, `submit`

**Status: blocks shipped code.** The dialect admits all four triggers and the
generator refuses three of them, which is the sharpest form a gap can take.
`uigen.Refusals()` names the list; the reason is that a control declares a
caption, a trigger and an event, and *nothing that says what kind of element it
is*. A `change` bound to a button emits a binding that can never fire.

Recorded as a design finding rather than a backlog item: "these three are not
'not done yet'; they are the language having a hole the generator cannot fill
on its own, and the fix is a dialect change rather than a generator change"
(P2's generation lab entry). One live region on the apex homepage cannot be a
widget at all because of it, and keeps a hand-written render and a hand-written
dirty predicate; the derivable-backlog entry for that region is stuck at
`derivation-built` naming this as the blocker.

**Fleet witnesses (3).** Coldstart wants a number — how many instances to keep
warm — and ships a button that asks for one. Roundabout wants a routing-policy
`select` and ships a `text` field the host writes and the card only reports.
Blobfish wants a storage-class `select` and does the same.

**Shape of the ask.** A closed `element` clause on `control` — the ontology
already notes that a hidden control reporting a browser measurement "needs its
own type, not a weakened one", so the element kind is a new concept rather than
a widening of an existing one. Additive: every v0 control is a button and
stays one.

---

### 2. A shared role and channel vocabulary across documents

**Status: the deferred measurement has now been taken.** § 11's row reads:
"Importing roles or channels from another document — every reuse mechanism is a
versioning problem; v0 has one document per widget", with the path column
"*the measured need from the second and third real widget decides the shape*".
The fleet is the sixth. The measurement:

| | Count |
|---|---|
| `role` declarations across the six documents | 17 |
| distinct role bodies among them | 7 |
| declarations covered by the three commonest bodies | 13 of 17 |
| documents declaring `token accent / marker large / emphasis allowed` | 5 of 6 |
| documents declaring `token positive / marker small / emphasis forbidden` | 5 of 6 |
| `channel` declarations | 16, of 13 distinct names |
| channel names declared in more than one document | `request`, `response`, `ack` |

Two role bodies appear in five of six documents under five different names —
`writeHub`, `router`, `rollup`, `hub`, `distributor` are the same three
clauses five times. That is not a fleet artefact: they are the same three
clauses because a distinguished centre node looks the same in every topology.

**Fleet witnesses (6, i.e. all of them).** Every document re-declares its own
roles, channels and placements, as `03-relay-pipeline.widget`'s header comment
already warned it would have to.

**Shape of the ask.** Deliberately not settled here. The § 11 row is right that
every reuse mechanism is a versioning problem, and the honest reading of this
table is that the duplication is in **roles first, channels second, placements
not at all** — the placements are genuinely per-topology and no two documents
share one. An import mechanism scoped to roles would capture 13 of 17 with the
smallest possible versioning surface.

---

### 3. Per-pulse motion gates

**Status: new, found by the fleet, and the fleet's own most-repeated
complaint.** A widget has exactly one motion gate and every pulse animates
inside it or not at all: "there is no per-pulse override and no animation
declared anywhere else". That is a good rule and the three cases below are all
the same violation of it — the *interesting* pulse is the one that happens when
the gate is shut.

**Fleet witnesses (3).**

- Queuecumber's `redrive` channel is carried by an edge and animates never. A
  message reaches the dead-letter store exactly in the states where `flowing`
  does not hold.
- Yakshave ships no rollback pulse at all. A rollback runs when the pipeline is
  *not* green, so a rollback pulse under the `shipping` gate could only fire in
  states where a rollback cannot happen.
- Dashbored's gate says `forbids silenced`, which stops the sample pulses when
  the operator silences the alerter — which is not what silencing means. The
  gate it wants is per-pulse: samples while `observing`, the breach while
  `observing` and not `quiet`.

**Shape of the ask.** An optional `when <predicate>` clause on `pulse` and
`emphasis`, conjoined with the widget gate rather than replacing it, so the
host status gate and the reduced-motion rule stay unconditional and no pulse
can escape them. Additive: an omitted clause is the widget gate, which is
exactly what v0 means today.

---

### 4. State-dependent role selection

**Status: new; adjacent to a recorded "not planned" and deliberately narrower
than it.** § 11 rules out "node shapes and per-node styling" because
"appearance is a Role; per-node styling defeats the point of roles". That
ruling is correct and this ask does not contest it. The ask is that a node's
**classification** may be a decision, while appearance stays entirely inside
roles. The only state-dependent appearance in v0 is an indicator's binary tone.

**Fleet witnesses (3).** Blobfish cannot draw the lagging zone as lagging;
Queuecumber cannot draw a worker that has stopped leasing; Roundabout cannot
draw an ejected backend. In all three the fact is carried by a shared caption
and a stat, and the picture — the thing the widget exists to be — does not
have it.

**Shape of the ask.** `role` on a `node` takes a binding-shaped ordered
decision over declared roles, `otherwise` required, exactly like a `binding`.
Totality is what keeps the render free of a fallback path, and reusing the
binding rule means no new evaluation semantics. The dirty projection widens to
include the guards, which the interpreter already computes for bindings.

---

### 5. A spelling for an implication between state fields

**Status: has already produced one green-but-wrong shipped spec.** A view is
authoritative only when it came from a leader, so `authoritative` implies
`leaderKnown` — a fact about the data source that no document states. W415
cannot see it and is not meant to: it decides subsumption from the document's
own predicate structure. In P2.5 the homepage was found to have a *passing*
spec for a binding clause whose fixture was `{Connected, Authoritative,
HasQuorum}` with no leader — a state the data source cannot emit. The spec
reached the guard by constructing an impossible state, so it was green while
the string it asserted never rendered in production.

The shape was already written down: "a dialect that lets a host make two state
fields dependent has clause orderings whose reachability is decided outside the
document".

**Fleet witnesses (2).** Yakshave's four stage flags are a total order —
`deployOk` implies `testOk` implies `buildOk` implies `checkoutOk` — and the
guard order in `pipelineStatusText` is correct only because a human ordered it.
Blobfish's `writeAcks` and `laggingZones` are dependent for the same reason.

**Shape of the ask.** A document-level `implications` block naming pairs the
host guarantees, feeding W415's reachability check and nothing else. It changes
no rendering, which is what makes it cheap; it is a claim the author makes and
the validator uses, and if the claim is false the document was already wrong.

---

### 6. A magnitude on a node

**Status: new.** A node carries a role, a placement, a title and an optional
caption, and nothing numeric. Every storage picture and every queue picture
ever drawn shows a level, and the fleet's two that want one put it in the
caption text and a stat line instead.

**Fleet witnesses (2).** Blobfish's zones want a fill; Queuecumber's broker
wants a depth.

**Shape of the ask.** A `level <countField>` clause on `node`, rendered by the
role rather than by the node — so it inherits the § 11 ruling that appearance
belongs to roles, and a role declares whether it renders a level at all. This
is the ask most likely to be refused on the grounds that a scene is a topology
and not a chart, and the refusal would be defensible; it is ranked here because
two independent services reached for it, not because it is obviously right.

---

### 7. Numeric bounds between two fields

**Status: an existing restriction with a stated reason.** "A numeric bound is a
comparison against an integer literal only. It never compares two fields, so no
evaluation order exists to get wrong." `atLeast` and `atMost` shipped together
"even though only one has a shipped caller"; this fleet is where the second one
finally got called, twice.

**Fleet witnesses (2).** Queuecumber's real invariant is `inFlight atMost
workersUp` — every leased message is inside some worker. Roundabout's is
`healthyBackends + ejectedBackends atMost 3`. Both live in the engine, and both
cards take the two numbers on trust.

**Shape of the ask.** `requires <field> atMost <field>`. The stated reason for
the restriction — evaluation order — does not apply to a comparison between two
already-resolved state fields, since neither is computed from the other. Sums
are a different and larger ask and are not part of this one.

---

### 8. Disjunction in predicates (`anyOf`)

**Status: § 11 row 1, "Additive block form".** The recorded reason still holds:
the shipped composite is a pure conjunction.

**Fleet witness (1).** Dashbored's alert is "the p99 breached OR the error rate
breached". The disjunction lives in the engine and arrives as one `breaching`
flag, which is the correct v0 answer — and is also why the card cannot say
which of the two fired without a `text` field carrying the name.

One witness. Under the ranking rule that makes it a note, and it stays on the
list only because § 11 already committed to the shape.

---

### 9. Texture and elevation tokens

**Status: the weakest-recorded of the pre-existing items — no document proposes
it and no § 11 row covers it.** The only evidence is one measurement, written
twice: of the 83 custom-property references in the homepage's stylesheet, ten
are in the card's block — "the seven token declarations, plus three paints the
closed seven do not name: the `--rule-faint` graph-paper texture twice and one
`--shadow`."

The countervailing evidence is strong and should be read first: the seven-token
mapping "was not designed, it was discovered", and two independent palettes
agreeing on seven roles is a much better argument for the closed set than any
argument made before that was checked. A second palette in this repository does
carry `Shadow`, `ShadowSmall`, `Radius` and `RadiusSmall` — so the names exist,
but as palette fields and not as a dialect proposal.

**Fleet witness (1).** Blobfish's cold storage tier wants a texture to say
"this copy is not the one you will be served from".

---

### 10. A radius class for orbits

**Status: an existing ruling, and this is a deliberately narrowed re-ask.** An
orbit declares a name and a token; the generator derives concentric ellipses
from the ordinal, each a step smaller and turned further, with a floor so that
many orbits never reach zero. The ruling is that there is no spelling for the
size "deliberately, because a scene with authored ellipse dimensions is a scene
whose author is drawing rather than declaring". That reason is right and this
ask does not contest it.

**Fleet witness (1).** Blobfish's two orbits are meant to be *tiers* — an inner
ring for the quorum, an outer one for the eventual third copy — and which is
which is decided by declaration order and cannot be stated.

**Shape of the ask.** Not dimensions: a closed set of named classes (`inner`,
`outer`) that the generator maps to its own derived geometry. The author says
which tier, the generator still says how big.

---

## Carried forward with no fleet witness

Recorded, unchanged, and not re-argued here. Six documents gave none of them a
second caller.

| Item | Where recorded | Position |
|---|---|---|
| Self-edges | § 11 | Needs a curve model first |
| Looping animation (`repeat`) | § 11 | Not planned |
| Per-binding debounce/throttle (`OnWith`) | § 11 + ontology deferred 5.5 | Additive clause on `control` |
| Several controls on one element (`OnAll`) | § 11 + ontology deferred 5.6 | Additive |
| `aria-live` politeness | § 11 + ontology deferred 1.27 | Additive line in `chrome` |
| Foreign DOM inside a widget (`Preserve`) | § 11 | Stays at the gotth-live layer |
| Multiple scenes per widget | § 11 | Not planned |
| Layout direction, arithmetic placement, anchors | § 11 | Needs a layout engine |
| Node shapes and per-node styling | § 11 | Not planned — and ask 4 above is deliberately not this |
| Write-once state fields | ontology deferred 2.23 | One shipped instance; no § 11 counterpart |
| A third indicator tone | ontology, Indicator invariants | Binary in v0; no invariant distinguishes a third |
| A second runtime signal | dialect § 7.1 | The signal set has exactly one member |
| A `prefers-reduced-motion` override | ontology, Motion invariants | None exists and none is planned |

## Asked and answered — do not re-ask

- **Per-construct identifier namespaces.** The flat namespace is a ruling, not
  an oversight: "a name that means two things in one file is a name an author
  will mix up", and one namespace is what lets every unknown-identifier
  diagnostic say what the name *was* declared as. Drafting the fleet collided
  on it four times and the right response was a naming convention, which
  [`fleet.md`](fleet.md) now states.

## Adjacent, and not dialect asks

All three are recorded open findings that the fleet makes more likely to bite.
They belong to the generator and the SDK, and they are listed here only so that
a v1 decision round does not mistake them for language work.

The build stage added the third and settled a question this list opened. **Every
`%%` GAP comment above is a language gap and not one generator refusal.** All
five built documents generated with zero findings and no change to `uigen`, so
each ranked ask below is a thing the dialect cannot say rather than a thing the
generator will not emit — which is the distinction the design stage flagged as a
risk and could not check without generating.

- **All-or-nothing event application.** The ontology says a whole event applies
  or none of it does; the generated reducer applies an event field-by-field,
  keeping the old value for any field that will not parse. Fixing it changes
  the reducer's shape for every widget. The fleet's events are wide — eight
  fields, seven, seven — so a partial application here would be a card showing
  a coherent-looking state that never existed.
- **W412 is reserved for the next dialect version and is unreachable in v0**,
  because `field <wire_name> writes <stateField>` carries no type of its own and
  the two cannot disagree. It becomes reachable the moment an `EventField`
  gains a type, which is a thing a v1 might do while doing something else.
- **A shut motion gate is an attribute, not absent markup.** The generator emits
  every declared pulse unconditionally and gates them with `data-motion` on the
  root, which the stylesheet reads. That is the right shape — the scene's
  declared motion is a property of the document rather than of state, so equal
  state renders byte-identical markup either way — but it means "nothing is
  animating" is a question about one attribute and never about a pulse count.
  Found while writing the fleet's card assertions, where a count of pulse
  elements is stable across every state a card can be in. Anything grading a
  card on "no motion" has to read `data-motion="false"`; a fixture that expected
  zero pulse elements would be unsatisfiable against generated output, and would
  be measuring the generator's markup strategy rather than the card.
