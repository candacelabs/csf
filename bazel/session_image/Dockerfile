# The session image: the pinned build image (bazel/execution_image.txt) plus
# what a harness session runs inside it — the executor CLIs (Claude Code and
# the GitHub Copilot CLI, whose standalone binary is npm's
# @github/copilot-linux-x64 package), gh, jq, Go and the
# csf binary the session gates call. Every addition is pinned by version and
# checksum or by digest. Build it with bazel/session_image/build.sh, which
# passes BUILD_IMAGE from execution_image.txt and records the result in
# bazel/session_image.txt.
ARG BUILD_IMAGE

FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY app/ ./app/
COPY csf/ ./csf/
COPY io/ ./io/
COPY ipc/ ./ipc/
COPY pkg/ ./pkg/
COPY proto/ ./proto/
COPY runtime/ ./runtime/
COPY services/ ./services/
COPY web/ ./web/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/csf ./app/csf/cmd

FROM ${BUILD_IMAGE}
USER root
ADD --checksum=sha256:fe503f65c6289d59c23e5b21ae44f03583f997dd33a2cbfc75ab4f96fb8fc73f \
    https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/2.1.286/linux-x64/claude \
    /usr/local/bin/claude
ADD --checksum=sha256:ffb3399216bccc7295cf2e7c579e6f161648fe355f6ec5eeea96ae736c1f30b1 \
    https://registry.npmjs.org/@github/copilot-linux-x64/-/copilot-linux-x64-1.0.90.tgz \
    /tmp/copilot.tgz
ADD --checksum=sha256:79e89a14af6fc69163aee00e764e86d5809d0c6c77e6f229aebe7a4ed115ee67 \
    https://github.com/cli/cli/releases/download/v2.45.0/gh_2.45.0_linux_amd64.tar.gz \
    /tmp/gh.tar.gz
ADD --checksum=sha256:5942c9b0934e510ee61eb3e30273f1b3fe2590df93933a93d7c58b81d19c8ff5 \
    https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64 \
    /usr/local/bin/jq
COPY --from=go /usr/local/go /usr/local/go
COPY --from=go /out/csf /usr/local/bin/csf
# The session runs as 1000:1000 with no-new-privileges, so sudo could not
# elevate anyway; the base image's sudo membership is removed all the same.
# /etc/csf-session-image is the marker tools/bazel.sh reads to run Bazel
# directly instead of starting a container. Go telemetry is off and the
# version checks' leftovers under /root are removed: telemetry counters carry
# the build date, which made two builds of one tree differ.
ENV GOTELEMETRY=off
RUN chmod 0755 /usr/local/bin/claude /usr/local/bin/jq \
    && tar -xzf /tmp/gh.tar.gz -C /tmp gh_2.45.0_linux_amd64/bin/gh \
    && install -m 0755 /tmp/gh_2.45.0_linux_amd64/bin/gh /usr/local/bin/gh \
    && rm -rf /tmp/gh.tar.gz /tmp/gh_2.45.0_linux_amd64 \
    && tar -xzf /tmp/copilot.tgz -C /tmp package/copilot \
    && install -m 0755 /tmp/package/copilot /usr/local/bin/copilot \
    && rm -rf /tmp/copilot.tgz /tmp/package \
    && gpasswd -d ubuntu sudo \
    && printf 'csf session image\n' > /etc/csf-session-image \
    && claude --version && copilot --version && gh --version && jq --version && /usr/local/go/bin/go version && csf 2>&1 | grep -q usage \
    && rm -rf /root/.config /root/.cache /root/.claude /root/.claude.json /root/.local /root/.copilot
ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    GOTOOLCHAIN=local \
    DISABLE_AUTOUPDATER=1
USER 1000:1000
ENTRYPOINT []
CMD ["sleep", "infinity"]
