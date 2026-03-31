# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG DEBIAN_SUITE=bookworm
ARG CODEX_VERSION=0.117.0

FROM golang:${GO_VERSION}-${DEBIAN_SUITE} AS builder

ARG MAESTRO_VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY skills ./skills

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${MAESTRO_VERSION}" -o /out/maestro ./cmd/maestro

FROM debian:${DEBIAN_SUITE}-slim

ARG MAESTRO_VERSION=dev
ARG VCS_REF=unknown
ARG CODEX_VERSION

LABEL org.opencontainers.image.title="Maestro" \
      org.opencontainers.image.description="Local-first orchestration runtime for agent-driven software work" \
      org.opencontainers.image.source="https://github.com/olhapi/maestro" \
      org.opencontainers.image.url="https://github.com/olhapi/maestro" \
      org.opencontainers.image.version="${MAESTRO_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}"

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates git nodejs npm \
    && npm install -g "@openai/codex@${CODEX_VERSION}" \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data

WORKDIR /data

COPY --from=builder /out/maestro /usr/local/bin/maestro

USER root

ENTRYPOINT ["maestro"]
CMD ["run", "--db", "/data/maestro.db"]
