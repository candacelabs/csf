# warden — candacenet fleet watchdog.
#
# Build context is the Go module root, the candace/ export root:
#
#   docker build -f app/warden/Dockerfile \
#     --build-arg VERSION="$(git describe --tags --always --dirty)" \
#     -t warden:latest candace/
#
# Multi-stage: a full Go toolchain compiles a static binary; the runtime image
# is distroless/static (nonroot) — no shell, no package manager, minimal
# attack surface. The binary is CGO-free and statically linked, so distroless
# static is the correct, smallest base.

# ---- builder --------------------------------------------------------------
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

# git is needed only if the build ever computes a version from the tree; the
# VERSION build-arg is the primary source. Kept minimal.
WORKDIR /src

# Prime the module cache separately so dependency downloads are cached across
# source-only changes.
COPY go.mod go.sum ./
RUN go mod download

# Bring in the compiled trees (the warden app imports pkg/core and the
# in-repo contract package).
COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/ ./services/
COPY app/ ./app/

ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod

RUN go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/warden \
    ./app/warden/cmd

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Election state persists here; declare it a volume so it survives restarts.
VOLUME ["/var/lib/warden"]

# Single HTTP port: cluster RPCs + dashboard + /api/status + /metrics.
EXPOSE 7717

COPY --from=builder /out/warden /warden

# distroless :nonroot already runs as uid 65532; make it explicit.
USER nonroot:nonroot

ENTRYPOINT ["/warden"]
