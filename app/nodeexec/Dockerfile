# Build from the Go module root (the candace/ export root):
#   docker build -f app/nodeexec/Dockerfile -t nodeexec:dev .

FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY ipc/ ./ipc/
COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/ ./services/
COPY web/ ./web/
COPY app/ ./app/

ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/nodeexec ./app/nodeexec/cmd

FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1

# docker-cli-compose supplies the v2 `docker compose` plugin. The agent is the
# sole Docker-socket holder, so a shell is not used by application execution.
RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose git \
    && mkdir -p /var/lib/nodeexec /var/lib/deploy/apps

VOLUME ["/var/lib/nodeexec"]
EXPOSE 8094

COPY --from=builder /out/nodeexec /usr/local/bin/nodeexec

# Docker socket access is host-root-equivalent. Runtime UID/GID policy belongs
# in the node's Compose declaration because the socket group varies by host.
ENTRYPOINT ["/usr/local/bin/nodeexec"]
