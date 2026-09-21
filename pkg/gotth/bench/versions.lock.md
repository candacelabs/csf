# Pinned versions (equivalence-spec §10, §5.2, §5.4)

Everything a run depends on, pinned, and pinned **by digest where a digest
exists**. §5.2: "Base images pinned by digest, not tag — including the proxy
image." A tag is a moving target and a re-run six weeks later against `caddy:2`
is not a re-run.

| Field | Value |
|---|---|
| Owner | BENCH-1 |
| Status | construction; no measurement has been taken under any of these |
| Recorded | 2026-08-04; the two gotth-live base-image rows added 2026-08-05 with D-9 |

---

## Next.js stack (§5.4 pins the configuration in advance, Q15)

§5.4: "**Next.js 15.x App Router, React 19, Node LTS**, `output: 'standalone'`,
self-hosted node server." Pinned exactly — `package.json` carries no `^` and no
`~`, and `package-lock.json` is committed (FR-74).

| Component | Version | Where |
|---|---|---|
| Next.js | `15.5.22` | `apps/*/next/package.json` |
| React | `19.2.8` | ditto |
| React DOM | `19.2.8` | ditto |
| SWR | `2.5.0` | ditto — §5.4 names it for the SSE variant |
| `ws` | `8.21.2` | ditto — §5.4's secondary variant |
| TypeScript | `5.9.3` | devDependency |
| `@next/bundle-analyzer` | `15.5.22` | devDependency, §5.4's audit |
| `depcheck` | `1.4.7` | root devDependency, §5.4's audit |
| Node | `24.19.0` | `engines.node`, and the runtime image below |

## Container images (§5.2 — by digest, not tag)

| Role | Image | Digest |
|---|---|---|
| TLS-terminating proxy (§3.6) | `caddy:2.11.4` | `sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648` |
| Next.js runtime + build | `node:24-bookworm` (Node v24.19.0) | `sha256:da4221677e02b54ef6335adfa447578d512ad14f251024fb92ea433c2c102760` |
| gotth-live build stage | `golang:1.25.12-bookworm` (Go 1.25.12) | `sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58` |
| gotth-live runtime | `debian:bookworm-slim` (Debian 12.15) | `sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241` |
| Build/harness toolchain | `dis-gotth-live-bench:latest` | local image; Chromium and Node live only here (FR-74) |

The two gotth-live rows are `docker/gotth.Dockerfile`'s `GO_IMAGE` and
`RUNTIME_IMAGE`, and the **tag column was resolved from the digest, not the
other way round**: each pin is an OCI index digest whose
`org.opencontainers.image.version` annotation names its own tag, read with
`docker buildx imagetools inspect <image>@<digest>` and confirmed from inside
each image (`go version` → `go1.25.12`; `cat /etc/debian_version` → `12.15`).
Worth stating because `golang:1.25-bookworm` **has since been rebuilt** and today
resolves to `sha256:908f8ff2…`, so the tag alone would no longer find this
compiler — which is §5.2's whole argument for digests, observed rather than
quoted.

`debian:bookworm-slim` and `node:24-bookworm` are the same Debian release to the
same point number, 12.15, so libc and the kernel-facing userland under the two
measured containers are common-mode. They are **not** the same rootfs: 88
installed packages against 413 (`dpkg -l | grep -c '^ii'`). Neither application
opens the difference and §3.5 counts nothing served from it, but "same base
image" would be the wrong claim to have made.

The proxy choice is `docs/OPERATOR-QUESTIONS.md` **Q-6**: the official upstream
Caddy image, because it is the proxy this monorepo already runs at the edge, so
its behaviour is the one the operator already reasons about and using the same
family removes a "you picked a proxy that flatters your stack" question. It is a
**bench-project container only** — not the edge, does not read the production
Caddyfile, binds `127.0.0.1`, adds no host or network policy, touches nothing in
`caddy/`.

§5.2: "A run in which the two sides' proxy image digests differ is void, not
corrected after the fact." `harness/assert-no-tls.mjs` compares the digest
against this table before a D3 cell is recorded and writes both into the
manifest.

## Browser (§4)

| Component | Version | Notes |
|---|---|---|
| Chromium | `Chrome/151.0.7922.71` | `/usr/bin/chromium` inside `dis-gotth-live-bench`; pinned by that image |
| Driver | `harness/cdp.mjs` | a CDP client over `ws@8.21.2`, **not Playwright** — see below |

**Why not Playwright.** §4 says "Playwright driving headless Chromium over CDP,
pinned version, same browser binary, same flags, same viewport, same profile
handling. No stack-specific branch exists in the harness." Every property that
sentence actually requires holds. What does not hold is the library name:
Playwright downloads its own browser build from a CDN at install time, which is
a network fetch outside the npm registry and outside what this tree is permitted
to make. §12 freezes §2, §3, §5, §7 and §8's row set; §4 is not in that list, so
this is an implementation choice inside a section the freeze leaves open, and it
is flagged for QA-2 rather than assumed.

## Load generator (§3.7) — NOT YET PINNED

§3.7: "`oha` or `vegeta` in constant-rate mode (choice pinned in
`bench/versions.lock.md`); `wrk2` acceptable as a cross-check."

**Not chosen and not installed.** Installing a load generator is a Phase 5
decision, not a side effect of construction, and pinning a digest for an image
this tree has never pulled would be a pin nobody checked.
`harness/measure-throughput.mjs` assembles the exact `oha` invocations so the
choice is a one-line change here plus a pull, and the D4 row reads "not
measured" until then rather than being estimated (§7).

## Fixtures (§2.5)

One committed generator, one SHA-256 per app. The JSONL itself is derived and
gitignored; `npm run fixtures:verify` regenerates in memory and compares.

| App | Ticks | Bytes | SHA-256 |
|---|---:|---:|---|
| chat | 14,400 | 1,718,569 | `b3ae29f6ed2d5ecc62b4d0ca19a0b34d529090b538f5e7a0ebbc8263f15029fb` |
| dashboard | 21,600 | 11,176,093 | `83002a360e5c925b21c7c175a80fbaaff5fff3f4903c3461502859413a59a966` |

Seed: the spec's `0xG07TH11VE`, used as the ASCII string it is written as
(G, T and H are not hex digits, so it is a mnemonic and not a literal), FNV-1a'd
to 32 bits. The derivation is in `fixtures/generate.mjs` so a reader who expects
an integer knows exactly what was done with the token.

## Shared shim (§2.0)

| File | Bytes | SHA-256 |
|---|---:|---|
| `harness/shim.js` | 9,323 | `4fa8d3238afd1f66e08d090c90ba392e899a03ec5535b6217affa4dd3f0d04a7` |

§2.0: one file, byte-identical, served by both stacks, its transfer bytes
subtracted from both stacks' client-JS figures with the subtracted amount
stated. `npm run verify:shim` asserts each app's served copy still matches.

## Compression (§3.5)

gzip **level 6**, applied in the proxy and nowhere else, for both stacks. Both
application containers serve identity-encoded bytes, so there is no second
compressor whose settings could disagree. Brotli is informational only and is
not enabled for the comparison figure. Serving one stack with brotli and the
other with gzip is a disqualifying method error.

## GC (§3.6 — pinned and disclosed, neither side tuned for the benchmark)

| Stack | Setting | Value |
|---|---|---|
| Next.js (Node) | `--max-old-space-size` | equal to the container memory limit; `docker/.env.example` sets 4096 against `BENCH_SUT_MEM=4g` |
| gotth-live (Go) | `GOGC` / `GOMEMLIMIT` | `100` / `4GiB` |

Recorded into every run manifest from the container's own environment, not from
this file.

---

## What is deliberately absent

- **No gotth-live SUT image digest**, and there will not be one. The two base
  images it is built FROM are pinned by digest above; the image it produces is a
  local `gotth-live-bench/<app>-gotth:local` tag that is never pushed, so it has
  no `RepoDigests` entry to record (`docker image inspect … --format
  '{{json .RepoDigests}}'` → `[]`). The same is true of the Next.js side. What a
  run manifest records instead is the local **image ID**, which is what the two
  measured containers were actually started from. On this host today, built from
  `docker/gotth.Dockerfile` at commit-time and reproduced twice:
  `dashboard` `sha256:81e6f6b5fe5e…`, `counter` `sha256:94bc1a397865…`,
  `chat` `sha256:a6cf85fbae12…`. Those are *observations of one host*, not pins —
  they are not reproducible across machines and nothing gates on them.
- **No load-generator digest.** See above.
- **No `data/` run ids.** Nothing has been measured. §12's amendment log makes
  "`bench/data/` contains no run ids" the checkable form of that claim, and it
  is still true.
