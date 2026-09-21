# Widget validator error catalogue, v0

Every error class the validator may produce, with the rule that fixes its
location, the message it must print, and the repair that message has to name.

Seventy classes. Sixty-seven were designed with this document; the other three
were found by running the interpreter at things. `W110` came out of building it,
which found the one shape of mistake the catalogue had no class for — a clause
whose arguments do not match the form it takes. `W419` and `W208` came out of
the P2 adversarial audit, which wrote two documents the interpreter called sound
— one that generated Go which does not compile, and one that named a palette
nobody ships. Each class is implemented in
[`internal/validate`](../internal/validate) and has at least one spec of its
own; the four worth reading the notes on are `W110`, `W208`, `W412` and `W419`.

## Why this file is written as carefully as the grammar

The program's bet is that an agent goes from under 20% to roughly 85% on an
unfamiliar language given three things: the rules in context, three to five
commented exemplars, and **a validator with precise errors**. The third is the
only one that operates during the work rather than before it. An error message
is the single piece of documentation that arrives at the exact moment the
author is wrong about something, addressed to the exact thing they were wrong
about — which makes the error text a training signal, not a courtesy.

That gives the catalogue three obligations:

1. **Name the subject.** "Invalid role" is not a diagnostic. `role
   "missingRole" is not declared` is.
2. **Name the repair.** Every message ends with a `fix:` line that is an
   imperative naming the exact spelling to write. A message that describes the
   problem and stops has made the author guess, and a guess is where an
   invented spelling comes from.
3. **Enumerate closed sets.** Where the language admits a fixed set of values,
   the message lists it. A closed set that the author has to go and look up is
   a closed set they will guess at.

The method is borrowed rather than invented: the palette validator already
reports every failing entry rather than the first, carries a human reason with
each forbidden sequence, and names both the token and what disqualified it
(`palette.go:119-127`, `151-164`, `187-190`).

## No warnings

Every class here is an **error** and blocks generation. There is no warning
tier.

A warning is a finding nobody has to act on. A language whose purpose is that
generated output can be trusted cannot have a category meaning "this may be
wrong and we generated it anyway": the second time a warning is ignored without
consequence, every warning becomes noise, and the first real one is lost in it.
Where a rule was too weak to justify blocking, the rule was left out of v0
instead — that is what the deferral list in
[`dialect.md § 11`](dialect.md#11-what-v0-deliberately-does-not-have) is.

## The whole-document pass

The validator runs to completion and reports **every** finding. It never stops
at the first. An author who fixes one error and re-runs to discover the next
learns that the tool tells them a fraction of the truth, and starts guessing
ahead of it.

Findings are sorted by `(line, column, id)`, so two runs over one document print
byte-identical output — the same determinism rule the renderer is held to
(`live/core.go:39-43`). One line may carry several findings; they are reported
per finding, never collapsed per line.

Exit status: `0` clean, `1` findings, `2` the document could not be read at all.
The third is a failure too — an unrun check must never read as a pass.

### When two classes match one construct

The **more specific** class is reported and the general one is suppressed, so
one mistake produces one finding. Four precedences are fixed — the first two
were settled with the catalogue, the last two by building the interpreter:

- A **W5xx canonical-form** class always beats `W004` and `W013`. An author who
  writes `nodeA --> nodeB` has made a recognised mistake with a known rewrite,
  and telling them "unknown statement keyword" instead of handing them the
  `edge` block would throw away the whole point of the group.
- A **reference** class (`W2xx`) beats an invariant that depends on the
  reference resolving. A pulse whose channel does not exist is `W201`, not
  `W404`: an invariant checked against an unresolved name reports a
  consequence of the first error as if it were a second one.
- A **canonical-form argument ends the clause it was written in.** `duration
  820ms` is `W506` alone, never `W506` plus a complaint that the word
  `milliseconds` is missing: the rewrite the first message names supplies the
  rest of the line, so reading further would report a consequence of a mistake
  already explained.
- **`W413` beats `W109`.** Two of the three runtime-minted names contain `:`,
  which is also what `W109` forbids. Telling an author to remove the colon from
  `timer:slow_client` would send them to a spelling that is still refused; the
  finding they need is that the name is the runtime's to mint.

---

## The message template

```
<document>:<line>:<column>: <id>: <subject>, <what is wrong>.
    fix: <imperative naming the exact spelling to write>
```

with, when a second location is relevant:

```
    <document>:<line>:<column>: <name> is declared here as <kind>
```

Worked — these three are the interpreter's own output for
[`examples/04-wrong-on-purpose.widget`](examples/04-wrong-on-purpose.widget),
wrapped for this page:

```
04-wrong-on-purpose.widget:162:10: W201: role "missingRole" is not declared.
    fix: add `role missingRole` to the `roles` block, or name one of the
         declared roles: `peer`.

04-wrong-on-purpose.widget:164:5: W306: placement "here" already holds node
    "nodeA", and one point holds one node.
    04-wrong-on-purpose.widget:153:3: "here" is used here by node "nodeA"
    fix: add a second placement to the `placements` block and name it here,
         for example `placement hereTwo left 78 top 50`.

04-wrong-on-purpose.widget:56:3: W401: binding "statusText" has no `otherwise`
    clause, so it produces no value when no guard matches.
    fix: add `otherwise "<text>"` as the last line of the binding.
```

The wrapping is this page's. A message is one line, a secondary anchor is one
line, and the `fix:` is one line: an interpreter never wraps, because wrapping
depends on a terminal width that a pull-request diff does not have.

Six rules the text obeys:

| | Rule |
|---|---|
| M1 | The subject is named by its identifier, in double quotes, in the first clause |
| M2 | What is wrong is stated in the present indicative. Never "should", never "must", never "invalid" without saying what would be valid |
| M3 | The `fix:` line is one imperative sentence naming the exact spelling — the keyword, the block it goes in, and a concrete value where one is needed |
| M4 | A closed set is enumerated in full when it has eight members or fewer; otherwise the three nearest by edit distance, followed by the count of the rest |
| M5 | The document is named by the path the author gave, unmodified. No absolute path is ever constructed, no working directory is ever printed |
| M6 | A message contains no host name, address, account or operator identifier, including when quoting the author's own text — a quoted literal is truncated to 40 characters and elided |

M5 and M6 are not stylistic. A validator's output is pasted into issues,
transcripts and pull requests, so it is a publication surface, and the same
rule the widget language itself obeys applies to the tool that checks it.

---

## Anchoring rules

Where a finding points is as load-bearing as what it says: an author navigates
to the anchor before reading the message.

| | Rule |
|---|---|
| **A1** | A finding anchors at the **first token of the offending construct** — never at its container's header, and never at its `end` |
| **A2** | A reference error anchors at the **use**, and carries a secondary anchor at the declaration whenever one exists |
| **A3** | A whole-document invariant — unreferenced, unwritten, cyclic — anchors at the **declaration of the thing it is about**. Never at end of file, never at a block header |
| **A4** | A duplicate anchors at the **second** occurrence, with a secondary anchor at the first |
| **A5** | A missing required part anchors at the **construct that should have contained it**, at its opening line |
| **A6** | A pairwise conflict anchors at the **second** participant |
| **A7** | Columns are 1-based and counted in Unicode code points, at the first character of the token |
| **A8** | A fault inside a template anchors at the **opening brace of the interpolation**, with the column counted inside the string literal |
| **A9** | A cycle anchors at the declaration of its **lexically first** member — the member whose declaration comes first in the document, not the one first in the alphabet — and lists the cycle from that member onwards, so the same cycle always reports in the same place |

A3 is the one most often got wrong. "Nothing writes `orphan`" is discovered
after the whole document is read, but it is *about* the `field orphan` line, and
that is where the author has to go.

---

## W0 — Document and block structure

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W001** | The first statement is not `widget` | The first non-comment line (A1) | The keyword found instead | Make `widget <Name>` the first statement; only whole-line `%%` comments may precede it |
| **W002** | A preamble directive is missing | The `widget` line (A5) | Which of the four is absent | Add the directive, with its expected value shape, in preamble order |
| **W003** | `dialect` names a version this parser does not implement | The `dialect` line (A1) | The version found and the version implemented | Write `dialect <implemented>`, or use a parser that implements the declared one — never "try anyway" |
| **W004** | A block name is not one of the fourteen | The block's opening line (A1) | The unknown name, plus the nearest three (M4) | Use one of the fourteen block names |
| **W005** | Blocks are out of canonical order | The block that arrived early or late (A1) | This block's ordinal and the ordinal of the block before it | Move the block to its position, naming both ordinals |
| **W006** | A block name appears twice | The second occurrence (A4) | The block name | Merge the two into one block at the first position |
| **W007** | A block contains no declarations | The block's opening line (A1) | The block name | Delete the block — an omitted optional block is the only spelling for "none" |
| **W008** | A block is never closed | The block's opening line (A5) | The block name and the line it opened at | Add `end` |
| **W009** | An `end` closes nothing | The `end` (A1) | — | Delete the `end`, or add the opening line it was meant to close |
| **W010** | A statement appears at document scope after the preamble | The statement (A1) | The keyword | Move the statement into the block that owns it, naming that block |
| **W011** | A required block is absent | The last preamble line (A5) | Which required block, of the six | Add the block, with its minimum content |
| **W012** | A block is nested deeper than container-then-declaration | The inner block's opening line (A1) | Both block names | Move the declaration to the top level of its container |
| **W013** | A statement keyword is unknown inside a known block | The statement (A1) | The keyword, the block, and the keywords that block accepts (M4) | Use one of the accepted keywords |

## W1 — Lexical and literal

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W101** | An identifier breaks its case convention | The identifier (A1) | The identifier and the convention its position requires | Rewrite it in the required case, showing the corrected spelling |
| **W102** | A string literal is not closed before end of line | The opening quote (A1) | — | Close the string on the same line; a literal never spans lines |
| **W103** | A template interpolation is unbalanced or empty | The opening brace (A8) | The malformed fragment | Close the interpolation, or write `{{` for a literal brace |
| **W104** | An integer is outside its range | The integer (A1) | The value, the field, and the permitted range | Write a value inside the range |
| **W105** | A value is outside a closed enumeration | The value (A1) | The value and the whole enumeration (M4) | Use one of the enumerated values |
| **W106** | A declaration is missing a required clause | The declaration's opening line (A5) | The construct, its identifier, and every missing clause | Add each missing clause, with its keyword and value shape |
| **W107** | A clause appears twice in one declaration | The second occurrence (A4) | The clause keyword | Delete one. Where repetition is legal — `requires`, `forbids`, `carries`, `stat`, `field` — this does not fire |
| **W108** | The region identity does not match `^[A-Za-z0-9_:.-]{1,64}$` | The `region` line (A1) | The offending character or the length | Rewrite the identity within the pattern, and say that a deployed identity may not be changed casually because a patch names it on the wire |
| **W109** | A wire name is empty or contains `:` or `;` | The `wire` line (A1) | The character found | Remove the character. The message states why: both are structural in the runtime's binding grammar, and the runtime panics rather than degrading (`live/templ.go:104-118`) |
| **W110** | A clause's arguments do not match the form it takes: one is missing, one is the wrong shape, or there are more than the clause carries | The offending argument (A1), or the clause's opening word when the argument is absent entirely | The clause, the form it is written in, and which argument is wrong | Write the clause in its one form, shown in full — `placement <id> left <left> top <top>` |

## W2 — Reference

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W201** | An identifier is not declared anywhere | The use (A2) | The name and the kind expected there, plus the declared names of that kind (M4) | Declare it, naming the block and the declaration form; or name one of the existing ones |
| **W202** | A reference points at a declaration later in the document | The use (A2), secondary at the declaration | Both positions and the block ordinals | Move the declaration to its canonical block, which is always earlier — never "reorder to taste" |
| ↑ | Two references are exempt, and both are exempt because another class owns them. A `predicate` naming a predicate declared later in its own block is W408's to report if it closes a cycle and legal if it does not — under a strict rule W408 could never fire alone, because every cycle contains a forward reference. And a `control` naming an `event` is the one forward reference the canonical block order *requires*, controls being block 12 and events block 13; an undeclared one is W411 | | | |
| **W203** | An identifier exists but is the wrong kind | The use (A2), secondary at the declaration | The kind found and the kind required | Name a declaration of the required kind, listing them (M4) |
| **W204** | A token name is not one of the seven | The `token` clause (A1) | The name and all seven (M4 lists them in full) | Use one of `surface`, `ink`, `muted`, `rule`, `accent`, `positive`, `warning` |
| **W205** | A `signal` name is not a known runtime signal | The `signal` clause (A1) | The name and the signal set | Use `slowClient`, or drop the clause and give the field an event writer |
| **W206** | A template interpolates a name that is not a state field | The opening brace (A8) | The name, plus the interpolable fields (M4) | Interpolate a declared `counter`, `count` or `text` field |
| **W207** | A template interpolates a `flag` | The opening brace (A8) | The field and its type | Replace the interpolation with a `when` clause on that flag — the message says why: rendering a boolean as words is the author's decision, not a formatter's |
| **W208** | The `palette` directive names a palette that does not exist | The palette name (A1) | The name and the palettes that do (M4) | Name one of the palettes that exist, listing them. States what a palette is — the seven token names mapped to values — and that the design system owns both the mapping and the set |
| ↑ | The interpreter does not *resolve* a palette and this class does not start it doing so: a document names seven token names and the design system owns their values (`dialect.md § 8`). The set of *names* is a different thing, closed and known, so a typo is refusable here — and until it was, `palette midnightNeon` validated, generated, compiled, and reached the host as a package-init panic | | | |

## W3 — Cardinality and duplication

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W301** | An identifier is declared twice | The second declaration (A4), secondary at the first | The name and both kinds | Rename one, showing a suggested spelling. States that identifiers are unique across the whole document, with the one deliberate `flag`/`predicate` sharing |
| **W302** | A singular statement of a block appears twice — two `title` lines, two `description` lines, two `restartOn` | The second (A4), secondary at the first | The construct | Delete one. A repeated *block* is W006 and a repeated clause of a *declaration* is W107; this class is the one in between, for the statements a block carries directly |
| **W303** | A container has fewer members than it requires | The container's opening line (A5) | The container, the count found, and the count required | Add the missing member, with its declaration form |
| **W304** | Two edges join the same unordered pair | The second edge (A6), secondary at the first | Both edge names and both endpoints | Merge them into one edge carrying both channels — the message says that direction belongs to the channel, not to a second edge |
| **W305** | Two pulses share an edge and a channel | The second pulse (A6), secondary at the first | Both pulse names, the edge, the channel | Delete one, or move one to another channel — a second pulse is a duplicate, not a second particle |
| **W306** | Two nodes name one placement | The second node's `at` line (A6), secondary at the first | The placement and both node names | Add a placement and name it, with a concrete `placement <id> left <n> top <n>` |
| **W307** | Two emphases name one node | The second (A6), secondary at the first | The node and both emphasis names | Delete one |

## W4 — Semantic invariants

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W401** | A binding has no `otherwise` | The `binding` line (A5) | The binding | Add `otherwise "<text>"` as the last clause. States the consequence: without it the binding produces no value when no guard matches |
| **W402** | A label has two sources, or none | The `label` line (A1) | The label and which sources are present | Keep exactly one of `text "…"` or `binds <binding>` |
| **W403** | An edge's `from` and `to` are the same node | The `to` line (A1) | The edge and the node | Point `to` at a different node. States that a self-edge has no derivable geometry in a point-placement scene |
| **W404** | A pulse names a channel its edge does not carry | The pulse's `channel` line (A1), secondary at the edge | The pulse, the channel, the edge, and the channels the edge does carry | Add `carries <channel>` to the edge, or name one the edge already carries |
| **W405** | A motion block holds a pulse or emphasis and has no `restartOn` | The `motion` line (A5) | The animations found | Add `restartOn <counterField>`, naming the declared counters (M4). States the consequence: nothing would re-arm the animation |
| **W406** | `restartOn` names a field that is not a `counter` | The `restartOn` line (A1), secondary at the field | The field and its type | Name a `counter` field, listing them |
| **W407** | An emphasis names a node whose role forbids emphasis | The emphasis's `node` line (A1), secondary at the role | The node, the role, and the role's `emphasis forbidden` clause | Change the role to `emphasis allowed`, or emphasise a node of a role that allows it. Never "add an override" — there is none |
| **W408** | The predicate reference graph has a cycle | The lexically first member's declaration (A9) | Every member of the cycle, in declaration order | Break the cycle by removing one clause, naming which |
| **W409** | A state field has no writer | The `field` line (A3) | The field | Add an event `field <wire_name> writes <field>`, a `toggles <field>` clause, or a `signal` binding — all three, since which is right depends on where the value comes from |
| **W410** | A declaration is referenced by nothing | The declaration (A3) | The declaration and its kind | Reference it, naming where a reference of that kind goes; or delete it. States that unreferenced vocabulary is the shape drift arrives in |
| **W411** | A control emits an event the `events` block does not declare | The `emits` line (A1) | The event name and the declared events (M4) | Declare the event with a `wire` name, or emit a declared one. States the consequence: the runtime is default-deny and would refuse it as `UNKNOWN_EVENT` at run time (`live/config.go:85-91`) |
| **W412** | An event field's type does not match the state field it writes | The `field` line (A1), secondary at the state field | Both types | Change one, naming which produces the intended reading |
| ↑ | **Unreachable in dialect 0**, and kept for the version that is not. `field <wire_name> writes <stateField>` carries no type of its own — the IR takes the state field's — so the two cannot disagree. The class keeps its number rather than being renumbered later, and the interpreter's suite records the gap rather than leaving it untested | | | |
| **W413** | A runtime-minted event name appears in `events` | The `wire` line (A1) | The name and the minted set | Delete the declaration; reach the slow-client signal through a `signal` field instead |
| **W414** | `restartOn` is declared in a motion block with no animation | The `restartOn` line (A1) | — | Delete the `restartOn`, or add the pulse or emphasis it was meant to re-arm |
| **W415** | A binding clause can never be reached | The clause (A1), secondary at the earlier clause that subsumes it | Both clauses and the implication between their guards | Reorder the two clauses, or weaken the later guard. States that it is dead text. The check is the decidable subset: a duplicate guard, or a guard whose `requires`/`forbids` sets are supersets of an earlier guard's. No general satisfiability is attempted, and the message does not claim otherwise |
| **W416** | A numeric bound names a field that is not `counter` or `count` | The bound (A1), secondary at the field | The field and its type | Bound a numeric field, or use the flag directly as a predicate |
| **W417** | `toggles` names a field that is not a `flag` | The `toggles` line (A1), secondary at the field | The field and its type | Name a `flag`, or use `field <wire_name> writes <field>` to assign a value instead |
| **W418** | A stream's `ordering` names a field that is not a `counter` | The `ordering` line (A1), secondary at the field | The field and its type | Name a `counter`, or drop the clause if the stream delivers no ordering |
| **W419** | A `signal` binds a field that is not a `flag` | The `signal` clause (A1) | The field and its type | Write the field as a `flag` carrying the same signal, showing the whole line; or drop the `signal` clause and write the value from an event, naming that line too |
| ↑ | The one field-type class with **no secondary anchor**, and the absence is the rule rather than an omission. W406, W416, W417 and W418 each name a field declared elsewhere, so the type the author has to go and look at is on another line. A `signal` is a clause of the field's own declaration, so the type it disagrees with is already on the line the finding points at, and a second anchor onto the same line would be noise | | | |

## W5 — Canonical form

These are the four Anka rules, enforced. Each message says what the dialect's
one form is, and each `fix:` shows the rewrite in full rather than describing
it — an author who reached for the mermaid spelling needs the replacement text,
not a rule number.

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W501** | A mermaid edge operator appears — `-->`, `---`, `-.->`, `==>`, `--x`, `--o`, `<-->`, `~~~`, or a labelled form | The operator (A1) | The operator found | The full `edge <id>` block, with `from` and `to` filled in from the two identifiers on the line and a suggested identifier |
| **W502** | A node identifier is followed by a mermaid shape bracket | The bracket (A1) | The bracket found | Delete the bracket; a node's appearance comes from its `role`, and the message names the roles declared (M4) |
| **W503** | A dropped mermaid keyword appears — `direction`, `classDef`, `class`, `style`, `subgraph`, `linkStyle` | The keyword (A1) | The keyword | The construct that replaced it: `subgraph` → `scene`, styling → `role` or `channel`, `direction` → nothing, because the scene is absolutely placed |
| **W504** | A `%%{ … }%%` init directive appears | The opening `%%{` (A1) | — | Move the setting to a preamble directive, naming the four |
| **W505** | A colour value appears where a token name belongs — a hex literal, a named CSS colour, `rgb(`, `var(` | The value (A1) | The value found | Use a token name, listing all seven (M4). States that there is no inline colour and no `var(--…)` escape hatch |
| **W506** | A time value carries a unit sigil — `820ms`, `0.82s` | The value (A1) | The value found | The converted value with its unit word: `820ms` → `820 milliseconds`, `0.82s` → `820 milliseconds` |
| **W507** | A string literal appears where a label reference belongs | The literal (A1) | The literal, truncated per M6 | Add a `label <id> text "…"` line to the `labels` block and name it here, with a suggested identifier derived from the construct |
| **W508** | A `%%` follows content on the same line | The `%%` (A1) | — | Move the comment to its own line above the statement |

## W6 — Accessibility and policy

| Id | Fires when | Anchors | Message names | The fix it names |
|---|---|---|---|---|
| **W601** | The `chrome` block has no `title` line | The `chrome` line (A5) | — | Add `title <label>`, naming the declared labels (M4). States the consequence: a widget with no accessible name, which no browser reports |
| **W602** | A scene has no `description` line | The `scene` line (A5) | The scene | Add `description <label>`. States that the scene is the widget's single accessibility boundary, so an absent description degrades to no accessibility rather than to less |
| **W603** | A scene's description is a literal label while the widget declares any predicate | The `description` line (A1), secondary at the literal label | The label and the count of predicates | Bind the description to a binding, showing the `binding <id>` skeleton. States that a fixed sentence describing a picture that changes is wrong the moment it changes |
| **W604** | An indicator's label resolves to empty text | The indicator's `label` line (A1), secondary at the label | The indicator and the label | Give the label text. States that colour alone is not a signal every viewer receives |
| **W605** | A literal contains a host name, a network address, a filesystem path, or an account identifier | The literal (A1) | The class of identifier found — never the identifier itself, per M6 | Replace it with a role name or a generic node name (`node-a`), and for a genuine example use a documentation-range address. States that a widget document is portable by construction, which is also what makes it publishable |

W605 deliberately reports the *class* and not the value. A validator that
echoed the identifier would copy it into every log, transcript and pull request
the validator's output reaches — which is the leak the rule exists to prevent,
committed by the tool that found it.

---

## What a message must never do

| Never | Because |
|---|---|
| Say "invalid", "bad" or "malformed" without saying what would be valid | The author already knows something is wrong; the message's whole value is the other half |
| Print a stack trace, a rule number alone, or a grammar production | None of them names a repair |
| Suggest disabling the check, lowering a threshold, or adding an exemption | Every class here blocks generation on purpose; a message that offers a way around itself teaches that the rule is optional |
| Guess and continue | A parser that recovers by assuming what was meant generates a widget the author did not write. `dialect` mismatch is the canonical case: refuse, never "try anyway" |
| Blame | "You forgot" and "you should have" cost a word each and buy nothing. The subject of a message is the construct, not the person |
| Print an absolute path, a working directory, a host name or an account | The validator's output is a publication surface (M5, M6) |
