# syntax=docker/dockerfile:1.7
#
# Multi-stage Dockerfile for the `gonext` admin CLI (cli/gonext).
#
# Build from the repository root:
#
#   docker build \
#     -f cli/gonext/Dockerfile \
#     --build-arg VERSION=$(git describe --tags --always) \
#     --build-arg COMMIT=$(git rev-parse HEAD) \
#     --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t ghcr.io/singleton-solution/gonext-cli:dev .
#
# Usage (the CLI is interactive — wrap it in `docker run --rm -it`):
#
#   docker run --rm ghcr.io/singleton-solution/gonext-cli:dev version
#   docker run --rm ghcr.io/singleton-solution/gonext-cli:dev --help
#
# See docs/09-deployment-ops.md §2.2 for the canonical layer story.

# ---------- Stage 1: build ----------
FROM golang:1.24-alpine AS build

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy just the slice of the workspace this module needs. We skip go.work
# on purpose — cli/gonext/go.mod already declares a `replace` for
# packages/go, and including go.work without the sibling modules would
# fail with "cannot load module ../../apps/api listed in go.work file".
COPY packages/go ./packages/go
COPY cli/gonext ./cli/gonext

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
# cli/gonext/go.mod resolves packages/go from ../../packages/go.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd cli/gonext && \
    go build \
      -trimpath \
      -tags "netgo,osusergo" \
      -ldflags "-s -w \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Version=${VERSION}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Commit=${COMMIT}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Date=${BUILD_DATE}'" \
      -o /out/gonext \
      .

# ---------- Stage 2: runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="gonext-cli" \
      org.opencontainers.image.description="GoNext administrative CLI" \
      org.opencontainers.image.source="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.url="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.documentation="https://github.com/Singleton-Solution/GoNext/blob/main/docs/09-deployment-ops.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Singleton Solution" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

COPY --from=build /out/gonext /app/gonext

USER nonroot:nonroot

# No HEALTHCHECK: CLI is a short-lived process.

ENTRYPOINT ["/app/gonext"]
