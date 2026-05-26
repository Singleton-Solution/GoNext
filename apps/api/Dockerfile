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

# IMAGEPROC_BACKEND must be declared BEFORE the first FROM so it is a
# global ARG visible to the `FROM runtime-${IMAGEPROC_BACKEND}` stage
# selector at the bottom. Per-stage ARG re-declarations below still
# work for build-time use; this top-level declaration is the one
# Docker reads when resolving the dynamic stage name.
ARG IMAGEPROC_BACKEND=stdlib

# ---------- Stage 1: build ----------
FROM golang:1.25-alpine AS build

# IMAGEPROC_BACKEND selects between the pure-Go default (`stdlib`) and
# the libvips-backed `govips` build of the on-the-fly image proxy
# (packages/go/media/imgproxy). `stdlib` keeps the runtime stage
# distroless/static and CGO-free — the right default for the small
# binary, fast cold-start path. `govips` pulls in libvips at runtime
# for the faster + higher-quality variant pipeline; it requires CGO
# and a Debian-based runtime so libvips dynlinks resolve.
ARG IMAGEPROC_BACKEND=stdlib

# Install build-time tools. ca-certificates and tzdata are pulled in here
# so the runtime stage can copy them over (distroless/static does ship
# ca-certificates, but we keep the COPY explicit for clarity).
#
# The govips build adds vips-dev + the gcc toolchain so cgo can compile
# the wrapper. We branch on $IMAGEPROC_BACKEND so the stdlib image
# remains tiny and free of compiler debris.
RUN apk add --no-cache git ca-certificates tzdata && \
    if [ "$IMAGEPROC_BACKEND" = "govips" ]; then \
      apk add --no-cache build-base vips-dev pkgconfig; \
    fi

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

# CGO + build tags are conditional on IMAGEPROC_BACKEND. The stdlib
# branch keeps CGO_ENABLED=0 so the runtime stays distroless/static;
# the govips branch flips CGO_ENABLED=1 and adds the `vips` tag so
# the transform_govips.go file is compiled in.
ENV GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    GOWORK=off

# Build the api binary from inside the module directory so the
# `replace` directive in apps/api/go.mod resolves ../../packages/go.
# GOWORK=off keeps the build module-scoped and reproducible.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/api && \
    if [ "$IMAGEPROC_BACKEND" = "govips" ]; then \
      export CGO_ENABLED=1 && export BUILD_TAGS="vips,netgo,osusergo"; \
    else \
      export CGO_ENABLED=0 && export BUILD_TAGS="netgo,osusergo"; \
    fi && \
    go build \
      -trimpath \
      -tags "$BUILD_TAGS" \
      -ldflags "-s -w \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Version=${VERSION}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Commit=${COMMIT}' \
        -X 'github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Date=${BUILD_DATE}'" \
      -o /out/api \
      ./cmd/server

# ---------- Stage 2a: runtime (stdlib backend, distroless static) ----------
#
# The default runtime. Pure-Go binary; no libvips on the runtime image.
# The smaller surface and absence of a libc make this the right base
# for the stdlib build. Selected when IMAGEPROC_BACKEND is stdlib
# (the default).
FROM gcr.io/distroless/static-debian12:nonroot AS runtime-stdlib
COPY --from=build /out/api /app/api

# ---------- Stage 2b: runtime (govips backend, debian-slim + libvips) ----------
#
# The govips runtime needs libvips at link time. distroless/cc-debian12
# would work but we want explicit control over the libvips package
# version, so we use debian:bookworm-slim and apt-get just the dynamic
# library + a single user setup. The runtime is still small (~80 MB)
# but markedly larger than the static image.
FROM debian:bookworm-slim AS runtime-govips
RUN apt-get update && apt-get install -y --no-install-recommends \
        libvips42 ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 65532 nonroot \
    && useradd -u 65532 -g 65532 -s /sbin/nologin nonroot
COPY --from=build /out/api /app/api

# ---------- Stage 3: final runtime selector ----------
#
# The two runtime stages above are mutually exclusive — Docker builds
# them both but only one is tagged as `runtime`. We pick via a build
# arg that re-uses IMAGEPROC_BACKEND so a single `--build-arg
# IMAGEPROC_BACKEND=govips` flips both the build flags and the final
# image base.
ARG IMAGEPROC_BACKEND=stdlib
FROM runtime-${IMAGEPROC_BACKEND} AS runtime

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

# Binary already COPYed in the runtime-stdlib / runtime-govips stages
# above so the layers are cached per-base; we don't need to re-COPY here.
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
