# The Next.js side's measured container (equivalence-spec §5.1, §3.6).
#
#   docker build -f docker/next.Dockerfile --build-arg APP=dashboard \
#     --build-arg VARIANT=sse -t gotth-live-bench/dashboard-next:sse .
#
# built from the bench root, because the workspace lockfile and the shared
# harness/shim.js both live there and both belong in the image.
#
# WHAT THIS IMAGE DOES NOT CONTAIN, and why each absence is a spec requirement:
#
#   no TLS key, no certificate, no TLS listener   §3.6's boundary. TLS is
#       terminated in the proxy container, on both stacks. Terminating it inside
#       one stack's container and outside the other's is a disqualifying method
#       error in either direction — worth ~18,000 B/session, and the direction
#       it swings is a choice that would otherwise be available after seeing the
#       number (T-21). harness/assert-no-tls.mjs proves the absence from
#       outside; this file is why there is nothing to find.
#
#   no source maps                                §3.5 excludes them as "not
#       served in production on either side"; next.config.ts sets
#       productionBrowserSourceMaps: false and the standalone output carries
#       none.
#
#   no dev server, no HMR runtime                 §5.4's audit checklist. The
#       entrypoint is the standalone server; `next dev` is not installed in the
#       runtime stage at all.
#
#   no npm at runtime                             the runtime stage copies the
#       traced standalone tree and nothing else, so the container cannot fetch
#       anything even if something asked it to.

ARG NODE_IMAGE=node@sha256:da4221677e02b54ef6335adfa447578d512ad14f251024fb92ea433c2c102760

# ---------------------------------------------------------------------- deps --
FROM ${NODE_IMAGE} AS deps
WORKDIR /bench
ENV NEXT_TELEMETRY_DISABLED=1
COPY package.json package-lock.json ./
COPY apps/counter/next/package.json apps/counter/next/package.json
COPY apps/chat/next/package.json apps/chat/next/package.json
COPY apps/dashboard/next/package.json apps/dashboard/next/package.json
# `npm ci` against the committed lockfile (FR-74): exact versions, no resolution
# at build time, and a build that cannot silently pick up a newer transitive.
RUN npm ci --no-audit --no-fund

# --------------------------------------------------------------------- build --
FROM ${NODE_IMAGE} AS build
WORKDIR /bench
ARG APP
ARG VARIANT=sse
ENV NEXT_TELEMETRY_DISABLED=1 NODE_ENV=production BENCH_VARIANT=${VARIANT}
COPY --from=deps /bench/node_modules ./node_modules
COPY . .
# sync-shim copies harness/shim.js into the app's public/ tree and prints its
# SHA-256. §2.0 requires ONE shim file, byte-identical, served by both stacks;
# the SHA in the build log is what the run manifest is checked against.
RUN node scripts/sync-shim.mjs apps/${APP}/next \
 && npm run build -w @gotth-live-bench/${APP}-next

# ------------------------------------------------------------------- runtime --
FROM ${NODE_IMAGE} AS runtime
WORKDIR /bench
ARG APP
ARG VARIANT=sse
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    BENCH_APP=${APP} \
    BENCH_VARIANT=${VARIANT} \
    BENCH_HOST=0.0.0.0 \
    PORT=3000

# The traced standalone tree, plus the two things `next build` deliberately does
# not put in it (public/ and .next/static are expected to be served by a CDN;
# we are not a CDN).
COPY --from=build /bench/apps/${APP}/next/.next/standalone/ ./
COPY --from=build /bench/apps/${APP}/next/.next/static ./apps/${APP}/next/.next/static
COPY --from=build /bench/apps/${APP}/next/public ./apps/${APP}/next/public
# The launcher and the ws sidecar. §5.4's secondary variant runs the sidecar as
# a second process in the SAME container, so §3.6 counts its RSS in M(x) — which
# is what a Next.js team actually pays for a WebSocket.
COPY --from=build /bench/scripts ./scripts
COPY --from=build /bench/apps/${APP}/next/ws-server ./apps/${APP}/next/ws-server
COPY --from=build /bench/node_modules/ws ./node_modules/ws

# Unprivileged: nothing in here needs root, and a bench container is still a
# container.
USER node

EXPOSE 3000 3101
ENTRYPOINT ["node", "scripts/start-app.mjs"]
CMD ["apps/dashboard/next"]
