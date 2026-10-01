# G2 re-measurement — raw data

Every run the harness emitted for [`../../../g2-baseline.md` §9](../../../g2-baseline.md),
in the layout `<campaign>-<arm>-obs<on|off>/<step>/`. The file set inside each
run directory is the one the parent [`../README.md`](../README.md) documents,
and `sut-*.log` is excluded for the reason given there.

## Why the ids are `stepNN` and not `rNN`

equivalence-spec §6's audit rule is that this directory holds **every run id the
harness emitted, with no gaps**, so a step number that is absent has to be
accounted for rather than left to look like a naming quirk. One was absent — `c1`
step 06 — and the section after this one accounts for it and publishes it.

This campaign interleaves arms run by run (§9.2's ABBA), and `measure.sh`
derives each run's within-run window order from the run index inside ONE
invocation. Interleaving therefore means one invocation per run, and the harness
emitted `n1000-obs-on-r01` on every one of them. The campaign driver renamed
each to its position in the campaign sequence so that they can live in one
directory without colliding.

Every run's own `run.json` still carries the harness's `run_id`, unaltered.

## Campaign `c1`'s step 06 was measured, was not published, and is now here

**Two claims are corrected here and both are quoted rather than deleted**, because
the second of them is this README's own first attempt at correcting the first.

**The claim as committed on 2026-08-04:**

> **The step numbers within each campaign run `01`…`NN` with no gaps, across the
> arms**: campaign `c1`'s six slots are `cur old old cur cur old`, so
> `c1-cur-obson` holds steps 01, 04, 05 and `c1-old-obson` holds 02, 03, 06. No
> run was started and discarded.

That was **true of the campaign and false of this directory**. `c1-old-obson` as
committed held steps 02 and 03 only. The sentence described a published run that
was not published, which is exactly what equivalence-spec §6's audit rule — this
directory holds every run id the harness emitted, with no gaps — exists to catch.

**The first correction, in commit `aa5e294e`, and it was also wrong:**

> `c1-old-obson` holds steps 02 and 03. **It does not hold step 06, and no
> directory for step 06 was ever written.**

A directory for step 06 *was* written. It was written to the campaign's scratch
area at `2026-08-04T22:53:56+00:00` and never copied into the repository. The
evidence for "never written" was the committed `campaign.log`, which stops in the
middle of step 06's `mn` window — and that log was itself an incomplete copy,
which is the same defect one level down. Checking the campaign's scratch
directory rather than trusting the truncated log is what found the run.

**What actually happened**, from the complete `campaign.log` now committed here:

- `c1`'s driver was given six slots, `cur old old cur cur old`, and **ran all
  six.** Step 06 (`arm=old`) started at `2026-08-04T22:43:13+00:00` and finished
  at `22:53:56`, and the log's final line is `=== campaign c1 complete`.
- The turn that published `c1` copied `campaign.log` **while step 06 was still in
  its `mn` window** — the committed copy was the complete log minus its final
  eight lines — and ended before copying step 06's run directory. So the campaign
  did not die; the publishing did, between two `cp` invocations.
- **No run was started and discarded.** Nothing was measured and then dropped for
  its value.

**What was done about it.** Step 06's run directory is now here, recovered from
the campaign scratch area, and its completeness was checked rather than assumed:
60 samples in each of its two windows, 1000 live sessions with 0 closes, 0 dial
errors and 0 read errors, the same image digest as every other run in this
directory, and the same `old` tree export — which was itself compared byte for
byte against `git archive 70abe339` and matched. `campaign.log` is replaced by
the complete log; the truncated copy was not a different log, it was less of this
one.

**This changes a published figure, and the change is stated rather than absorbed.**
[`../../../g2-baseline.md` §9.4](../../../g2-baseline.md) pooled the `old` arm
over **two** runs because only two had been copied. Three exist. The corrected
three-run figure and everything that follows from it — including the `old`→`cur`
delta §9.4 reports — are in §9.10 of that document. §9.4 is left as it was
written, because it was true of the runs it had.

## The campaigns in this directory

| Campaign | Slots, in order | Arms | Log | Runs |
|---|---|---|---|---|
| `c1` | `cur old old cur cur old` | `cur` = `ce52d2f9`, `old` = `70abe339` | [`campaign.log`](campaign.log) | 6 of 6 — step 06 recovered, above |
| `c2` | `eng cur cur eng` | `eng` = `5a2ca417`, `cur` = `ce52d2f9` | [`campaign-c2.log`](campaign-c2.log) | 4 of 4 — collected 2026-08-04, published later, below |
| `c3` | `head cur cur head head cur head cur head cur` then `head head` (obs **off**) | `head` = `d66e4953`, `cur` = `ce52d2f9` | [`campaign-c3.log`](campaign-c3.log) | 12 of 12 |

`c3`'s step numbers run `01`…`12` with no gaps. Steps `01`–`10` are the
observability-**on** cells (`c3-head-obson`, `c3-cur-obson`, five runs each) and
steps `11`–`12` are the observability-**off** cell at the same tree
(`c3-head-obsoff`), which [`../../../g2-baseline.md` §9.9.4](../../../g2-baseline.md)
had recorded as owed. Slots 07–10 were an extension from three runs to §6's five;
the decision, its reason and a stopping rule fixed at five were appended to
`campaign-c3.log` **before** those runs executed, and that prologue is part of
the committed log.

**`campaign-c3.log` carries a prologue and the log below it is unedited.** The
driver's internal label for its own campaign was `c2`; it is published as `c3`
because a different campaign had already been collected under that name. The
prologue says so; nothing inside the log was changed.

## `c2` was collected on 2026-08-04 and published in a later turn

`c2` is the arm [`../../../g2-baseline.md` §9.6](../../../g2-baseline.md) promised
and did not deliver: *"The `eng` arm measuring it was still collecting when this
section landed. Its cell and its figures are added in the follow-up commit to
this document rather than estimated here."* The follow-up commit did not happen —
the turn ended between collecting and publishing, which is the same death that
left `c1`'s step 06 uncopied. The data was recovered from the campaign scratch
area in a later turn.

**Its provenance was verified rather than asserted**, because data published by
an agent that did not collect it is worth exactly what the checks on it are
worth:

| Check | Result |
|---|---|
| `/tmp/…/eng` tree vs `git archive 5a2ca417` | byte-identical, no differing files |
| `/tmp/…/new` tree vs `git archive ce52d2f9` | byte-identical, no differing files |
| `measure.sh` across both trees and every other tree measured here | one sha256, `6f1155ed…4182c` |
| samples per window, all 8 windows | 60, as §3.6 requires |
| driver counters, all 4 `mn` windows | 1000 live, 0 closed, 0 dial errors, 0 read errors |
| image digest, all 4 runs | `sha256:e146d50d5de6…`, the same one every run in this directory used |

What cannot be recovered is an independent witness to the host during those four
runs. The `run.json` manifests carry host state before and after every window —
uptime, load average, memory, and the count and names of the unrelated containers
— and that is the same evidence every other run in this directory rests on, but
it is what each run says about itself. It is stated here rather than left for a
reader to notice.

## Two manifest fields that read oddly, and are not defects

`git_sha` reads `unknown` and `git_dirty` reads `true` in every manifest here,
because each arm was measured from a `git archive` export outside the worktree —
so that neither another agent's commits nor the measuring agent's own edits
could move a tree mid-campaign — and `measure.sh` asks git about a directory
that is not a repository. The commit each arm is measured from is in §9.2's
table and in `campaign.log`.

## Recomputing

```bash
docker run --rm -v "$PWD/gotth-live:/w" \
    -v "$PWD/gotth-live/docs/bench/data/g2-baseline/remeasure-2026-08-05/c1-cur-obson:/cell" \
    -w /w/test/memory dis-gotth-live:latest \
    bash -c 'go run ./cmd/memstat -cell /cell'
```
