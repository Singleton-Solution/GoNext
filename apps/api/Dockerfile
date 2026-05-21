# syntax=docker/dockerfile:1.7
#
# Multi-stage Dockerfile for the GoNext API server (apps/api/cmd/server).
#
# Build from the repository root:
#
#   docker build \
#     -f apps/api/Dockerfile \
#     --build-arg VERSION=$(git describe --tags --always) \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t ghcr.io/singleton-solution/gonext-api:dev .
#
# Layer story (see docs/09-deployment-ops.md §2.2):
#   1. golang:1.25-alpine builder with module cache mount
#   2. distroless/static-debian12:nonroot runtime, ~25-30 MB
#
# The Go workspace at the repo root (go.work) references this module plus
# packages/go and the sibling apps/cli modules. apps/api/go.mod additionally
# declares a `replace` for packages/go pointing at ../../packages/go, so the
# build context MUST be the repo root — we need go.work, packages/go, and
# apps/api visible in /src for `go build` to resolve the workspace.

# ---------- Stage 1: build ----------
FROM golang:1.25-alpine AS build

# Install build-time tools. ca-certificates and tzdata are pulled in here
# so the runtime stage can copy them over (distroless/static does ship
# ca-certificates, but we keep the COPY explicit for clarity).
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy just the slice of the workspace this module needs. We deliberately
# skip go.work here — apps/api/go.mod already declares a `replace` for
# packages/go pointing at ../../packages/go, and copying go.work without
# the sibling modules (worker, cli) would cause go to fail with
# "cannot load module ../worker listed in go.work file".
COPY packages/go ./packages/go
COPY apps/api ./apps/api

# Build-time identity. Stamped into packages/go/buildinfo via -ldflags.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    GOWORK=off

# Build the api binary from inside the module directory so the
# `replace` directive in apps/api/go.mod resolves ../../packages/go.
# GOWORK=off keeps the build module-scoped and reproducible.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/api && \
    go build \
      -trimpath \
      -tags "netgo,osusergo" \
      -ldflags "-s -w \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Version=${VERSION}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Commit=${COMMIT}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Date=${BUILD_DATE}'" \
      -o /out/api \
      ./cmd/server

# ---------- Stage 2: runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="gonext-api" \
      org.opencontainers.image.description="GoNext HTTP API server" \
      org.opencontainers.image.source="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.url="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.documentation="https://github.com/Singleton-Solution/GoNext/blob/main/docs/09-deployment-ops.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Singleton Solution" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

COPY --from=build /out/api /app/api
# Seed theme content is embedded via go:embed (packages/go/theme/seed),
# so the seeder unpacks to GONEXT_THEME_DIR at boot — no in-image theme
# tree is required. Compose mounts a writable volume at /themes so the
# seeder can write to it under the distroless `nonroot` user (uid 65532).

# api: 8080 (HTTP), 9090 (metrics — wired up in a later issue).
EXPOSE 8080 9090

# Distroless ships a `nonroot` user (uid 65532). The base image already
# sets USER nonroot, but we re-state it for clarity.
USER nonroot:nonroot

# HEALTHCHECK: GoNext exposes /healthz once the readiness probes land
# (issue tracked separately). For now we declare the policy via comment
# and rely on the orchestrator (Kubernetes, Compose) to probe externally.
# When /healthz is implemented, uncomment:
#   HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
#     CMD ["/app/api", "healthcheck"]
# (distroless/static has no shell, so the check must be a binary subcommand
# or be defined at the orchestrator level.)

ENTRYPOINT ["/app/api"]
