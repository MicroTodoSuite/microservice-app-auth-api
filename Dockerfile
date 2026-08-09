# Build stage. Pinned, not "latest": every build resolves the identical toolchain.
FROM golang:1.23.4-bookworm AS build

WORKDIR /app

COPY *.go ./

ENV GO111MODULE=on
# go mod tidy alone resolves the newest release of every dependency at build time,
# which is not reproducible and breaks silently when a maintainer's newest release
# needs a newer Go than the one pinned above (observed with client_golang >=1.25).
# Pin the direct dependency that forces that requirement before tidy resolves the rest.
RUN go mod init github.com/bortizf/microservice-app-example/tree/master/auth-api \
    && go get github.com/prometheus/client_golang@v1.20.5 \
    && go mod tidy \
    && CGO_ENABLED=0 go build -o auth-api .

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