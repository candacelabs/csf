FROM bufbuild/buf:1.72.0@sha256:65bd496a89c762ad7151ca9e7d885a45dacb3671a8e8ec39738b9f844d3405ea AS googleapis

# Match the immutable googleapis module commit in grpc-gateway v2.30.0's
# checked-in buf.lock. grpc-gateway does not ship these source protos.
RUN buf export buf.build/googleapis/googleapis:62f35d8aed1149c291d606d958a7ce32 \
      --path google/api/annotations.proto \
      --path google/api/http.proto \
      --path google/api/field_behavior.proto \
      --output /googleapis

FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599

ARG PROTOC_VERSION=35.1
ARG PROTOC_LINUX_X86_64_SHA256=6930ebf62bd4ea607b98fff052596c6ee564b9835b4ce172c75a3f53ae9d91b7

RUN apt-get update \
    && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL -o /tmp/protoc.zip \
      "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip" \
    && echo "${PROTOC_LINUX_X86_64_SHA256}  /tmp/protoc.zip" | sha256sum -c - \
    && unzip -q /tmp/protoc.zip -d /usr/local bin/protoc 'include/*' \
    && rm /tmp/protoc.zip \
    && chmod 0755 /usr/local/bin/protoc

RUN GOTOOLCHAIN=local go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 \
    && GOTOOLCHAIN=local go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2 \
    && GOTOOLCHAIN=local go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv3@v2.30.0

COPY --from=googleapis /googleapis /opt/googleapis

ENV GOTOOLCHAIN=local
ENV PATH="/go/bin:${PATH}"

WORKDIR /workspace
