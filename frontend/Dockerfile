# =============================================================================
# AlgoForge Frontend - Multi-stage Dockerfile
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Dependencies
# ---------------------------------------------------------------------------
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS deps

WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci

# ---------------------------------------------------------------------------
# Stage 2: Build
# ---------------------------------------------------------------------------
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS builder

WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .

# Build arguments for Next.js public env vars
ARG NEXT_PUBLIC_API_URL=
ARG NEXT_PUBLIC_WS_URL=

ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_WS_URL=$NEXT_PUBLIC_WS_URL

RUN npm run build

# ---------------------------------------------------------------------------
# Stage 3: Production
# ---------------------------------------------------------------------------
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS production

ARG SOURCE_REVISION

WORKDIR /app

ENV NODE_ENV=production

# Non-root user
RUN case "${SOURCE_REVISION}" in ''|unset|unknown|latest) exit 1 ;; esac \
    && addgroup -g 1001 -S algoforge \
    && adduser -u 1001 -S algoforge -G algoforge \
    && printf '%s\n' "${SOURCE_REVISION}" > /app/source-revision.txt \
    && chmod 0444 /app/source-revision.txt

LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"

COPY --from=builder /app/public ./public
COPY --from=builder --chown=algoforge:algoforge /app/.next/standalone ./
COPY --from=builder --chown=algoforge:algoforge /app/.next/static ./.next/static

USER algoforge

EXPOSE 3000

ENV PORT=3000
ENV HOSTNAME="0.0.0.0"

CMD ["node", "server.js"]
