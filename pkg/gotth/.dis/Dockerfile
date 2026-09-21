FROM golang:1.26

LABEL dis.shell="/bin/bash"

# The library toolchain image.
#
# Deliberately absent: node and npm. That absence is the mechanism, not a
# preference — it is what makes "a clean clone builds and runs with no node,
# no npm and no protoc installed" a property of the file layout
# rather than a promise somebody has to keep. Node lives in
# .dis/Dockerfile.bench and nowhere else in this project.

RUN apt-get update \
    && apt-get install -y --no-install-recommends git curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# protoc + well-known-type .proto includes.
ARG PROTOC_VERSION=35.1
RUN curl -sSL -o /tmp/protoc.zip \
        https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip \
    && unzip -q /tmp/protoc.zip -d /usr/local bin/protoc 'include/*' \
    && rm /tmp/protoc.zip \
    && chmod +x /usr/local/bin/protoc

# Go protobuf codegen and the templ compiler. Versions match the dependency
# ledger: protoc-gen-go is the same version as the runtime the generated code
# imports, and the templ CLI is the same version as the templ runtime.
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 \
    && go install github.com/a-h/templ/cmd/templ@v0.3.1020

# staticcheck, pinned. The toolchain criterion names it alongside gofmt, vet
# and the race detector, and a criterion the project's own image cannot run is
# a criterion nobody was ever going to check — which is how it went unrun until
# QA-1 installed it by hand to produce a gate report.
RUN go install honnef.co/go/tools/cmd/staticcheck@2025.1.1

# protoc-gen-liquidproto is NOT installed here. It is built from the canonical
# pkg/liquidproto source by gen.sh in the mounted checkout, so the plugin that
# generates the committed code is always the plugin committed alongside it.
# That source is a sibling package in the same module, so generation needs the
# EXPORT ROOT mounted — the Go module root, one level above pkg/ — rather than
# just this directory. From that root:
#
#     docker run --rm -v "$PWD:/workspace" \
#         -w /workspace dis-gotth-live:latest bash pkg/gotth/gen.sh
#
WORKDIR /workspace
