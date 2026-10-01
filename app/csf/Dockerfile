# Build from the public module root: docker build -f app/csf/Dockerfile -t csf:local .
FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY app/ ./app/
COPY csf/ ./csf/
COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY services/ ./services/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/csf ./app/csf/cmd

FROM node:22-bookworm@sha256:8a34c4ab3ea2c5cd194f07e317b2a8f09461d3c8b05c4e34c8ccd56d56024c4d AS ui
WORKDIR /src/services/copilot-adapter/ui
COPY services/copilot-adapter/ui/package*.json ./
RUN npm ci
COPY services/copilot-adapter/ /src/services/copilot-adapter/
COPY pkg/widget/ /src/pkg/widget/
RUN VITE_CSF_DASHBOARD_URL=/ npm run build

FROM node:22-bookworm@sha256:8a34c4ab3ea2c5cd194f07e317b2a8f09461d3c8b05c4e34c8ccd56d56024c4d AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git git-lfs openssh-client \
    && git lfs install --system --skip-repo \
    && git lfs version \
    && rm -rf /var/lib/apt/lists/*

ARG TARGETARCH
ARG COPILOT_CLI_VERSION=1.0.86
ARG COPILOT_CLI_LINUX_X64_SHA256=ea4a519d7b2ff54e9c7d10ae9921cf4ab3f5041489ec340852f47f4f63fc535b
ARG COPILOT_CLI_LINUX_ARM64_SHA256=3c0e1f939931ddf19cf4d6a2d77922afb937dbfa8d373fe623b8befab5bd7fdb
RUN case "${TARGETARCH}" in \
        amd64) copilot_arch=x64; copilot_sha256="${COPILOT_CLI_LINUX_X64_SHA256}" ;; \
        arm64) copilot_arch=arm64; copilot_sha256="${COPILOT_CLI_LINUX_ARM64_SHA256}" ;; \
        *) printf 'unsupported Copilot CLI architecture: %s\n' "${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL -o /tmp/copilot.tar.gz \
      "https://github.com/github/copilot-cli/releases/download/v${COPILOT_CLI_VERSION}/copilot-linux-${copilot_arch}.tar.gz" \
    && printf '%s  %s\n' "${copilot_sha256}" /tmp/copilot.tar.gz | sha256sum -c - \
    && tar -xzf /tmp/copilot.tar.gz -C /usr/local/bin copilot \
    && rm /tmp/copilot.tar.gz \
    && chmod 0755 /usr/local/bin/copilot \
    && copilot --version

COPY --from=build /out/csf /usr/local/bin/csf
COPY --from=ui /src/services/copilot-adapter/ui/dist /app/workbench-ui
RUN ln -s /usr/local/bin/csf /app/candace-runtime
WORKDIR /workspace
ENV COPILOT_CLI_PATH=/usr/local/bin/copilot
USER node
EXPOSE 14111
ENTRYPOINT ["/usr/local/bin/csf"]
CMD ["serve", "--listen", "0.0.0.0:14111", "--workbench-ui", "/app/workbench-ui"]
