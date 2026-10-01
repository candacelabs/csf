# The gotth-live side's measured container (equivalence-spec §5.1, §3.6).
#
#   cd bench
#   docker build -f docker/gotth.Dockerfile --build-arg APP=dashboard \
#     -t gotth-live-bench/dashboard-gotth:local ..
#
# RUN FROM bench/, exactly as next.Dockerfile's command is, but with a context of
# `..` — the GOTTH-LIVE ROOT, not bench/. The two halves of that sentence are
# both load-bearing and they are easy to conflate: `-f` is resolved against the
# WORKING DIRECTORY and not against the context, so the `-f bench/docker/...`
# spelling a reader might reach for fails with `lstat bench: no such file or
# directory` from either place it could plausibly be typed. One command, two
# directories, and it is written out here because getting it wrong is a build
# error rather than a wrong image.
#
# Built from the gotth-live root is the one structural asymmetry with
# docker/next.Dockerfile and it is declared rather than hidden:
#
#   * the three benchmark apps are packages of the same module as the library
#     and import it from source, which is what makes them measure the WORKING
#     TREE rather than a published version. The library source therefore has to
#     be in the build context, so the context cannot stop at bench/;
#   * the Next.js side gets its dependency from `npm ci` against the committed
#     package-lock.json, so its context can stop at bench/.
#
# It is not a fairness problem, and here is the whole of why. Each side builds
# from its own ecosystem's normal source of truth — a Go module replace onto the
# checkout, an npm lockfile — and NEITHER side fetches the code under test over
# the network at build time. Both DO fetch their third-party dependencies from
# their ecosystem's registry, pinned by a committed lock: `go.sum` here,
# `package-lock.json` there. The asymmetry is in which directory the `docker
# build` command names, not in what either image ends up containing.
# bench/README.md, "The measured topology", carries the same note.
#
# WHAT THIS IMAGE DOES NOT CONTAIN, and why each absence is a spec requirement:
#
#   no TLS key, no certificate, no TLS listener   §3.6's boundary. TLS is
#       terminated in the proxy container, on both stacks. Terminating it inside
#       one stack's container and outside the other's is a disqualifying method
#       error in either direction — worth ~18,000 B/session, and the direction
#       it swings is a choice that would otherwise be available after seeing the
#       number (T-21). harness/assert-no-tls.mjs proves the absence from
#       outside, with a real ClientHello to every port the kernel says this
#       container is listening on; this file is why there is nothing to find.
#
#   no source maps, no debug endpoints        §3.5 excludes source maps as "not
#       served in production on either side". Nothing here compiles or serves
#       one, and net/http/pprof is not imported by any of the three apps.
#
#   no dev mode                               §5.4's audit checklist, and the
#       gotth-live equivalent of "no `next dev`". `live.Config.Dev` is false in
#       all three apps' Config, which is what keeps a panic value and its stack
#       out of the error frame the browser receives; there is no build arg here
#       that can turn it on, because there is no code path that reads one.
#
#   no npm, no node, at build time OR at runtime   FR-74 quarantines node to
#       bench/. The build stage is a stock `golang` image and the runtime stage
#       is a Debian base with a static binary copied into it; neither can run a
#       package manager, and the runtime stage cannot fetch anything even if
#       something asked it to.
#
#   no templ, no protoc                       the generated code is committed
#       (`*_templ.go`, `client/codec.gen.js`, the protocol's generated Go), so
#       code generation is not part of building this image. `ci.sh`'s
#       generated-code drift check is where that stays honest; an image that
#       regenerated would be an image that could silently disagree with the tree
#       the tests ran against.
#
#   no Go toolchain in the runtime stage      `go` exists only in the build
#       stage. The runtime stage cannot compile, which means the thing measured
#       is the binary that was built and audited, not one the container made.
#
# WHAT IT DOES CONTAIN THAT THE APPLICATION DOES NOT NEED, stated because the
# absence list above would otherwise be read as "FROM scratch":
#
#   a shell (Debian's /bin/sh) and coreutils   NOT for the application. The
#       ENTRYPOINT is the binary alone: it takes no argv, expands no variable,
#       and would run identically in an empty image. The shell is there because
#       §3.6's own instrumentation reads from INSIDE the measured container on
#       both stacks — harness/assert-no-tls.mjs runs
#       `docker exec <sut> sh -c 'cat /proc/net/tcp …'` so the listening-socket
#       list is the KERNEL's and not the application's opinion, and
#       harness/measure-memory.mjs runs `docker exec <sut> sh -c 'cat
#       /sys/fs/cgroup/memory.current …'` for every M(x) sample. An image the
#       harness cannot exec into is an image whose D3 cell cannot be taken and
#       whose TLS boundary cannot be asserted, and quietly shipping one to look
#       minimal would be trading a checkable property for an aesthetic.
#       The runtime base is Debian bookworm for the same reason the Next.js
#       runtime is: `node:24-bookworm` IS Debian bookworm, so the libc and the
#       kernel-facing userland underneath the two measured containers are
#       common-mode rather than a second variable. Both are Debian **12.15**,
#       the same point release, read out of /etc/debian_version in each image
#       rather than assumed. What is NOT identical is how much of Debian is
#       installed: this pin is `bookworm-slim` at 88 packages and the Next.js
#       pin is the node image's fuller base at 413 (`dpkg -l | grep -c '^ii'`).
#       Neither side's application opens any of the difference, and nothing
#       §3.5 counts is served from it — but "same base image" would be the
#       wrong claim, so the right one is written instead.

# Pinned BY DIGEST, not tag (§5.2: "Base images pinned by digest, not tag").
# Both digests are recorded in bench/versions.lock.md.
#
# GO_IMAGE is the same `golang:1.25-bookworm` family tools/g11/run.sh already
# uses for the clean-clone gate — a STOCK upstream image, deliberately not
# dis-gotth-live:latest, so nothing this repository builds can be the reason a
# build succeeds. The apps' go.mod say `go 1.25.0`; this digest carries 1.25.12.
#
# Each digest is an OCI **index** digest, and each names its own tag in
# `org.opencontainers.image.version`, which is how versions.lock.md's two rows
# were filled in rather than guessed:
#
#   docker buildx imagetools inspect <image>@<digest>
#     golang@sha256:ea34…  → 1.25.12-bookworm   (`go version` in it: go1.25.12)
#     debian@sha256:abd6…  → bookworm-slim      (/etc/debian_version: 12.15)
#
# The `golang:1.25-bookworm` TAG has since been rebuilt and today resolves to
# sha256:908f8ff2… instead. That is not a problem to fix, it is the reason §5.2
# says digest and not tag: the build below still gets the compiler these apps
# were audited against, and a re-run six weeks from now still will.
ARG GO_IMAGE=golang@sha256:ea341baa9bd5ba6784f6d7161ace70544349a6242d54d34a0fbfd2c4d51c9d58
ARG RUNTIME_IMAGE=debian@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241

# --------------------------------------------------------------------- build --
FROM ${GO_IMAGE} AS build
ARG APP
WORKDIR /src
COPY . .

# One image, three apps, selected exactly as next.Dockerfile's `--build-arg
# APP=` selects one of three workspaces. A missing or unknown APP fails HERE,
# with the list, rather than producing an image whose ENTRYPOINT is not there.
RUN case "${APP}" in \
      counter|chat|dashboard) : ;; \
      *) echo "build-arg APP must be counter | chat | dashboard, got '${APP}'" >&2; exit 2 ;; \
    esac

# CGO_ENABLED=0 so the runtime stage needs no libc of its own and the binary is
# the whole application. -trimpath removes the build machine's paths, and
# -buildvcs=false because .git is not in the build context (see
# gotth.Dockerfile.dockerignore) and a VCS stamp that is present in one checkout
# and absent in another is a binary that differs for a reason that is not source.
#
# NOT stripped. `-ldflags=-s -w` would be deterministic and would cost nothing
# in reproducibility, but it would also remove the symbols a profile needs, and
# §3.6's secondary runtime-internal figures (deviation D-8) are still open on
# both stacks. An image built to make the numbers unobtainable is not the image
# to be holding when somebody asks for them.
RUN cd bench/apps/${APP}/gotth \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -o /out/bench-app .

# The two files the apps read from a path at run time, staged where the runtime
# stage can take them in one COPY. The third — §2.5's fixture — is deliberately
# NOT here: docker/compose.yaml mounts ../fixtures read-only at /bench/fixtures
# on BOTH stacks, so "both servers read the same bytes" is a property of the
# topology rather than of two Dockerfiles agreeing.
#
# The SHA-256 print is next.Dockerfile's, by the same method and for the same
# reason: §2.0 requires ONE shim file, byte-identical, served by both stacks,
# and the digest in the build log is what a run manifest is checked against. It
# is `sha256sum` here and `node scripts/sync-shim.mjs` there because this image
# has no node — the same number, computed two ways, which is exactly what makes
# the two build logs comparable.
#
# The HTMX bundle is staged for all three apps even though only the dashboard
# serves it (§2.4 gives region E to plain HTMX on this stack, AS-3). 48 KB of
# image that counter and chat never open keeps the recipe `ARG APP`-uniform;
# §3.5 counts bytes SERVED, so an unopened file is not on anybody's D1 row.
RUN mkdir -p /out/bench/harness /out/bench/vendor /out/bench/fixtures \
 && cp bench/harness/shim.js /out/bench/harness/shim.js \
 && cp test/internal/conformance/testdata/htmx-2.0.10.min.js /out/bench/vendor/htmx-2.0.10.min.js \
 && echo "bench: §2.0 shim and §2.4 HTMX bundle, as baked into this image:" \
 && sha256sum /out/bench/harness/shim.js /out/bench/vendor/htmx-2.0.10.min.js

# ------------------------------------------------------------------- runtime --
FROM ${RUNTIME_IMAGE} AS runtime
ARG APP
ARG VARIANT=sse

# BENCH_VARIANT is accepted and recorded and changes nothing on this side: the
# gotth-live transport is one WebSocket at the app's mount path, so there is no
# sse/ws/poll fork to take. It is set so the two images answer the same question
# the same way when the manifest reads their environment back.
ENV BENCH_APP=${APP} \
    BENCH_VARIANT=${VARIANT} \
    BENCH_HOST=0.0.0.0 \
    PORT=3000 \
    BENCH_SHIM_PATH=/bench/harness/shim.js \
    BENCH_HTMX_PATH=/bench/vendor/htmx-2.0.10.min.js \
    BENCH_FIXTURE_DIR=/bench/fixtures

# BENCH_ORIGIN is NOT defaulted here. In the topology the browser's origin is
# the PROXY's `https://127.0.0.1:<BENCH_PROXY_PORT>`, which is a property of the
# host's docker/.env and not of this image; baking a port in would be an image
# that works until somebody moves the port and then 403s every upgrade with a
# stale allowlist and nothing in the log to say so. compose.yaml passes it, the
# app prints the allowlist it ended up with at startup, and getting it wrong is
# a refused WebSocket rather than a wrong number.

COPY --from=build /out/bench /bench
COPY --from=build /out/bench-app /usr/local/bin/bench-app

# Unprivileged, numerically: 65534 is Debian's `nobody`, spelled as a number so
# it does not depend on a passwd entry. Nothing in here needs root — the binary
# binds :3000, which is above the privileged range — and a bench container is
# still a container. /proc/net/tcp and /sys/fs/cgroup/* are world-readable, so
# the harness's two `docker exec` reads work as this user.
USER 65534:65534

# ONE port. gotth-live has no WebSocket sidecar: its WS lives at the app's own
# mount path on this same port (/counter/live, /chat/live, /dashboard/live),
# where the Next.js side's §5.4 `ws` variant runs a second process on 3101. So a
# gotth run sets BENCH_UPSTREAM_WS=app:3000 in docker/.env — an env value, not a
# second Caddyfile. docker/Caddyfile is one file serving both stacks (§5.2) and
# it stays that way: `handle /ws*` never matches a gotth mount path, the mount
# path falls to the final `handle`, and Caddy's reverse_proxy hijacks a 101 into
# a bidirectional stream there exactly as it does under `@stream` — flush_interval
# governs buffered responses and an upgraded connection is not one.
EXPOSE 3000

ENTRYPOINT ["/usr/local/bin/bench-app"]
