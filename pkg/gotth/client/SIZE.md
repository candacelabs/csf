# gotth-live client size ledger

| | |
|---|---|
| **Status** | Phase 4, measured — §1.1 is the delta since checkpoint 2, landing by landing; §2.1 and §2.2 are the two separate dev-only artifacts |
| **Date** | 2026-08-05 |
| **Author** | DEV-2 (Client Runtime) |
| **Gate** | PRD **NFR-2** — ≤ **12,288 bytes**, `gzip -9` over the minified single file. PRD **NFR-8** — ≤ **40,960 bytes** over the separate dev inspector (§2.1). **FR-57** — ≤ **8,192 bytes** over the separate dev-reload client (§2.2), a ceiling this project set rather than one the PRD states |
| **Also satisfies** | NFR-3 (per-subsystem breakdown), NFR-4, NFR-5, NFR-6, NFR-8, FR-57; review-checklist §7.1, §7.2 |
| **Supersedes** | the estimated column of [RFC-0001 §10.4](../docs/rfc/001-architecture.md) for every line except morph, which was already measured |

Reproduce, from `candace/pkg/gotth/tools/`:

```
docker run --rm -v "$(git rev-parse --show-toplevel):/workspace" \
    -w /workspace/candace/pkg/gotth/tools dis-gotth-live:latest \
    go run ./minify
```

**Where the pieces live.** `client/` is the source of truth for the runtime
source, the generated codec, the dev inspector, the dev-reload client, the
predicate manifest and the node tests, and holds **no Go file**. The shipped
bytes are emitted to **`live/clientjs/gotth-live.min.js`** — the inspector's to
**`live/clientjs/gotth-live-inspector.min.js`** and dev reload's to
**`live/clientjs/gotth-live-dev-reload.min.js`** — and embedded by `live` by
exact filename.
There is exactly one copy of each: an `//go:embed` pattern may not contain `..`
or name a file outside its own package directory, so the artifacts have to sit
under `live/`, and keeping a second copy beside the sources would create a
two-copy equality invariant that somebody would then have to check. `-check`
rebuilds `client/` and compares all three — the FR-7 staleness check, guarding
three artifacts rather than an equality between six. `live/clientjs/` holds no Go file
and is therefore not a package, so the C-12 cap of two exported packages stands
(L9-1 addendum to [docs/reviews/module-init.md](../docs/reviews/module-init.md),
2026-08-04; C-20 makes that a test).

---

## 1. The number

| | Bytes |
|---|---:|
| `runtime.js` source | 59,266 |
| `codec.gen.js` source (generated) | 6,290 |
| **`live/clientjs/gotth-live.min.js` — the file the library serves** | **10,387** |
| **`gzip -9` over it — the NFR-2 gate** | **4,459** |
| Ceiling | 12,288 |
| **Headroom** | **7,829 (63.7 %)** |

`gzip -9` is measured with Go's `compress/gzip` at `BestCompression`, inside the
tool, so CI needs no external binary. GNU `gzip -9` on the same file reports
**4,433**, and `gzip -9 -n` — no name, no mtime in the header — reports
**4,415**: **26 and 44 bytes below** the tool.

That gap wants a correction to what this paragraph used to say. The two agreed
exactly at checkpoint 1, differed by one byte at checkpoint 2, agreed again at
the reconnect landing, and this file called the difference *header framing*. It
is not: the two implementations make different matching choices on the same
input, and the earlier agreements were the coincidence, not the rule. Nothing
about the gate changes — **the tool's number is the gate**, it is the larger of
the two, and it is re-measured on every landing rather than
carried — but a reader should not infer from this row that the two
implementations are interchangeable to within a byte. They are not.

> **⟨CORRECTED 2026-08-05 — the three GNU figures above were re-measured at
> `2311280b` and two of them moved; a third did not survive re-derivation.⟩**
> This paragraph carried **4,397** and
> **4,379**, *"24 and 42 bytes below"*, and attributed the difference to
> *"35 bytes"*. Those were measured against the `10,391`-byte artifact and were
> never re-taken when `2ab18690` and then `0b9e32e7`/`2311280b` moved it. Taken
> again on the shipped bytes with `gzip 1.13` in `dis-gotth-live:latest`:
> **4,433** and **4,415**, so the gaps are **26** and **44**.
> **The `35` is struck rather than updated.** The 18 B between the two GNU
> columns is exactly the stored filename (`gotth-live.min.js`, 17 B + NUL), so
> the header-free comparison is the `-n` one and the algorithmic gap is **44 B**.
> `35` reconstructs from neither column at this tree or the previous one, and a
> number that cannot be re-derived should not be carried; the claim it was
> supporting — that the gap is matching choices and not framing — is unaffected
> and is now carried by the `-n` figure, which has no framing in it at all.
> Found by DEV-2 during the FR54-2 sweep; it is **outside** the
> `10,391 / 4,429 / 10,306 / 4,421` grep that defined that sweep, which is the
> point worth keeping.

Brotli is reported for information only under NFR-2 and is not measured here:
the bench image would be needed for a `brotli` binary and the gate is gzip.

### 1.1 Delta since checkpoint 2 (NFR-3)

| | Minified | gzip |
|---|---:|---:|
| Checkpoint 1 | 9,143 | 3,874 |
| Checkpoint 2 | 9,343 | 3,961 |
| Checkpoint 3, the reconnect state machine | 9,731 | 4,124 |
| Checkpoint 3, the key filter | 9,754 | 4,137 |
| Checkpoint 3, the resync re-arm | 10,178 | 4,360 |
| Checkpoint 3, the snapshot boundary | 10,391 | 4,429 |
| **Phase 4, the dev session inspector** | **10,391** | **4,429** |
| **Phase 4, FR-57 dev reload** | **10,391** | **4,429** |
| **Phase 4, FR-54 failure 2: per-binding options** | **10,306** | **4,421** |
| **Phase 4, FR-54 failure 1: NoModifiers and PreventDefault (this)** | **10,387** | **4,459** |
| **Delta since checkpoint 2** | **+1,044** | **+498** |

Six landings, measured separately, because a checkpoint total that nobody can
attribute is the number NFR-3 exists to prevent.

**The dev-inspector and dev-reload rows are zeroes, and they are the point of
§2.1 and §2.2.** FR-44's inspector and FR-57's dev reload both landed on
2026-08-05 and this artifact did not change by one byte for either, because
neither has a seam here to attach to. Rows of zeroes in a delta table are normally rows not worth
writing; these two are the evidence for NFR-8's second clause and for FR-57
inheriting it, so they are written.

#### 1.1.6 `NoModifiers` and `PreventDefault` — **+38 gzipped bytes**, and **+4 over a pre-registered ceiling** (FR-54 failure 1)

Two more subscripts into the `split` §1.1.5 was already performing, and no new
attribute: `s[6]` is "no modifier held" and `s[7]` is this binding's own
`preventDefault`. `docs/reviews/fr-54.md` §12 is the ruling and §14 C-3 is the
budget it was accepted at.

| | Minified | gzip | Passes the committed specs? |
|---|---:|---:|---|
| baseline (`2ab18690`, this file's row above) | 10,306 | 4,421 | — (predates both components) |
| **C-3's pre-registered ceiling** | **10,368** | **4,455** | — (a number, not a shape; **no shape reaches it**) |
| §12.1's prototype, rebuilt and re-measured here | **10,368** | **4,455** | **NO — C-9 forbids the placement** |
| the cheapest of twelve spellings tried | 10,366 | 4,456 | **NO — turns one spec red** |
| **shipped — the prototype with C-9's correction** | **10,387** | **4,459** | **yes**, 23/23 |
| the same correction folded into one expression (rejected on merit, see below) | 10,372 | **4,458** | **yes**, 23/23 |
| **⇒ the floor over shapes that pass** | **10,372** | **4,458** | — |

**Measured, not estimated**, with `tools/minify` against copies under the
container's `/tmp` and the worktree mounted read-only; the shipped row is
`tools/minify -check` against the committed artifact.

**Read the last column before the byte columns.** Two of the four shapes above
are **defective**, and the cheaper a row is the more likely it is to be one of
them: the two cheapest numbers in this table are both shapes that must not
ship. **`4,458` is the floor, not `4,456`** — the only number here a later
reader should treat as a target, and it is *available and correct* and refused
on merit rather than on price.

> **⟨CORRECTED 2026-08-05 — FR54-10, `reviews/fr-54.md` §18.2 and §23. The
> `10,366 / 4,456` row was tabulated as *"the cheapest of twelve spellings
> tried"* with no indication that the spelling **fails**.⟩** It hoists the
> composition guard **above** the submit/anchor `preventDefault` and folds
> everything below it, so a submit or an anchor click mid-composition has its
> default no longer suppressed and **navigates for real**. L9-1 built it and
> turned exactly one committed spec red; **I rebuilt it here rather than take
> the finding on the record**, in `dis-gotth-live-bench:latest` against a `/tmp`
> copy with this worktree mounted read-only:
>
> ```
> # composition guard hoisted above submit/anchor, everything folded below
> tools/minify -check   -> Shipped gotth-live.min.js  10366  4456     (reproduces exactly)
> node --test client/test/binding.test.mjs
>   ✖ a submit still has its default suppressed mid-composition
>     AssertionError: the page would have navigated away mid-composition
>     0 !== 1                                             22 pass / 1 fail
> ```
>
> The spec is `client/test/binding.test.mjs:437`, and its own header comment
> names this mutation in advance — *"the obvious way to satisfy C-9 is to fold
> `s[7]` into it and push the whole thing below the guard, which would let a
> form navigate for real mid-composition — a second defect wearing the first
> one's fix."* **The control caught the number this table was publishing as a
> floor.** The `4,458` folding, by contrast, I also rebuilt: `10,372 / 4,458`,
> **23 pass / 0 fail**, so its rejection below is genuinely on merit and not on
> a hidden failure.
>
> **What this does *not* change.** The conclusion *"no correct landing meets
> 4,455"* is untouched and is now stronger — it held *a fortiori* even from a
> defective floor, and from the passing floor of `4,458` it holds by 3 B rather
> than by 1 B. **Only the premise was wrong**, and it was wrong in the way that
> matters: it mixed shapes that pass with a shape that does not, and a reader
> handed `4,456` as "the floor" would have reimplemented the defect while
> believing they were hitting a published target. Owner DEV-2; the ruling is
> L9-1's at §18.2.

**The ceiling is exceeded by 19 B minified and 4 B gzipped, and the whole of the
difference is C-9.** L9-1's §11 price was taken on the §12.1 prototype, which
calls `preventDefault` **above** the composition guard — and §14's C-9, written
afterwards, says that placement is wrong: `Enter` during an IME composition
commits the candidate, so suppressing it there breaks every CJK composer (FR-26).
The prototype's number reproduces here **exactly**, to the byte, in both columns,
which is what makes the attribution clean rather than a claim: C-3 priced a shape
C-9 forbids, and the re-price was never taken. The floor across every spelling
tried is **4,456**, so **no correct landing meets 4,455.** Held here at the
number rather than argued away; L9-1 owns the ruling (`api-surface.md` §10).

> **⟨CORRECTED 2026-08-05 — both halves of the paragraph above have been ruled
> on, and the ruling went the way it asked for.⟩** Two corrections, one to this
> file and one to the constraint it was measuring against.
>
> **The premise, per FR54-10.** *"The floor across every spelling tried is
> 4,456"* mixes shapes that pass with a shape that does not. **The floor over
> spellings that pass is `4,458`** — see the table's last column and the block
> beneath it. The conclusion *"no correct landing meets 4,455"* stands, and
> stands by more room than it claimed.
>
> **The constraint, per `reviews/fr-54.md` §18.3.** *"Held here at the number
> rather than argued away"* was the right posture and L9-1 took it seriously:
> **C-3 is amended, to `≤ 10,387 minified / ≤ 4,459 gzipped`** — the corrected
> shape as built. L9-1's stated reason is that they *"priced before I corrected
> and then never re-priced"*, that `4,455` is reachable **only** by the shape C-9
> forbids, and that *"a constraint with no satisfying artifact does not gate the
> work, it only gates the worker."* **So this landing is no longer over a
> ceiling; it is at one.** The `+4 over a pre-registered ceiling` in this
> section's heading, and *"the ceiling is exceeded by 19 B minified and 4 B
> gzipped"* above, are both true **of C-3 as pre-registered** and false of C-3 as
> amended. They are left standing rather than rewritten — the heading is an
> anchor other pages link to, and the arithmetic against the original number is
> the evidence for why it moved.
>
> **NFR-2's ceiling does not move and never has**: 12,288 B, **7,829 B headroom
> (63.7 %)**, `tools/minify -check` at HEAD. Neither does §13's T-2 re-open
> envelope at ≤ 4,475 B gzipped.

**Why the cheaper folding was not taken.** `(s[7] && !composing) || …` is
semantically identical and 15 minified bytes cheaper, and it puts a **second
copy of the composition condition** in the file — one in the guard and one three
tokens above it. A later edit that moves the guard leaves the copy behind, and
the failure it produces is a suppressed IME commit on a keyboard most reviewers
do not have. One condition, one place; the 15 bytes buy that.

**What the two components cost separately** is not additive and is not reported
as if it were: `s[6]`'s modifier test and `s[7]`'s suppression share the `split`,
the loop and the matched spec, and esbuild's identifier assignment shifts under
either. §11's table has the per-shape prices measured the same way.

#### 1.1.5 Per-binding options — **−8 gzipped bytes** (FR-54 failure 2)

The first landing in this table that costs nothing, and the reason is worth a
line: **three attribute reads were replaced by three array indexes into a split
that was already happening.** `data-gotth-fields`, `data-gotth-debounce` and
`data-gotth-throttle` are gone from the vocabulary; the values are components
four, five and six of the binding in `data-gotth-on`, and `dispatch` reads them
off the spec it matched.

| Change | Where | Why |
|---|---|---|
| `A_FIELDS`, `A_DEBOUNCE`, `A_THROTTLE` deleted; `d = +s[3]`, `th = +s[4]`, `fields(el, s[5])` | events, bootstrap | three constants, three `getAttribute` calls and their argument strings, against three subscripts. An absent component is `undefined`, `+undefined` is `NaN` and `NaN \|\| 0` is `0`, so a trimmed binding costs no extra test |
| the timer record keyed by the matched spec inside the element's entry | events | `timers` stays a `WeakMap` on the element so a removed node still takes its timers with it; `st[specs[i]]` is what keeps one binding's pending send out of another's reach. Two bindings whose specs are byte-identical are the same binding twice and correctly share one record |
| nothing stored for a binding that neither debounces nor throttles | events | the old code wrote a `WeakMap` entry on the first dispatch of any bound element. Most bindings are a plain click, and they no longer enter the map at all |

**Two defects, one cause, and each needs its own half of the fix.** The interval
was per element and so was the timer slot. A per-binding timer with a
per-element interval still delays a key binding by 150 ms for a reason its
author never wrote down; a per-binding interval with a per-element timer still
loses an event whenever two bindings on one element both debounce. QA-1's
mutation control measured the first half alone at **+15 B minified, +9 B
gzipped** and observed that it left the delay standing. Both halves together,
with the attribute reads removed, are **−85 B minified and −8 B gzipped**.

**Events, 504 → 516 marginal gzip bytes**, minified **1,424 → 1,387**, and its
source grew by **3,066 B** — the argument written down, at the ratio §2 exists to
make visible. The marginal column went *up* while the region got smaller, which
is the cross-region sharing this file describes below: the three attribute-name
string literals this region no longer spells were the region's cheapest bytes to
share with `bootstrap`, which spelled them too.

**`api-surface.md`'s standing claim that this could not be done is now
measured.** Its `OnAll` consequence row said the options *"cannot be per binding
without a second timer table in the runtime"*. There is no second table: the
entry that already existed grew a key. That row is corrected in place with the
figure beside it.

#### 1.1.4 The snapshot boundary — +69 gzipped bytes (REV-INV U-1/U-2, REV-DEL 2.10)

Two changes in opposite directions, measured **separately** against the same
starting artifact rather than netted, because they answer different questions
and only one of them is a spend:

| Change | Minified | gzip |
|---|---:|---:|
| Starting point (the resync re-arm) | 10,178 | 4,360 |
| REV-DEL 2.10 — `match()`'s redundant cursor guard deleted | 10,175 | 4,357 |
| REV-INV U-1/U-2 — the `applied()` boundary check | **10,391** | **4,429** |

**−3 / −3 for the deletion.** `match()` guarded a cursor its only caller had
already guarded — `morphChildren` writes `m = cur ? match(cur, nc) : null` — so
the `if` was always taken and the `return null` behind it was unreachable. The
caller's guard is the one kept, because it also skips the call. REV-DEL
measured 2.10 and 2.12 together at −7 / −4; this is 2.10 alone, and 2.12 is not
in this stream.

**+216 / +72 for the check**, in the provenance region, and it is the whole of
H-13's second enforcement site:

| Change | Where | Why |
|---|---|---|
| `p.server_seq <= seq` → close `4002` | provenance | U-2. A Snapshot could move the client's high-water mark BACKWARDS, and the next ack then went backwards too — which the server closes as 4002 under H-7. The session ended either way; without this it ended citing the client's ack instead of the frame that caused it. The patch path cannot reach it: `onMessage` discards anything that is not `seq + 1` first |
| `superseded_from_seq` / `superseded_through_seq` read and checked | provenance | U-1. The generated codec has decoded fields 10 and 11 since they were added and nothing read them, so H-13's *"on both the outbound boundary and the client decoder"* shipped in one of the two places it named. Four comparisons: both zero, or both non-zero with `from === seq + 1 <= through < server_seq` |
| two close reasons carrying their numbers | provenance | `"server_seq 1 at 2"` and `"supersession 4-6 at 2"`. The bytes are the point of the check — a self-inflicted 4002 whose reason does not name what disagreed sends the operator to read the wrong side |
| **not** H-13's `Origin.kind == RESYNC` iff clause | (not landed) | it needs `OriginKind`, and the generated enum is one object, so importing it to compare one member ships all six the way `ErrorCode` ships all eight — §1.1.3 measured that shape at **126 gzipped bytes**, which is larger than this whole landing. The range clause constrains what the client does next; the kind clause only labels the frame, and the outbound boundary already refuses a mislabelled one |

**Provenance, 232 → 309 marginal gzip bytes**, and its source grew 3,859 B for
224 in its own minified column (+216 in the artifact) — the argument for why a
client checks something the server also
checks (it names the error, and it is the only side that knows where the DOM
stopped), and the two failure shapes the range can have: a **hole**, where
`from > seq + 1` leaves sequences neither applied nor superseded, and an
**overlap**, where `from <= seq` covers state already applied. The overlap is
REV-INV BR-9's shape seen from this end, and BR-9's server-side clamp
(`from := max(last_applied, acked) + 1`) is what makes the two sides agree by
construction.

#### 1.1.3 The resync re-arm — +223 gzipped bytes (QA-2's D-29)

A resync the server's own budget refuses is answered with `Error{RATE_LIMITED}`
and no render (RFC §7.6), which is neither a Patch nor a Snapshot — and those
were the only two frames that cleared the gap latch. So a refused client stayed
latched, discarded every later patch, and stopped acknowledging as a side
effect of having stopped applying, until the slow-client eviction closed the
connection about thirty seconds later. Where the bytes went:

| Change | Where | Why |
|---|---|---|
| `refused()`, `ask()`, `RESYNC_BASE`, and the `gapTries`/`gapTimer` pair | provenance | the retry itself: `bound/2 + random(0, bound/2)` over `bound = min(CAP, 1000·2^n)`. **Equal** jitter, not §8.4's full jitter, and the floor is the point — a delay near zero is precisely the request the server has just refused, and a refused resync has no herd to spread because the resync bucket is per session |
| the ack on a discarded patch | transport | one ack per patch received, naming the sequence the client actually holds. The client used to go silent while latched, because the ack was written by `applied()` and nothing else, which is what made a latched client indistinguishable from a dead one |
| `else if (gapTimer)` in `vis()` | transport | becoming visible pulls an armed retry forward. It cannot arm one, which is what keeps a flapping tab from bypassing the schedule |
| `clearTimeout` in `newSession()` and `applied()` | provenance | a retry armed for a gap that is closed, or for a session that is gone, must not fire |
| `ErrorCode` added to the codec import | (codec) | **126 of the 223 bytes.** The generated enum is one object, so importing it to compare one member ships the other seven; a bare `6` measures 4,231 B instead of 4,357 B against the same runtime. The import is kept: `PatchOp` and `ResyncReason` are already imported whole for the same reason, the generated table is the single source of truth for a value the wire fixes, and 126 B buys that against 7,928 B of headroom. Recorded here so the trade can be reversed by whoever needs the bytes — or removed entirely, if `gen-clientcodec` ever emits one constant per member instead of one object |

**Provenance, 185 → 232 marginal gzip bytes**, and its source grew by 8,126 B
for **188 minified bytes** — the most lopsided ratio in this file. Almost all of
it is the argument written down: why a refusal re-arms the request and not the
detector, why the client keeps acknowledging while it cannot apply, why the
jitter here has a floor where §8.4's does not, and what the wire does not carry
(neither a retry-after on `Error` nor the resync budget in the `Snapshot`'s
session parameters, so `RESYNC_BASE` is the documented default and a guess).
**Transport, 580 → 595**, for the discarded-patch ack and the visibility line.

#### 1.1.1 The key filter — +13 gzipped bytes (FRICTION.md F-3, FR-54)

`data-gotth-on` gains an optional third component, one `KeyboardEvent.key`
value the event must carry, and `dispatch` compares it before it matches. Where
it went:

| Change | Where | Why |
|---|---|---|
| `(!s[2] || s[2] === e.key)` in `dispatch`'s spec loop | events | the whole of it. A binding already splits on `":"`, so the filter is the third field of a split that was happening anyway — one comparison against a value the event already carries |
| nothing else | — | **no new attribute**, so no second `getAttribute` and no new constant; **no list parsing**, because one key per binding makes a key list several bindings and the loop was already iterating them; **no allocation** on the dispatch path, which a `split(",")` per keystroke would have been |

The proposed shape was a `data-gotth-keys` attribute on the element, and it is
**not** what landed. Per element is one filter for every binding on that
element: a composer bound `input:chat.draft;keydown:chat.clear` would have had
its INPUT binding filtered by a key an input event does not carry, and the
draft would have stopped being sent with no error anywhere. It also cannot say
which of two keys raises which event, which is exactly what the benchmark
counter's F-CTR-6 needs. Per binding costs less (the attribute read was already
there) and expresses more; the argument is written out above `dispatch` in
`runtime.js`.

The **events** region is the only line that moved for a reason of its own:
**488 → 504 marginal gzip bytes**, minified **1,401 → 1,424**, and its source
grew by **2,538 B** — 99 % of which is the rule written down, at the ratio §2
exists to make visible. Every other subsystem moved by ±2 B with no source
change at all, which is the cross-region DEFLATE sharing described below; the
figures are re-measured rather than carried.

#### 1.1.2 The reconnect state machine — +163 gzipped bytes (RFC-0001 §8.4)

The line §3 and PRD R-2 have both been carrying as a known future spend since
checkpoint 1. It was booked; this is it landing. Where it went:

| Change | Where | Why |
|---|---|---|
| `schedule()`, `BASE`, `CAP`, and the `attempt` counter | transport | §8.4's `delay = random(0, min(cap, base·2^n))`, base 250 ms, cap 15 s, unlimited attempts. `Math.pow` overflows to `Infinity` before `attempt` can, and `Math.min` then yields the cap, so no clamp is bought |
| `vis()` and the `visibilitychange` listener | transport | §8.4's pause and resume. A hidden tab holds **no timer at all**, so the resume is immediate by construction rather than by racing a schedule the browser throttles to once a minute anyway |
| `open()` split out of `start()` | transport | one place constructs a socket, on the first connect and every retry alike, so "a reconnect is a new session" cannot be true on one path and false on the other |
| `onClose` classifies rather than reporting | transport | the `TERMINAL` list already existed and its bytes are already counted; what is new is that a non-terminal close now arms a retry and a terminal one provably does not — including against a later `visibilitychange`, which is the other way back in |
| `attempt = 0` on the Snapshot | transport | the backoff resets when a connection reaches **live**, not when the socket opens. A server that accepts a connection and closes it — a crash loop, a draining instance — is exactly what full jitter is for, and resetting on open would hand it 250 ms retries for ever |
| `newSession()` | provenance | §8.1: session lifetime is exactly connection lifetime, so `sid`, `seq`, `ref` and `gap` are connection-scoped and clear on every attempt. Two of the four have a falsifying spec and two do not; the comment above the function says which, and why they stay |

Nothing was removed and no other subsystem was touched. The **marginal gzip**
figures for morph, events, bootstrap and the codec each moved by a few bytes in
§2 without their source changing at all, which is the cross-region sharing that
file's own note describes: transport's new string literals and control flow
change what DEFLATE can match elsewhere. The numbers are re-measured rather than
carried, and the movement was ±12 B then and ±2 B for the key filter.

`save()`/`restore()` is **unchanged and still 617 minified bytes** — QA-1's
**D-21**. It is not dead code and it has not been removed: it is the only thing
standing between FR-25 and a regression when an id-matched element's **tag**
changes, which sends `morphNode` down `replaceWith` and brings the subtree back
as fresh server markup. What D-21 actually found is that nothing tested it.
That is now `dom_preservation_test.go`'s *"restores focus, the caret and scroll
across a patch that REPLACED the node holding them"*, and QA-1's own N4 and N5
mutations — drop the scroll capture, then neuter `restore()` entirely — each
turn exactly that spec red where they previously turned nothing red anywhere.

---

## 2. Per subsystem, measured

| Subsystem | Source B | Minified B | Marginal gzip B | RFC §10.4 budget | Delta |
|---|---:|---:|---:|---:|---:|
| morph | 15,605 | 2,991 | 1,070 | 5,000 | **−3,930** |
| codec (generated) | 6,290 | 3,245 | 1,321 | 2,000 | **−679** |
| events | 11,464 | 1,468 | 555 | 1,300 | −745 |
| transport | 8,367 | 1,587 | 576 | 1,600 | −1,024 |
| provenance + telemetry | 15,983 | 893 | 302 | 700 | −398 |
| bootstrap + status + errors | 1,467 | 642 | 219 | 500 | −281 |
| *shared / residual* | | −380 | 457 | | |
| **Total** | | **10,387** | **4,459** | 11,100 | **−6,641** |

**Events moved this landing and nothing else was edited**, and it is FR-54
failure 1 (§1.1.6): the whole of `NoModifiers` and `PreventDefault` is inside
the events region, whose source grew **9,876 → 11,464** for **1,387 → 1,468**
minified and **516 → 555** marginal gzipped bytes. It is at **43 % of the
1,300 B** RFC §10.4 allowed for "delegation, debounce, serialization", the
largest share it has taken. Morph, transport and provenance were not touched
and moved by +3, +1 and +1 marginal bytes for the reason the column note below
gives; the residual absorbed the other direction (462 → 457).

> **⟨CORRECTED 2026-08-05 — this table was carried, not re-measured, through
> `0b9e32e7`/`2311280b`, and every row of it was stale.⟩** It read
> `10,306 / 4,421` in the Total, which was the shipped figure at `2ab18690` and
> stopped being HEAD's when `NoModifiers` and `PreventDefault` landed. **This is
> the same defect as `reviews/fr-54.md` §8's FR54-2, one landing later and in
> the same file**, and §8's own sweep could not have caught it: L9-1 grepped
> `10,391|4,429`, and §2 was carrying the *other* stale pair, `10,306|4,421` —
> which was still correct at the tree L9-1 reviewed. Re-measured here with
> `tools/minify -check` at HEAD, all six regions and the residual, not just the
> Total. Owner DEV-2; the four earlier landing paragraphs below are unedited.

**Events and bootstrap moved for a reason of their own at FR-54 failure 2**, and it is
FR-54 failure 2 (§1.1.5): events minified **1,424 → 1,387** and bootstrap
**712 → 642**, for the three attribute constants and the three `getAttribute`
calls that no longer exist. Both marginal columns moved in the direction the
note below the table predicts rather than with their minified sizes — events
**504 → 516** while shrinking, because the strings it stopped sharing with
bootstrap were the cheapest it had. The other four regions were not edited at
all and moved by ±8 B, and the residual absorbed the difference (451 → 462).

The four paragraphs below are the four earlier landings, kept in place. **One of
them cross-references the live table and that reference has aged**: the
snapshot-boundary paragraph ends *"the table above is the current one, and it
reads 301 (43 %) and 1,067."* That was true at `2ab18690`; the table above now
reads **302 (43 %) and 1,070**, neither region having been edited since. The
dated sentence is left standing per this file's convention — only its pointer
is corrected, here, and the percentage it quotes is unchanged.

**Transport, 429 → 580 marginal gzip bytes**, which is the whole of §8.4, at
**36 % of the 1,600 B RFC §10.4 budgeted for "WS, backoff+jitter, heartbeat,
ack"** — the one budget line that explicitly named this work. Its **source**
grew by 3,378 B and its **minified** size by 338 B, and provenance's source grew
2,488 B for 33 minified bytes: the ratio this file exists to make visible.
Nearly all of the reconnect landing is the written-down rule, not code — why
full jitter and not equal, why the reset is on live and not on open, and which
two of `newSession()`'s four fields have a spec that fails without them.

**Provenance, 185 → 232 and transport, 580 → 595 marginal gzip bytes**, which
is the resync re-arm (§1.1.3); the shipped total also carries
126 B of generated `ErrorCode` table that tree shaking used to remove, which
belongs to the codec line in the shipped artifact but not to the marginal
column, because the marginal baseline never tree-shakes.

**Provenance, 232 → 309 and morph, 1,067 → 1,064 marginal gzip bytes**, which
is the snapshot boundary and the `match()` deletion (§1.1.4). Provenance was at
**44 % of its 700 B** budget at that landing — the largest share any line in
this table took of its own budget, and the smallest budget RFC §10.4 drew — and
morph was the only subsystem here that had ever gone down. Both figures moved
without either region being edited when per-binding options landed: the table
above is the current one, and it reads 301 (43 %) and 1,067.

**Three lines moved without their regions being touched at all**: codec
1,336 → 1,329, events 506 → 504, transport 595 → 583. Nothing was edited in any
of them this landing. A marginal is `gzip(baseline) − gzip(baseline without
that region)`, so it is a property of the WHOLE file rather than of the region:
the new provenance text spells `close(4002`, `server_seq` and `seq`, which the
transport region also spells, so deleting transport now loses fewer bytes that
appear nowhere else — and the difference moves into the shared residual
(434 → 451). It is the effect the column note below describes as the reason the
marginals do not sum, seen from the other side, and it is worth saying out loud
because the other reading — that the transport subsystem got 12 bytes cheaper —
is simply wrong.

**Events, 488 → 504 marginal gzip bytes**, which is the key filter (§1.1.1).
Its source grew by 2,538 B for **23 minified bytes**, the most lopsided ratio in
this file so far: one comparison, and four paragraphs saying why it compares
`e.key` and nothing else, why it is a component of the binding rather than an
attribute of the element, and which two characters a key value cannot be. The
line is at **39 % of the 1,300 B** RFC §10.4 allowed for "delegation, debounce,
serialization".

Morph's **source** grew by 5,593 B at checkpoint 2 and its **minified** size by
105 B, for the same reason; this checkpoint it grew another 235 B of source
while its minified size **fell** by 3, which is what a deletion with its
reasoning written down looks like in this ledger.

Subsystem boundaries are `//#region` markers in `runtime.js`, and the tool
**fails** if a line of code sits outside every region. That is what makes the
ledger exhaustive rather than approximately exhaustive: silent growth is the
failure NFR-3 exists to catch, and a line nobody counted is exactly how it
would happen.

### What each column means, and what it does not

**Minified B** is the region minified on its own. Exact, additive, and it says
where the source went. It over-counts by 402 B against the bundle, because
bundling renames identifiers across the whole file — that is the negative
residual, and it is a saving, not a discrepancy.

**Marginal gzip B** is `gzip(baseline) − gzip(baseline without that region)`:
the compressed bytes the file would lose if the subsystem were not there. It is
the number that answers *what is this costing me*.

The marginals **do not sum to the total**, and nothing here pretends they do.
DEFLATE shares matches across regions, so part of the compressed size belongs to
no single subsystem; that part is the +451 B residual line, reported rather than
distributed, because distributing it would be an invention.

The marginal baseline is built with **tree shaking off** (10,450 B minified,
4,473 B gzip) while the shipped artifact is built with it on. Deleting a region
also deletes the last use of things it referenced, so a tree-shaken variant
measures the dead-code cascade instead of the region. The first version of this
tool reported `bootstrap` — 712 minified bytes — as costing 1,402 gzip bytes,
which is how the problem announced itself.

### 2.1 The dev inspector — a second artifact, a second ceiling (NFR-8)

| | Bytes |
|---|---:|
| `inspector.js` source | 35,690 |
| `live/clientjs/gotth-live-inspector.min.js` | 14,905 |
| **`gzip -9` over it — the NFR-8 gate** | **6,211** |
| Ceiling | 40,960 |
| **Headroom** | **34,749 (84.8 %)** |

Measured 2026-08-05 by the same tool, in the same run, with the same
`compress/gzip` at `BestCompression`. `go run ./minify` prints both tables and
fails on either ceiling.

**It costs the NFR-2 artifact nothing, and that is checkable rather than
claimed.** `live/clientjs/gotth-live.min.js` did not change by one byte in the
landing that added the inspector — `git show --stat` on that commit is the
evidence, and `client/test/bundle.test.mjs` holds the property going forward by
asserting the shipped runtime contains no occurrence of `inspect` at all.

That is a design decision with a cost, and the cost is worth writing down
because the alternative is the one most people would reach for. A callback the
runtime offers — `gotthLive.inspect(fn)`, one variable and two call sites —
measured **~70 gzipped bytes** against 7,859 of headroom. It was not taken. The
inspector instead replaces `globalThis.WebSocket` with a function returning a
real socket with `send` wrapped, filtered to the `gotth-live.v1` subprotocol,
and decodes frames with its own bundled copy of the generated codec. Two
reasons, in order:

1. **NFR-8 says the inspector must not count against NFR-2.** Seventy bytes is
   not a capacity problem; spending a little of a MUST NOT is a precedent
   problem. The runtime now has no inspector-shaped seam for a later feature to
   widen.
2. **`client/test/harness.mjs` already argues the same thing from the testing
   side** — "No seam was added to the runtime for testability, and none should
   be: a seam is a byte cost on every page and a second code path nothing in
   production takes." A seam for a dev tool is the same seam.

What it costs instead is **load order**: the runtime opens its socket during
its own script execution, so the inspector's tag must come first. That is a
silent failure mode by nature, so it is not left silent — the inspector detects
that `gotthLive` already exists and opens its panel with that message.

**The 1,329-byte codec line above is paid twice, once per artifact.** The
inspector bundles its own copy rather than reaching into the runtime's, because
the runtime is an IIFE that exports nothing. That is not a regression against
any budget: NFR-2 measures the runtime and NFR-8 measures the inspector, and
duplicated bytes in the second are bytes the first never carries.

**No per-region ledger for this file.** NFR-3's marginal machinery is pointed
at `runtime.js`, whose ceiling is tight enough that a subsystem's cost is a
design input. The inspector sits at 15 % of a ceiling six times larger, so the
same instrument would be measurement for its own sake. If it ever passes half
its ceiling, the regions are two hours of work and this paragraph is the
argument for doing them.

### 2.2 Dev reload — a third artifact, a third ceiling (FR-57)

| | Bytes |
|---|---:|
| `dev-reload.js` source | 14,951 |
| `live/clientjs/gotth-live-dev-reload.min.js` | 2,452 |
| **`gzip -9` over it** | **1,260** |
| Ceiling | 8,192 |
| **Headroom** | **6,932 (84.6 %)** |

Measured 2026-08-05 by the same tool, in the same run, with the same
`compress/gzip` at `BestCompression`. `go run ./minify` prints all three tables
and fails on any of the three ceilings.

**The ceiling is invented, and saying so is the point.** FR-57 has no byte
budget in the PRD — NFR-2 is the runtime's and NFR-8 is the inspector's. 8,192
exists anyway, because the reason the inspector got one applies here unchanged:
a dev-only file with no gate is a file that grows until somebody measures it,
and the number that must not move is NFR-2's. It is set at roughly six times
the current size, which is the same ratio NFR-8 gives the inspector, and it is
one flag in `tools/minify` if it is ever wrong.

**It costs the NFR-2 artifact nothing, and that is checkable rather than
claimed.** `live/clientjs/gotth-live.min.js` did not change by one byte in the
landing that added dev reload — 10,391 minified and 4,429 gzipped before and
after, and `git show --stat 7cff113a` is the evidence: the file is not in that
diff. `client/test/bundle.test.mjs` holds it going forward by asserting the
shipped runtime contains no occurrence of `dev-reload`, `devReload` or
`gotth-live-dev`.

There was no seam to refuse this time, which is worth one sentence because it
is the *reason* the number is zero rather than small. The inspector had to work
for it — it needed the frames the runtime was already decoding, so it taps
`globalThis.WebSocket` rather than taking a callback. Dev reload needs nothing
the runtime has: its whole input is one HTTP route and one attribute on its own
script tag, so the two files have no relationship at all to keep out of the
budget. That also means there is no load-order rule here and nothing to get
backwards.

**No codec, and that is where the 1,260 comes from.** The inspector bundles the
generated codec (§2.1's 1,329-byte line, paid twice) because it reads frames.
This file reads a 24-byte string over `fetch`, so it imports nothing at all —
the artifact is one IIFE with no dependency in it, which is why it is a fifth
of the inspector's size while doing something a user notices more often.

**No per-region ledger, for §2.1's reason with more room.** At 15 % of a
ceiling this file would have to grow sixfold before a subsystem breakdown told
anybody anything.

---

## 3. Reading the deltas honestly

**The total is 4,459 B against an 11,100 B estimate. That is a large miss in
the safe direction, and PRD R-2 should be revisited, not celebrated.** Two
things account for nearly all of it, and neither is cleverness:

1. **Morph came in at 1,070 B against a 5,000 B budget**, and the budget was
   anchored to idiomorph 0.7.4 at 3,350 B gzip (RFC §10.4). Ours is smaller
   because it does less: id-matching with a persistent-ID pantry, a same-tag
   soft match at the cursor, the FR-25 controlled/uncontrolled rule and the
   FR-27 opt-out — and none of idiomorph's configuration surface, callback
   hooks, head-merging strategies, `outerHTML`/`innerHTML` mode switching, or
   its several morph styles. **The comparison is not like for like**, and the
   right conclusion is that 5,000 B was budgeted for a library we did not need,
   not that morph is cheap. Checkpoint 2 has now added the rest of the FR-25
   suite and the line grew by 28 B for it (§1.1) — the FR-25 completion cost
   far less in the artifact than in the source, because most of it is the rule
   written down.

2. **The codec's table beat its 2,000 B estimate** at 1,321 B. The schema table
   is one ~1.3 KB string of repeated field names and separators, which is close
   to the best case DEFLATE has; straight-line per-field code would have been
   larger and would have grown with the schema.

> **⟨CORRECTED 2026-08-05 — the three figures in the two points above were
> carried through two landings and re-measured here.⟩** They read **4,429**,
> **1,064** and **1,329**. At HEAD they are **4,459**, **1,070** and **1,321**,
> from the §2 table this section reads off — which was itself stale and is
> corrected at its own dated block. Neither morph nor the codec was edited in
> either landing; both moved because a marginal is a property of the whole file,
> which is the effect §2's column note describes. **No argument in this section
> moves**: the miss against RFC §10.4's 11,100 B estimate is 6,641 B rather than
> 6,671 B, and *"a large miss in the safe direction"* survives a 30-byte
> correction comfortably. Recorded because §3 is the section that exists to say
> the ledger should be read honestly, and it was reading a stale table.

**The reserve is real headroom, and the ledger is now mostly spent against
things that exist rather than things that are coming.** Checkpoint 2's FR-25
completion drew **87 gzipped bytes** of it, checkpoint 3's reconnect state
machine drew **163**, the key filter drew **13**, the resync re-arm drew
**223**, and the snapshot boundary drew **69** (§1.1) — every one of them
measured rather than estimated. Two of the five were carried in this file as
known future spends; the key filter, the re-arm and the boundary check were
not, because all three are defects found after the line was drawn — the last of
them a review finding that the client decoded two fields H-13 names it the
enforcer of and read neither. The
re-arm is the most expensive of the five and 126 B of it is one generated enum
table the import stopped tree-shaking away, which §1.1.3 records with the
measured alternative so the trade can be reversed without re-deriving it;
§1.1.4 declines the same trade for `OriginKind` on the strength of that
measurement, which is the ledger being used rather than merely kept.
**One estimate is left:** the `matches`/numeric predicate evaluator
protocol.md §10.3 declines to ship, still 600–1,200 B and still the only line
here that is not a measurement. It fits inside 7,859 B of headroom six times
over.

Worth stating plainly, because the ratio is the interesting part: reconnect was
budgeted implicitly inside RFC §10.4's 1,600 B transport line and came in at
**150 B of marginal growth on that line**. The estimate was not wrong by much
in absolute terms — the transport line as a whole is still 1,017 B under — but
the reason it fits is the same as morph's: the runtime does exactly what §8.4
specifies and nothing configurable around it.

---

## 4. Minify or not — the choice, and why

**Minified, with esbuild, from Go, and the artifact committed.**

The task allowed shipping unminified if the source were written compactly
enough. It is not, and it should not be: `runtime.js` is 53,591 bytes of which
**76 % is comment**, and the comments are where the reasoning lives.
Unminified it would gzip to well over the ceiling.

The minifier is **`github.com/evanw/esbuild` v0.28.1 in `candace/pkg/gotth/tools/`,
its own Go module** — which is not a choice made here but one
[docs/dependencies.md §3](../docs/dependencies.md) already settled, listing
esbuild at exactly that version and location "specifically so it stays out of
the library's `go.mod`". Following it beats the alternative the brief offered:

- **No node is involved at all.** The bench image is the only one with node
  (FR-74), and this does not need it — it runs in `.dis/Dockerfile`, the image
  that deliberately has none. The quarantine is not merely respected; the
  build step never approaches it.
- **`live/clientjs/gotth-live.min.js` is committed**, so a clean clone serves
  the runtime with no minifier, no node, no network and no build step (FR-7,
  G11, NFR-5).
- **The library's `go.mod` is untouched.** `tools/` has its own module and
  nothing in gotth-live's build graph refers to it.

`go run ./minify -check` rebuilds from `client/` and fails if the committed
artifact is stale, so the source and the served bytes cannot drift apart
silently. CI should run it beside the codegen reproducibility check.

---

## 5. What is in the file, and what is honestly not

**In**, and measured above: WebSocket connect with the `gotth-live.v1`
subprotocol; in-band protocol-version negotiation against the first `Snapshot`;
heartbeat echo; the generated frame codec with unknown-tag skipping and
client-side length predicates; delegated event capture with per-binding
debounce, throttle, static fields and key filtering, and form serialization;
morph with the FR-25
preservation rules and the FR-27 opt-out; cumulative acks; `ClientTelemetry`
morph and apply timings; sequence-gap detection driving a `ResyncRequest`;
H-13's client half — a `Snapshot` may not move the client's sequence backwards,
and a supersession range must meet exactly the sequence the client stopped at
(protocol.md §4.3), with `4002` naming which of the two disagreed;
close-code classification; the retry of a `ResyncRequest` the server's own
budget refused, on a bounded equal-jitter schedule, with the client
acknowledging what it holds while it waits; and RFC §8.4's reconnect state
machine — exponential backoff with full jitter, paused while the tab is hidden
and resumed the moment it is visible, with terminal close codes never retried.

**Not in, and the file says so where it matters:**

- **A reflected attribute other than `<details open>`.** `<dialog open>` and a
  custom element reflecting its own state have D-15's shape exactly — two
  authors, one bit — and the runtime keeps the server's word for neither, so a
  patch reverts the user's. FR-25 names `<details>` and every wired entry costs
  bytes here, so the boundary is a decision rather than an oversight; it is
  **measured** by `test/internal/conformance/reflected_attribute_test.go`,
  which also proves `data-gotth-preserve` is a working remedy for the elements
  outside it. The `declared` record is keyed by element rather than by tag, so
  adding one is a branch and not a mechanism.
- **Modifier state on a key binding**, and any `preventDefault` for a key. A
  filter names `KeyboardEvent.key`, which already folds Shift into a printable
  value, and a bound key still does what the browser was going to do with it —
  so "Enter sends, Shift+Enter newlines" is not expressible with a filter
  alone. Asserted rather than assumed, in `keybinding_test.go`.

  > ⟨**CORRECTED 2026-08-05 (`0b9e32e7`). Both halves of this entry are now IN
  > the file, and it is kept because it is the argument that priced them.**
  > `dispatch` reads the four `*Key` booleans for a binding that sets component
  > **7**, and calls `preventDefault` for a binding that sets component **8** —
  > §7 below is the spelling of both, and it is the row to read rather than
  > this one. What survives unedited is the *reason*: a filter alone still
  > cannot express it, which is why this needed two components rather than
  > none, and both default off so **`keybinding_test.go`'s spec is still green
  > and still drives a binding that sets neither**. What is genuinely still not
  > in the file is a way to REQUIRE a modifier — no modifier set, no bitmask,
  > nothing but "none held" — and that is a refusal with a pre-registered
  > re-open trigger (`docs/reviews/fr-54.md` §13) rather than a boundary this
  > entry drew.⟩
- **Any knowledge of the server's actual resync budget.** `RESYNC_BASE` is
  1,000 ms because RFC §7.6's *default* `MinResyncInterval` is one second, and
  that default is all the client has: `Error` carries a code, a message, the
  causal ids and `fatal` and **no retry-after** (protocol.md §3.3), and the
  `Snapshot` re-asserts the heartbeat interval, the inbound frame cap and the
  ack window but **not** the resync budget. An operator who lengthens
  `MinResyncInterval` therefore tells the client nothing, and the schedule
  climbs to the cap instead of being told. Closing that is a wire change and
  belongs to whoever owns the schema; it is filed, not smuggled in here.
- **Any client-side attempt to distinguish which frame a `RATE_LIMITED` error
  refused.** It is not expressible: a refused `ResyncRequest`'s error carries a
  server-minted `event_id` stood in for `client_ref` as well, which is
  indistinguishable from the ids on an error refusing an ordinary `Event`. The
  runtime conditions on its own latch instead, which is sound in both
  directions and is written out above `refused()`.
- **A retry for any error code other than `RATE_LIMITED`.** A resync answered
  with `INTERNAL` or `RESYNC_FAILED` still latches the client until the
  connection ends; those codes say the request would fail again, and inventing
  a retry for them here would be inventing a policy the RFC has not set.
- **H-13's third clause — `Origin.kind == RESYNC` iff the supersession range
  is non-zero.** The client checks the range and not the label, and the reason
  is a measurement rather than a preference: `OriginKind` is not imported and
  the generated enums are one object each, so importing it to compare one
  member ships all six, which §1.1.3 measured at 126 B for `ErrorCode`'s eight
  — larger than the entire boundary landing. The range is what constrains the
  client's next frame; the label only names the frame in hand, and
  `ValidateOutbound` refuses a mislabelled one on the side where enforcement is
  total. Reversible for 126 B if the wire ever needs a second reader of it.
- **The dev-mode provenance inspector** (FR-44, NFR-8) shipped as of
  2026-08-05, as a separate opt-in file measured in §2.1. It does not count
  against this ceiling and it did not move this artifact by one byte: it reads
  the session's frames off the socket rather than through a seam here.
- **Numeric and `matches` predicate enforcement** on the client, by design —
  see `predicates.manifest.txt`, which lists all 44 predicate terms and which
  of the 14 the decoder checks.

---

## 6. Static checks

| Check | Requirement | Result |
|---|---|---|
| `eval`, `new Function`, `setTimeout("string")`, dynamic `import()` | NFR-4, checklist §7.3 | **0 matches** in `runtime.js`, `codec.gen.js`, `inspector.js`, `dev-reload.js` and **all three** built artifacts |
| Remote fetch, CDN, npm package | NFR-5, NFR-6, checklist §7.4 | **0 matches**; asserted in `client/test/bundle.test.mjs` against the shipped bytes of all three artifacts. Dev reload's own poll is a same-origin `fetch` of a path the SERVER wrote into its tag, so the scan for `https?://` still finds nothing |
| Single file, self-contained | checklist §7.5 | one 10,387-byte IIFE; the bundle contains no `require` and no `import`. The inspector is a second 14,905-byte IIFE with the same property, and the dev-reload client a third at 2,452 B — which imports nothing at all, not even the codec |
| One global | checklist §7.9 | `globalThis.gotthLive`; asserted by loading the shipped bytes and diffing the global object. The inspector adds **none**: it replaces `globalThis.WebSocket` with a wrapper and installs nothing of its own. The dev-reload client adds none either, and on a page with no dev-reload tag it also starts no timer and issues no request — all asserted the same way |
| One DOM-mutation entry point | checklist §4.4, §7.6 | `apply(p)`, which takes the patch. `innerHTML` appears once, in `parse()`. The one carve-out is `setStatus` writing `data-gotth-status` on `<html>`, which RFC §8.2 requires and which touches nothing inside a fragment |
| No inline script, no inline style | checkpoint-1 CP1-13 | the inspector's panel and the dev-reload badge both style through a constructed `CSSStyleSheet` adopted by a shadow root and through `element.style` property writes — both CSSOM, neither governed by `style-src`. A `<style>` element or a `style=` attribute would need `'unsafe-inline'` or a nonce neither file can supply. Scanned over both built artifacts, together with `innerHTML`/`insertAdjacentHTML`: the inspector shows server markup as text, and the dev-reload badge is given no server markup at all |

> **⟨CORRECTED 2026-08-05 — FR54-2, the blocking half.⟩** The §7.5 row read
> *"one **10,391**-byte IIFE"*. That figure was last true at `2ab18690^`: the
> per-binding-options landing took it to 10,306 and `2311280b` took it to
> **10,387**, and this row was edited in neither. It is **inside the ledger NFR-2
> is gated on**, which is why `reviews/fr-54.md` §8 made it the one §9.7 block of
> the six sites it found. Re-measured with `tools/minify -check` at HEAD, not
> copied from the review — which matters, because §8's *"is"* column says
> **10,306** and that is itself one landing stale. The inspector's 14,905 and dev
> reload's 2,452 did not move and are re-confirmed by the same run.

---

## 7. Attribute vocabulary — the contract the server must match

`docs/api-surface.md` §5.2 names the templ helpers but does not fix their
attribute spellings. They are fixed here, in the `data-gotth-*` family the
design documents already use for `data-gotth-preserve` and `data-gotth-status`.
**`Region`, `On`, `OnWith`, `OnAll`, `Preserve` and `DevReloadScript` must emit
exactly these.**

**Every option a binding declares is a component of that binding.** Nothing in
the `Bind` vocabulary is an attribute of the element, and the runtime reads none
of it off the element: `dispatch` matches a spec and then takes the interval,
the rate and the static fields off the spec it matched.

That is a correction, and the sentence it replaces is worth keeping visible
because it was the defect's own argument. This section used to say that
`data-gotth-fields`, `data-gotth-debounce` and `data-gotth-throttle` *"are read
from the ELEMENT and the key filter is read from the BINDING, which is not a
symmetry anyone chose: it is what the two can mean. A debounce is a property of
an element's event stream, and a key is a property of one binding."* **The first
clause of that last sentence is false.** A debounce is a property of one
binding's event stream, and an element carries several — so the guide's own
composer, an `Escape` binding beside a 150 ms `input` binding, gave the `Escape`
an interval it never asked for and one shared `clearTimeout` that **destroyed**
the pending clear when the next character arrived. Driven in Chromium,
`docs/qa/fr-54-debounce-repro.md`, verdict REPRODUCES. §1.1.5 is the fix and its
measurement.

The half that was right is left standing: a key filter must be per binding,
because an `input` event carries no key and a per-element filter would filter the
draft out of existence. That argument turned out to be the whole argument, and it
applied to the three options beside it a release earlier than it was made.

**Components 7 and 8 landed on 2026-08-05 for FR-54 failure 1 and reserve
nothing new.** They are two more `:` fields of a split that was already
happening, each rendered `"1"` or trimmed away, so **every binding this library
rendered before they existed is byte-identical after** — which is the property
the mixed-version window (FR54-1) turns on, since a browser holding a cached
pre-landing runtime reads specs that have not changed. `:` and `;` remain
exactly what a key may not be.

| Helper | Attribute | Value |
|---|---|---|
| `Region(id)` | `data-gotth-region` | the `fragment_id` |
| `On(dom, name)` | `data-gotth-on` | `"<domEvent>:<eventName>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>[:<noModifiers>[:<preventDefault>]]]]]]"`, `;`-separated for several, matched in order with the first match winning. Trailing empty components are trimmed and an empty component means "not set" |
| `OnWith` → `Bind.Keys` | `data-gotth-on`, component 3 | ONE `KeyboardEvent.key` value the event must carry. A list is one binding per key, so no separator is reserved for it and every printable key value — including `,` and `" "` — can be named. `:` and `;` separate the grammar and cannot be keys. An empty third component, and no third component, both mean every key |
| `OnWith` → `Bind.Debounce` | `data-gotth-on`, component 4 | milliseconds, for THIS binding |
| `OnWith` → `Bind.Throttle` | `data-gotth-on`, component 5 | milliseconds, for THIS binding |
| `OnWith` → `Bind.Fields` | `data-gotth-on`, component 6 | URL-encoded `k=v&k2=v2`, for THIS binding. `net/url`'s query encoding escapes `:` and `;` in keys and values alike, so a field cannot split the binding it sits in — asserted in `live/binding_test.go` rather than assumed, because it is the only component whose content is a caller's data |
| `OnWith` → `Bind.NoModifiers` | `data-gotth-on`, component 7 | `"1"` when set, absent when not. The client matches THIS binding only when none of the event's four `*Key` booleans — `shiftKey`, `ctrlKey`, `altKey`, `metaKey` — is held. It is tested whether or not component 3 is set, and a `MouseEvent` carries the same four, so it means a plain click on a click binding. `AltGr` sets `ctrlKey` **and** `altKey`; `CapsLock` and `NumLock` set none of them. A binding it filters out does **not** end the match loop |
| `OnWith` → `Bind.PreventDefault` | `data-gotth-on`, component 8 | `"1"` when set, absent when not. `preventDefault()` on the browser event when THIS binding matches and only then — and **only below the composition guard**: nothing is suppressed while an IME composition is active, because `Enter` commits the candidate there (FR-26). The recognised-submit and anchor-click cases stay **above** the guard and are unchanged |
| `OnAll(…)` | `data-gotth-on` | the bindings joined with `;`, in the order given, each carrying its own options. A binding rendered by `OnAll` is byte-identical to that binding rendered alone |
| `Preserve()` | `data-gotth-preserve` | present, no value |
| — (runtime writes it) | `data-gotth-status` on `<html>` | `connecting` \| `live` \| `reconnecting` \| `closed` |
| `Script()` | `data-gotth-url` on the `<script>` tag | the WebSocket endpoint; the runtime reads it from `document.currentScript` |
| `(*App).DevReloadScript()` | `data-gotth-dev-url` on its own `<script>` tag | the mount, which the dev-reload client joins with `gotth-live-dev-build` to get the path it polls. **Dev only** — with `Config.Dev` false the tag is not written at all |
| `(*App).DevReloadScript()` | `data-gotth-dev-build` on the same tag | the identity of the build that rendered THIS document, and the baseline every poll is compared against. **Dev only** |

`FragmentUpdate.html` means: for `MORPH`, the complete markup of the fragment
root element **including** its `data-gotth-region` attribute; for `APPEND` and
`PREPEND`, child markup to add inside the region.

---

## 8. Tests behind these numbers

| Suite | Where it runs | What it covers |
|---|---|---|
| `internal/clientcodec` (Ginkgo v2 + Gomega) | `go test`, **no node** | generator determinism; a descriptor walk asserting every field of every message reaches the table at the right number and wire kind; manifest completeness; refusal of an unclassifiable predicate; the Go half of the golden round-trip |
| `client/test/codec.test.mjs` | bench image, node | Go→JS decode and JS→Go byte-identical re-encode over 14 generated vectors, unknown-tag skipping, enforced and deliberately-unenforced predicates |
| `client/test/morph.test.mjs` | bench image, node | the traversal and the controlled/uncontrolled rule, against `dom.mjs` — a shim written for this and nothing else. `dom.mjs` models `details.open` as a **reflected** attribute and `checked`/`selected` as live properties, which is the asymmetry the HTML standard has and the one D-15 turned on |
| `client/test/bundle.test.mjs` | bench image, node | the committed `live/clientjs/` artifacts parse and boot: the runtime installs one global, the inspector installs none and wraps only the `gotth-live.v1` socket, the dev-reload client installs none and starts no timer on a page that carries no tag for it; the no-`eval`, no-remote-fetch, strict-CSP and no-`innerHTML` scans over all three, and the runtime asserted to contain neither an inspector seam nor a dev-reload one |
| **manual browser check, 2026-08-05, NOT in CI** | bench image, headless Chromium 151 | the panel end to end: it mounts a shadow root, adopts its constructed stylesheet, paints, wraps only the `gotth-live.v1` socket, and — after one real click on a real session — shows the mount snapshot, the event, the patch joined to it with `CLIENT_EVENT event:counter.increment ← #2`, `origin.event_id`, the morph and apply timings, and the hx-* ownership warning. **It found a defect nothing else could**: `render()` called `requestAnimationFrame` through `(globalThis.rAF || setTimeout)(...)`, which invokes it with no receiver and throws "Illegal invocation" from inside `mount()`, leaving a panel that was mounted, styled and permanently empty. Every node spec still passed. The harness was a throwaway and is **not committed**, so this row is evidence for one tree and not a gate — the standing version belongs in `test/internal/conformance/`, which has the CDP client for it |
| `client/test/dev-reload.test.mjs` | bench image, node | FR-57's decision: the same build is nothing to do, a different one reloads and keeps reloading, a restart into the same build is a reconnect rather than a reload, a 200 that is not a build identity is not evidence about the build, a tag with no baseline records rather than adopts, the four-step poll cadence asserted at misses 1/40/41/100/101, and a refused connection and a 502 both read as "waiting" rather than as an exception into the page |
| **manual browser check, 2026-08-05, NOT in CI** | bench image, headless Chromium 151, Go 1.26.5 | FR-57 end to end against `examples/counter` under `internal/cmd/gotth-live-dev`, driven over CDP with a `window` marker as the reload witness: a templ change to the `<h1>` — outside every live region, so no patch could carry it — reloaded the page in **1,810 ms**; a Go change in **2,715 ms**; a rebuild that changed no bytes restarted the process and reloaded **nothing**, with the marker and a `live` socket intact; and restoring the sources returned the build identity to byte-identically its baseline value. A second run checked the badge, which no node spec can see: absent while healthy, and on the first failed poll mounted with a shadow root, one adopted constructed stylesheet, `position: fixed` and the right text — the inspector's paints-nothing defect (`0c711b70`) is the reason that was checked rather than assumed. Both harnesses were throwaways and are **not committed**, so this row is evidence for one tree and not a gate — the standing version belongs in `test/internal/conformance/`. `docs/guide/dev-reload.md` carries both transcripts |
| `client/test/inspector.test.mjs` | bench image, node | FR-44's chain, folded from frames that were encoded and decoded by the real codec: the event→patch join on `Origin.client_ref`, `contributing_event_ids` resolved where this client was told the mapping and left as bare ids where it was not, telemetry joined by `patch_id`, errors landing on the event whose outcome they are, a reconnect refusing to join across sessions, the bounded ring, and the hx-* ownership audit including the preserved-subtree opt-out |
| `client/test/reconnect.test.mjs` | bench image, node | §8.4's backoff arithmetic against a stubbed `Math.random` and a fake clock, the visibility pause, every close code in and out of the terminal set, and which frames cross a reconnect |
| `client/test/resync.test.mjs` | bench image, node | D-29: a refused resync retried and the page recovered on the same connection with no eviction; the acks a latched client keeps sending and their H-7 legality; the schedule's floor, growth, cap and one-request-per-gap bound; and the two schedules proved not to disturb each other |
| `client/test/supersession.test.mjs` | bench image, node | H-13's client half (REV-INV U-1/U-2): the legal resync range applied and acked, a range that leaves a hole, one that overlaps applied state, one whose end precedes its start, each half-set frame on its own, a range reaching the `Snapshot`'s own sequence, and a `Snapshot` at or behind the sequence the client holds — every one of them closing `4002` without applying and without acking. Eight of the eleven fail with either guard removed, and the three that do not are the positive controls: a legal range, a first `Snapshot` with no range, and the patch path, which discards a stale sequence rather than closing on it |
| `client/test/binding.test.mjs` | bench image, node | FR-54 failure 2, on the reading side: a binding's own debounce in force for that binding and no other, a filtered sibling neither delayed nor cancelled by it, both directions of the interference QA-1 measured, two debounced bindings on one element holding independent timers, a throttled binding not throttling its sibling, each binding sending the fields it declared, and the removed element attributes proved not to be a fallback. Two mutation controls: keying the record by the element again turns the two-timers spec red, and reinstating either element attribute as a fallback turns the removal spec red — one red each, and the spec that goes red is the one written for that half |
| `live/binding_test.go` (Ginkgo v2 + Gomega) | `go test`, **no node** | the same contract on the emitting side: component order, trimming, the key-list expansion carrying the whole `Bind`, the percent-encoding that keeps a field value from splitting its binding, the composed pair from the guide, the two-bindings-disagree case this suite used to assert the opposite of, and `OnAll` output byte-identical to the binding rendered alone |
| `client/test/harness.mjs` | — | not a suite: the fake clock, the fake socket and the document shim the four above share, so they cannot drift into driving four different systems |
| `test/internal/conformance/keybinding_test.go` (Ginkgo v2 + Gomega, `browser`) | bench image, Chromium | the key filter against a real `KeyboardEvent`: the keys a binding does not name raise nothing, two keys on one focused element raise two events, an unfiltered binding still raises one per key, a filter on a click never fires, and a bound key still does what the browser was going to do with it |
| `test/internal/conformance/reflected_attribute_test.go` (Ginkgo v2 + Gomega, `browser`) | bench image, Chromium | the reflected-attribute property over the set: a census of what a real browser writes for every attribute the morph rule reads, held against the tags `syncProps` branches on; what a silent patch does to each reflected attribute, wired and unwired; and `data-gotth-preserve` as the remedy for the unwired ones |

The JavaScript suites are **dev-only and quarantined**: never served, never
bundled, and reachable only from the one image with node in it. The Go suite
validates the same fixture with no node at all, so a clean clone still checks
the codec (G11).

```
# the node suites
docker run --rm -v "$(git rev-parse --show-toplevel):/workspace" \
    -w /workspace/candace/pkg/gotth dis-gotth-live-bench:latest \
    bash -c 'for f in client/test/*.test.mjs; do node --test "$f"; done'

# the browser suites, which are where the key filter and the reflected-attribute
# property live. go test hides a passing package's output, so -v is not optional
# if the report entries are wanted.
docker run --rm -e CHROME_BIN=/usr/bin/chromium \
    -v "$(git rev-parse --show-toplevel):/workspace" \
    -w /workspace/candace/pkg/gotth dis-gotth-live-bench:latest \
    bash -c 'go test -v ./test/internal/conformance/ -count=1 -args -ginkgo.label-filter=browser'
```
