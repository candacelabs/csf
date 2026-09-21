# gotth-live comparison bench

> # ⚠ NO MEASUREMENT HAS BEEN TAKEN, AND NONE MAY BE TAKEN FROM HERE
>
> **Every command in this file that would produce a number refuses to run.**
> The harness will not record a cell until four gates have actually passed
> (`harness/gate.mjs`), and one of them cannot pass yet: Phase 3's tuning is
> unfinished. `coalesce_flush_at`, `MinResyncInterval`/`ResyncBurst` and the
> provenance-log volume are still **safety-chosen defaults, never measured**
> (equivalence-spec Appendix B, QA3-1/2/3), and **two of the three move numbers
> this spec publishes**. A re-tune landing after Phase 5 measurement has begun
> forces full re-collection of the affected cells under §12.
>
> **Measurements are operator-initiated only** (§5.7). The harness refuses to
> start without `--operator-approved`, refuses to record without the §3.6
> **10-real-tab driver-validation gate**, refuses without the §2.5 conformance
> test, and refuses while a GPU streaming session is live on the host
> (`docs/OPERATOR-QUESTIONS.md` Q-7 — runs are *skipped, not degraded*, and
> waiting is the only permitted mitigation).
>
> `bench/data/` contains **no run ids**. That is the checkable form of the claim
> in the spec's own amendment log, and it is still true. It now contains one
> file that is not a run: `driver-validation.json`, which records that §3.6's
> gate was **attempted and did not run**, and the reason why. A refused
> attempt happens before a manifest is opened, so it consumes no run id and the
> sequence stays contiguous.
>
> That list was **four blockers on 2026-08-05 and is one after the same day's
> re-run.** Three were work and got done — the gotth-live SUT image and its
> committed recipe (deviation D-9), `docker/.env`, and the driver cpuset. The
> one left is **Q-7**, and it is not work: a GPU streaming container is running
> on this host, waiting is the only permitted mitigation, and nothing in this
> tree may stop or reconfigure a co-tenant container to shorten the wait. The gate's
> verdict is unchanged — `status: "not-run"`, `G-DRIVER` still refusing — and
> the honest difference is that it now names one thing instead of four.
>
> **The D3 orchestration is built and the gate that governs it is not open.**
> `measure-memory.mjs` no longer refuses because it is unimplemented; it
> refuses because `G-DRIVER`, `G-CONFORM` and `G-PHASE3` have not passed.
> `G-DRIVER` is the one gate nobody can assert by hand: it derives its verdict
> from the four measured numbers in `bench/data/driver-validation.json`, after
> merging `gates.json`, so a hand-written `{"driverValidation":{"pass":true}}`
> is overwritten by whatever the artifact actually says.

Binding document: [`docs/bench/equivalence-spec.md`](../docs/bench/equivalence-spec.md).
Every section reference below (`§2.4`, `§3.6`, …) is to that file. Where this
tree and the spec disagree, the spec wins and the disagreement is listed under
[Readings, deviations and ambiguities](#readings-deviations-and-ambiguities).

---

## What is here

```
bench/
  apps/{counter,chat,dashboard}/next/   the three Next.js applications  ← built
  apps/{counter,chat,dashboard}/gotth/  the gotth-live sides           ← built
  harness/            shim.js and ready.js (both SOURCES — the app trees hold
                      generated copies), the CDP driver, driver.mjs (§3.6's
                      synthetic session driver), bench-tls.mjs (how the node
                      half trusts the proxy's certificate), one file per
                      interaction ID, the five measure-* entrypoints,
                      validate-driver.mjs, the gates, and *.test.mjs for all of it
  docker/             the §3.6 topology: one proxy, one measured container, and
                      one Dockerfile per stack that builds the measured half
  fixtures/           the seeded generator and its committed SHA-256s
  audit/              §5.4's pessimization audit, generated and committed
  scripts/            build/launch/audit helpers
  versions.lock.md    everything pinned, by digest where a digest exists
  data/               no run ids; one driver-validation.json saying the §3.6
                      gate did not run, and why — see the banner
```

Everything node/npm lives under this directory and nowhere else (FR-74). The
library and all three gotth-live examples build and run on a machine with no
node installed; nothing here is in the Go module build.

---

## Everything runs in a container

The host has no node, no npm and no chromium. All of them live only in
`dis-gotth-live-bench:latest`. Run every command from the **repository root**:

```bash
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth/bench \
  dis-gotth-live-bench:latest bash -c '<command>'
```

Use `bash -c`, not `bash -lc`: the login shell strips the Go/node toolchain from
`PATH` in these images.

**The parts of the harness that talk to Docker** — `assert-no-tls.mjs`,
`host-state.mjs`, `measure-memory.mjs`, `validate-driver.mjs`, `run.mjs` — need
the Docker CLI and the socket, which the bench image does not carry. Mount both,
and add the host's `docker` group so the socket is readable:

```bash
docker run --rm --network host -u "$(id -u):$(id -g)" -e HOME=/tmp \
  --group-add "$(getent group docker | cut -d: -f3)" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(which docker):/usr/bin/docker:ro" \
  -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth/bench \
  dis-gotth-live-bench:latest node harness/assert-no-tls.mjs
```

(The repository root is mounted rather than `bench/` alone, because the
gotth-live driver imports `../../client/codec.gen.js` — the shipped codec, by
relative path per FR-74(a) — and a mount that stopped at `bench/` would put that
file outside the container.)

They read `docker inspect`, `docker ps` and the containers' own cgroup files;
they never start, stop or reconfigure anything outside the `gotth-live-bench`
project. `host-state.mjs` reads the co-tenant container list precisely so a
run can **wait** for a GPU streaming session to end (Q-7), which is the only
permitted mitigation. Which container that is belongs to the deployment rather
than to this library, so it is `BENCH_GPU_SESSION_MATCH` in `docker/.env` —
substrings matched against every running container's name and image, defaulting
to `steam,selkies`, and falling back to that default rather than to an empty
match list when the value is blank.

---

## Building and running an app

```bash
npm ci                                            # once; lockfile is committed

npm run fixtures                                  # §2.5's committed corpus
npm run fixtures:verify                           # regenerate in memory, compare SHA-256

npm run build -w @gotth-live-bench/counter-next   # next build, output: standalone
npm run build -w @gotth-live-bench/chat-next
npm run build -w @gotth-live-bench/dashboard-next

PORT=3000 npm run start -w @gotth-live-bench/dashboard-next
#   → http://127.0.0.1:3000/dashboard
```

`npm run start` launches the **production standalone server** (`NODE_ENV=production`,
no `next dev`, no HMR, plaintext only). Nothing here terminates TLS — that is
§3.6's boundary and it lives in the proxy container.

### Choosing the live-data variant (§5.4)

A **build-time** choice, not a runtime one, so the measured bundle provably
contains one transport and D1 cannot charge Next.js for two it never opens:

```bash
BENCH_VARIANT=sse  npm run build -w @gotth-live-bench/dashboard-next   # primary
BENCH_VARIANT=ws   npm run build -w @gotth-live-bench/dashboard-next   # secondary
BENCH_VARIANT=poll npm run build -w @gotth-live-bench/dashboard-next   # D3/D4 only
```

`ws` additionally starts the sidecar as a second process **in the same
container** — which is the point: §3.6 counts whatever processes the idiomatic
architecture requires, so the sidecar's RSS is part of the ws variant's memory
number, exactly as gotth-live's single Go binary is the whole of its own.

All three variants ship. `docs/OPERATOR-QUESTIONS.md` Q-5: the PRD makes it a
Phase 5 gate that none is dropped for schedule (FR-76).

---

## Smoke verification (allowed — it is not a measurement)

One tab, every interaction driven once, paint predicates asserted, **no timings
recorded**. A duration *is* produced by the shim and is deliberately thrown
away, because a number printed by a smoke run is a number somebody will quote.

```bash
PORT=3000 npm run start -w @gotth-live-bench/dashboard-next &
npm run smoke -- --app dashboard --stack next --origin http://127.0.0.1:3000
```

`--stack` is `next` or `gotth` and defaults to `next`, as every `measure-*.mjs`
does. It is **told**, not sniffed from the page: its only job is to decide
whether a `nextOnly` row is driven, and a wrong guess there does not report a
wrong guess — it reports a pass. An unrecognised value is refused rather than
treated as "not `next`".

Last run, on this tree:

| app | passed | failed | skipped | DOM at rest | §2 bound |
|---|---:|---:|---|---:|---:|
| counter | 7 | 0 | CTR-7 (cross-tab) | 45 elements | ≤ 150 |
| chat | 8 | 0 | CHT-3 (push) | 237 elements | ≤ 2000 |
| dashboard | 6 | 0 | DSH-7, DSH-8 (push) | 2945 elements, 729 SVG | ≤ 4000, ≤ 800 |

The skipped rows are the push and cross-tab interactions: they need §3.2's
clock-offset procedure and a second tab, which is the run driver's job and not a
smoke tab's.

```bash
npm test                 # node --test: the §6 statistics, the §2 registry, the gates
npm run verify           # both of the below
npm run verify:shim      # §2.0: the served shim is byte-identical to the source
npm run verify:ready     # D-6: each app's embedded ready.js matches harness/ready.js
npm run audit            # §5.4's pessimization audit; writes audit/
```

`verify:ready` is a shell script, not a node one, and that is load-bearing —
see [D-6](#d-6-the-source-of-truth-for-readyjs).

---

## D-6: the source of truth for ready.js

`bench/harness/ready.js` is the source. The three
`bench/apps/<app>/gotth/bench/ready.js` are generated copies:

```bash
node scripts/sync-ready.mjs      # rewrite all three  (npm run sync:ready)
sh scripts/verify-ready.sh       # check all three    (npm run verify:ready)
```

The copies are **git-tracked**, and `.gitignore` says so in a comment where a
reader would otherwise expect the shim's ignore rule to have a twin.

### Why this deviates from the review's specification

[REV-DUP's D-6](../docs/reviews/deduplication.md) specified the shim's model
verbatim: *"a `sync-ready.mjs` mirroring `sync-shim.mjs`, one source at
`bench/harness/ready.js`, **the three copies gitignored**, and a `verify:ready`
target joined to `verify:shim`."* Everything in that sentence is implemented
except the gitignore, which **cannot be done**, and the finding is not wrong to
have asked — the constraint is one the review had no reason to look for.

`shim.js`'s copies land in `apps/<app>/next/public/` and are read by Node at run
time, so ignoring them costs nothing: a stack that serves them is a stack that
ran `npm` to exist. `ready.js`'s copies are consumed by
`//go:embed bench/ready.js`, which the **compiler** resolves against files on
disk before any build step runs. Ignoring them makes a clean checkout fail:

```
$ rm bench/apps/counter/gotth/bench/ready.js          # what a .gitignore would do
$ (cd bench/apps/counter/gotth && go build ./...)     # in dis-gotth-live:latest
bench.go:42:12: pattern bench/ready.js: no matching files found
go build exit=1
```

That image has no node, so it cannot run `sync-ready.mjs` to repair itself, and
it is the image `ci.sh` builds all three bench modules in. Gitignoring the
copies would trade a duplication finding for a broken build in the library gate.

**So the copies stay tracked and the verification replaces the ignore.** What
D-6 actually found missing was never the ignore rule — it was that nothing
failed when the three drifted. That half is now real, and it is the half that
matters: a drifted copy still **compiles**, so the compiler will never find it.

### Why the verifier is `sh`, not `node`

One verifier, two callers. `npm run verify:ready` is the bench-image entry
point; `sh scripts/verify-ready.sh` is the same script run where node does not
exist — which is exactly where tracked copies can drift and where `ci.sh`'s
`bench/apps/<app>/gotth` step runs. A check reachable only through `npm run` is
a check that gate cannot run. It needs a shell and `cmp` and nothing else.

Answering a duplication finding with two copies of the check would have been a
poor joke.

### What this costs, stated rather than hidden

The source-of-truth banner is in the served bytes, because a banner only in the
source is a banner the person editing a copy never sees. `ready.js` therefore
grew **4,103 → 4,340 B raw and 1,868 → 1,986 B gzip-6** (measured, `wc -c` and
`gzip -6 -c`), and §3.5 counts every one of those bytes against **gotth-live's**
D1 figure — see G-6. It is a real regression in this stack's own payload number
and it is not netted off anywhere.

### The better home this could not use

The natural place for this assertion is each app's existing
`§2.0 the shared assets, byte for byte` Ginkgo `Describe`, which already
`Expect(stylesheet).To(Equal(want))` for the committed stylesheet copy: same
shape, same suite, already run by `ci.sh`'s bench step, and **no new `ci.sh`
line**. It was not used because `bench/apps/*/gotth/*_test.go` was outside the
write scope of the change that landed this. If it is ever adopted, the shell
script and its `ci.sh` invocation should come out with it rather than sit beside
it.

---

## The measured topology (§3.6) — construction only

```bash
sh docker/gen-cert.sh                       # local self-signed cert; prints the SPKI pin
cp docker/.env.example docker/.env          # then set the four cpusets for THIS host

# The Next.js side. Context is bench/ — `.`
docker build -f docker/next.Dockerfile --build-arg APP=dashboard \
  --build-arg VARIANT=sse -t gotth-live-bench/dashboard-next:sse .

# The gotth-live side. Same directory to run it from, context is `..`
docker build -f docker/gotth.Dockerfile --build-arg APP=dashboard \
  -t gotth-live-bench/dashboard-gotth:local ..

docker compose -f docker/compose.yaml up -d --force-recreate
```

Two containers. `proxy` terminates TLS, compresses at gzip 6, and is the **only
one that publishes a port** (`127.0.0.1` only). `app` is the server under test:
plaintext HTTP/WebSocket, **no published port, no TLS listener**. Swapping
`BENCH_SUT_IMAGE` is the only difference between an A run and a B run — same
proxy image by digest, same Caddyfile, same constraints, same cpuset. On a
gotth-live run two more values move with it, and they are **values, not a second
proxy configuration**: `BENCH_UPSTREAM_WS=app:3000`, because this stack has no
WebSocket sidecar and its WS lives at the app's own mount path, and
`BENCH_ORIGIN`, because the library's Origin allowlist is deny-by-default and
the origin the browser sends is the *proxy's*. `docker/.env.example` sets both
alongside the port they have to agree with.

**The build-context asymmetry, declared.** The Next.js build's context stops at
`bench/`; the gotth-live build's context is the gotth-live root, which is why
the two commands above differ in their last argument. The reason is each
ecosystem's own dependency mechanism: `bench/apps/*/gotth/go.mod` carries a
`replace` onto the checkout — that is what makes these apps measure the
**working tree** rather than a published version — so the library source has to
be inside the context, and the only context containing it is the repository
root. The Next.js side gets its dependency from `npm ci` against the committed
`package-lock.json`, so nothing above `bench/` is needed.

It is not a fairness problem and the whole of why is short: each side builds from
its ecosystem's normal, lockfile-pinned source of truth, and **neither side
fetches the code under test over the network at build time**. Both do fetch their
third-party dependencies from their registry, pinned by a committed lock —
`go.sum` on one side, `package-lock.json` on the other. The asymmetry is in which
directory `docker build` is pointed at, not in what either image ends up
containing. `docker/gotth.Dockerfile.dockerignore` is BuildKit's per-Dockerfile
ignore form, so the wider context does not mean a wider image: `.git`, the 13 MB
of derived fixtures, `bench/apps/*/next`, every `node_modules`, `docs/`,
`examples/`, `tools/` and `docker/tls` are all excluded, which is checkable by
building `--target build` and listing `/src`.

Both images are audited against §3.6's absence list by inspection — no Go
toolchain, no node, no npm, no TLS material anywhere on the filesystem, PID 1 is
the application binary itself, and `USER 65534:65534`:

```bash
docker inspect gotth-live-bench/dashboard-gotth:local \
  --format 'User={{.Config.User}} Entrypoint={{json .Config.Entrypoint}}'
#   User=65534:65534 Entrypoint=["/usr/local/bin/bench-app"]
```

```bash
node harness/assert-no-tls.mjs   # §3.6's boundary assertion, three checks
```

Verified against the live topology on this tree:

```json
{ "pass": true, "sutListeningPorts": [3000, 45129], "sutTlsPorts": [],
  "sutPublishedPorts": 0,
  "proxyDigests": ["caddy@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648"] }
```

and the proxy leg, showing gzip and the §3.1 headers arriving together:

```
HTTP/2 200
content-encoding: gzip
cross-origin-embedder-policy: require-corp
cross-origin-opener-policy: same-origin
```

**On HTTP/2.** The proxy negotiates h2 with a browser, and h2 for both stacks is
the same h2 — it is common-mode and it is what a real deployment behind this
proxy would serve. §3.7's "HTTP/1.1 on both" is a constraint on the **D4 load
generator**, which is invoked with `--http-version 1.1`; it is not a claim about
browser traffic. Stated because a reader who sees `HTTP/2` above should not have
to wonder whether §3.7 was ignored.

Since D-9 closed, the same assertion runs against the **gotth-live** SUT in the
same topology (`BENCH_SUT_IMAGE=gotth-live-bench/dashboard-gotth:local`), which
is the claim `docker/gotth.Dockerfile` exists to make:

```json
{ "boundary": "outside", "sut": "bench-app", "pass": true, "findings": [],
  "sutListeningPorts": [3000, 38151], "sutTlsPorts": [], "sutPublishedPorts": [],
  "proxyImage": { "repoDigests": ["caddy@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648",
                                  "caddy@sha256:844f60b64e4724a5aa8245e019dace0d3f199f7433ce6c57676cb30a920dbad9"] } }
```

Two things in that output would otherwise invite a wrong reading. The second
listening port is **not the application**: `docker exec bench-app cat
/proc/net/tcp` shows it bound to `0B00007F:9507`, which is `127.0.0.11:38151` —
Docker's embedded DNS resolver inside the container's own network namespace. It
is present on both stacks, it answers no TLS ClientHello, and the app's own
socket is the `[::]:3000` (`0BB8`) beside it. And the proxy carries **two**
`repoDigests` because `caddy:2.11.4` has been rebuilt since the pin was taken;
the pinned one is still in the list, which is what §5.2's comparison compares
against.

The third check is the one that cannot be talked around: a real TLS ClientHello
to every port the container is listening on. A plaintext HTTP server cannot
answer one, so a negative is *positive evidence* of absence rather than absence
of evidence. This asymmetry is worth ~18,000 B/session and is disqualifying **in
either direction** — the pre-amendment spec bound gotth-live alone and the
asymmetry ran *against* gotth-live (T-21).

---

## How the harness invokes measurements — COMMANDS ONLY, NOT EXECUTED

Reproduced so the Phase 5 turn runs a documented command (FR-75) rather than
inventing one. **Every one of these refuses today.**

```bash
# D1 — client JS payload, transfer + decoded, at t_ready and after the full
#      interaction set (§3.5). 20 cold loads per app per stack.
node harness/measure-payload.mjs --stack next --app dashboard --variant sse \
  --origin https://127.0.0.1:18443 --spki "$(cat docker/tls/bench.spki)" --operator-approved

# D2 — event→paint latency (§3.1, §3.2). 200 warm-up + 200 samples per run,
#      5 runs, gap U(400,600) ms, per network profile.
node harness/measure-latency.mjs --stack next --app dashboard --variant sse \
  --profile lan --origin https://127.0.0.1:18443 --operator-approved

# D3 — memory per session + the CPU figure that must ship beside it (§3.4, §3.6).
#      Refuses on the gates, not on being unimplemented: the orchestration runs
#      warm-up → M(0) → establish N → the SAME warm-up → M(N), with the TLS
#      boundary asserted and the GC configuration and four cpusets recorded
#      before the first window opens.
node harness/measure-memory.mjs --stack next --app dashboard --variant sse \
  --workload active-heavy --n 1000 --sut bench-app --proxy bench-proxy --operator-approved

# §3.6's DRIVER VALIDATION GATE — 10 real Chromium tabs vs 10 synthetic
#      sessions, on both stacks, within 10 %. This is the one that unblocks the
#      1k cells, and it writes bench/data/driver-validation.json whether it runs
#      or not: "not run, and why" is publishable under §7 and an estimate is not.
BENCH_CPUSET_DRIVER=10-17 \
node harness/validate-driver.mjs --app dashboard --variant sse --workload idle \
  --gotthImage gotth-live-bench/dashboard-gotth:local \
  --nextImage  gotth-live-bench/dashboard-next:sse \
  --spki "$(cat docker/tls/bench.spki)" --operator-approved

# D4 — SSR throughput at p99 ≤ 200 ms, open model, generator addresses the PROXY (§3.7).
node harness/measure-throughput.mjs --stack next --app dashboard \
  --url https://proxy:8443/dashboard --cpuset 10-17 --operator-approved

# D5 — TTI cold and warm, with §3.3's external validation of the ready signal.
node harness/measure-tti.mjs --stack next --app dashboard --profile lan \
  --origin https://127.0.0.1:18443 --operator-approved

# The A/B/A/B run driver (§5.2). Interleaves at the RUN level, brings the proxy
# up fresh with each stack (§5.7), writes a manifest for every run it starts.
node harness/run.mjs --dimension D2 --app dashboard --variant sse --operator-approved
```

**Why interleaving and not "five of A then five of B".** The host CPU governor
cannot be pinned — host changes are out of bounds for this project — so thermal
and frequency drift is made **common-mode** rather than controlled away. On a
host `docs/OPERATOR-QUESTIONS.md` Q-2 records as shared and non-quiescent, five
runs of A followed by five of B would attribute an hour of somebody else's load
to whichever stack went second.

---

## Readings, deviations and ambiguities

Everything in this tree that is a judgement rather than a transcription. §12
freezes §2, §3, §5, §7 and §8's row set; nothing below changes any of them, and
each is flagged for QA-2 to overrule rather than left to be discovered.

### Readings of the spec

| # | Where | Reading, and why |
|---|---|---|
| R-1 | §2.3 CHT-4, room switching | A **Server Action, not a navigation**. §2 forbids client-side routing on both sides, and §3.2 requires `t_input` and `t_paint` from the *same page's* `performance.now()` timeline. A document navigation puts them in two timelines and makes CHT-4 unmeasurable under the spec's own definition. The `[room]` segment is the entry point, not a router. |
| R-2 | §2.4 DSH-5, pause | **Server-authoritative.** §2.4 says pause "halts application of live updates (client-visible), stream continues server-side". A client-side pause would make DSH-5 a local paint here and a round trip on the other stack — the category error §2.2 exists to keep out of the tables. The feed keeps running for other sessions and a resume shows the *current* tick, which is the gotth-live dashboard's own behaviour. |
| R-3 | §2.4 controls | The status filter and rows-per-page are **buttons**, not `<select>`s. §2.4 writes "select 50 / 100 / 200", read as "choose one of". Buttons make DSH-1 and DSH-4 a native `pointerdown`, which is what §3.2's `t_input` is defined against; a `<select>` would put the causal start in a change event the spec does not define. |
| R-4 | §2.4 regions A and C | Each sparkline point and each series point is **its own element**. §2.4 sizes region A at "8 × ~70 nodes = 560", which is unreachable with a single `<polyline>` — the region would be an order of magnitude cheaper than the document the spec asks to be measured. Both stacks render per-point elements; the SVG budget lands at 729 of ≤ 800. |
| R-5 | §2.4 push payload | The push channel carries **patches**, not whole views. A full `DashView` is ~14 KB at perPage 200; pushing one twice a second would be 28 KB/s/session of ~90 % unchanged bytes, and §4.6's wire-byte row would be measuring an author's choice rather than a framework. |
| R-6 | §2.1 F-CTR-1 | The counter is **global**, not per session. F-CTR-1 says "server state, per session"; the gotth-live counter this app must match keeps one counter shared by every connection, and its "2 tabs sharing this counter" line is that sharing made visible. Under E1/E3/E4 the app that gets measured is the app that exists. The `session` scope is implemented too, so a spec amendment toward the literal reading is not blocked by this side. |
| R-7 | §2.3, chat stress row | One fixture, two rates. The corpus is generated at the 2 msg/s latency rate; the 20 msg/s stress row replays **the same committed bytes** with the tick interval divided by 10. §2.5 requires both servers to read the same bytes, not that a rate be baked into them. The interval in force is recorded in the manifest. |
| R-8 | §3.6, "differ by more than 10 %" | The denominator is the **10-real-tab figure**, not the mean and not the larger of the two. The clause reads "the driver misrepresents a browser", so the browser is the quantity being represented and is what the error is relative to. It is also the **stricter** of the two readings whenever the synthetic side is the larger one — the direction that would invent per-session memory a real tab does not cost — so §12's "take the reading least favourable to gotth-live" is discharged by taking it in both directions. Worked example in `harness/driver.test.mjs`: 100 000 vs 110 500 B/session refuses here and would pass under the max-denominator reading. |
| R-9 | §3.6, "consumes and discards pushed payloads at the rate a browser would" | The gotth-live driver sends an **Ack and a ClientTelemetry per applied frame**, as `client/runtime.js` does — at the dashboard's rate that is ~106 client frames/s/session the server would otherwise never see. It has no DOM, so `morph_micros` / `apply_micros` carry its own decode-and-discard timings: same frame, same rate, same uint32 clamp, two different values inside. Declared at the top of `harness/driver.mjs`. |
| R-10 | §3.6, "the same number of full-page loads" | §3.6 fixes only that M(0)'s warm-up **matches** M(N)'s; it names no count. This tree uses **50 loads**, identical on both stacks, sequential, recorded in the manifest — and `warmUpsMatch()` asserts the counts are equal and publishes **both** elapsed times, because a clause reading "the same elapsed time" is not discharged by a harness that only promises it. |

### Deviations from the spec's letter

| # | Where | Deviation, and why |
|---|---|---|
| D-1 | §4, "Playwright" | The browser driver is a **CDP client over the container's pinned Chromium**, not Playwright. Playwright downloads its own browser build from a CDN at install time — a network fetch this tree is not permitted to make. Every property §4 actually requires holds: one harness for both stacks, same binary, same flags, same viewport (1440×900 DPR 1), same profile handling, no per-stack branch. §4 is not in §12's freeze list. |
| D-2 | §5.3, "self-signed cert **committed** to the bench tree" | The **generation script** is committed and the key is not. Committing a TLS private key is a habit worth not establishing, and this repository's own rules put key material on the never-commit list. What §5.3 needs — reproducible, local, trusted by nothing but the harness — is delivered by `docker/gen-cert.sh` plus an SPKI pin passed to the browser, which trusts *exactly* that certificate rather than the blanket `--ignore-certificate-errors` a committed cert would have needed anyway. **Both halves of the harness trust it, and neither does so by disabling verification.** The browser half takes the pin (`--ignore-certificate-errors-spki-list`); the node half — the synthetic driver, its three Next.js channels and §3.6's warm-up — adds the certificate to the process's default CA store in `harness/bench-tls.mjs`, so chain *and* hostname verification stay on and a name the certificate does not cover is still refused. The pin is recomputed from the certificate there and a disagreement between `bench.crt` and `bench.spki` is a startup failure, because two halves of one harness trusting two different servers is not a thing that should be discoverable only from the numbers. |
| D-3 | §5.4, "Mutations … Server Actions" | The chat **typing heartbeat** is a Route Handler. React serialises Server Actions, so a keystroke heartbeat would queue in front of the user's Send and CHT-2 — the headline chat latency — would be measuring the heartbeat draining. Listed as a declared deviation in the audit output. |
| D-4 | §5.4, "bundle-analyzer output committed" | The analyzer runs in **JSON mode** and `scripts/audit.mjs` distils it into `audit/bundle-analyzer/*.json`. The default HTML report is ~1 MB per app per side; committing 6 MB of generated report to satisfy "output committed" would be committing a screenshot of the evidence. The raw JSON is regenerable by the documented command. |
| D-5 | §5.1, Next's own compression | `compress: false`. §3.5 mandates gzip level 6 on both stacks and calls a mismatch a disqualifying method error; the only place one level can be guaranteed for both is the container they share — the §3.6 proxy. Both application containers serve identity-encoded bytes. |
| D-7 | §3.4's active-light / active-heavy, **Next.js side only** | **The synthetic driver refuses to dispatch a Server Action, so those two rows read "not measured" per §7 on the Next.js stack.** §5.4 makes every Next.js mutation a Server Action; a Server Action is a `POST` carrying a build-time `Next-Action` id and a body in React's Flight reply encoding, neither of which is a protocol this tree can write against without guessing — and "a driver written against a guessed frame layout would fail the validation gate for a reason that is not the stack" is the exact failure §3.6's gate exists to catch. The alternative, a Route Handler the app does not use, would measure a mechanism §5.4 forbids it from using. The gotth-live side of both workloads **is** driven, as real `Event` frames over the real transport. **This is an asymmetry in what the harness can drive, and QA-2 should rule on it before Phase 5**: either the Flight encoding is implemented against the built app's client chunks (where `createServerReference("<id>", …, "<export>")` names both halves), or §3.4's grid publishes four cells and two "not measured, and why" for this stack. It is never inferred from the idle row. |
| D-8 | §3.6's two secondary figures | **Both read "not measured" on both stacks, symmetrically.** The runtime-internal row needs Go's `runtime/metrics` and Node's `process.memoryUsage()`/`v8.getHeapStatistics()` read from *inside* the measured container, and the forced-GC floor needs `debug.FreeOSMemory()` and `--expose-gc` + `global.gc()`. Neither bench image carries an introspection route and the Node image is not started with `--expose-gc`, so there is nothing symmetric to read. §3.6 says the floor is "a secondary, labelled number on both sides or on neither", so `secondaryFigures()` returns the pair or neither and refuses to take whichever happens to be reachable. **What would close it is one introspection route per stack, added in the same landing or not at all** — a route on one side only is the method error the clause names. |
| D-9 | §3.6's topology, gotth-live side | ~~**There is no gotth-live SUT image and no committed recipe that builds one.** `docker/` carries `next.Dockerfile` and no counterpart, so `docker/compose.yaml`'s `BENCH_SUT_IMAGE` can be pointed at only one of the two stacks today. §5.2 requires both sides behind the same proxy image by digest with identical constraints and cpuset, so the missing half is a blocker for **every** D3 and D4 number, not only for the validation gate — which is why `validate-driver.mjs`'s preflight names it as its own blocker rather than failing at `docker compose up`.~~ **Resolved 2026-08-05.** `docker/gotth.Dockerfile` is committed and both stacks now run under the same `docker/compose.yaml`; see [The measured topology](#the-measured-topology-36--construction-only) for the build command and what was checked. Struck rather than deleted because a deviation that was real for the length of a phase is part of the record. The one thing that did **not** become true is a published image digest — the image is a local tag, never pushed, so `versions.lock.md` pins the two bases it is built FROM and records image IDs as observations. |

### Ambiguities in the spec, resolved and flagged for QA-2

| # | Clause | Ambiguity | How this tree resolved it |
|---|---|---|---|
| Q-A | §2.0 vs §2.3 CHT-2 | §2.0 defines `data-bench-value` as *the element whose `textContent` is the predicate's subject*; §2.3 reads it as an attribute holding the message body ("last message node's `data-bench-value` === sent body"). | **Both.** The `<li>` carries `data-bench-value="<body>"` *and* the body span's `textContent` is the same string, so either reading of the predicate is true. No behaviour depends on which one QA-2 meant. |
| Q-B | §2.5 seed `0xG07TH11VE` | Not a hex literal — `G`, `T` and `H` are not hex digits. | Used as the **ASCII string it is written as**, FNV-1a'd to 32 bits. The derivation is in `fixtures/generate.mjs` rather than left for a reader to reverse-engineer. |
| Q-C | §2.4, "≈ 53 logical updates/s" | The stated rates (A 8×1 Hz, B 20×2 Hz, C 2×1 Hz, D 5 Hz) sum to **55**, not 53. | The **rates** are implemented as stated, since they are the load; the arithmetic is reported as 55 and the 2-update discrepancy is flagged here rather than fudged in either direction. |
| Q-D | §2.1 F-CTR-1 vs the gotth-live example | See R-6. | Recorded here because the counter app's own source references a `Q-BENCH-1` in `docs/OPERATOR-QUESTIONS.md` that **does not exist** — that file has Q-1..Q-7 and no bench series. The same is true of the `Q-BENCH-2` reference for the polling interval. Both defaults are documented here instead; adding them to `OPERATOR-QUESTIONS.md` is outside this turn's write scope. |
| Q-E | §2 apps vs `gotth-live/examples/` | §10 puts the gotth-live side at `bench/apps/<app>/gotth/`, distinct from `examples/<app>/`. The existing examples do **not** implement §2.3's or §2.4's product surface — `examples/chat` has one room, no typing indicator and no unread badges; `examples/dashboard` has meters/alerts/controls rather than regions A–E. | These apps are built to **§2's tables**, because E2 says the harness drives identical `data-bench-id` hooks per §2 and §12 freezes §2. `bench/apps/{chat,dashboard}/gotth/` must therefore be built to §2 as well, and is not the same program as `examples/{chat,dashboard}`. **This is the largest open item and QA-2/PM-1 should confirm it.** |
| Q-F | §3.6, driver validation gate | §3.6 fixes N at 10 and the tolerance at 10 %, and says nothing about **which workload** (§3.4 defines three) or **which app** the validation runs against. A driver validated on idle sessions has not been validated on the event path. | `validate-driver.mjs` takes `--app` and `--workload`, defaults to `dashboard`/`idle`, and **writes both into the artifact**; `G-DRIVER` then refuses a run whose app or variant the artifact does not name. So a validation is never silently reused across a pair it did not cover, and the narrowness is visible rather than assumed. QA-2 should say whether the gate must be run per workload as well as per app. |

### Defaults this tree took that the spec left open

| Default | Value | Why |
|---|---|---|
| Polling interval (§5.4 names the mechanism, not the interval) | 1000 ms | The rate at which the dashboard's slowest region updates (§2.4 region A, 1 Hz), so a polling client is not asked to be slower than the app's own slowest live region. The same value in all three apps so the three polling columns are comparable. D4 should sweep it rather than assume it. |
| SSE heartbeat | 15 s | Well inside any default proxy idle timeout. Its bytes are counted in §4.6 exactly like gotth-live's heartbeats. |
| Chat typing heartbeat | 1 Hz | The coarsest rate that keeps "N people are typing" continuously true while somebody types, against F-CHT-6's 3 s decay. |
| Session eviction grace | 30 s | D4 requests the document with a **fresh cookie per request** at the highest rate the stack will serve. Without eviction the RPS ceiling would be measuring an unbounded map and the D3 numbers taken afterwards would be measuring its residue. |
| Default rows-per-page | 200 | The size §2.4's DOM bound is stated against ("200 × 10 = 2000") and the state DSH-7's push row is measured in. DSH-4 drives 50 → 200, so its setup establishes 50 first. |

---

## The gotth-live sides

`bench/apps/{counter,chat,dashboard}/gotth/`. Three standalone Go modules, each
with a `replace` directive onto the checkout exactly as `examples/*/go.mod` has,
so nothing here is in the library's module build and the FR-74 quarantine still
holds in both directions: no node in the Go build, no Go module under `bench/`
that the library compiles.

They are built to **§2's tables**, not to `examples/{chat,dashboard}` — which is
ambiguity **Q-E** above, and this side confirms it rather than reopening it. The
counter is close to `examples/counter` because that example already carries §2's
hooks; the chat room and the dashboard are different programs from the examples
of the same name.

### Building and running

The host has no Go. Everything runs in `dis-gotth-live:latest`, from the
repository root, with `bash -c` and not `bash -lc`:

```bash
docker run --rm -v "$PWD:/workspace" -w /workspace/candace/pkg/gotth/bench/apps/counter/gotth \
  dis-gotth-live:latest bash -c 'templ generate && go build ./... && go test -race ./...'
```

Each app takes the same three paths and defaults them relative to its own
directory, so `go run .` from the app directory needs no flags:

```bash
go run .                     # counter    → http://127.0.0.1:3000/counter
go run .                     # chat       → http://127.0.0.1:3000/chat/alpha
go run .                     # dashboard  → http://127.0.0.1:3000/dashboard

go run . -addr 127.0.0.1:3100 -origin http://proxy:8443
go run . -shim ../../../harness/shim.js        # §2.0's shared shim
go run . -fixtures ../../../fixtures           # §2.5's committed corpus (chat, dashboard)
go run . -tick 10ms                            # chat: §2.3's 20 msg/s stress row, same bytes (R-7)
go run . -htmx ../../../../test/internal/conformance/testdata/htmx-2.0.10.min.js
```

`npm run fixtures` in `bench/` must have run first for chat and dashboard: the
generator and the SHA-256 are committed, the JSONL is not, and each app hashes
the bytes it read and publishes the digest on `/api/bench/clock` so the run
manifest records what was actually replayed. A missing fixture, a missing shim
or an HTMX bundle whose digest is not the recorded one is a **startup failure**,
not a warning — a page that 404s the shim connects, repaints and looks entirely
healthy, and the only symptom is a harness run failing on `window.__bench`
twenty minutes later.

Nothing here terminates TLS. Each process serves plaintext HTTP and WebSocket on
its own port and holds no key and no certificate; §3.6's boundary lives in the
proxy container and `harness/assert-no-tls.mjs` proves the absence from outside.

**The measured form of all three is a container**, and `go run .` above is the
development form. `docker/gotth.Dockerfile` is the committed recipe; run it from
`bench/`, with a context of `..` because these apps' `replace` directives put the
library source in the build:

```bash
docker build -f docker/gotth.Dockerfile --build-arg APP=counter   -t gotth-live-bench/counter-gotth:local ..
docker build -f docker/gotth.Dockerfile --build-arg APP=chat      -t gotth-live-bench/chat-gotth:local ..
docker build -f docker/gotth.Dockerfile --build-arg APP=dashboard -t gotth-live-bench/dashboard-gotth:local ..
```

`--build-arg APP` is validated in the build stage, so a typo or an omission
fails with the list of three rather than producing an image whose entrypoint is
missing. The image takes its paths from the environment `docker/compose.yaml`
sets, so nothing here is passed as a flag and the ENTRYPOINT is the binary and
nothing else. See [The measured topology](#the-measured-topology-36--construction-only)
for what the image deliberately does not contain and why the context differs
from the Next.js side's.

### Smoke verification, gotth-live side

Same runner, same one tab, same thrown-away durations as the Next.js side:

```bash
(cd apps/dashboard/gotth && go run . &)          # in dis-gotth-live-bench:latest
npm run smoke -- --app dashboard --stack gotth --origin http://127.0.0.1:3000
```

Against the **§3.6 topology** rather than a bare `go run`, which is the form that
also exercises the proxy, TLS and the Origin allowlist, it takes the proxy's
origin and the SPKI pin the browser is to trust:

```bash
npm run smoke -- --app dashboard --stack gotth \
  --origin https://127.0.0.1:18443 --spki "$(cat docker/tls/bench.spki)"
#   smoke: Chrome/151.0.7922.71 against https://127.0.0.1:18443/dashboard  [stack: gotth]
#     ok   window.__bench.ready  (§3.3 hydration + channel open + first message applied)
#     ok   DSH-1 … DSH-6        paint predicate became true
#     skip DSH-7, DSH-8         (push/cross-tab: needs the run driver, not a smoke tab)
#     DOM  2971 elements, 729 inline SVG nodes
#   smoke: 6 passed, 0 failed, 2 skipped
```

The `ready` line is the one that proves the whole path: §3.3 does not set it
until the document has loaded, the channel is open **and the first message has
been applied**, so a 403'd upgrade cannot reach it. Directly, the upgrade through
the proxy answers `101` with `Sec-WebSocket-Protocol: gotth-live.v1`, and the
same handshake with an origin the allowlist does not carry answers `403` — which
is what makes `BENCH_ORIGIN` load-bearing rather than decorative, and what a
wrong value looks like when it is wrong.

Last run against a bare `go run .`, on this tree:

| app | passed | failed | skipped | DOM at rest | §2 bound |
|---|---:|---:|---|---:|---:|
| counter | 7 | 0 | CTR-7 (cross-tab) | 29 elements | ≤ 150 |
| chat | 7 | 0 | CHT-2b (`nextOnly`), CHT-3 (push) | 214 elements | ≤ 2000 |
| dashboard | 6 | 0 | DSH-7, DSH-8 (push) | 2971 elements, 729 SVG | ≤ 4000, ≤ 800 |

Row by row against the Next.js table above: every non-push, non-cross-tab row
the Next.js side passes, this side passes — **except the one §2 specifies it
cannot, which is now skipped rather than failed.**

The chat row was **re-taken on 2026-08-05** after the composer adopted F-CHT-3
(see item 1 below) and is reproduced above unchanged, to the element: 7 passed,
0 failed, the same two skips, 214 elements. A row copied forward across a change
to the app it describes is a row nobody re-ran, so it was re-run; that it did not
move is the finding, and it is the one the adoption predicted, because a binding
that adds no element and matches no key any `CHT-*` row presses cannot move a
smoke count.

**CHT-2b is `nextOnly` and is skipped here.** It is the optimistic-send row,
AS-2, `nextOnly: true` in its own interaction file: *"the gotth-live column for
this row reads 'no equivalent', never a blank and never a slower number."* There
is no optimistic UI on this stack by construction (BL-4), so no markup can ever
carry `data-bench-state="pending"` and the predicate times out.

`smoke.mjs` used to skip only `push` and `crossTab`, so the `nextOnly` flag did
not reach it, the row was driven, and `npm run smoke -- --app chat` exited
non-zero against this stack for a reason that was not a defect. It now skips
`nextOnly` rows **when `--stack` is not `next`** and names the category on the
skip line, the way the push/cross-tab skip already did. **The exclusion is still
reported and still counts against this stack in §7 and §8** — a skipped smoke
row is not a row that stopped existing, and the D1/D2 tables carry "no
equivalent" for it either way.

The asymmetry is deliberate and is the reason `--stack` exists at all: on
`--stack next` the row is **driven**, because a skip that fired on both stacks
would hide a regression in the one capability §2.3 exists to credit Next.js
with. Both directions are covered by `npm test`.

### Where §2 forced a different structure from the library's natural idiom

Honesty notes, mirroring the deviations section above. Each is in the source at
the place it applies as well as here.

| # | Where | What, and why |
|---|---|---|
| G-1 | chat, `view.templ` | **The textarea is rendered with no text content and the composer is wrapped in a `<form>`.** The empty render is the runtime's controlled/uncontrolled rule: a non-empty server render overwrites the live value, an empty one leaves it alone — which is what makes F-CHT-8 and CHT-7 true even while the log repaints twice a second. The `<form>` is because the runtime serialises an event's fields from the bound element's form when it has one and from its own name/value when it does not, so a Send **button** outside a form would carry no body at all. That is one element more than the Next.js markup in region B. |
| G-2 | chat, `chat.go` | **A confirmed send clears the composer by changing the textarea's `id`.** The rule that preserves a draft also means the server cannot clear the box: an empty render is "uncontrolled", not "empty". Changing the id makes morph's id match fail, so the node is replaced rather than reconciled and the replacement is empty. It costs the composer's focus on a send. |
| G-3 | chat, dashboard | **Every session folds its own copy of the shared data** — all three rooms' logs, and the dashboard's 200 rows — where the Next.js stores keep one array and derive per-session views from it. `live.Event.Fields` is `map[string]string`, so an effect cannot hand a session a pointer to a shared immutable value, and a reducer that reached into the feed for one would not be a pure function of `(state, event)`. **This is a real per-session memory cost that D3 will measure**, and it is a property of today's API rather than an implementation choice. |
| G-4 | dashboard, `dashboard.go` | **A tick's twenty changed rows travel as one compact string, not as fields.** protocol H-4 bounds `Event.fields` at 64 and §2.4's "20 rows changed per tick" is 120 values on its own. It never reaches a wire — an emitted event is delivered in-process and what leaves the server is rendered HTML — so it is not the JSON side channel review-checklist §3.2 forbids. |
| G-5 | dashboard, `view.templ` | **Region E is not a live fragment.** §2.4 gives it to plain HTMX on this stack (AS-3, FR-62), and a patch that named it would revert HTMX's swap on the next tick. Its panel is keyed by the page-load cookie rather than by the live session, because a plain HTMX `GET` carries cookies and nothing else — so two tabs of this app in one browser share region E's refresh counter where two Next.js tabs do not. No `DSH-*` row opens a second tab. |
| G-6 | all three, `bench/ready.js` | **§3.3's `ready` signal is a served `.js` file, not an inline `<script>`.** It is 4,340 B raw / 1,986 B gzip-6, and it is served with a JavaScript MIME type **so §3.5 counts it against gotth-live's D1 figure**. (It was 4,103 B / 1,868 B before D-6's source-of-truth banner was added to it; the banner is served, so it is counted, and it is counted against **this** side. Measured with `wc -c` and `gzip -6 -c`, not estimated.) Inlining it would have moved those bytes into the HTML total, which is an accounting advantage this side has not earned: the Next.js equivalent lives inside its hydration bundle and is counted there. It is byte-identical across the three apps and it also mirrors `data-gotth-status` onto `data-bench-status`, because the one stylesheet each app serves is the Next.js side's file byte for byte and that file selects the connection indicator on the latter. |
| G-7 | all three | **The stylesheets are committed copies of `apps/*/next/src/app/*.css`, and a spec `cmp`s them.** A copy is a second file that agrees today; the agreement is asserted rather than promised. The shim is not copied at all — each app reads `harness/shim.js` at run time and serves those bytes, so §2.0's "one file, byte-identical" has nothing left to verify. |
| G-8 | chat, `chat.go` | **F-CHT-9's refusal is in the reducer, not in `Config.Authorize`.** A `live.DenyError` rejects the event before the reducer runs, so there is no render, so there is no **visible** error — and "rejected server-side with a visible error" is the whole of F-CHT-9. The library has no application hook that can render a denial (there is no patch hook, by design, api-surface §7.1). `Authorize` is still a real check, and the executor refuses the same send a second time, so the rule is enforced twice and rendered once. |
| G-9 | chat | **The composer's debounced draft binding doubles as the typing signal.** The Next.js side sends a separate 1 Hz typing ping (its declared deviation D-3); this side derives F-CHT-6 from the draft event it already sends. Because the debounce is trailing-edge, continuous typing sends *nothing* until the typist pauses 150 ms, so the outbound rate is roughly one event per burst — fewer frames than the ping, not more. Declared because §4.6 counts frames in both directions. |
| G-10 | counter, `bindings.go` | **F-CTR-6 is implemented with a real key filter, on both stacks.** `live.Bind.Keys` and `live.OnAll` landed at api-surface checkpoint 3 (F-3) *for exactly this row*, so `+` and `−` are two filtered bindings on one focusable element and CTR-5 passes. `CTR-5.mjs` used to carry a note saying gotth-live could not express the row; it was true when written and is now **corrected in that file** rather than only contradicted here. ~~The residual — `Bind.Keys` compares the key and not the modifier state, and a key binding never calls `preventDefault` — is F-CHT-3's problem, not this row's, and is recorded in full below.~~ **Corrected 2026-08-05: there is no residual.** `Bind.NoModifiers` compares the modifier state and `Bind.PreventDefault` takes the key, both per binding and both defaulting to what this row already had, so **CTR-5's two bindings render byte-identically** — the counter's `+` and `−` set neither option and must not, because `+` **is** Shift+`=`. The sentence is struck rather than deleted because it was true when written and because it is the sentence that routed the gap to the chat app, which is where it was closed. See the corrected item 1 below. |

### What §2 asks for that today's library API cannot express

~~Reported, not worked around, and **not fixed by extending the library** — that
is not this turn's call to make.~~

**Corrected 2026-08-05.** It was not this turn's call and it became somebody's:
**item 1 was fixed by extending the library**, in three landings, and this app
has adopted the fix. The list is one shorter and the entry stays on the page
with what closed it underneath, because the useful thing about a list like this
is which of its entries stopped being true and when. Items 2, 3 and 4 stand
exactly as written.

1. ~~**F-CHT-3's "Enter sends, Shift+Enter newlines" is not expressible**, for
   three independent reasons, any one of which is enough:~~
   - ~~`live.Bind.Keys` compares the key and **not** the modifier state, so
     `Shift+Enter` arrives as `"Enter"` and would send;~~
   - ~~a key binding **never calls `preventDefault`** (`templ.go` says so and
     means it), so Enter would insert the newline *as well as* sending;~~
   - ~~`Fields`, `Debounce` and `Throttle` are read from the **element** and not
     from the binding, so a composer bound for both `input` and `keydown` shares
     one debounce timer — the trailing `input` event that Enter's own newline
     produces would cancel the pending send outright.~~

   ~~So this side binds Send to the button only. **No harness row drives Enter**,
   so no interaction is affected; F-CHT-3 is half-met and belongs in §8's parity
   table rather than in a latency row. api-surface §10 already records the
   `preventDefault` half as "a finding for PM-1"; this is the second consumer to
   hit it.~~

   **F-CHT-3 IS MET. 2026-08-05. Three reasons, three landings, and the app
   adopted it.**

   Every sentence above is struck and none is deleted: this is a benchmark
   report, and a reader has to be able to see that the row moved and what moved
   it, not a report that was always green. What replaced each reason, in the
   order they were written:

   | # | The reason | What closed it | Where |
   |---|---|---|---|
   | 1 | the modifier state is not compared | **`Bind.NoModifiers`**, component 7 of the binding grammar | the FR54-6 Part B landing, `live/templ.go` + `client/runtime.js` |
   | 2 | a key binding never calls `preventDefault` | **`Bind.PreventDefault`**, component 8 | same landing |
   | 3 | `Fields`/`Debounce`/`Throttle` are read from the element | every option moved **into the binding that declared it** | `2ab18690` |

   Reason 3 is the one worth pausing on, because it is the reason that would
   still have sunk the row with the other two fixed: `NoModifiers` and
   `PreventDefault` buy nothing if the send binding beside the draft binding
   inherits the draft's 150 ms timer and Enter's own trailing `input` event
   cancels the pending send. All three had to land, and they landed separately.

   **`bench/apps/chat/gotth` has adopted it**, so the claim is about the
   *measured artifact* and not about the library in principle. The composer's
   textarea now carries

   ```
   data-gotth-on="keydown:chat.send:Enter::::1:1;input:chat.draft::150"
   ```

   — one element, two bindings, one of which has an interval. `ComposerBinding`
   in `apps/chat/gotth/bindings.go` says what each component does; four Ginkgo
   specs in `chat_test.go` pin it by component subscript.

   **Driven in Chromium against this app, not asserted about it** (Chrome/151,
   the bench image, the committed fixture replaying beside it): `Shift+Enter`
   left `value="hi\n"`, appended **no** message and updated the server's draft
   to 3 characters; `Enter` raised the send and the room confirmed a body of
   exactly `"hi\n"`; `Enter` on an **empty** box — rejected by F-CHT-4, so the
   node survives to be looked at — left the box empty, which is `preventDefault`
   firing. The same drive against the composer as it was before this landing
   fails on the spec, on the confirmed body and on `preventDefault`, which is
   what makes the ten passes worth reading.

   **Nothing measured moved.** No `CHT-*` row drives Enter (CHT-1 types `x`,
   CHT-7 types `a`…`z`, and CHT-2 / CHT-2b / CHT-5 / CHT-8 click Send), a
   non-Enter keydown fails the key filter and raises nothing, and the smoke run
   below is unchanged at **7 passed, 0 failed, 2 skipped, 214 elements**.

   **Three corrections to what the struck text said, which a later auditor
   should not have to reconstruct:**

   - *"api-surface §10 already records the `preventDefault` half as a finding for
     PM-1"* — **that finding is discharged.** `docs/api-surface.md:739`'s row is
     marked `⟨SUPERSEDED 2026-08-05⟩` beside itself at `:740`. What survives of
     it is only its *reason*: a key filter **alone** still cannot express
     F-CHT-3, which is why this cost two more fields rather than none.
   - *"belongs in §8's parity table"* — **it does not, and it never did.** §8's
     frozen row set is `N-1`…`N-14` and `G-1`…`G-13`, capabilities rather than
     features, and none of them is F-CHT-3. F-CHT-3 is a row of the frozen §2.3
     **feature** table, where E1 ("same product surface") is what it answers to.
     The mis-routing is recorded rather than quietly fixed because it is why the
     gap sat in a prose list for a phase instead of in a table somebody grades.
   - *"No harness row drives Enter, so no interaction is affected"* — true, and
     it was doing more work than it should have. **A feature nothing drives is
     still a feature E1 requires**, and while this side did not have it the
     difference between the two apps was an asymmetry that is **not in §2.6's
     register** — a closed list only §12 may add to. So the exposure was not
     "half-met", it was an undeclared asymmetry of exactly the class amendment
     A-2 was raised to fix for G-3 and G-5, and E6 prices it at *"invalidates
     the affected dimension and forces a re-run"*. **Adoption is the only remedy
     this tree could take on its own**; the alternative was a §2.6 amendment,
     which is L9-1's to approve and not this report's to write. That, and not
     tidiness, is why the app changed rather than only the sentence.
2. **An emitted event cannot carry an opaque payload.** `live.Event.Fields` is
   `map[string]string`, bounded at 64 entries. Everything in G-3 and G-4 above
   follows from it: per-session copies of shared data, and a hand-rolled
   encoding for anything wider than 64 scalars. A `Payload any` on `Event`, or
   an emitter that could hand a session an immutable value, would remove both —
   and would be a real API decision with a real cost, which is why it is
   reported here rather than taken.
3. **A denial cannot be rendered.** See G-8. `Authorize` is the specified place
   for F-CHT-9's rule and it is structurally the wrong place for F-CHT-9's
   *visible error*.
4. **A server-rendered empty textarea cannot be cleared by the server.** See
   G-2. The id-rotation workaround is sound and is not obvious; the rule that
   makes it necessary is the same rule that makes FR-25's preservation work, so
   this is a trade rather than a defect.

### What is deliberately absent

- **The counter's C-A row.** §2.2 makes the client-local `useState` counter
  Next.js-only by specification and BL-3 makes it unimplementable here. There is
  no `/counter-local` on this side, and `counter.go`'s package comment says so
  where a harness reader looking for the hook will be. Reporting C-A is the
  point; suppressing it would be the strawman FR-73 forbids.
- **Optimistic send (CHT-2b).** Same shape, AS-2, BL-4. `nextOnly: true`, so
  the smoke runner skips it on `--stack gotth` and drives it on `--stack next`.
  See the smoke table.
- **`window.__bench` timings.** The smoke runner produces a duration and throws
  it away, here exactly as on the other side. `bench/data/` still contains no
  run ids.

---

## What Phase 5 still needs

Nothing below is blocked on the Next.js side; all of it is either the
gotth-live half or an operator decision.

1. ~~**`bench/apps/{counter,chat,dashboard}/gotth/`.**~~ **Built** — see
   [the gotth-live sides](#the-gotth-live-sides) below. All three serve the
   byte-identical `harness/shim.js` (§2.0) and the byte-identical stylesheets,
   expose the same `data-bench-id` hooks, and chat and dashboard expose the same
   `/api/bench/clock` shape §3.2's skew estimate needs. Struck rather than
   deleted, because a list of what is missing is more useful to the next reader
   when it also records what stopped being missing.
2. **The §2.5 conformance test.** Both servers' rendered DOM compared at a fixed
   tick under a paused clock. It **gates the measurement**: it must pass before
   any run counts.
3. ~~**The synthetic session driver (§3.6).**~~ **Built** — `harness/driver.mjs`.
   It speaks each stack's actual protocol: for gotth-live, liquid proto over the
   ADR-001 `internal/wsx` transport, with the frame layout **imported from the
   shipped `client/codec.gen.js` by relative path** (FR-74(a)) rather than
   re-implemented, and with `client/runtime.js`'s behaviour transcribed — the
   `gotth-live.v1` subprotocol, an Origin the wsx allowlist accepts, the
   document fetch whose cookie the upgrade carries, an Ack **and** a
   ClientTelemetry per applied frame, a verbatim heartbeat echo, and FR-11's gap
   latch. For Next.js, the document fetch, the server-minted `sessionKey` read
   out of the document rather than invented, and the SSE / WS / poll channel
   consumed and discarded with no backpressure. Verified live against
   `bench/apps/dashboard/gotth` and `gotth-live-bench/dashboard-next:sse` — ten
   concurrent sessions each, no errors, no timings recorded. What it will not do
   is deviation **D-7**.
4. **The 10-real-tabs vs 10-synthetic validation gate**, on both stacks, within
   10 %, published with the report. Without it the 1k number is an assertion
   about a synthetic client, not about sessions (T-9). **The runner is built**
   (`harness/validate-driver.mjs`) and **the gate has not run.** It was
   attempted on `node-a` and refused; `bench/data/driver-validation.json`
   records `status: "not-run"` with the host state and its blockers, and
   `G-DRIVER` goes on refusing in §3.6's own words. It named four on 2026-08-05
   and names **one** after the same day's re-run — the artifact is the same
   refusal, against a shorter list:
   - **Q-7** — a GPU streaming container is up on this host. Waiting is the only
     permitted mitigation; the check is conservative by design and a *running*
     container blocks whether or not anybody is streaming through it. **Still
     standing, and it is the only one.** Nothing in this tree may stop, restart
     or reconfigure a co-tenant container to shorten it.
   - ~~**no gotth-live SUT image**, and no recipe that builds one — deviation
     D-9.~~ **Discharged** — `docker/gotth.Dockerfile`, and all three
     `gotth-live-bench/{counter,chat,dashboard}-gotth:local` built from it.
   - ~~**no `docker/.env`** on this host, so compose has no parameters.~~
     **Discharged** — `docker/.env` exists (gitignored, as it must be) and the
     two-container topology comes up under it.
   - ~~**no `BENCH_CPUSET_DRIVER`**, which follows from the line above, and §3.6
     requires the driver pinned disjoint from the SUT.~~ **Discharged** —
     `.env.example` and `.env` both carry all four disjoint sets. Note what that
     buys on a guest: §5.2's disjointness here is *logical*, because this VM's
     32 CPUs each report a single thread sibling and one shared L3, so no set
     can be placed against an SMT sibling or inside a cache domain. `.env.example`
     says so at the point of use.
5. **§3.6's two secondary figures**, which need one introspection route per
   stack, added in the same landing or not at all — deviation **D-8**.
6. **A load generator**, chosen and pinned (§3.7). Until then D4 reads "not
   measured" rather than being estimated (§7).
7. **Phase 3's tuning finished** — QA3-1, QA3-2, QA3-3 (Appendix B). This is the
   gate that makes everything above premature rather than merely incomplete.
8. **Lighthouse against the §3.6 topology** (§5.4's audit item A-7), and a
   re-run of `npm run audit` immediately before collection.

## What the report must say, whatever the numbers turn out to be

- **No independent Next.js reviewer exists.** `docs/OPERATOR-QUESTIONS.md` Q-1:
  internal control only, disclosed **in the report body, not a footnote**, in
  those words. `audit/nextjs-pessimization-checklist.md` is the whole of the
  fairness control on this side.
- **The host was not quiescent.** Q-2 takes `node-a`, which serves a
  browser-streamed GPU desktop and a long tail of other projects. Every run
  carries its `contended` flag and its host state. This is spec threat **T-5**,
  not a solved problem.
- **C-A and CHT-2b ship.** The client-local counter and the optimistic send are
  interactions gotth-live structurally loses, they are measured, and they are
  reported in the same table with the same typography. Suppressing them is the
  strawman FR-73 forbids.
- **The Next.js static/ISR row ships.** §5.5 forbids caching on the *measured*
  route because the equivalent gotth-live route is dynamic; the cached variant
  is measured separately and published as an explicit Next.js-advantage row.
