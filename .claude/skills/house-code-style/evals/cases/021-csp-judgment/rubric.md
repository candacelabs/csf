# Rubric — case 021, one lock, two answers

Tier 2, **split verdict**. Neither "convert it" nor "leave it" is the answer.
Scoring procedure and row format are in `../../README.md`.

**Why this case exists.** CS-5 says *prefer CSP over shared-memory locking*,
which is trivially gamed in one direction — answer "rewrite it with channels"
to everything and score full marks on any corpus of positive cases, then go
convert a 40k/sec read path into a queue behind one goroutine. Cases 010 and
011 introduced the opposite gaming risk: with two negative cases in the corpus,
a model that answers "don't" to every judged prompt also scores full marks.
This case is constructed so that **both blanket answers fail R21-1**. The
prompt deliberately puts an honest leaf lock and a state machine behind the
same `sync.RWMutex`, because that is what real code looks like and because the
rule's content is the distinction between them, not the primitive.

---

## R21-1 — The verdict is split, and it is the top-line answer (required)

The response separates the two paths and gives a different answer to each:

- **Read path — keep it out of a channel.** The flag lookup is a leaf critical
  section: many readers, one occasional writer, no negotiation between the
  readers, nothing to sequence. CS-5's own text calls a mutex the honest
  primitive here.
- **Reload path — this is the one CS-5 is about.** Three goroutines negotiate
  over `reloadState` plus a generation counter, with rules about who may act
  given what someone else already did. That is a protocol, and the lock is
  standing in for it. One owning goroutine, reload requests arriving as
  messages, is the CS-5 answer.

**Fails if** the response gives one verdict for the whole package in either
direction — including "convert it all, the read path will be fine" and
"leave it alone, `-race` is clean". Everything below is scored only if this
passes.

## R21-2 — The criterion is the presence of a protocol, not the lock's cost

The two paths are told apart by *what the lock is doing*: guarding one datum
with no cross-goroutine agreement (leaf, keep) versus sequencing decisions
several goroutines make about each other (protocol, convert). A response may
reach the right split for the wrong reason — "the read path is hot so a channel
would be slow" is a performance argument, and it would licence keeping a
contended state machine under a lock the moment someone waves a benchmark.

**Fails if** the split is justified only by throughput, or only by "the read
path is simpler", without naming the protocol/no-protocol distinction. Note the
throughput argument is *correct and worth stating* — see R21-3 — it just is not
sufficient on its own.

## R21-3 — The cost of converting the read path is stated concretely

A single owning goroutine serializes reads that are currently parallel: 40k/sec
through one goroutine, each read costing a reply channel and two channel
operations, replacing an `RLock` that several cores can hold at once. Naming
this is what shows the response understands why CS-5 has a counterweight rather
than reciting that it has one.

**Fails if** the response converts the read path, or declines to convert it
without being able to say what conversion would have cost.

## R21-4 — The reload rewrite is described concretely enough to be wrong

"Use channels" is not a design. A passing response says what the owner
goroutine owns (`reloadState`, the generation counter, the backoff schedule),
what the messages are (a reload request from the watcher, one from the admin
handler, a timer tick, a completed-reload result carrying its generation), and
what happens to the current rules — the duplicate-reload suppression and the
stale-generation check become ordinary `if` statements inside one goroutine,
where they can be read in one place.

**Fails if** the recommendation stops at "give it a channel" or "use a
goroutine to own the state" with no account of the messages or of where the
existing rules end up.

## R21-5 — The gate finding is treated as a locator, not a defect

`check_style.py` will keep reporting the surviving read-path mutex forever, and
that is correct behavior: the heuristic flags every declaration because no
lexer can tell the two cases apart. A passing response says so plainly and does
not propose to make the finding go away.

**Fails if** the response treats the finding as something to be cleared, or
proposes silencing it — an exclusion list, a narrowed heuristic, a marker
comment, a threshold. It also fails if it converts the read path *in order to*
get a clean report, which is the gate driving the design instead of the other
way around.

## R21-6 — Mutex and channel are not presented as the only two options

The read path has a third form that is better than either: an immutable
snapshot published by pointer (`atomic.Pointer[Config]`, copy-on-write on
reload), which removes the lock without adding a protocol. A response may
recommend it or may keep the `RWMutex` as adequate, but it should not present
the choice as binary — and if it does raise the snapshot, it must not describe
that as satisfying CS-5's preference for message passing. It satisfies CS-5 by
removing the *sharing*, which is a different move.

**Fails if** the response frames the decision as mutex-or-channel only, or
labels an atomic snapshot as "the CSP version".

## R21-7 — It says what would flip the read-path answer

Name the trigger: the moment a reader needs to observe a specific generation,
wait for a reload to land, be notified that the flags changed, or write
anything, the read path has grown a protocol and CS-5 bites there too. A
verdict with no re-entry condition is a preference, not a rule.

**Fails if** the "keep it" half is unconditional.

---

## Judge's note

Two plausible wrong answers, and the case is built to catch both.

The first is fluent CSP maximalism: *"Per CS-5, prefer channels — I'll put the
config behind an owner goroutine and have callers request flags over a
channel."* It cites the rule, it is internally consistent, and it puts a
40k/sec read path behind a single scheduler.

The second is the corpus-shaped answer. A model that has seen cases 010 and 011
learns that judged cases are about restraint and answers *"keep the mutex,
CS-5 has counterweights, `-race` is clean"* — which is right about the read
path, wrong about the reload path, and arrives at both without looking. Grade
R21-1 strictly: a response that never separates the two paths has not answered
the question, whichever verdict it landed on.
