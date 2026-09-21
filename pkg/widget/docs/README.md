# The widget language

A small language for declaring **widgets**: self-contained vertical slices of
UI that mount into one Go host binary, each with its own state, its own live
region and its own motion. A widget is written once in this language and its
UI code is generated.

The language is a **Mermaid dialect**, chosen for a measured reason rather than
an aesthetic one: an invented DSL starts an agent below 20% accuracy on syntax
unfamiliarity alone, and mermaid's training-data presence means a dialect pays
tokens only for its custom vocabulary.

**Design came first, deliberately**, because an agent that can see the
generator shapes the language to suit the generator and the dependency has to
run the other way. The interpreter now exists — [`..`](..) — and it was built
against these documents rather than the documents against it. Where building it
found a defect in the design, the design was corrected in place: `dialect.md
§ 9` gained the two exemptions to one-pass resolution, `errors.md` gained a
class and two precedences, and `dialect.md § 10.3` records the one design
ruling the implementation reversed, with the reason.

## Reading order

| | Document | What it settles |
|---|---|---|
| 1 | [`inventory.md`](inventory.md) | 135 concepts harvested from shipped code, with file:line provenance. Deliberately untyped |
| 2 | [`ontology.md`](ontology.md) | Those concepts typed: 25 entries, each with a declared type, typed relations with cardinality, invariants a validator can fail on, and permitted verbs. Plus the disposition of all 31 cuts |
| 3 | [`dialect.md`](dialect.md) | The surface syntax: base-form choice, the standard-mermaid ruling, the four Anka rules and where each lands, all fourteen blocks, and the document-level IR |
| 4 | [`errors.md`](errors.md) | 70 error classes, each with its anchoring rule, message template and named fix |
| 5 | [`examples/`](examples/) | Four heavily commented documents, the last of which does not validate on purpose |

An agent authoring a widget for the first time should read `dialect.md` and
[`examples/01-cluster-heartbeats.widget`](examples/01-cluster-heartbeats.widget),
in that order, and reach for `errors.md` only when the validator names a class.

## The shape of a document, in twenty lines

```widget
widget NodeStatus
dialect 0
region "widget.node-status"
palette fieldStation

state
  field reachable type flag
end

bindings
  binding statusText
    when reachable then "reachable"
    otherwise "unreachable"
  end
end

labels
  label titleLabel text "Node status"
  label nodeALabel text "node-a"
  label statusLabel binds statusText
end
```

…and so on through fourteen blocks in one fixed order. The full minimal
document is [`examples/02-node-status.widget`](examples/02-node-status.widget).

## The five decisions worth knowing before reading anything else

1. **Flowchart flavour, not state-diagram.** A widget's scene is a topology, and
   a pulse is traffic on an edge rather than a transition between
   configurations. The lifecycle *is* a state machine, and it is fixed by the
   SDK, so no widget ever declares one.
2. **The fence is `widget`, not `mermaid`.** A markdown renderer shows the
   source and never invokes mermaid. Forced to parse one, mermaid rejects the
   whole block — which is correct, because a renderer that drew the topology
   while silently dropping the roles, the motion gate and the bindings would
   let a reviewer believe they had reviewed a widget they had reviewed a third
   of.
3. **Seven semantic tokens, closed.** `surface`, `ink`, `muted`, `rule`,
   `accent`, `positive`, `warning`. A widget writes a token name, never a
   value, so the same document renders under any palette that maps the seven.
4. **Bindings are total.** Every binding has an `otherwise`, so a generated
   render needs no fallback path.
5. **Nothing animates outside the motion gate**, the gate always includes the
   host's connection status whether or not the author writes it, and every
   animation is finite and re-armed by a tick — so the picture moves exactly as
   often as the data does.

## Handoffs, and what the interpreter did with them

- The **IR is hand-written Go**, not a Liquid Proto contract — the one design
  ruling the implementation reversed. [`dialect.md § 10.3`](dialect.md#103-who-owns-the-irs-definition)
  carries the argument on both sides and the path back.
- The generator must compile the host's connection status into every motion
  gate. It is a host fact a widget cannot declare, and it is not optional.
  `Motion.HostStatusGate` carries the obligation into the IR so a generator
  reads it rather than remembering it. **Still open: no generator exists yet.**
- Three IR records are **computed, never parsed**: edge geometry, the legend,
  and the dirty projection. All three are in the IR, so a generator need not
  re-derive them, and absent from the grammar, so an author cannot contradict
  them.
- The palette named by a document must resolve the seven semantic tokens. The
  mapping table in [`dialect.md § 8`](dialect.md#8-tokens-a-closed-semantic-namespace)
  is the starting point for two existing palettes. **Still open: the
  interpreter does not resolve a palette — it checks that a document names one
  of the seven token names and leaves the values to the design system.** What
  it does check is the palette's *name*, against the closed set the SDK ships
  (`W208`): a name is refusable where a colour value is not.
- [`examples/04-wrong-on-purpose.widget`](examples/04-wrong-on-purpose.widget)
  is the validator's acceptance test, and it is wired up as one: twenty-nine
  faults across twenty-four classes, each annotated with the class it must
  produce, each asserted by anchor and class in the golden suite.
