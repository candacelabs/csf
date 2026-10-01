# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599

RUN GOBIN=/usr/local/bin go install \
    github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0

ENTRYPOINT ["/usr/local/bin/oapi-codegen"]
