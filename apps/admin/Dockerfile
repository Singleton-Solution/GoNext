# syntax=docker/dockerfile:1.7
#
# apps/admin — GoNext admin dashboard (Next.js)
#
# Status: skeleton. The current pnpm scripts only echo placeholders;
# this Dockerfile is wired so that once the real Next.js app lands
# (subsequent issues), `docker build` produces a runnable image with no
# Dockerfile changes — only the entrypoint shifts from `pnpm start` to
# `node server.js` once `output: 'standalone'` is set in next.config.js.
#
# Build from the repo root:
#   docker build -f apps/admin/Dockerfile -t gonext-admin:dev .
#
# See docs/09-deployment-ops.md §2.3 for the canonical multi-target
# Dockerfile design; this file is the per-app variant scoped to issue #49.

ARG NODE_VERSION=22-alpine

# ---------- Stage 1: deps ----------
# Install workspace dependencies with the pnpm lockfile cache layer.
FROM node:${NODE_VERSION} AS deps
WORKDIR /repo

# Enable corepack so the pinned pnpm version from package.json is used.
RUN corepack enable

# Copy only the manifests needed to resolve the workspace graph. This keeps
# the deps layer cacheable across source-only changes.
COPY package.json pnpm-workspace.yaml ./
# Lockfile is optional today (the repo may not yet have one committed); when
# present, `--frozen-lockfile` will be honored, otherwise pnpm falls back to
# a regular install. The wildcard makes COPY tolerant of either case.
COPY pnpm-lock.yaml* ./

# Workspace package manifests — copy each so pnpm can build the dependency
# graph without pulling in the rest of the source.
COPY apps/web/package.json apps/web/
COPY apps/admin/package.json apps/admin/
COPY packages/ts/ packages/ts/

# Install. Prefer frozen lockfile; gracefully fall back if absent.
RUN if [ -f pnpm-lock.yaml ]; then \
      pnpm install --frozen-lockfile; \
    else \
      pnpm install; \
    fi

# ---------- Stage 2: builder ----------
# Copy full source and produce the build artifacts for apps/admin.
FROM node:${NODE_VERSION} AS builder
WORKDIR /repo
RUN corepack enable

# Reuse the resolved install state from the deps stage. pnpm keeps the real
# package contents in /repo/node_modules/.pnpm and exposes the hoisted set
# via /repo/node_modules; per-workspace node_modules directories are created
# lazily and only when a workspace actually declares runtime deps. Today the
# placeholder apps/web and apps/admin manifests declare none, so those
# per-workspace directories may not exist — copying the entire deps state
# (the whole /repo tree, which at this point contains only the manifests +
# node_modules layout pnpm produced) keeps this Dockerfile correct both
# today and after the apps get filled in.
COPY --from=deps /repo /repo

# Overlay workspace manifests and source on top of the deps state. The
# source COPYs intentionally come after the deps copy so they win on
# conflict (e.g. when a source file replaces something pnpm wrote).
COPY package.json pnpm-workspace.yaml ./
COPY pnpm-lock.yaml* ./
COPY apps/web/ apps/web/
COPY apps/admin/ apps/admin/
COPY packages/ts/ packages/ts/

# Build-time configuration plumbing for Next.js public env vars.
ARG NEXT_PUBLIC_API_URL=""
ARG NEXT_PUBLIC_VERSION="dev"
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL} \
    NEXT_PUBLIC_VERSION=${NEXT_PUBLIC_VERSION} \
    NEXT_TELEMETRY_DISABLED=1 \
    NODE_ENV=production

# Build just the @gonext/admin target. While the package.json `build` is still
# a placeholder echo this is a no-op; once Next.js is in place the same
# command produces .next/standalone (when next.config.js sets
# `output: 'standalone'`).
RUN pnpm --filter @gonext/admin run build

# ---------- Stage 3: runner ----------
# Minimal runtime image. Runs as the unprivileged `node` user shipped with
# the official Alpine images (uid/gid 1000) — no need to provision one.
FROM node:${NODE_VERSION} AS runner
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

# wget is part of busybox in alpine and is used by HEALTHCHECK below.
# No extra packages are installed in the final image.

# Copy build outputs and runtime manifests. The pnpm CLI is enabled via
# corepack so `pnpm start` works without a global install.
RUN corepack enable

# Copy the built workspace. node_modules is included so `next start` (or the
# standalone server, once enabled) can resolve dependencies. When the app
# adopts `output: 'standalone'`, swap the COPYs below for the standalone
# bundle and drop node_modules.
COPY --from=builder --chown=node:node /repo/package.json /repo/pnpm-workspace.yaml ./
COPY --from=builder --chown=node:node /repo/pnpm-lock.yaml* ./
COPY --from=builder --chown=node:node /repo/node_modules ./node_modules
COPY --from=builder --chown=node:node /repo/apps/admin ./apps/admin
COPY --from=builder --chown=node:node /repo/packages/ts ./packages/ts

USER node

EXPOSE 3000

# Liveness probe. Uses busybox wget; tolerates non-200 (placeholder app
# currently echoes to stdout and may not bind a port — once the real Next.js
# server lands this healthcheck flips to a meaningful signal).
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O- --tries=1 --timeout=4 http://127.0.0.1:3000/ >/dev/null 2>&1 || exit 1

# OCI metadata. Values can be overridden at build time via --build-arg.
ARG VERSION="0.0.0"
ARG COMMIT="unknown"
ARG BUILD_DATE="unknown"
LABEL org.opencontainers.image.title="gonext-admin" \
      org.opencontainers.image.description="GoNext admin dashboard (Next.js)." \
      org.opencontainers.image.source="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.url="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.documentation="https://github.com/Singleton-Solution/GoNext/blob/main/docs/09-deployment-ops.md" \
      org.opencontainers.image.licenses="SEE LICENSE IN LICENSE" \
      org.opencontainers.image.vendor="Singleton-Solution" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# Runtime entrypoint.
#
# The container must be runnable today (placeholder apps/admin/package.json
# has no `start` script and no real Next.js server) AND keep working once
# the real Next.js app lands. We resolve this with a small shell fallback
# chain rather than hard-coding `pnpm start`:
#
#   1. If apps/admin/server.js exists (the artifact `next build` emits
#      when next.config.js sets `output: 'standalone'`), run it under node
#      — this is the long-term steady state.
#   2. Else if `next` is resolvable from apps/admin, exec `next start`
#      from that workspace — the pre-standalone runtime.
#   3. Else fall back to a harmless message so `docker run` exits 0 with
#      a clear signal that the placeholder app is what's shipped.
#
# When `output: 'standalone'` lands this fallback collapses to step (1)
# with no Dockerfile change needed.
ENTRYPOINT ["/bin/sh", "-c"]
CMD ["if [ -f apps/admin/server.js ]; then exec node apps/admin/server.js; elif pnpm --filter @gonext/admin exec -- node -e \"require.resolve('next/package.json')\" >/dev/null 2>&1; then exec pnpm --filter @gonext/admin exec -- next start -p \"${PORT:-3000}\" -H \"${HOSTNAME:-0.0.0.0}\"; else echo '@gonext/admin: placeholder image (no Next.js server yet).'; fi"]
