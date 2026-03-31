# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG DEBIAN_SUITE=bookworm
ARG ALPINE_VERSION=3.20
ARG ALPINE_DIGEST=sha256:a4f4213abb84c497377b8544c81b3564f313746700372ec4fe84653e4fb03805
ARG CODEX_VERSION=0.117.0

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-${DEBIAN_SUITE} AS maestro-build

ARG MAESTRO_VERSION=dev
ARG TARGETOS
ARG TARGETARCH

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
    GOOS="${TARGETOS:-linux}" \
    GOARCH="${TARGETARCH:-$(go env GOARCH)}" \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=${MAESTRO_VERSION}" -o /out/maestro ./cmd/maestro

FROM --platform=$BUILDPLATFORM node:24-${DEBIAN_SUITE}-slim AS codex-fetch

ARG CODEX_VERSION
ARG TARGETARCH

WORKDIR /tmp/codex

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) openai_arch="x64"; codex_triple="x86_64-unknown-linux-musl" ;; \
      arm64) openai_arch="arm64"; codex_triple="aarch64-unknown-linux-musl" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tarball="$(npm pack "@openai/codex@${CODEX_VERSION}-linux-${openai_arch}")"; \
    tar -xzf "${tarball}"; \
    install -D -m 0755 "package/vendor/${codex_triple}/codex/codex" /out/codex

FROM alpine:${ALPINE_VERSION}@${ALPINE_DIGEST}

ARG MAESTRO_VERSION=dev
ARG VCS_REF=unknown

LABEL org.opencontainers.image.title="Maestro" \
      org.opencontainers.image.description="Local-first orchestration runtime for agent-driven software work" \
      org.opencontainers.image.source="https://github.com/olhapi/maestro" \
      org.opencontainers.image.url="https://github.com/olhapi/maestro" \
      org.opencontainers.image.version="${MAESTRO_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}"

RUN apk add --no-cache ca-certificates git ripgrep \
    && mkdir -p /data

WORKDIR /data

COPY --from=maestro-build /out/maestro /usr/local/bin/maestro
COPY --from=codex-fetch /out/codex /usr/local/bin/codex

USER root

ENTRYPOINT ["maestro"]
CMD ["run", "--db", "/data/maestro.db"]
