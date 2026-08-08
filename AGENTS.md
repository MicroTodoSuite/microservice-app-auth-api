## Overview
This Go service authenticates a small hardcoded set of credentials, retrieves the matching profile from the Users API, and returns a signed JWT.
It also exposes version and Prometheus metrics endpoints and can trace inbound and outbound HTTP calls through Zipkin.

## Stack
- Go 1.18.2 is the tested language version documented in `README.md`; the Docker build instead uses the unpinned `golang:latest` image.
- Echo 3.2.6 is the HTTP framework version resolved in `Gopkg.lock` from the 3.2.2 constraint in `Gopkg.toml`.
- Runtime libraries include jwt-go 3.1.0, Zipkin Go at locked revision `3741243`, and an unpinned Prometheus Go client.
- Release automation uses Node.js 22 and semantic-release 24.2.3.

## Commands
- Build from a clean checkout, exactly as documented and performed by the Dockerfile:
  ```sh
  export GO111MODULE=on
  go mod init github.com/bortizf/microservice-app-example/tree/master/auth-api
  go mod tidy
  go build
  ```
- Test: there are no Go test files or Go test command. The only declared script, `npm test`, intentionally prints `Error: no test specified` and exits with status 1.
- Run the built binary locally using the documented command:
  ```sh
  JWT_SECRET=PRFT AUTH_API_PORT=8000 USERS_API_ADDRESS=http://127.0.0.1:8083 ./auth-api
  ```

## Structure
- `main.go`: Echo setup, middleware, `/login`, `/version`, `/metrics`, and JWT issuance.
- `user.go`: credential allowlist and authenticated HTTP lookup of `/users/{username}` in the Users API.
- `tracing.go`: optional Zipkin server middleware and traced outbound HTTP client.
- `Gopkg.toml` and `Gopkg.lock`: legacy Go `dep` constraints and resolved revisions.
- `package.json`, `package-lock.json`, and `.releaserc`: semantic-release tooling; they are not application runtime files.
- `Dockerfile`: builds the root Go package into `auth-api`; `.github/workflows/` builds, publishes, releases, and deploys the image.

## Conventions
- All application code is in the root `main` package rather than `cmd/` and `internal/` packages.
- No `go.mod` or `go.sum` is committed; the Docker build creates them and resolves dependencies during every image build, while the legacy `Gopkg` files remain in the repository.
- Login credentials are hardcoded as `username_password` keys, but user profile data is fetched from the Users API before a token is issued.

## Notes for the Kubernetes migration
- The server listens on `AUTH_API_PORT`; the documented local value is `8000`. The Dockerfile has no `EXPOSE`, and its comment incorrectly claims the entrypoint sets the port.
- Required configuration is `AUTH_API_PORT` and `USERS_API_ADDRESS`. `JWT_SECRET` is optional in code but falls back to `myfancysecret`; provide it as a Kubernetes Secret shared with JWT-verifying services.
- `ZIPKIN_URL` is optional. When set, it configures Zipkin reporting and tracing for the Users API client.
- The only runtime service dependency found is the Users API over HTTP at `${USERS_API_ADDRESS}/users/{username}`. No database or Redis dependency is present.
- `/metrics` exposes Prometheus metrics and `/version` returns HTTP 200; there is no dedicated readiness or liveness endpoint.
- Review the single-stage, root-running `golang:latest` image, dynamic `go mod init`/`go mod tidy`, missing dependency cache, unpinned Prometheus dependency, and absence of `EXPOSE` or `HEALTHCHECK`.
- The Azure Container Apps workflow pushes both a release tag and `latest`, deploys `latest` with `az containerapp update`, and assumes an existing app. No checked-in Container Apps definition records ingress, target port, environment variables, probes, or scaling; these must be made explicit in Kubernetes manifests.
