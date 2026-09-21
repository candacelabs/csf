[identifiers genericized for publication - measurements unmodified]

# G11 — consumable from a clean clone, run rather than asserted

**Date:** 2026-08-05
**Tree gated:** `5c751ae9a3e0e1d71615deeb51d7be22e0a90caf` (branch `dev-/gotth-live-orchestrator-c3efc4`)
**Gate box:** PRD G11 — *"`git clone && go run ./examples/<name>` works for all
three examples with no node, npm, protoc, or refinec installed."* Gate: QA-1,
Phase 4. Phase-4 exit box 7.
**Runner:** [`tools/g11/run.sh`](../../tools/g11/run.sh) and
[`tools/g11/inside.sh`](../../tools/g11/inside.sh)
**CI step:** `ci.sh:876`, labelled so that `grep -n "G11" ci.sh` returns it.
**Run by:** DEV-2, on the `node-b` host, outside every project image.

---

## 0. Re-run, 2026-08-27

Everything below is the 2026-08-05 run, unmodified — it is the artifact for the
tree named above and rewriting it would falsify it. Two things about it are no
longer true of this tree, and both are improvements:

- **§1 says the three examples are separate Go modules and that G11's own
  wording is therefore impossible.** The single-module fold retired that: the
  examples are packages of `github.com/candacelabs/candace` at
  `examples/gotth/<name>`, and `go run ./examples/gotth/<name>` now works from
  the export root. The runner measures this rather than asserting it, and
  reports `as worded ... works`.
- **The transcript's `cd examples/<name>` paths moved** to
  `examples/gotth/<name>`.

The gate was re-run twice on 2026-08-27, concurrently, against the tree this
export publishes: once in the monorepo layout and once against a repository
built from the export root's tracked paths, which is the layout a clone of
`candacelabs/candace` has. **Both green, all three examples PASS in each.**
Getting there fixed three defects in the runner itself — a hardcoded monorepo
prefix, a toolchain check that had been reading a `go.mod` that no longer exists
and reporting "clean" from an empty string, and a teardown whose `pkill` pattern
stopped matching when Go 1.26 began running the cached build artifact directly.
The commit message on `7caf3013` has the detail.

---

## 1. Verdict, in the two halves it actually has

**The property G11 is about HOLDS, on all three examples. G11's own sentence
does not, and no work on this tree could make it.** Those are different
findings with different owners and this artifact refuses to average them into
one word.

| | Result |
|---|---|
| **G11's property** — a clean clone plus a stock Go toolchain, no node, no npm, no protoc, no refinec, all three examples serving a live UI | **PASS** — counter, chat and dashboard, each fetched over HTTP and checked for the markup the client morphs against |
| **G11's wording** — `go run ./examples/<name>` | **IMPOSSIBLE**, from either plausible working directory. The three examples are separate Go modules; that package path is not in the library module. §4 |

**So the Phase-4 box is answerable but not tickable as written.** The
measurement the gate report asked for — *"one clean export, one image without
node, three `go run` invocations, and a recorded result"* — is below and it is
green. What is not green is the criterion's own text, and the fix for that is a
one-line edit to `docs/PRD.md` that **DEV-2 did not make**, because PRD.md
belongs to PM-1. §7 F-1 has the exact replacement string.

**What this replaces.** The Phase-4 gate graded G11 *"NOT MET — asserted, never
run"*: `docs/README.md` asserted it, `grep -n "G11" ci.sh` returned nothing, and
no artifact recorded the invocation on a machine without those four tools. All
three of those are now false. The grep returns fifteen lines; this file is the
artifact; and the four tools were proved absent inside the image rather than
assumed. §3.

---

## 2. The exact commands

One command reproduces everything in this document:

```bash
# from anywhere, on a host with a docker daemon — NOT inside dis-gotth-live
bash candace/pkg/gotth/tools/g11/run.sh
```

What it runs, in order, with nothing elided:

```bash
# 1. a real clone, over the pack protocol, at the source's HEAD
git clone --depth 1 "file:///home/dev/worktrees/gotth-live-orchestrator-c3efc4" \
    /tmp/g11-clean-clone-XXXXXX/clone

# 2. a stock upstream Go image, as the invoking uid, no published ports
docker run --rm --network bridge --user 1000:1000 \
    -v /tmp/g11-clean-clone-XXXXXX:/g11 -w /g11 -e HOME=/g11/home \
    golang:1.25-bookworm bash /g11/inside.sh

# 3. inside it — the precondition, then the criterion as worded, then as documented
command -v node npm protoc refinec           # all four empty
cd /g11/clone/gotth-live       && go run ./examples/counter    # G11 as worded
cd /g11/clone                  && go run ./examples/counter    # G11 as worded
cd /g11/clone/gotth-live/examples/counter   && go run .        # as the tree supports
cd /g11/clone/gotth-live/examples/chat      && go run .
cd /g11/clone/gotth-live/examples/dashboard && go run .
curl http://127.0.0.1:8080/                        # and 8081, 8082
curl http://127.0.0.1:8080/live/gotth-live.min.js  # the URL the page itself names
```

---

## 3. The environment, and the proof that it is the right one

| Field | Value |
|---|---|
| Image | `golang:1.25-bookworm` |
| Digest | `golang@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58` |
| Image ID | `sha256:bd70579a624df39bce056a80ccda1b689b89a6e8393ceaef5878b1a087ac7b1b` |
| Image created | `2026-07-14T03:18:20Z` |
| Go | `go version go1.25.12 linux/amd64` |
| OS | Debian GNU/Linux 12 (bookworm) |
| `go.mod` requires | `go 1.25.0` — satisfied, and the runner checks it rather than assuming it |
| docker | 29.6.2 |
| git | 2.43.0 |

### 3.1 The four tools G11 names, proved absent

Printed by the run, inside the image, before anything else happens:

```
--- the four tools G11 names must be absent
absent:  node
absent:  npm
absent:  protoc
absent:  refinec
also, past what G11 names:
  templ          absent
  buf            absent
  protoc-gen-go  absent
  go-bindata     absent
```

The second block is beyond what G11 requires and is here because
`docs/README.md`'s sentence says *"no generator"*: templ is a generator, and
the examples' `view_templ.go` files are committed, so templ being absent while
all three examples build is that claim measured rather than repeated.

**A run in which any of the four is present fails immediately.** The check is
fatal, not advisory, because a gate that does not verify its own precondition
is asserting the thing it was written to measure.

### 3.2 Why the project's own image cannot answer this criterion

The Phase-4 gate's phrasing — *"the three example steps run inside an image
chosen for the toolchain it has, not for the tools it lacks"* — is exactly
right, and here is the measurement behind it:

```
$ dis run bash -c 'for t in node npm protoc refinec templ buf; do ... done; protoc --version; templ version'
node     absent
npm      absent
protoc   /usr/local/bin/protoc
refinec  absent
templ    /go/bin/templ
buf      absent
libprotoc 35.1
v0.3.1020
```

**`dis-gotth-live:latest` ships protoc 35.1 and templ v0.3.1020** — two of the
four tools G11 names, plus the generator. `ci.sh`'s three example steps run
there. They are good steps and they check other things; they cannot check this,
and no relabelling would change that. That is why the G11 step is the one skip
in `ci.sh` that points *out* of the images rather than into a bigger one.

---

## 4. G11 as worded — the discrepancy, measured

G11 names no working directory, so the runner tries both. All six probes, in
the clean container, against the clean clone:

```
from /g11/clone:
  $ go run ./examples/counter    -> exit 1
      go: cannot find main module, but found .git/config in /g11/clone
  $ go run ./examples/chat       -> exit 1   (same)
  $ go run ./examples/dashboard  -> exit 1   (same)

from /g11/clone/gotth-live:
  $ go run ./examples/counter    -> exit 1
      main module (github.com/candacelabs/candace/pkg/gotth) does not
      contain package github.com/candacelabs/candace/examples/gotth/counter
  $ go run ./examples/chat       -> exit 1   (same, for .../examples/chat)
  $ go run ./examples/dashboard  -> exit 1   (same, for .../examples/dashboard)
```

**This is not a bug in the tree and fixing it in the tree would be wrong.** Each
example is a separate module with its own `go.mod` and its own
`replace github.com/candacelabs/candace/pkg/gotth => ../..`, and each of
those `go.mod` files argues for the separation in its own header comment: an
example must not be able to put a dependency into a consumer's module graph,
the example has to *measure* like a consumer for `docs/dependencies.md` §5, and
`internal/arch`'s two-package cap has to stay a statement about the library
rather than one with exceptions in it. A separate module is not a package of its
parent, so `./examples/<name>` is outside the main module by construction.

**The invocation the tree supports, and the one `docs/README.md` documents, is:**

```bash
cd gotth-live/examples/<name> && go run .
```

`ci.sh`'s own example steps already use that form (`ci.sh:295`, `:302`, `:322`),
so nothing else in the gate was relying on the wording being right. **Only the
PRD sentence is wrong.** §7 F-1.

---

## 5. The clone, and why it is a clone

A copy of the working directory would carry the three built example binaries
(16–17 MB each, gitignored), `bench/node_modules`, and anything a previous run
generated — and any one of those could make a failing tree pass. So:

* **`git clone`, not `cp`.** Over `file://` rather than a plain path, which
  forces git to take the same pack protocol a stranger gets over the network
  instead of the local hardlink shortcut. The clone shares no objects with the
  checkout.
* **`--depth 1`,** which is what makes that affordable: this monorepo's object
  store is ~5 GB, and the depth-1 clone is **30 MB**. Depth changes nothing the
  gate looks at, because the gate looks at checked-out files.
* **Asserted pristine before the run**, and the assertion is printed:

  ```
  ==> the clone carries committed files and nothing else
  clean: no untracked, no ignored, no built binaries, and all 7 generated files committed
  ```

  That is `git status --porcelain --ignored` empty, `examples/*/{counter,chat,dashboard}`
  absent, and these seven present — the files that would otherwise need templ or
  protoc, which is the mechanism G11 depends on:
  `examples/{counter,chat,dashboard}/view_templ.go`,
  `internal/protocol/gotthlivepb/frame.pb.go`,
  `internal/protocol/gotthlivepb/frame_refine.pb.go`,
  `internal/protocol/refinepb/refine.pb.go`,
  `live/clientjs/gotth-live.min.js`.
* **Asserted pristine again after the run:** `clean: the run added and changed
  nothing in the clone`. This is the check that says `go run` needed to generate
  nothing, update no `go.sum`, and write nothing into the tree — that "clone and
  run" is the whole procedure and not the procedure plus a step nobody wrote
  down.

### 5.1 What cloning the monorepo implies

`candace/pkg/gotth/` is a subdirectory of the `candace-server` monorepo, so the clone
is of the whole repository: **2,013 files, 1,049 of them under `candace/pkg/gotth/`**,
30 MB. Three consequences, all benign and all recorded rather than assumed:

1. **The `replace ... => ../..` directives resolve.** They point at
   `<clone>/gotth-live`, which a repository clone puts exactly where the example
   expects it. They would resolve identically if `candace/pkg/gotth/` were ever
   extracted into a repository of its own, since `examples/` would still be one
   level below the module root.
2. **The five submodules are empty directories** — `0` entries each, because
   `git clone` does not recurse by default. Their names are withheld: they are
   unrelated sibling repositories of the deployment the monorepo also carries,
   and naming them here would publish that roster for no gain — G11's finding
   is the count and the emptiness, both of which are stated. Nothing under
   `candace/pkg/gotth/` reads any of them, and all three examples built and
   served with them empty. A stranger who cannot fetch those SSH remotes is not
   blocked from the examples.
3. **`research/protobuf-refinement-types/` comes with the clone**, so a reader
   who wants `gen.sh --check` has it. G11 does not need it; nothing in this run
   touched it.

### 5.2 What the run needed that G11 does not mention

**Network.** `go run` downloaded **seven modules** from the module proxy on the
first example — `github.com/a-h/templ`, `github.com/coder/websocket`,
`github.com/cespare/xxhash/v2`, `google.golang.org/protobuf`,
`go.opentelemetry.io/otel`, `.../otel/metric`, `.../otel/trace` — and zero on the
other two, which shared the cache within the run. `HOME` lives inside the
throwaway work directory, so `GOMODCACHE` and `GOCACHE` start **empty on every
run**: this is a from-scratch resolution, not a warm cache.

G11 says nothing about being offline and a stranger cloning from GitHub has a
network, so this is not scored as a failure. It is recorded so that nobody later
reads this gate as an offline claim it never made. **Nothing was vendored and no
`vendor/` directory exists**; an offline variant of this criterion would need
one and would be a different requirement.

---

## 6. The three examples, with the evidence

Each example is run with a bare `go run .` — no flags — so the port is the
example's own documented default, and everything is bound on `127.0.0.1`
*inside* the container. No host port is published.

"It served" is not the assertion. For each example: the page is fetched, its
byte count recorded, the `data-gotth-region` attributes the client morphs
against are counted and their IDs printed, the client runtime is fetched **from
the URL that page itself names**, the process is confirmed still running at the
end, and the port is confirmed to stop answering once it is killed — which is
what says the answers came from the process this gate started.

### counter — PASS

```
$ cd examples/counter && go run .
  served after 7s; 7 modules downloaded from the proxy first
    counter: http://127.0.0.1:8080
    counter: allowed origins [http://127.0.0.1:8080 http://localhost:8080]
  GET /  ->  200, 2032 bytes
  live regions: data-gotth-region="counter.controls" data-gotth-region="counter.value"
  GET /live/gotth-live.min.js  ->  200, 10391 bytes, built by nothing on this machine
  the process was still running when all of that was fetched
  port 8080 stopped answering once the process was killed
```

### chat — PASS

```
$ cd examples/chat && go run .
  served after 2s; 0 modules downloaded from the proxy first
    chat: http://127.0.0.1:8081
    chat: allowed origins [http://127.0.0.1:8081 http://localhost:8081]
    chat: members [alice bob mallory olive trudy]
  $ curl -c jar -L 'http://127.0.0.1:8081/login?user=alice'   (this example signs in first)
  GET /  ->  200, 2603 bytes
  live regions: data-gotth-region="chat.composer" data-gotth-region="chat.log" data-gotth-region="chat.roster"
  GET /chat/live/gotth-live.min.js  ->  200, 10391 bytes, built by nothing on this machine
  the process was still running when all of that was fetched
  port 8081 stopped answering once the process was killed
```

**The sign-in is chat behaving correctly, not a workaround.** `GET /` with no
identity cookie renders the login page — 814 bytes, no live regions, no runtime
tag — and `examples/chat/README.md` says so in the "What to expect" section:
*"Pick somebody to be — the sign-in page lists the cast."* The first version of
this gate fetched `/` and reported chat broken. **The defect was the gate's**,
and it is written down here because a reader automating anything against chat
will hit the same wall.

### dashboard — PASS

```
$ cd examples/dashboard && go run .
  served after 2s; 0 modules downloaded from the proxy first
    dashboard: http://127.0.0.1:8082
    dashboard: allowed origins [http://127.0.0.1:8082 http://localhost:8082]
    dashboard: feed sampling every 50ms over [cpu memory requests]
    dashboard: backpressure metrics at http://127.0.0.1:8082/metrics.txt
    dashboard: htmx 2.0.10 served at /htmx.min.js (51238 bytes, digest verified)
  GET /  ->  200, 4005 bytes
  live regions: data-gotth-region="dashboard.alerts" data-gotth-region="dashboard.controls" data-gotth-region="dashboard.meters"
  GET /dashboard/live/gotth-live.min.js  ->  200, 10391 bytes, built by nothing on this machine
  the process was still running when all of that was fetched
  port 8082 stopped answering once the process was killed
```

### 6.1 The 10,391 bytes are the whole point

All three served **10,391 bytes** of `gotth-live.min.js`, which is
`client/SIZE.md`'s minified figure for the shipped runtime, from three different
mount paths. There is no node in that container. Nothing there could have built
that file, minified it, or fetched it from a CDN. It came out of the Go binary,
which is the committed `live/clientjs/gotth-live.min.js` — **that is the "no npm,
no build step" half of G11 measured rather than asserted.**

The URL is read out of the served page rather than hardcoded, which is also why
this check is worth anything: `live.Script` renders the runtime's `src` under
each application's own mount path, and the three mount at `/live`, `/chat/live`
and `/dashboard/live`. A gate with `/live` written into it reported two of the
three as serving no runtime — that was this gate's first run, and it was wrong.
Reading the URL from the page asserts the stronger property anyway: the URL a
browser would request is the URL that answers.

### 6.2 The gate can go red — a negative control

A check that cannot fail is indistinguishable from one that passes. Run with
`--deadline 1`, giving each example one second where counter needs seven:

```
=== counter ===
  FAIL: nothing served on 127.0.0.1:8080 within 1s. Its output:
    go: downloading github.com/a-h/templ v0.3.1020
    ...
--- in-container verdict
FAILED:
  - examples/counter did not serve within 1s
  - examples/chat did not serve within 1s
  - examples/dashboard did not serve within 1s
```

Runner exit code 1, `ci.sh` step red.

---

## 7. Findings that belong to somebody else

DEV-2 owns `ci.sh`, `tools/g11/**` and this file, and touched nothing else.
Each item below is stated with the exact edit so its owner does not have to
re-derive it.

### F-1 — PRD G11's wording names a command that cannot work. **Owner: PM-1** (scope), QA-1 gates.

`docs/PRD.md` states the criterion in two places, and both say
``git clone && go run ./examples/<name>``:

* the G11 row of the success-criteria table, `docs/PRD.md:203`;
* the Phase-4 exit box in §9, `docs/PRD.md:1911`.

That command fails from every directory (§4), for a reason that is a deliberate
design decision recorded in three `go.mod` headers. **Suggested replacement, in
both places:**

> `git clone && cd gotth-live/examples/<name> && go run .` works for all three
> examples with no node, npm, protoc, or refinec installed

Nothing else changes: the property, the gate owner and the phase are unaffected,
and this artifact is the evidence for the corrected sentence. **Until that edit
lands, the honest state of the box is "the property is measured and green; the
criterion's text is unsatisfiable".**

### F-2 — the Phase-4 gate report's row 7 and §4.7 are now out of date. **Owner: PM-1.**

`docs/gates/phase-4.md:139` grades G11 *"NOT MET — asserted, never run"* on
three specific grounds, and §4.7 repeats them. All three are now false:
`grep -n "G11" ci.sh` returns fifteen lines including a step at `ci.sh:876`;
this artifact records the invocation; and the four tools were proved absent
rather than assumed. §4.7's own prescription — *"one clean export, one image
without node, three `go run` invocations, and a recorded result"* — is what
happened, with a real clone rather than an export.

### F-3 — no CI job runs G11, and `ci.sh` alone cannot fix that. **Owner: whoever owns `.github/workflows/`.**

The library job in `.github/workflows/gotth-live-checks.yml` runs `ci.sh` inside
`docker run`, and that container has no docker socket, so the new step skips
there exactly as it does in a developer's `dis run`. The runner needs the GitHub
runner's *own* docker. **The fix is a workflow step beside `docker build`, not
inside `docker run`:**

```yaml
      - name: G11 — consumable from a clean clone
        run: bash candace/pkg/gotth/tools/g11/run.sh
```

`actions/checkout` must fetch enough history for a `--depth 1` clone of the
checkout to resolve `HEAD`, which its default shallow fetch already does.
**Until that job exists, G11's evidence is this file, not a green badge**, and
`ci.sh`'s header now says so in those words.

### F-4 — `docs/README.md`'s sentence is correct, and is the only correct statement of the command in the tree. **Owner: DEV-3, optional.**

`docs/README.md:69` reads *"`git clone && go run .` in any of them works with no
node, npm, protoc or generator installed: the generated code is committed."*
That is **true**, verified above, and it is the form the PRD should adopt in
F-1. The only nit available is that *"in any of them"* carries the `cd`
implicitly; spelling it as ``cd examples/<name> && go run .`` would make the
sentence copy-pasteable. Not a defect, and DEV-2 did not touch it.

### F-5 — nothing here grades FR-60…FR-63's "polished and documented" clause.

That box is separately open in the Phase-4 report with **DEV-3 to present and
QA-1 to grade**. This gate says the three examples clone, build, start and serve
a live UI. It says nothing about whether they are polished.

---

## 8. What this run does and does not claim

**Does claim,** with the method above: at `5c751ae9`, a `git clone` of this
repository, in `golang:1.25-bookworm` with node, npm, protoc, refinec, templ and
buf all proved absent, builds and serves all three examples; each returns a
document containing the live-region markup; each serves the 10,391-byte client
runtime from the URL its own page names; and running them writes nothing into
the clone.

**Does not claim:**

* that G11's literal command works — it cannot (§4);
* that the examples work *offline* — seven modules are downloaded (§5.2);
* that the examples work in a **browser** — this gate speaks HTTP, not DOM. The
  browser evidence is the conformance suite's `browser`-labelled specs, run in
  `dis-gotth-live-bench:latest`, and is a different gate;
* that the examples are polished or well documented (§7 F-5);
* that CI runs this on every push — it does not yet (§7 F-3);
* anything about the two `docs/guide/` files and one `_samples/` directory a
  concurrent agent had uncommitted in this worktree during the run. The runner
  printed them and excluded them; the gate covers `5c751ae9` and nothing else.

---

## 9. Reproducing this

```bash
# on any host with a docker daemon and a network:
bash candace/pkg/gotth/tools/g11/run.sh              # ~70s, exit 0 expected
bash candace/pkg/gotth/tools/g11/run.sh --keep       # leaves the clone on disk
bash candace/pkg/gotth/tools/g11/run.sh --deadline 1 # the negative control in §6.2
bash candace/pkg/gotth/tools/g11/run.sh --help

# through the gate, which is where it belongs:
bash ci.sh                                    # runs it if docker is reachable,
                                              # announces the skip if it is not
```

Exit codes are a contract with `ci.sh`: **0** the property holds, **1** G11
fails, **2** the gate did not run because a prerequisite it refuses to work
around is missing. `ci.sh` turns 2 into an announced skip and only 1 into a
failure, because "could not run" and "ran and failed" being confusable is the
defect every skip in that file exists to prevent.
