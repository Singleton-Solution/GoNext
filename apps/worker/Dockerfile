# syntax=docker/dockerfile:1.7
#
# Multi-stage Dockerfile for the GoNext background worker
# (apps/worker/cmd/worker).
#
# Build from the repository root:
#
#   docker build \
#     -f apps/worker/Dockerfile \
#     --build-arg VERSION=$(git describe --tags --always) \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t ghcr.io/singleton-solution/gonext-worker:dev .
#
# See docs/09-deployment-ops.md §2.2 for the canonical layer story.
# The Go workspace at the repo root (go.work) plus apps/worker/go.mod's
# `replace` for packages/go requires the build context to be the repo
# root so go.work, packages/go, and apps/worker are all visible in /src.

# ---------- Stage 1: build ----------
# Pinned to a specific patch so govulncheck's stdlib coverage is reproducible
# across runs. Bumped from 1.25 → 1.25.10 to clear the stdlib CVEs that the
# `security-scan` CI job flagged (net/mail, html/template, net, crypto/x509,
# crypto/tls, net/http, os, net/url all have fixes in 1.25.8–1.25.10).
FROM golang:1.25.10-alpine AS build

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy just the slice of the workspace this module needs. We skip go.work
# on purpose — apps/worker/go.mod already declares a `replace` for
# packages/go, and including go.work without the sibling modules would
# fail with "cannot load module ../api listed in go.work file".
COPY packages/go ./packages/go
COPY apps/worker ./apps/worker

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    GOWORK=off

# Build module-scoped (GOWORK=off). The replace directive in
# apps/worker/go.mod resolves packages/go from ../../packages/go.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/worker && \
    go build \
      -trimpath \
      -tags "netgo,osusergo" \
      -ldflags "-s -w \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Version=${VERSION}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Commit=${COMMIT}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Date=${BUILD_DATE}'" \
      -o /out/worker \
      ./cmd/worker

# ---------- Stage 2: runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="gonext-worker" \
      org.opencontainers.image.description="GoNext background job runner (Asynq consumer)" \
      org.opencontainers.image.source="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.url="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.documentation="https://github.com/Singleton-Solution/GoNext/blob/main/docs/09-deployment-ops.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Singleton Solution" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

COPY --from=build /out/worker /app/worker

# Worker exposes only an internal metrics endpoint (wired up later).
EXPOSE 9090

USER nonroot:nonroot

# No HEALTHCHECK: the worker has no HTTP listener in the current
# skeleton. Once /metrics + a /healthz endpoint are added, wire a
# binary-subcommand healthcheck here (distroless has no shell).

ENTRYPOINT ["/app/worker"]
