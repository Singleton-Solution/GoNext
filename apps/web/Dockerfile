# syntax=docker/dockerfile:1.7
#
# apps/web — GoNext public site (Next.js App Router)
#
# Status: skeleton. The current pnpm scripts only echo placeholders;
# this Dockerfile is wired so that once the real Next.js app lands
# (issue #6 and follow-ups), `docker build` produces a runnable image
# with no Dockerfile changes — only the entrypoint shifts from
# `pnpm start` to `node server.js` once `output: 'standalone'` is set
# in next.config.js.
#
# Build from the repo root:
#   docker build -f apps/web/Dockerfile -t gonext-web:dev .
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
# Copy full source and produce the build artifacts for apps/web.
FROM node:${NODE_VERSION} AS builder
WORKDIR /repo
RUN corepack enable

# Reuse the resolved node_modules from the deps stage.
COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/apps/web/node_modules ./apps/web/node_modules
COPY --from=deps /repo/apps/admin/node_modules ./apps/admin/node_modules

# Bring in workspace manifests and source.
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

# Build just the @gonext/web target. While the package.json `build` is still
# a placeholder echo this is a no-op; once Next.js is in place the same
# command produces .next/standalone (when next.config.js sets
# `output: 'standalone'`).
RUN pnpm --filter @gonext/web run build

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
COPY --from=builder --chown=node:node /repo/apps/web ./apps/web
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
LABEL org.opencontainers.image.title="gonext-web" \
      org.opencontainers.image.description="GoNext public site renderer (Next.js)." \
      org.opencontainers.image.source="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.url="https://github.com/Singleton-Solution/GoNext" \
      org.opencontainers.image.documentation="https://github.com/Singleton-Solution/GoNext/blob/main/docs/09-deployment-ops.md" \
      org.opencontainers.image.licenses="SEE LICENSE IN LICENSE" \
      org.opencontainers.image.vendor="Singleton-Solution" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# When `output: 'standalone'` is enabled, replace the CMD below with:
#   CMD ["node", "apps/web/server.js"]
# For now `pnpm start` defers to the package.json script (placeholder today).
ENTRYPOINT ["pnpm", "--filter", "@gonext/web"]
CMD ["start"]
