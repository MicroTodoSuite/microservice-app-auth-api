# Build stage. Pinned, not "latest": every build resolves the identical toolchain.
FROM golang:1.25-bookworm AS build

WORKDIR /app

# Committed go.mod/go.sum are the source of truth (spec 006 migrated auth-api off
# `dep` to Go modules). Download deps in a cached layer before copying sources so
# the build is reproducible and does not resolve "newest" at build time.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -o auth-api .

# Runtime stage. Minimal Debian base, no Go toolchain: only what auth-api needs to run.
FROM debian:bullseye-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 auth-api \
    && useradd --uid 10001 --gid auth-api --no-create-home --shell /usr/sbin/nologin auth-api

WORKDIR /app
COPY --from=build --chown=auth-api:auth-api /app/auth-api ./auth-api

USER 10001:10001

# Documents the default; the app still reads the real value from AUTH_API_PORT at runtime.
EXPOSE 8000

# Shell form is required here for ${AUTH_API_PORT} expansion and the `|| exit 1` fallback.
# hadolint ignore=DL3025
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f "http://localhost:${AUTH_API_PORT:-8000}/version" || exit 1

ENTRYPOINT ["./auth-api"]