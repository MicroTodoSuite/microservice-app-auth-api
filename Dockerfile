# Both stages are pinned by immutable multi-architecture digest. The runtime
# contains only the static binary and CA certificates and runs as UID 65532.
FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY *.go ./
RUN go test ./... \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -buildid=" -o /out/auth-api .

FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

WORKDIR /app
COPY --from=build --chown=65532:65532 /out/auth-api /app/auth-api

# Kubernetes can verify this numeric identity before starting a container with
# runAsNonRoot. The distroless nonroot account maps to UID/GID 65532.
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["/app/auth-api"]
