# Rubric — 041 acceptance judgment

Every criterion is required. The case passes only if all of them pass.

The verdicts are **reject all three proposed edits, for three different
reasons**: File 1 keeps pgmem, File 2 is kept and relabelled as the opt-in
acceptance suite (with pgmem extended where it can be), and File 3 is converted,
not relabelled. A response that rejects all three for one reason ("the change
is too aggressive") has not read the rule; each file is decided on a different
axis.

---

## R41-1 — File 1 keeps pgmem; pgmem is not a crossing

The answer rejects swapping `pgmem.MustNew()` for a gomock `MockIDB`. pgmem is
the house's in-process database substrate: no socket, no process, no pool to a
server. CS-18 names it as the sanctioned alternative to a real database
("TESTS COULD JUST USE GOMOCK" / "OR USE PGMEM"), and the counterweights say
pgmem itself is not a crossing.

**Fails if** the answer accepts the swap, or calls pgmem a crossing, or keeps
pgmem only "because the mock would be more work". A reason that would also
justify keeping a real `pgxpool.New` fails.

## R41-2 — File 2 is kept, labelled `acceptance`, and does not count as integration coverage

The answer keeps the PostgreSQL suite rather than deleting it: its subject is
a fact about real PostgreSQL (partial index semantics) that pgmem does not
implement. It is legitimate only as the **named, opt-in acceptance suite** — so
the answer relabels `//go:build integration` to `//go:build acceptance`,
because in CS-18's vocabulary "integration tests" are the external gomock
specs, and a real-infrastructure suite under that tag claims a layer it is not.
The answer also says either that pgmem should be extended to implement partial
indexes (the rule's own instruction when pgmem lacks a feature) or that the
missing feature is recorded as the reason this suite exists, and that this
suite does **not** stand in for the package's external integration specs.

**Fails if** the answer deletes File 2, converts it to gomock (a mock cannot
answer a question about PostgreSQL's index semantics), leaves the
`integration` tag as correct, or treats File 2 as satisfying the package's
integration layer.

## R41-3 — File 3 is converted, not relabelled

The answer rejects adding `//go:build acceptance` to File 3. The subject of
File 3 is the notifier's own request-building behaviour, not a property of
real infrastructure, so the label would be a way of hiding a crossing from the
locator rather than naming a real-infrastructure suite. The conversion keeps
the spec external and in-process: an injected HTTP transport/`http.Handler`
seam or a gomock mock of an exported interface the notifier depends on — no
listening socket.

**Fails if** the answer accepts the label, or justifies converting File 3 only
by "sockets are slow". The discriminator is what the suite is *about*: File 2
is about PostgreSQL, File 3 is about the ledger's own code.

## R41-4 — the reason, not the locator, decides each file

The answer grounds each verdict in what the test is for (substrate, real
infrastructure under test, own code), not in what makes the locator read
zero. It notices that File 2 and File 3 would both become silent under the
same one-line label and that only one of them should.

**Fails if** the answer's justification for any file is "this clears the
report" or its inverse, "this keeps the report honest", without naming what
the test is for.

---

## Judge's note — the plausible wrong answers

Three fluent failures, each built from an earlier case's lesson.

**"Tests make no real crossings, so no database"** — swaps pgmem for a mock
(R41-1) and deletes File 2 (R41-2). This is the headline of CS-18 applied
without its counterweights, the same failure `033` built against for CS-11.

**"Split by substance: keep 1, keep 2, convert 3"** — the shape the judged
corpus has trained since `021`. It gets three verdicts right and usually
leaves File 2's `integration` tag alone, because nothing in the shape says the
tag is wrong. The operator's definition does: "integration tests" are outside
the package and use gomock. R41-2 fails that answer.

**"Label anything real as acceptance"** — learns from File 2 that the label is
legitimate and applies it to File 3. That is the anti-gaming case: the label
is honest only when the real infrastructure is the subject. R41-3 fails it.
