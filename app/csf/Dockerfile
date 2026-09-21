FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local \
    go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/csf ./app/csf/cmd

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241 AS copilot
ADD --checksum=sha256:ea4a519d7b2ff54e9c7d10ae9921cf4ab3f5041489ec340852f47f4f63fc535b \
  https://github.com/github/copilot-cli/releases/download/v1.0.86/copilot-linux-x64.tar.gz /tmp/copilot.tar.gz
RUN mkdir -p /tmp/copilot /out && \
    tar -xzf /tmp/copilot.tar.gz -C /tmp/copilot && \
    copilot_binary="$(find /tmp/copilot -type f -name copilot -print -quit)" && \
    test -n "$copilot_binary" && install -m 0755 "$copilot_binary" /out/copilot

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash=5.2.15-2+b13 \
    ca-certificates \
    git=1:2.39.5-0+deb12u3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build --chown=65532:65532 /out/csf /usr/local/bin/csf
COPY --from=copilot --chown=65532:65532 /out/copilot /usr/local/bin/copilot
ENV COPILOT_CLI_PATH=/usr/local/bin/copilot
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/csf"]
