# Checkpoint-3 batch — L9-1 review and rulings

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Reviewed** | the checkpoint-3 batch, pinned at **`af9057d1`** (`c11045a9..af9057d1`) |
| **Ruled on** | **D-31** (filed to me) · PM-1's four rulings · QA-2's re-verification PASS · the transport lifetime change |
| **Ruled against** | [review checklist](../review-checklist.md) §0.4, §6.4, §6.5, §6.8, §8.6, §8.8, §9.1, §9.2, §9.7 · [ADR-001](../adr/001-transport.md) §7 X3 and condition **C-14** · [RFC-0001](../rfc/001-architecture.md) §3.4, §6.2 · [PRD](../PRD.md) FR-53, FR-70, FR-77 · [equivalence-spec](../bench/equivalence-spec.md) E6, §2.5, §2.6 · [api-surface](../api-surface.md) · [dependencies](../dependencies.md) |
| **Prior rulings** | [module-init](module-init.md) (C-1…C-20) · [checkpoint-2 batch](checkpoint-2-batch.md) (C-21…C-27) · [checkpoint-2 round](checkpoint-2-round.md) (C-28…C-32) |
| **Disposition** | **BLOCK.** Three blocking findings, eight recorded. Conditions **C-33…C-43**. |

```
Commits reviewed: 13   Accepted on their merits: 10   Blocked: 3
Rulings: 5 issued, 0 deferred
  1. D-31                     UPHELD  — a protocol-visible client schedule that no
                                        binding document describes. C-41 closes it
  2. PM-1 ruling 1 (case 8)   AFFIRM  — the reasoning survives the §6.1.2 test, and
                                        the test PM-1 applied is now written down
  3. PM-1 rulings 2, 3, 5     AFFIRM  — internally consistent; FR-70 gains C-40
  4. QA-2's PASS              AFFIRM, NARROWED — it was earned at ce52d2f9 and does
                                        not reach the transport change (C-35)
  5. ADR-001 §7 X3 / C-14     BREACHED — 5a2ca417 moved one of X3's four named
                                        lines and neither X3 nor §6.2 moved with it

The gate at af9057d1:  bash ci.sh EXIT 1.
  FAILED: bench/apps/chat/gotth, bench/apps/dashboard/gotth
  — the two steps af9057d1 itself adds, on a checkout the workflow will make.

Conditions opened: C-33…C-43            Blocking the gate: C-33, C-34, C-35
Defects only running could find, this round: 5 (C-33, C-34, C-36, C-37, C-39)
Files I did not touch, by rule: any .go, .js, .templ, .sh; docs/qa/; docs/PRD.md;
                                docs/adr/; docs/bench/; gotth-live/bench/;
                                live/**, internal/**, test/memory/**
```

**Measurement hygiene.** DEV-1 is editing `live/**`, `internal/**` and
`test/memory/**` in this worktree as I write. Following the C-27 precedent,
**every number below was taken against a pristine `git archive af9057d1` export**
in `/tmp/l9-cp3`, and every probe or mutation was written into a *copy* of that
export (`/tmp/l9-probe`, `/tmp/l9-parent`) which was then discarded. Nothing in
the worktree was edited except this file. Where I compare against the parent
commit, that is `9c85fe43` exported to `/tmp/l9-parent` by the same method.

**Not approved here.** The proposed `docs/adr/002-*.md` is not in this batch and
I am **not** approving it, in advance or by implication. Neither the G2 memory
numbers nor `docs/bench/g2-baseline.md` are reviewed here: DEV-1 was
re-measuring while I read, and a review of a moving figure names nothing. Where
a finding below touches the *arithmetic that G2's documents are built on* — C-35
— it is a finding about a **binding condition of ADR-001**, not about the number.

---

## 1. What I verified by running it, not by reading it

Everything ran in `dis-gotth-live:latest` (go 1.26.5) or `dis-gotth-live-bench:latest`
(node v24.19.0), with `bash -c`, never `bash -lc`.

| # | Check | Command | Result |
|---|---|---|---|
| **1** | **The whole gate** | `bash ci.sh`, repository root mounted at the export | **EXIT 1.** `FAILED: bench/apps/chat/gotth (equivalence-spec §2)`, `bench/apps/dashboard/gotth (equivalence-spec §2)` — **the two steps `af9057d1` adds**. See §2 → **C-33** |
| **2** | The rest of the gate | same run | build, vet, gofmt, staticcheck clean. `go test -race -count=1 ./...` green across the module (chaos **94.5 s**, conformance **160.5 s**). Every other module green |
| **3** | **The chaos suite's expensive half, at `af9057d1`** | same run, `GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1 … -timeout 35m` | **green, 420.372 s.** This matters: QA-2 measured at `ce52d2f9`, three commits before the transport change, and the suite is nonetheless green over it. That is a mitigation, not a sign-off — §4.3 |
| **4** | FR-65, FR-7, NFR-2/3 | same run | surface matches the ledger (`live` 49/49, 50/50). Committed generated output **byte-identical** to a fresh generation — `10015779`'s enumeration fix works. Client **10,178 B** minified / **4,360 B** gzip against 12,288 — 64.5 % headroom |
| **5** | **The hijack wrapper is silently conditional** | probe: a ResponseWriter implementing only `Unwrap()` — Go 1.20+'s documented middleware shape — passed through `rightSized`, then through coder/websocket's own `hijacker()` walk | control (direct `http.Hijacker`): **reader 512, writer 1,024**. Behind the Unwrap-only wrapper: **reader 4,096, writer 4,096**. The optimization does not apply, **6,656 B/session**, and nothing logs, counts or fails. → **C-36** |
| **6** | **The fallback is client-controlled** | same probe at pipelined = 0, 1, 512, **513**, 4,000 bytes | 0/1/512 → 512 + 1,024. **513 and 4,000 → 4,096 + 4,096.** A peer decides, by writing one extra byte behind its upgrade, whether the server keeps this memory. → **C-37** |
| **7** | **The priming is complete — I tried to break it and could not** | real `net.Listen` + `http.Server`, raw client writing the upgrade request **and** a masked binary frame in ONE `Write`, at payloads 16 / 400 / 600 / 3,000 B, through `rightSized` | **all four delivered byte-intact.** Both branches exercised (`wrapped` at every size, because ≤512 B *buffered* is not ≤512 B of *payload*). The `Peek`-prime-`Peek` sequence is sound. **Recorded as a claim that survived attack** |
| **8** | **`Sessions()` is not settled when `Dial` returns** | 200 rounds, dial then sample immediately | **50 of 200 (25 %)** at `af9057d1`; **13 of 200 (6.5 %)** at `9c85fe43`. Pre-existing, ~4× wider |
| **9** | **`App.Close` drops established sessions and reports success** | 300 rounds: dial, start a reading goroutine, `Close(3 s)`, then check | **32 of 300 (10.7 %)** at `af9057d1`; **13 of 300 (4.3 %)** at `9c85fe43`. `Close` returned **nil** every time. → **C-34** |
| **10** | **`Fixed1` against a real ECMAScript engine** | node v24.19.0 `Number.prototype.toFixed(1)` vs the committed Go function, 11 values | **4 of 11 disagree, every one of them a negative tie.** `-0.25` → Go `-0.2`, ECMA `-0.3`. Through the fixture's own integers: prev 400 → v 399 renders **`-0.2%`** here and **`-0.3%`** there. → **C-39** |
| **11** | **The `Skip` that never fires** | scratch program: `os.IsNotExist` and `errors.Is(_, fs.ErrNotExist)` over the exact wrap `LoadFixture` builds | `os.IsNotExist(wrapped)` = **false**; `errors.Is(wrapped, fs.ErrNotExist)` = **true**. That one line is the whole of C-33 |
| **12** | **The exported godoc, as a consumer reads it** | `go doc ./live App.Handler`, `go doc ./live App.Close` | `Handler` says nothing about returning at the upgrade, `context.WithoutCancel`, or where middleware now completes. `Close` says *"drains **every** session"*, which check 9 falsifies. → **C-38** |
| **13** | **The read buffer's unmeasured claim** | `BenchmarkL9FrameRead`, 512 B vs 4,096 B buffer, payloads 64/512/4,096/**65,536** (= `MaxInboundFrameBytes`' default), 200k iterations | at the protocol's own cap: **8,346 ns vs 8,318 ns — 0.3 %**. I attempted the falsification and **the claim survives at the bound that matters.** Intermediate rows are producer-paced and prove nothing either way; §5.11 says so |

**Not measured, and why.** I did not run the browser conformance matrix or the
node client suite: nothing in this batch touches `morph`, `Script`'s writer or
the bundle's own suite, and `ci.sh` announces both skips loudly and correctly.
I did not re-run QA-2's Appendix-B measurements — §R5.2 re-took them at
`ce52d2f9` and the host is the same contended one, so a third set of numbers
adds noise, not information. I did not measure G2, by rule. I did not re-derive
D-30's arithmetic: QA-2 holds it with three specs and MR3/MR4/MR5, and I read
the mutation table rather than re-running it.

---

## 2. C-33 — BLOCKING — the gate is red at `af9057d1`, on the two steps `af9057d1` adds

**Severity: CRITICAL. Owner: the orchestrator (`ci.sh`) + BENCH-1 (the app
suites). Checklist Gate 0.4 — an automatic return on its own.**

```
==> bench/apps/chat/gotth (its own module, equivalence-spec §2)
  fixtures ABSENT — the §2.5 digest specs skipped; regenerate with 'npm run fixtures'
Ran 61 of 61 Specs — 60 Passed | 1 Failed | 0 Pending | 0 Skipped
...
==> verdict
FAILED:
  - bench/apps/chat/gotth (equivalence-spec §2)
  - bench/apps/dashboard/gotth (equivalence-spec §2)
EXIT=1
```

**`0 Skipped`.** Five specs fail where the commit message promises they skip —
`chat_test.go:453`, `dashboard_test.go:355`, `:370`, `:387`, `:547`.

### 2.1 The mechanism, in one line

Each guard is

```go
fixture, err := LoadFixture(DefaultFixtureDir)
if os.IsNotExist(err) { Skip("run `npm run fixtures` in bench/ first (§2.5)") }
```

and `LoadFixture` returns `fmt.Errorf("…: %w\n\tgenerate it with …", path, err)`.
**`os.IsNotExist` does not unwrap.** Its own godoc says so: *"This function
predates errors.Is… New code should use errors.Is(err, fs.ErrNotExist)."*
Measured (check 11): `os.IsNotExist(wrapped)` **false**, `errors.Is(wrapped,
fs.ErrNotExist)` **true**. The guard has never fired for either app.

### 2.2 Why this is not a local artifact

`bench/fixtures/*/ticks.jsonl` is gitignored by design — `bench/.gitignore:12`,
and `af9057d1` tracks only `generate.mjs` and the two `.sha256` files.
`.github/workflows/gotth-live-checks.yml`'s library job checks out fresh, builds
`dis-gotth-live:latest`, and runs `bash ci.sh` **with no fixture step**. So the
workflow makes exactly the checkout I made, and the gate goes red on main the
moment this lands.

### 2.3 What makes it a block rather than a bug

Three things, and the third is the one I want on the record.

1. **The counter passes**, because it has no fixture-reading spec. The
   three-app loop was generalised from a green sample of one, and the two apps
   that actually exercise the mechanism were never run through it.
2. **The gate prints a false statement.** `fixtures ABSENT — the §2.5 digest
   specs skipped` is computed from the presence of the files, not from what the
   run did, and it is wrong. `ci.sh`'s own header says *"A check that cannot fail
   is indistinguishable from a check that passes, and the failure is always
   silent."* This is that sentence in a mirror: a check that announces a skip it
   did not take. The step added to make a skip visible reports the wrong outcome
   in the first environment it meets.
3. **One of the five is not a §2.5 digest spec at all.**
   `dashboard_test.go:542` — *"stays inside §2.4's element and SVG bounds at the
   real shapes"* — reads the fixture for an element-bound assertion. Even with
   the predicate fixed, the note's scope ("the §2.5 digest specs") is narrower
   than the set of specs that need the fixture. Fix the predicate **and** the
   sentence.

**Falsifier.** Replace `os.IsNotExist(err)` with `errors.Is(err, fs.ErrNotExist)`
in all six guards; `bash ci.sh` at the repository root in `dis-gotth-live:latest`
exits **0**, the three bench steps report `clean`, and the two suites report a
non-zero `Skipped` count. Until that run exists and is quoted, checkpoint 3 may
not claim the bench applications are gated.

---

## 3. C-34 — BLOCKING — `App.Close` reports a successful drain over sessions it never touched

**Severity: HIGH. Owner: DEV-1. Checklist §6.8 (deregistered exactly once,
closed with a protocol close frame), §8.6 (no QA-2 sign-off on this path).**

`docs/api-surface.md` row: *"Drains sessions, closing each with `GOING_AWAY`."*
`go doc`: *"Close drains **every** session."* Measured (check 9): **32 of 300
rounds** at `af9057d1`, `Close` returned `nil`, `Sessions()` was still 1, and the
client's socket was still live 500 ms later. At `9c85fe43`: 13 of 300.

The window is `admit()` → … → `websocket.Accept` → `register()`. `admit` is the
only thing that consults `h.draining`, and `Close` snapshots `h.sessions`; a
session admitted before the flag and registered after the snapshot is invisible
to both. It is **pre-existing** — I measured it at the parent and say so — and
`5a2ca417` widens it ~2.5× by moving `register` onto a goroutine `ServeHTTP` does
not wait for. What makes it blocking now rather than then is that the same commit
moves the session onto a goroutine with **no `WaitGroup` and no waiter**, so the
window is no longer bounded by anything a caller can observe: after `Close`
returns, a session may still be *starting*.

I am not prescribing the fix, but the cheap one is visible: `Accept` succeeds on
the `ServeHTTP` goroutine, and registration could happen there, before the `go`.

**Falsifier.** A spec that dials, calls `Close`, and asserts the client observes a
close status — run 300 times under `-race`, zero escapes; and `Sessions() == 0`
when `Close` returns nil. My probe is the shape; it belongs in `internal/wsx`,
not in my file area.

---

## 4. The transport change (`5a2ca417`, `fb0b21c5`) — judged

This is the most consequential change in the batch and most of it is right. I
want the good parts named before the findings, because the findings are about
its edges and not its centre.

### 4.1 What is right, and was checked rather than believed

**The pipelining question is answered, and the answer holds under attack.** I
went after the one thing that could have made this silently lossy — a client
that writes its first frame in the same TCP segment as its upgrade request — and
built the case end to end over a real socket at four payload sizes through a real
`net/http` hijack (check 7). Every frame arrived intact, on both branches. The
`Peek(buffered)` → copy → `MultiReader` → `Peek(buffered)` sequence is correct,
and the reason it is correct is written down at the line that does it. The
existing spec that replays coder/websocket's `accept()` verbatim is the right
shape for that property and I want it kept exactly as it is.

**The 4,096 B fallback is the right instinct.** *"Correctness is never traded for
the memory"* is enforced by code, not asserted.

**The close path is now safer than it was.** The teardown and the panic guard in
one defer, registered before anything that can panic, is the correct ordering: a
`time.NewTicker` on a bad interval used to panic inside `ServeHTTP`, be caught by
net/http, and leak a hijacked socket that nothing would ever close. It now closes
`4012`. `deregister` moving ahead of `register` is safe — `delete` on a missing
key is a no-op and IDs are 16 crypto-random bytes — and I checked it rather than
assuming it.

**The write-buffer trade is priced, stated, and stated where a maintainer meets
it.** `BenchmarkFrameWrite` is a real measurement of the real cost, the band is
identified from bufio's own branch structure rather than guessed, and the number
lives in `hijack.go`'s const block, which is the file a future maintainer opens
when they wonder why 1,024. Checklist §8.8 is met for the writer.

**`WriteHeaderNow` is forwarded**, and the reason — gin records a status until
something flushes it, and gin's own `Hijack` does not — is the kind of detail
that is found by running `test/routers` and not by reading. I verified against
`coder/websocket@v1.8.15/hijack.go:22-33` that `hijacker()` does test
`http.Hijacker` before `Unwrap`, so the interception is legitimate exactly as
claimed.

### 4.2 C-35 — BLOCKING — ADR-001 §7's X3 and condition C-14

**Severity: HIGH. Owner: DEV-1 (the arithmetic) + L9-1 (I hold the ADR).**

ADR-001 §7:

> **X3** — Transport's share of idle memory ≤ **16,384 B/connection**… **Maps to
> exactly four lines of RFC-0001 §6.2**: WebSocket read buffer (4,096) +
> WebSocket conn struct (2,000) + the conn read-pump goroutine stack (8,192) +
> its runtime `g` (≈500) = **14,788 B estimated, 9.7 % headroom**

and condition **C-14**, which I wrote and which is stated in the ADR as binding:

> 1. **If any of the four lines moves, X3 and §6.2 change in the same PR.**
> 2. **X3 never becomes the looser of the two.** … a transport ceiling with slack
>    in it stops constraining the transport.
> 3. **A change to X3 quotes the arithmetic**, not just the new figure.

`readBufferBytes = 512` moves the first of the four named lines, by 3,584 B.
Neither X3 nor RFC §6.2's table moved in `5a2ca417`, and **neither ADR-001 nor
C-14 is named anywhere in the commit.** The commit does flag documents as owed —
RFC-0001 §2's lifecycle table and §3.4's note — and it named the smaller half.
§2 and §3.4 are prose. X3 is a **gate**, and C-14 exists precisely so that the
number cannot drift away from the lines it is derived from.

The consequence is not cosmetic. On the new arithmetic the four lines are
512 + 2,000 + 8,192 + 500 = **11,204 B**, and a 16,384 B ceiling over 11,204 B
carries **46 % slack** where the condition's whole point is 9.7 %. C-14(2) says
X3 ratchets rather than being left generous. It has not.

**This is a finding against the documents, not a licence to amend them.** I am
not amending ADR-001 here and I am not setting a new X3 from the estimate: the
ratchet in C-14(2) and §6.1.2 is keyed to a *measurement*, and DEV-1's
re-measurement is in flight. What is owed at the gate is the arithmetic restated
with its four current lines and X3 re-derived from them, in the PR that carries
the measurement, with L9-1 approval — which is C-14(1) and (3), unchanged.

**Falsifier.** ADR-001 §7's X3 row reads `512 + 2,000 + 8,192 + 500 = 11,204`,
RFC §6.2.2's "WebSocket read buffer" row reads 512, both cite `5a2ca417`, and the
ceiling is either re-derived or explicitly deferred to the measurement with a
dated owner. Until then, `grep -n '4,096' docs/adr/001-transport.md` returns a
line that is false of the code.

### 4.3 C-36 — the memory win is silently conditional on a shape the library does not control

**Severity: MEDIUM. Owner: DEV-1.**

`rightSized` declines unless `w` implements `http.Hijacker` **directly**:

```go
if _, ok := w.(http.Hijacker); !ok { return w }
```

Since Go 1.20 the documented way to write ResponseWriter middleware is to
implement `Unwrap() http.ResponseWriter` and let `http.ResponseController` find
capabilities — such wrappers routinely do **not** implement `Hijack`. Behind one,
`rightSized` hands back the original, coder/websocket's `hijacker()` walks past it
to net/http's own, and the session pays 4,096 + 4,096 for its life. Measured
(check 5): **reader 4,096, writer 4,096**, versus 512/1,024 on the control.
**6,656 B/session**, lost silently: no log, no metric, no failing spec, and no
line in the G2 method saying which shape the number was taken under.

The wrapper already knows how to walk `Unwrap` — it *implements* `Unwrap` for
exactly that reason. It does not *use* it. That asymmetry is the finding.

**Falsifier.** A spec that mounts the handler behind an `Unwrap`-only
ResponseWriter and asserts the transport received `readBufferBytes` /
`writeBufferBytes`; and one sentence in `hijack.go` and in the G2 method naming
the ResponseWriter shape the figure assumes. Either fix it or state it — what is
not acceptable is a per-session number whose validity depends on an integrator's
middleware and says so nowhere.

### 4.4 C-37 — a peer decides whether the server keeps the memory

**Severity: MEDIUM. Owner: DEV-1 + QA-2.**

Measured (check 6): pipelining **513 bytes** behind the upgrade request puts the
session back on net/http's 4,096 B pair. This is *correct* — it is the fallback
working — but it means the saving is not a property of the server. A client that
writes one extra byte, or N clients that do, restore the status quo ante at
~6.6 KB/session, and neither the metric set nor the G2 report has any way to say
so afterwards.

This is not a DoS: the ceiling is the *old* behaviour, so nothing grows past
where it already was, and §5.7's "bounded before allocation" is satisfied. It is
an honesty problem about a published number. QA-2 owns the half that matters —
whether the G2 workload's client pipelines, and whether the report states the
adversarial figure beside the benign one.

**Falsifier.** The G2 method states the per-session bytes under both a benign and
a pipelining client, or states that the measured client never pipelines and how
that was checked (a `Buffered()` histogram at hijack time would do it).

### 4.5 C-38 — the exported contract moved and the exported godoc did not

**Severity: MEDIUM. Owner: DEV-1. Checklist §9.2, §9.7.**

`fb0b21c5` is good work and it documents the change in `internal/wsx/doc.go`.
`internal/` is invisible to `go doc` for a consumer. Run as a consumer runs it
(check 12), `App.Handler()` — the **one exported entry point this change is
about** — says nothing about returning at the upgrade, nothing about
`context.WithoutCancel`, and nothing about middleware now completing at the
upgrade rather than at the end of the session. §9.2 asks for the *concurrency
contract* on every exported symbol; this is the concurrency contract, and it
moved.

`App.Close`'s godoc is worse than silent: it is false (C-34).

`docs/api-surface.md`'s two rows carry the same gap, and the FR-65 gate cannot
see it — it counts identifiers, and no identifier changed. That is exactly the
class the standing instruction points at.

**Falsifier.** `go doc ./live App.Handler` mentions the early return and the
context; `go doc ./live App.Close` states what it does *not* wait for; the two
`api-surface.md` rows say the same, with `5a2ca417` cited in §10's changelog.

### 4.6 C-35(b) — RFC §3.4 now contains a sentence that is false

**Severity: MEDIUM. Owner: DEV-1 (source) + L9-1 (RFC). Filed inside C-35's
document sweep rather than as a separate number, because it closes with the same
edit pass.**

RFC-0001 §3.4:

> Effect goroutines are transient, spawned through one `spawn` helper that
> installs the panic guard, the metric, and the `WaitGroup` registration —
> **there is no bare `go func()` in the library.**

`internal/wsx/handler.go:224` is one. `grep -rn 'go func' live/ internal/` over
non-test files returns five sites; four are accounted for (`session/effects.go`'s
`spawn`, `session/actor.go`'s bounded waiter, `wsx/conn.go`'s actor pump under
`actorDone`, `wsx/handler.go:320`'s `Close` fan-out under `closing`). The fifth
has no `WaitGroup`, no metric, and its panic guard is inside the function it
calls rather than at the spawn. §3.4's other claim — *"both owned and both
**waited for at shutdown**"* — is falsified by C-34's 32/300.

Checklist §6.4 calls a fire-and-forget `go func()` a block outright. I am not
returning the commit on §6.4 alone, because the goroutine *does* have a named
owner and a defined stop condition and two thirds of §6.4 is met; the third —
"a place that waits for it" — is C-34, and that is where I am blocking.

### 4.7 The read buffer's claim, attacked and surviving — NO CONDITION

Recorded as a **non-finding**, because I went looking for one. `hijack.go`
argues the 512 B reader from bufio's source and measures only the writer, while
`MaxInboundFrameBytes` defaults to **65,536** — 128× the buffer — so "every
inbound frame in this protocol is small" is a claim about traffic, not about the
configured bound. I benchmarked it (check 13). At 65,536 B: **8,346 ns vs
8,318 ns**, 0.3 %. The claim holds at the bound that matters. The intermediate
rows in my run are producer-paced and I draw nothing from them. §8.8 is satisfied
in substance; a one-line note in `hijack.go` saying the reader was checked at the
cap and not only argued would close the paperwork, and is a nit.

---

## 5. Rulings

### 5.1 D-31 — UPHELD. Owner: L9-1 + PM-1, from DEV-2's source. → C-41

QA-2 filed this to me and PM-1, and asked for a ruling. **It is a real finding and
I am upholding it at LOW, with the closing condition sharpened.**

The question I was asked is whether a protocol-visible client retry schedule that
no design document describes violates this project's binding-document discipline.
It does, and the discipline is not abstract here — the project has already paid
for it once. D-29 *was* a defect of exactly this shape: RFC §7.6 said a refused
resync costs "no render" and stopped, the client was built to that, and the
consequence (a latched client that stops acking, fills the window and is rescued
by the slow-client eviction ~30 s later) was invisible in both documents and
present in the running system. `c3a91af8` fixed the client. §7.6 still describes
the pre-fix behaviour, so **the document that produced D-29 is still capable of
producing D-29**, in the hands of a second client implementer or a second server.

Two aggravations QA-2 did not press and I will:

1. The schedule is not merely undocumented, it is **contradicted**. §8.4
   documents full jitter as *the* client schedule. There are now two,
   deliberately different, and the difference is load-bearing (equal jitter's
   floor is the point — a delay near zero is the request the server just
   declined). A reader who follows §8.4 builds a client that spends its refusals.
2. `client/runtime.js` itself says *"Closing that would be a wire change; it is
   filed, not smuggled in here."* That sentence is correct and it is the right
   call. It also means the source knows the wire cannot carry the retry-after,
   and the schedule is therefore a **guess about a server-side default** — which
   is precisely the kind of coupling a protocol document exists to record.

**C-41, and it is mine to discharge.** RFC-0001 §7.6 gains a paragraph stating
the client's response to `Error{RATE_LIMITED}` on a `ResyncRequest`: one request
in flight per gap, equal jitter over `bound = min(15 s, 1000 ms · 2ⁿ)`, the
schedule terminating at the server's own `3 × ResyncBurst` close, and the fact
that the base is a **guess at the default** because the wire carries no
retry-after. §8.4 gains a sentence saying there are two schedules and why they
differ. It stays LOW; it does not block; and it is owed before `protocol.md` is
called complete, not before this gate.

**Falsifier.** A second client implemented from §7.6 and §8.4 alone re-arms after
a refusal and does not re-create D-29. Cheaper proxy: `grep -c 'equal jitter'
docs/rfc/001-architecture.md` returns non-zero.

### 5.2 PM-1's ruling 1 (case 8 struck) — AFFIRM, and the test is now written down

**The reasoning survives the §6.1.2 test, and I want to say exactly why, because
this is the shape of method error this project forbids elsewhere and PM-1 did not
name it.**

The project's own rule, applied by PM-1 himself two rulings later, is §6.1.2's:
a target does not move to fit a measurement, **and specifically not in the same
pass that measured the miss**. Ruling 1 strikes a Phase 3 criterion in the same
pass in which QA-2 measured that the library violates it. On its face that is the
forbidden move.

It survives, on one ground and only one: **the argument that decided it is
independent of the measurement.** PM-1's reasoning is about *who can emit a
duplicate* — the client has no queue, no pending buffer and no resend, so a second
identical frame is always sender-originated; a user who clicked twice issued two
intents, and an attacker who can replay can equally send two different frames and
mint their own nonce. That argument would hold identically if the library **had**
deduplicated; it does not depend on which way the measurement came out. §6.1.2's
prohibition is aimed at outcome-shopping — choosing after seeing a number — and an
argument that is invariant to the number is not outcome-shopping. I verified the
factual premise rather than taking it: `client/runtime.js` has one `send()`, it
returns false unless `readyState === 1` and `sid` is set, and nothing in the
reconnect path or the D-29 re-arm buffers or replays an `Event`. The premise is
true.

Two things make me comfortable, and they are PM-1's credit: the criterion was
**inverted, not deleted** — case 8 still gates, on a positive requirement the
existing spec already asserts — and the cost was priced rather than waved
(double-charge on a double-click; the one-intent-two-executions case at-most-once
does not solve).

**C-42, non-blocking, PM-1.** The distinguishing test above goes into the PRD §9
preamble in as many words: *a criterion may be struck after a measurement only
when the argument for striking it is invariant to that measurement's outcome, and
the ruling must say so against §6.1.2 by name.* Ruling 1 passes that test; it
should not be cited as precedent without it, because the next ruling of this shape
may not.

**FR-77 is a real requirement, not a promise to write documentation.** I checked
clause (a)'s falsifier rather than taking it: `test/internal/chaos/case8_replay_test.go`
asserts `state_version` advances between the two patches and that the effect ran
twice under one ledger key, and QA-2's MR-series reddens it. Adding deduplication
goes red. Clauses (b) and (c) are documentation obligations at `Phase: 4`,
`Gate: QA-1` — which is this PRD's ordinary shape for a docs deliverable, not a
softer one. Affirmed.

### 5.3 PM-1's rulings 2 and 3 — AFFIRM. Ruling 3 gains C-40

**Ruling 2 (Q-BENCH-1/2 fenced).** Correct, and the fencing is the part worth
keeping: `OPERATOR-QUESTIONS.md`'s one contract is that Q-1…Q-7 are questions only
the operator can settle, and appending two bench defaults unfenced would have
diluted it. The finding inside the ruling — that ratifying Q-E costs Q-BENCH-1 its
stated reason, leaving an E1 conformance question against a frozen §2.1 — is
routed to QA-2 correctly and is not mine to resolve.

**Ruling 3 (Q-E ratified, FR-70 amended).** Ratification is right and the reason
given is the strongest one available: E1/E2 are *unsatisfiable* against
`examples/`, so refusing Q-E would not produce a fairer benchmark, it would
produce one that cannot satisfy its own equivalence rules. The replacement
obligation — consumer-reachable API only, no `internal/` import, no build tag, no
undocumented configuration, bench-driven choices declared in the method — is
checkable where the phrase it replaces was not.

**C-40, non-blocking, BENCH-1 + QA-2.** FR-70's new obligation is checkable and
nothing checks it. `grep -rn '"github.com/candacelabs/candace/pkg/gotth/internal'
bench/apps/*/gotth/` and a build-tag scan are two lines in `ci.sh`, in the step
`af9057d1` just added. An obligation asserted in a requirement and enforced by
nobody is the failure mode this repository has now caught five times.

### 5.4 PM-1's ruling 5 (FR-53 missed at 46) — AFFIRM, warmly

This is the ruling that earns the batch its credibility. The alternative — Go
only, 27, green — was available, defensible in a sentence, and PM-1 rejected it
on the ground that matters: a budget met by moving code across a file boundary
measures file layout, and a 27 published against a JSX count that includes markup
would be FR-73's strawman aimed at ourselves. Recording a miss rather than
defining it away, and pre-registering that raising 30 is unavailable *in the same
pass that measured the miss*, is §6.1.2's discipline applied without being asked.

The sub-finding is the useful one and I want it carried: twelve of the 27 Go lines
are the eight `Config` fields `live.New` requires, so most of the overage is an
**API** finding. That is DEV-1's, and it should be worked before anyone argues
about 30.

### 5.5 QA-2's PASS — AFFIRM, and NARROWED. → C-35's second half

**The PASS is earned for what it covers.** I checked the two closures rather than
taking them. D-23: the ranges are compared verbatim in three places, the class is
closed by a reflection property over every configuration `New` accepts, and the
adversarial follow-up was looked for on the axis the closure does *not* cover —
which is how D-30 was found, from the value D-23's own error message recommends.
That is the right way to verify a fix and I want it repeated. D-29: the re-arm
reaches the wire and the server serves it, 2.462 s and 2.515 s against a pre-fix
control still green in the same run at 6.002 s and a 4009. The mutation table
(MR1–MR5) reddens every added spec, and MR3 — the mutation that *fixes* D-30 —
reddens the two specs that record it, which is the property that makes a
defect-recording spec worth having.

**The correction on D-25 is the best paragraph in the report.** Discovering that
one of your own six "held by assertions" claims is held by an `AddReportEntry`,
that the number did not reproduce, and then *declining to add the assertion*
because `len(x) > 0` on a timing race is this project's own recurring defect class
wearing the opposite mask — that is a QA agent correcting itself against its own
interest. Affirmed in full, including the refusal.

**Now the narrowing, and it is not a criticism of QA-2.** The re-verification was
taken against a clean export of **`ce52d2f9`**, correctly and for stated reasons.
`5a2ca417` — the connection-lifetime change, the goroutine that now runs the
session, the panic-recovery ownership, the teardown reordering and both transport
buffers — landed **three commits later**, and the report was committed
(`9892c991`) after it without naming it. §R8 accounts for "the two intervening
commits" (`9d3029ab`, `b1641f4e`); the transport change is not among them because
it did not exist yet.

So: **QA-2's PASS does not reach the transport change, and there is no QA-2
sign-off on it.** Checklist §8.6 requires one for resilience changes and this is
the resilience surface — case 3 (restart under load), case 4 (slow client, and
the heap figure is measured over buffers this commit resizes), case 6 (partition
and reclamation), case 7 (10k churn, goroutine count). C-34 is a defect on
precisely that surface and no suite in this repository catches it.

**The mitigation, measured and stated.** I ran the full chaos suite with
`GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1` at `af9057d1` (check 3): **green, 420.372 s**.
So the transport change does not break the checkpoint-3 gate's eight cases. That
is worth a great deal and it is why C-35's second half is a condition and not a
second block: **QA-2 re-runs its verification at the batch head and states which
of §R8's rows the transport change could move.** Until that exists, checkpoint 3
may claim QA-2 cleared Phase 3's eight cases *at `ce52d2f9`*, and may not claim
QA-2 cleared the transport.

---

## 6. The bench batch (`9c85fe43`, `0c969145`, `036bca7b`, `10015779`, `b1641f4e`)

### 6.1 Construction only — verified, and it is true

I checked for Phase-5 contamination directly rather than accepting the claim.
`grep -rn 'time.Now|time.Since|Nanoseconds()|Milliseconds()' bench/apps/*/gotth/`
over non-test files returns fixture replay scheduling, an application `Age`
feature that is part of §2.1's own table, and `bench.go`'s `/api/bench/clock`.
That last one is the only candidate and it is not a measurement: it **publishes**
the server's wall clock, its monotonic reading since process start, the tick and
the fixture digest, so that the harness's single `estimateClockSkew()` — which
§4 forbids to branch per stack — can run against both. It computes no latency,
aggregates nothing, and stores nothing. **Not one timing is taken in a bench app.**
Confirmed.

The `encoding/json` in `bench.go` is on an ordinary HTTP control route, not on the
live wire, so checklist §3.2 and ADR X4 are untouched.

### 6.2 C-39 — `Fixed1` is wrong on every negative tie, and its spec locks the error in

**Severity: MEDIUM (HIGH for the equivalence claim). Owner: BENCH-1.**

The function exists for exactly the right reason, and the tie-detection is
genuinely elegant: an exactly-representable one-decimal tie is an odd multiple of
¼, so `4x` is an odd integer, `×4` is exact, and the ordinary path costs one
comparison. I checked that derivation and it is correct.

The transcription of the rounding is not. ECMA-262 21.1.3.3 extracts the sign
**first** — step 9: *if x < 0, set s to "-" and x to -x* — and only then picks the
larger n. So "larger n" operates on |x|, which makes `toFixed` round **half away
from zero**, not half toward +∞. Measured against node v24.19.0 (check 10):

| x | `Fixed1` | `toFixed(1)` | |
|---:|---|---|---|
| 0.25 | `0.3` | `0.3` | ok |
| **−0.25** | **`-0.2`** | **`-0.3`** | **mismatch** |
| −0.75 | `-0.7` | `-0.8` | mismatch |
| −1.75 | `-1.7` | `-1.8` | mismatch |
| −2.25 | `-2.2` | `-2.3` | mismatch |

Reachable through the fixture's own integers, and it is the exact mirror of the
case the doc comment cites as the reason the function exists: prev 400 → v **401**
is +0.25 % and both stacks render `+0.3%`; prev 400 → v **399** is −0.25 % and
this side renders **`-0.2%`** where the other renders **`-0.3%`**.

**The part that makes this more than a bug.** The committed spec asserts the wrong
value — `Entry("a negative tie takes the larger n too", -0.25, "-0.2")` — and the
prose comment states the wrong rule ("toward +∞ and not away from zero"). So
fixing `Fixed1` turns its own conformance table red, and the defect is held in
place by the artifact that exists to prevent it. This is a §2.5 conformance
claim, checked by comparing rendered DOM under E1, and it would surface at
Phase 5 as an equivalence failure that reads like a fixture problem — which the
comment predicts, in the sentence directly above the error.

**Falsifier.** `Fixed1(-0.25) == "-0.3"` and `Fixed1(-1.75) == "-1.8"`; the
`DescribeTable`'s entries are regenerated from `node -e '…toFixed(1)'` in
`dis-gotth-live-bench:latest` and the command is quoted in the file, so the
oracle is the engine and not a second transcription. The rest of the function —
tie detection, integer rendering, the `-0.0` argument — needs no change.

### 6.3 C-43 — declared asymmetries live outside the register E6 names

**Severity: LOW. Owner: BENCH-1 + QA-2 (spec owner) + L9-1 (§12 approval).**

E6: *"**Any place the two differ** appears in §2.6's asymmetry register with a
reason, and is either excluded from the measured surface or measured as its own
labelled row."* §2.6 is a **closed list**; additions go through §12.

`bench/README.md`'s G-series is excellent work and I do not want it diluted. Two
of its entries are asymmetries, not construction notes:

- **G-3** — *"Every session folds its own copy of the shared data… where the
  Next.js stores keep one array… **This is a real per-session memory cost that D3
  will measure**."* That is a difference between the implementations that is
  expressly **not** excluded from the measured surface. E6's remedy for that case
  is "measured as its own labelled row", and only §2.6 can create one.
- **G-5** — region E's panel is keyed by page-load cookie, so *"two tabs of this
  app in one browser share region E's refresh counter where two Next.js tabs do
  not."* AS-3 covers region E's *mechanism* and says **"Same visible behaviour"**.
  G-5 qualifies that sentence from outside the frozen register.

Both are cheap to close **now** and expensive later — E6's own text: *"An
undeclared asymmetry discovered after measurement invalidates the affected
dimension and forces a re-run under §12."* Nothing has been measured
(`bench/data/` contains no run ids), so the amendment log's "measurement taken
under old text?" column reads **no**, which is the only condition under which a
definition may safely move.

**A second thing, recorded because it will matter at the gate.** The brief I
work from calls the equivalence spec FROZEN. Its own header says *"Status: Draft
— Phase 0 exit artifact, **pending L9-1 + PM-1 sign-off**"*, and §12 makes freeze
conditional on L9-1 + PM-1 + QA-2 sign-off. I have not signed it off in any
committed document and I am not doing so here. Checkpoint 3 may say §2 is treated
as frozen in practice; it may not say the spec is frozen under §12.

**Falsifier.** §2.6 carries AS-8 (per-session state duplication, gotth-live side,
its own labelled D3 row) and either an AS-9 or a qualification on AS-3's "same
visible behaviour"; §12's amendment log records both with L9-1 approval and
"measurement taken under old text? no".

### 6.4 C-44 — the three bench Go modules are not in `dependencies.md`

**Severity: LOW. Owner: DEV-1 (`dependencies.md`).**

The root `go.mod` is **unchanged** across the whole batch — I checked
(`git diff c11045a9..af9057d1 -- gotth-live/go.mod` is empty). The library gains
no dependency, which is the right answer.

But `af9057d1` adds three Go modules to the gate, each with its own `go.mod`
requiring `templ`, Ginkgo, Gomega and the library through a `replace`.
`dependencies.md` §2.1 enumerates the separate-module pattern as *"`test/routers`
(§2.2), `test/sampling` (§2.3) and `test/memory`"* — three, where there are now
six — and `grep -n 'bench/apps' docs/dependencies.md` returns nothing. §5.2's
`bench/` row covers node, npm and the Next.js lockfile, not Go modules.

This is not pedantry: §9's own erratum records that §2.3's "Where" column *"was
wrong within hours of being written"* for exactly this reason — two modules landed
concurrently and neither author read the other's `go.mod`. Three more just landed.

**Falsifier.** `dependencies.md` §2 names `bench/apps/{counter,chat,dashboard}/gotth`
with their direct requires and the FR-74 reason, and §2.1's enumeration reads six.

### 6.5 The rest, accepted

**`10015779`** is right and its reasoning is the reason: the two `.templ` files
are **listed, not excluded**, and the commit says in as many words that excluding
them would have made the gate green by shrinking what it looks at. FR-7 is green
at `af9057d1` (check 4) — *"the committed output is byte-identical to a fresh
generation"*. Worth recording as a process note rather than a finding: the walk's
completeness check held `gen.sh --check` red across `58b3dcc4..036bca7b`, so FR-7
was un-gated for that window. The guard worked exactly as designed and nothing
claimed a green FR-7 while it was red — I checked the intervening commit messages.

**`0c969145`** is a good small change: two real failure paths added, four inline
constructions folded into one helper whose comment explains why `live.Anonymous`
is the right identity here. `nit:` its footer reads `Co-Authored-By: Claude Fable 5`
where every other commit in the batch reads `Claude Opus 5`. Trivial, and the
only reason I mention it is that the log style is a stated convention.

**`b1641f4e`** is gofmt on one line. Fast-pass eligible on all four boxes.

**`9c85fe43`** — the dashboard itself — is a large and careful piece of work
(979 lines of `dashboard.go`, 343 of `view.templ`, five regions, six controls,
region E genuinely on plain HTMX with nothing on that path touching a session).
Its declared frictions are honest to the point of being uncomfortable, which is
what they are for. Accepted, subject to C-39 and C-43.

---

## 7. `af9057d1` — the orchestrator's own commit, reviewed like anyone else's

The intent is right and the diagnosis is right: three modules under FR-74's
quarantine were invisible to every step in the file, `./...` included, and their
suites were green in the sense that they had never run. Putting them in the gate
is correct. The comment block is the best explanation of *why* in the file.

It is also **the finding**. The step was added, the message asserts a skip
behaviour that has never worked, and the run that would have shown it — the one I
made, which is the one the workflow makes — was not taken. C-33.

Two smaller notes, non-blocking:

- The step is **construction only**, and the commit says so, and it is true (§6.1).
  Good.
- `bench_fixture_note` is computed from `[ -f … ]` and then printed as a claim
  about what the suite *did*. Even with C-33's predicate fixed, that is a
  prediction dressed as an observation. Ginkgo prints its own skip count; the
  step should print the regeneration hint and let the suite report its own
  outcome, which is the same rule D-19 established for `gofmt`.

---

## 8. `docs/api-surface.md` and `docs/dependencies.md` at `af9057d1`

The mechanical gate is green: `live` **49/49** identifiers, **50/50** fields;
`live/livetest` 4/10 and 0/6, under the ceiling as C-21 provides. No exported
identifier moved in this batch, which is again the right answer for a batch whose
content is a memory change, a document sweep and three bench apps.

What the gate cannot see, and what I found:

| | |
|---|---|
| `(*App[S]).Handler()` | its **meaning** changed — the handler now returns at the upgrade — and neither the godoc nor the row says so. **C-38** |
| `(*App[S]).Close()` | its documented behaviour is **false** 10.7 % of the time. **C-34** |
| `dependencies.md` §2.1 | enumerates three separate modules where there are six. **C-44** |

`dependencies.md` is otherwise true: no new module reaches a consumer, the root
`go.mod` is byte-identical to `c11045a9`, and the bench quarantine holds.

---

## 9. Verdict

```
Verdict: BLOCK

Sections walked: 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11
  N/A: none — §2 and §7 reached through the bench apps and the client schedule
Diff size (counted): ~3,100 lines across 13 commits. Prior design notes exist for
  the transport change (RFC §6.1.2/§6.2.3) and the bench apps (equivalence-spec §2)
Client runtime: 4,360 B gz (budget 12,288), 64.5 % headroom   delta vs main: 0
QA-1: n/a this batch      QA-2: PASS at ce52d2f9 — does NOT cover 5a2ca417 (C-35)

Blocking:
  - §0.4      — bash ci.sh exits 1 at af9057d1, on the two steps af9057d1 adds (C-33)
  - §6.8/§9.7 — App.Close reports a successful drain over sessions it never
                touched: 32/300 measured, and its godoc says "every" (C-34)
  - ADR-001 C-14 — 5a2ca417 moved one of X3's four named lines; neither X3 nor
                RFC §6.2 moved with it, and X3 did not ratchet (C-35)

Non-blocking: C-36…C-44, and one nit (a commit footer).
```

### What checkpoint 3 MAY claim on my authority

- The eight PRD Phase 3 chaos cases are **MET**, and I re-ran the full suite with
  soak and measurements on at `af9057d1`: **40 of 40, 420.372 s, green under
  `-race`.** QA-2's closures of D-23 and D-29 are real and I checked them.
- **FR-7 is green** and the generator enumeration is complete: the committed
  output is byte-identical to a fresh generation, with the two new `.templ`
  files listed rather than excluded.
- **FR-65 is green** and the batch spends **zero** exported identifiers.
- **NFR-2/NFR-3**: 4,360 B gzipped, 64.5 % headroom, measured today.
- **The transport's pipelining behaviour is correct.** I attacked it end to end
  over a real socket and could not lose a byte. This is the strongest single
  claim the transport change has, and it is earned.
- PM-1's four rulings are **internally consistent with the documents they did not
  change**, and ruling 1 survives the §6.1.2 method test for a stated reason.
- **FR-53 is MISSED at 46 against 30**, recorded rather than defined away.
- The bench applications take **no measurements**; construction only, verified.

### What checkpoint 3 MAY NOT claim on my authority

- **That the gate is green.** It is red, at the pinned commit, on the two steps
  the pinning commit adds. Nothing in this checkpoint may be reported as
  CI-verified until C-33's run exits 0 and is quoted.
- **That the three benchmark applications are gated.** They are invoked; two of
  them fail; the mechanism that was supposed to make that acceptable has never
  worked.
- **That QA-2 cleared the transport change.** QA-2 cleared `ce52d2f9`. The
  connection lifetime, the session goroutine, the panic-recovery ownership and
  both transport buffers changed afterwards, with no QA-2 sign-off (§8.6).
- **That `App.Close` drains every session**, or that `docs/api-surface.md`'s row
  for it is true.
- **Any per-session byte figure from the transport change**, until C-35's
  arithmetic is restated and C-36 says which ResponseWriter shape the figure
  assumes. The commit is right to claim no byte count; the *documents* must not
  claim one either, and ADR-001 §7 currently claims 4,096.
- **That `docs/bench/equivalence-spec.md` is frozen under §12.** Its own status
  is "Draft — pending L9-1 + PM-1 sign-off", and I have not signed it.
- **That the §2.5 conformance of the dashboard's rendered numbers holds.** It
  does not, on every negative tie, and the spec asserts the wrong value.

### Unblocking is cheap

C-33 is one predicate in six places. C-34 is a registration that could happen on
the goroutine that already owns the socket. C-35 is arithmetic plus L9-1
approval, and I am here. None of the three is a redesign, and the batch under
them is good work — the pipelining proof, D-23's closure-then-attack, the D-25
self-correction, and PM-1's refusal to round 46 down to 27 are all things I want
repeated. Re-request review with the three green and I expect to approve.

---

*Reviewed at `af9057d1`. Probes and mutations ran in `/tmp/l9-cp3`, `/tmp/l9-probe`
and `/tmp/l9-parent`; `git status --porcelain` over my file area was empty before
this file and holds only this file after it.*

---

## 10. Re-review — 2026-08-05, at `73f5bf2f`

*My BLOCK's closing line was "Re-request review with the three green and I expect
to approve." This is that re-review. It is written against the tree, against
QA-2's `docs/qa/checkpoint-3-chaos.md` §R12–§R18, against PM-1's closure ledger,
and against my own rulings of an hour ago — which I re-checked like anyone
else's.*

### 10.0 Verdict

```
Verdict: APPROVE

Sections walked: 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11
  N/A: none
Tree: 73f5bf2f. The LIBRARY is byte-identical to 1864cf92, the tree QA-2 graded:
  git diff 1864cf92 HEAD -- live internal client proto  ->  EMPTY (checked)
  The only code change in range is QA-2's own spec file (34945818), whose suite
  QA-2 re-ran green at 42/42 (§R12.4).
Client runtime: 4,429 B gz (budget 12,288), 64.0 % headroom — read from
  client/SIZE.md's gate row, not re-measured by me
QA-1: n/a this batch   QA-2: PASS at 1864cf92, covering 5a2ca417 and everything
  after it (§R12–§R18). C-35(c) DISCHARGED.

Blocking: none. All three of my BLOCK's items are discharged:
  - C-33 — bash ci.sh EXIT 0 at 99b769be on a fixture-less export, quoted in
           PM-1's ledger §9 by the orchestrator who ran it
  - C-34 — App.Close: registration is under Close's own lock, deregister
           precedes close(c.done); read in the tree, and re-run by QA-2 at HEAD
           (internal/wsx 38/38, cases 3 and 7 green)
  - C-35 — (a) ruled at ead612c5, X3 = 13,759 B; (b) RFC §3.4 corrected from the
           tree; (c) discharged by QA-2 here

New conditions on this gate: NONE. Five conditions filed yesterday (C-45…C-49)
  and every open engineering row are CARRIED FORWARD with owners — §10.5 says
  which, so that a row that is not a condition stops being re-litigated.
Binding instrument: §10.4's MAY-NOT list, which replaces §9's.
```

**Approving is the easy half. §10.4 is the half that binds**, and it is longer
than my BLOCK's was — not because the batch got worse, but because three
campaigns of measurement and one honest QA-2 pass have produced more claims that
are nearly true.

### 10.1 What I checked myself, rather than accepted

QA-2 filed a defect against their own suite for a spec that could not fail. That
is exactly the moment to audit the falsifiers rather than admire the candour.

| Check | Result |
|---|---|
| Is the library I am approving the library QA-2 graded? | **Yes.** `git diff 1864cf92 HEAD -- live internal client proto` is **empty**. The only code in range is `test/internal/chaos/case8_replay_test.go` |
| Is D-32's vacuity claim true, or is it self-flattering? | **True, and visible in the diff without running anything.** The old spec's entire assertion was `Consistently(w.isClosed, …).Should(BeFalse())` under a title about dropping a report. A spec whose only instrument is "the connection is still up" cannot distinguish *used* from *dropped* |
| Are the new H-11 arms wired to instruments that can go red? | **Yes, and they are the shipped ones.** `live.Config.Metrics` is a `metric.MeterProvider`, so `obstest.NewMetrics()` records through the real `internal/obs` instruments. `Actor.telemetry` has exactly two exits — `slotFor` misses ⇒ `ClientTelemetryDropped(…, "unknown_patch")` and return; `slotFor` hits ⇒ `ClientTiming` ⇒ `gotthlive_client_morph_duration_seconds`. `used()` and `dropped()` are those two branches and nothing else |
| Do the three H-11 arms have genuinely separate falsifiers? | **Yes**, traced through the code: MH2 (ack evicts) kills arm 1 only; MH4 (`slotFor` always resolves) kills arm 2 only; MH3 (`retentionSlots → 1<<30`) kills arm 3 only. No arm shares a falsifier with another, which is the property that makes three arms three checks |
| Is the §R8 row that moved attributed or argued? | **Attributed — but the mutation is stronger than its label says. §10.3** |
| Are the three declared coverage gaps real? | **All three verified. §10.2** |
| Case 8's spec count | **5 `It`s**, matching "Ran 5 of 42" for the focused runs; the H-11 rework is one `It` with three arms, so 42 stays 42 |

**What I did not do: I ran nothing.** A `bash ci.sh` gate run is executing
against `73f5bf2f` as I write, and a second container of mine on the same host
would contend with a suite whose chaos specs carry timing bounds — I would be
risking a false red in somebody else's evidence to re-derive a number I can get
from the code. Every finding in §10.3 is from reading the tree, QA-2's published
numbers, and their mutation script. **Where a conclusion needs execution to be
certain, I say so and name the run.**

### 10.2 The three declared gaps — all three are real, one needs sharpening

An honestly-stated gap is worth more than a green cell, and these are stated. I
checked each against the tree:

1. **BR-8's process-limit half is genuinely unexercised here.** `live/config.go`
   defaults `MaxSessions: 0`, `handler.go:306` guards on `MaxSessions > 0`, and
   the only chaos configuration sets `MaxSessionsPerIdentity: 200` and nothing
   else (`test/internal/chaos/cmd/chaossrv/main.go:166`). So the guard never
   opens. QA-2 points at `internal/wsx`'s own spec, and it exists and is the
   right shape — *"admits exactly one of many concurrent upgrades against a
   process limit of one"* (`wsx_test.go:277`), deliberately concurrent because a
   serial one passes against the defect — and they ran that package. **Correctly
   scoped, correctly delegated.**
2. **D-4's rejection-reason ordering is genuinely unpinned.**
   `checkFieldInvariants` is one walk over `fields`, returning `ReasonListBound`
   or `ReasonEnumDomain` at the first violation, so field order now decides which
   label a doubly-invalid frame carries. `grep` over the chaos suite finds no
   assertion on a rejection reason. **Accurate, and the right home named.**
3. **BR-3's `Commit`/`Discard` branch — accurate in substance, imprecise in
   wording, and the precision matters for whoever reads it next.** QA-2 says the
   suite "does not reach the changed branch". It *does* reach it: `send` returns
   `ok=false` on the fatal transport path too, and `emitPatch`'s `if !ok` runs
   `Discard`/`noteStale`/`redefer` for a cut socket like any other failure. What
   the suite cannot reach is the branch's **observable consequence** — on the
   fatal path the session closes, so there is no next render to be emitted rather
   than suppressed. The gap is real; the sentence should say *unobservable here*
   rather than *unreached*. **Observation for QA-2, not a condition.**

### 10.3 The falsifier audit — one finding, and it strengthens the attribution

**MH1's mutation is bigger than the table says it is, and the difference is the
whole of BR-9 rather than half of it.** §R16 describes MH1 as *"BR-9's
`applied = max(applied, a.lastSnapshotSeq)` floor removed"*. `mutate.py`'s MH1 —
which is still on disk under `/tmp/qa2-mut3/`, and I read it — substitutes:

```go
_ = a.lastSnapshotSeq
applied = m.lastAppliedSeq        // <- this line is what does the work
```

That second line **discards the acked floor as well**, resetting `applied` to the
raw client cursor. It removes the clamp entire, which is what the script's own
comment says (*"BR-9's clamp removed"*) and what §R13.5's prose says
(*"the clamp lifts `applied` to the acknowledged high-water mark"*). Only the
§R16 row label reads as though the snapshot floor were the operative half.

**This does not weaken the attribution — it is what makes it work**, and the
arithmetic says why. At the first budget-allowed replay the mount `Snapshot` is
the last snapshot emitted and three patches followed it, so
`lastSnapshotSeq < serverSeq` strictly. The snapshot floor therefore *cannot* be
what lifts `applied` to `serverSeq`; only `win.ackedSeq()` can. The measured
result — **0 snapshots** at HEAD — is the acked floor's, and MH1's **3** is what
comes back when both floors go. Both numbers are exactly what the code predicts.

**Two consequences, and the second is a gap nobody has stated:**

- §R16's MH1 row should name the substitution, not one line of it. **QA-2's
  document, one sentence.**
- **No mutation in §R16 isolates BR-9's `lastSnapshotSeq` floor, and by the
  arithmetic above, one that did would not redden this spec** — the acked floor
  alone already produces 0. So this suite carries a falsifier for BR-9's clamp
  *as a whole* and none for the half that `d3c06eb7`'s own commit message says
  the acked floor is insufficient without: the retry-that-outruns-an-acknowledgement
  interleaving. That interleaving is where the snapshot floor earns its place,
  and it is not this suite's row — it is `internal/session`'s. **Carried forward
  to DEV-1 + QA-2 as an observation with its exact falsifier: a mutation removing
  only `applied = max(applied, a.lastSnapshotSeq)`, against a spec that replays a
  latched cursor after a refusal and before the acknowledgement of the answering
  snapshot.** Not a condition on this gate: it falsifies a fix that is already
  correct in the tree and reasoned in place, and no measurement here depends on
  it.

**D-32 itself I accept in full, and the grading is right.** MEDIUM rather than
LOW is correct: H-11 is a *defence*, BR-1 deliberately widened what the ring
resolves, and this was precisely the spec that was supposed to say the widening
did not spend the defence. The sixth instance of this class is a pattern, and
QA-2 naming it as the sixth — after C-21, D-19, D-20, C-33's skip and the
`Fixed1` table — is the right way to carry it.

**One overstatement, immaterial to the spec.** The H-14 comment says one fixed
cursor can produce at most one Snapshot *"no matter how much budget it is
given"*. The short circuit compares `applied >= a.serverSeq`, so the bound holds
while the server emits nothing new; a concurrent emission the client has not yet
acknowledged would make a second reachable. In this spec nothing emits during the
burst — `commit()` is the only patch source and the ticker is opt-in through
`chaos.ticks` — so the assertion is sound and not flaky. Worth a clause so that
nobody generalises it into a protocol claim.

### 10.4 §9's "MAY NOT claim" list, answered at HEAD

**This list, not §10.0's verdict, is what constrains the gate report.** PM-1's §8
is its mirror and quotes it.

| §9 said checkpoint 3 MAY NOT claim | At `73f5bf2f` |
|---|---|
| **That the gate is green** | **LIFTED, with its anchor named.** `bash ci.sh` **EXIT 0** at `99b769be` against a `git archive` export with `bench/fixtures/*/ticks.jsonl` absent — C-33(a)'s falsifier exactly — plus the two steps the library image cannot run, quoted separately (PM-1 ledger §9; the orchestrator ran it and I did not). **What may NOT be claimed is a green whole-gate run at HEAD**: one spec file has changed since, the run against `73f5bf2f` is in flight, and it is the orchestrator's to quote, not mine and not PM-1's-by-inference |
| **That the three benchmark applications are gated** | **LIFTED.** C-33(b) at `99b769be` makes each bench step report the skips the run had: counter 0 of 49, chat 1 of 62, dashboard 4 of 88, per Ginkgo's own report. The mechanism that never worked now works and is observable |
| **That QA-2 cleared the transport change** | **LIFTED — this is C-35(c), and it is the item this re-review exists for.** QA-2's PASS at `1864cf92` covers `5a2ca417`, C-34, BR-1…BR-9, U-5, U-6, D-4 and the `livetest` extraction, names which of §R8's rows each item could move, and reports the one that did |
| **That `App.Close` drains every session**, or that api-surface's row is true | **LIFTED.** The ordering is in the tree — `register` takes the lock `Close` takes and refuses while draining; `deregister` precedes `close(c.done)`; `Close` waits `c.done` bounded by the caller's context — and `docs/api-surface.md:78` now states it citing `ed9f73b6`/C-34. Residual, trivial and named by PM-1: api-surface §10's changelog does not cite `5a2ca417` (DEV-1) |
| **Any per-session byte figure from the transport change** | **LIFTED for the arithmetic, and replaced by a narrower prohibition.** C-35's arithmetic is restated and **ruled**: X3 = 13,759 B (ADR-001 §7.2), and C-36 names the ResponseWriter shape the figure assumes. What replaces it: **no X3 figure may be quoted as measured** — its largest line (the read-pump stack, 8,192 B) is an estimate bounded above, not measured (C-45) |
| **That `equivalence-spec.md` is frozen under §12** | **STANDS.** Its status line still reads *"Draft — Phase 0 exit artifact, pending L9-1 + PM-1 sign-off"* while four documents call §2 frozen. **My half is given in §10.6**; the line moves when PM-1 co-signs and DEV-1 (file owner) applies it |
| **That the dashboard's §2.5 rendered-number conformance holds** | **LIFTED.** C-39 is closed and I read it: `Fixed1` takes the sign off first and renders tenths from an integer, so `-0.25 → "-0.3"`, away from zero as ECMA-262 21.1.3.3 step 6 requires. PM-1 reports byte-identical output against node v24 over all 79,400 deltas the fixture's domain produces, 146 of them negative ties on which the old code disagreed |

**And what is newly not claimable, at HEAD:**

| New | Why |
|---|---|
| **That G2 is met** | The shipping tree measures **45,768.7 B against a 46,080 B gate** — under by 0.68 %, against a 5.5 % cell spread, with **2 of 5 runs over**, and 7.2 % between-campaign drift on unchanged code. §3.6's **10-real-tab driver-validation gate has been run by none of the four campaigns**, so in §3.6's own words every 1k figure is "an assertion about a synthetic client, not about sessions". E1's second falsifier (N=100 sub-linearity) has been unmeasured since §4.3. **The tree is *at* the gate, not clear of it** |
| **That `docs/instrumentation.md` describes the metric set the code exports** | `:133` and `:853` assert the fragment label's *"field, attribute, branch and label row deleted together"* and the field, attribute and branch are still in `internal/obs/metrics.go` (PM-1 §7.3 / REV-DEL 3, DEV-1) |
| **That the dashboard example's resync cost is measured** | `examples/dashboard/README.md`'s figure is byte-identical at `ce52d2f9` and HEAD while `resync.go` was rewritten by `c1338120` *because BR-9 made its old request unanswerable* — the published number was produced by a request shape the fixed harness no longer sends (QA-2 §R17, DEV-3) |
| **That QA3-3's provenance-log throughput is a point figure** | It moved ~2× on the *same tree* under load, measured ABBA against `ce52d2f9` to show the host did it. It is a range with its load attached, ≈1.5–3 cores at D3's N=1000 (QA-2 §R14.3, PM-1/I6) |
| **That BR-9's `lastSnapshotSeq` floor is covered by a falsifier** | §10.3. The suite falsifies the clamp as a whole; nothing isolates that half |

### 10.5 Conditions on this gate vs carried forward — the split PM-1 asked for

**Conditions on this gate: none.** Everything below is carried forward with an
owner. **A row in the second table is not a condition and should stop being
re-litigated as one.**

| Carried forward | Owner | Severity | Why it is not a condition |
|---|---|---|---|
| **C-45** read-pump stack via `memsrv -probe` | DEV-1 | LOW | X3 is adopted with the line bounded above from the settled campaign; the value only tightens it further |
| **C-46** the per-connection `context.WithCancel` line | DEV-1 | LOW | Charging the transport all ≈1,200 B still leaves 13,708 ≤ 13,759 |
| **C-47** the observability-off cell at five runs | DEV-1 | LOW | The budget is labelled provisional wherever it is quoted, and ratchets when the cell lands |
| **C-48** the retained-state composition row in RFC §6.2 | DEV-1 | LOW | The gate sub-line landed; this is the second, smaller half |
| **C-49** `spawn`'s godoc, which claims what RFC §3.4 used to | DEV-1 | LOW | One comment; the RFC half is corrected |
| **C-40** FR-70's consumer-reachable-API check in `ci.sh` | BENCH-1 + QA-2 | MEDIUM | Two greps; it constrains the *next* bench change, not this batch's claims |
| **C-42** ruling 1's distinguishing test into PRD §9 | PM-1 | LOW | An amendment-log edit that belongs with the gate report — where PM-1 is going next |
| **C-43** AS-8 and AS-3's qualification in the closed §2.6 register | BENCH-1 + QA-2 + L9-1 (§12) | LOW | `bench/data/` has no run ids, so nothing was measured under the old text. **My §12 approval is given in §10.6** — it should ride with the status-line fix |
| **equivalence-spec §7.1 freeze inconsistency** | DEV-1 (file) + PM-1 (co-sign) | MEDIUM | One status line. It gates a *claim*, not the code — §10.4 holds the claim |
| **REV-DEL 3** (the fragment field/attribute/branch deletion) | DEV-1 | MEDIUM | Its document half already landed, which is why §10.4 forbids the claim |
| **REV-DEL 6, 11, 12; U-7; U-8; D-3's `test/routers`** | DEV-1 / the stream owners | LOW–MEDIUM | Engineering rows with reproductions, none changing exported surface or contradicting an approved document |
| **D-22, D-24, D-26, D-27, D-28** | DEV-1 (D-26 also PM-1/L9-1) | MEDIUM–LOW | QA-2 reproduced each at HEAD and grades none a condition; I agree. D-26 is the one I would schedule first: an eviction that cannot fire against a client that acknowledges is a *policy* that does not exist |
| **D-25** | PM-1 + DEV-1 | LOW | Its evidence is a printed number whose value is a race — 14 this run, 13 at §4.1, 0 at §R5.1 — which is §R5.1's point |
| **QA-2 → DEV-3**: the dashboard resync figure | DEV-3 | MEDIUM | §10.4 forbids quoting it, which is the containment |
| **QA-2 → PM-1/I6**: QA3-3 as a range | PM-1 | LOW | Same |
| **QA-2 → DEV-1**: D-4's rejection-reason label | DEV-1 | LOW | `internal/protocol/outbound_test.go` is where it belongs |
| **§10.3**: MH1's row label, and BR-9's snapshot floor unfalsified | QA-2 (label) + DEV-1/QA-2 (the mutation) | LOW | The fix is correct in the tree and reasoned in place; the missing falsifier is a test-coverage debt, not a defect |
| **api-surface §10 changelog does not cite `5a2ca417`** | DEV-1 | NIT | One line |
| **`ci-intermittents.md`'s stale size reconciliation** | DEV-2 | NIT | The gate row itself is met with 7 KB in hand |

### 10.6 Two signatures, so that I stop being the thing they wait on

**equivalence-spec §12 / C-43.** I approve, as §12 requires, the amendment adding
**AS-8** for the two `bench/README.md` G-series entries and qualifying **AS-3**'s
"same visible behaviour". The window argument holds: `bench/data/` contains no run
ids, so the amendment log's *"measurement taken under old text?"* column reads
**no**, and this is the last moment that is true.

**equivalence-spec status line.** **L9-1 signs off**, on the ground that matters
for a freeze: §3.6 has been run unmodified by four measurement campaigns with one
`measure.sh` sha256 across every tree, and §5.6's headline rule has been upheld
three times *against* the person quoting it, including twice by me this week. My
fairness veto is not exercised. **This does not move the line by itself** — PM-1's
co-signature is the other half and the file is DEV-1's — and until it moves,
§10.4's prohibition stands.

### 10.7 What checkpoint 3 MAY claim on my authority, restated at HEAD

- **The eight PRD Phase 3 chaos cases are MET at `1864cf92`**, re-run by QA-2 with
  soak and measurement on: **42 of 42 in 425.840 s under `-race`**, plus 4/4
  unraced-and-pinned, plus `internal/wsx` 38/38 with D-10 green on both the heap
  and the RSS budget. Their run, their host, labelled contended per §3.6 — quoted
  as theirs, not re-derived by me.
- **The gate is green at `99b769be`**, on a fixture-less export, with the two
  unrunnable steps quoted separately.
- **The transport change is cleared end to end**: C-35(a) ruled, C-35(b)
  corrected, C-35(c) discharged, C-36 and C-37 closed against their falsifiers.
- **X3 = 13,759 B/connection**, derived from five named §6.2 lines with the
  arithmetic quoted, adopted rather than proposed.
- **ADR-002 is accepted with conditions**, and default-on observability now has a
  budget line — 4,050 B/session, inside the 46,080 B gate and never carved out of
  it.
- **One §R8 row moved and it was attributed by mutation rather than argued**, and
  the one spec that could not fail was found by its own author, published with
  the two-greens repro, and fixed with three separate falsifiers.
- **This batch spends zero exported identifiers**, and FR-7, FR-65, NFR-2 and
  NFR-3 are green.

**What I want repeated**, and it is the same list as last time with one addition:
the pipelining proof, D-23's closure-then-attack, PM-1's refusal to round 46 down
to 27 — and now **QA-2 running the same unmodified spec against a library and its
exact inverse, getting two greens, and reporting that as a defect of their own.**
That is the single most valuable thing in this batch, and it is worth more than
the row it found.

---

*Re-reviewed at `73f5bf2f`. I ran nothing: a gate run was executing on this host
and the findings above are from the tree, from QA-2's published numbers, and from
`/tmp/qa2-mut3/mutate.py`. `git status --porcelain` over my file area holds only
this file.*

### 10.8 Two rows closed while §10.5 was being written, and one consequence

*Appended after the fact rather than folded into §10.5's table, for the reason
§10.3 and the strike in [rulings-review-wave §8.6](rulings-review-wave.md) give:
a row that was true when written and false forty minutes later is this project's
most-caught defect, and the visible correction is cheaper than the invisible one.*

- **C-40 is CLOSED** at `597902f7`, and I read the step rather than the subject.
  It computes each bench module's imports from **its own import lists** and
  deliberately not from `go list -deps` — which would report the library's
  `internal/` packages, imported by the library rather than by the bench app, and
  would have failed on every tree that has ever existed. That is the trap this
  condition was one line away from, and the step avoids it and says why. It also
  names the **two of FR-70's four clauses it does not check** — unexported hooks
  and undocumented configuration, neither greppable — and leaves them with QA-2 at
  Phase 5. A step that covers half and says which half is the right answer.
  §10.5's row for C-40 is superseded; the owners were BENCH-1 + QA-2 and the work
  is done.
- **The `ci-intermittents.md` size NIT is CLOSED** at `99b3a7df`, and the fix is
  better than the row I filed: the two figures were never the same measurement —
  10,178 B is the **minified** artifact and 64.5 % is `1 − 4360/12288`, the
  **gzip** figure's headroom — so the document had merged two of the tool's own
  columns into one clause. The gate passed on either reading, which is precisely
  why the sentence survived. §10.5's row is superseded.

**One consequence for §10.4, and it tightens rather than loosens.** `597902f7`
changes **`ci.sh` itself**. So the whole-gate run in flight against `73f5bf2f`
does not cover HEAD's gate script either — not just HEAD's spec file. **The
prohibition stands with a second reason behind it**: no whole-gate green may be
claimed at HEAD until a run against a tree containing this step is quoted by the
owner who ran it. The first thing that run has to show is the new step's own
output, `clean: 3 modules, no internal/ import and no build tag`.

**Neither closure changes the verdict**, which was APPROVE with no conditions and
is unaffected by two rows moving from "carried forward" to "done".
