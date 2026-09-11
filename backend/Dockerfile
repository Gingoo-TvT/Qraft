# =============================================================================
# AlgoForge Backend - Multi-stage Dockerfile
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Build
# ---------------------------------------------------------------------------
FROM golang@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache git ca-certificates tzdata

ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

WORKDIR /app

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build     -ldflags="-s -w"     -o /app/bin/server     ./cmd/api

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build     -ldflags="-s -w"     -o /app/bin/worker     ./cmd/worker

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build     -ldflags="-s -w"     -o /app/bin/migrate     ./cmd/migrate

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build     -ldflags="-s -w"     -o /app/bin/testdata-tool     ./cmd/testdata-tool

# Bind the runner to the exact, ordered SQL chain packaged in the artifact.
RUN find migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort | xargs sha256sum > /app/bin/migrations.sha256 \
    && sha256sum /app/bin/migrations.sha256 | cut -d ' ' -f 1 > /app/bin/migration-chain.sha256

# ---------------------------------------------------------------------------
# Database migrations: immutable runner and SQL chain packaged together
# ---------------------------------------------------------------------------
FROM alpine@sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1 AS migrations

ARG SOURCE_REVISION

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1001 -S algoforge-migrate \
    && adduser -u 1001 -S algoforge-migrate -G algoforge-migrate

WORKDIR /app
ENV MIGRATIONS_DIR=/app/migrations

COPY --from=builder /app/bin/migrate /app/migrate
COPY --from=builder /app/migrations /app/migrations
COPY --from=builder /app/bin/migrations.sha256 /app/migrations.sha256
COPY --from=builder /app/bin/migration-chain.sha256 /app/migration-chain.sha256

RUN case "${SOURCE_REVISION}" in ''|unset|unknown|latest) exit 1 ;; esac \
    && printf '%s\n' "${SOURCE_REVISION}" > /app/source-revision.txt \
    && chmod 0444 /app/source-revision.txt /app/migrations.sha256 /app/migration-chain.sha256

LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"

USER algoforge-migrate
ENTRYPOINT ["/app/migrate"]

# ---------------------------------------------------------------------------
# Stage 2: Production
# ---------------------------------------------------------------------------
FROM alpine@sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1 AS production

ARG SOURCE_REVISION

RUN apk add --no-cache ca-certificates tzdata wget g++ python3

WORKDIR /app

COPY --from=builder /app/bin/server ./server
COPY --from=builder /app/bin/worker ./worker
COPY --from=builder /app/bin/testdata-tool ./testdata-tool
COPY --from=builder /app/migrations ./migrations

# Non-root user and immutable source identity.
RUN case "${SOURCE_REVISION}" in ''|unset|unknown|latest) exit 1 ;; esac \
    && addgroup -g 1001 -S algoforge \
    && adduser -u 1001 -S algoforge -G algoforge \
    && printf '%s\n' "${SOURCE_REVISION}" > /app/source-revision.txt \
    && chmod 0444 /app/source-revision.txt

LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
USER algoforge

EXPOSE 8080

CMD ["./server"]
