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
- Write pull-request bodies bilingually: every section in English, then repeated under a `## Español` heading with the same content, not a summary. Titles, commits, code comments, documentation, and specification text stay English-only. As an AI agent you write both halves yourself.
- Open every pull request through `.github/pull_request_template.md` and follow `microservice-app-docs/docs/Pull request and task tracking conventions.md`: one concern per short-lived `<type>/<summary>` branch, a Conventional Commit title with a scope, and every template section filled. Constitution principle 13 makes this binding, not advisory.
- Keep the Spec-Driven Development commit pair intact: `test(<scope>): specify ...` must be committed failing before `feat(<scope>): implement ...`. Never squash the pair; the failing-test commit is the evidence the cycle was followed.
- Track every task. Name in the pull-request body the task IDs it advances, qualified by repository and spec, and update `tasks.md` in that same pull request rather than a follow-up. Mark a task `[X]` only after locating and inspecting its named artifact — never from a summary, a green check, a rendered manifest, or recollection. Annotate partial delivery instead of ticking it; work no register covers either gains a task or records in the PR body why none applies.
- Reconcile, never quietly edit, when a register and reality disagree: a specification that pins a version nobody shipped is a maintainer decision, and `microservice-app-docs/full-platform/plan-reconciliation.md` is the worked example.
- Never merge with `--admin`, force-push to `main`, disable a branch protection rule to land your own work, or approve your own pull request. As an AI agent you may open, describe, and update a pull request; you may never approve one and never author an acceptance or approval artifact — only a named human unlocks a gate.
- Report outcomes faithfully in commits and pull-request bodies: name what is red, say what was skipped, and correct an earlier claim that turns out to be wrong rather than leaving the record wrong.

## Notes for the Kubernetes migration
- The server listens on `AUTH_API_PORT`; the documented local value is `8000`. The Dockerfile has no `EXPOSE`, and its comment incorrectly claims the entrypoint sets the port.
- Required configuration is `AUTH_API_PORT` and `USERS_API_ADDRESS`. `JWT_SECRET` is optional in code but falls back to `myfancysecret`; provide it as a Kubernetes Secret shared with JWT-verifying services.
- `ZIPKIN_URL` is optional. When set, it configures Zipkin reporting and tracing for the Users API client.
- The only runtime service dependency found is the Users API over HTTP at `${USERS_API_ADDRESS}/users/{username}`. No database or Redis dependency is present.
- `/metrics` exposes Prometheus metrics and `/version` returns HTTP 200; there is no dedicated readiness or liveness endpoint.
- Review the single-stage, root-running `golang:latest` image, dynamic `go mod init`/`go mod tidy`, missing dependency cache, unpinned Prometheus dependency, and absence of `EXPOSE` or `HEALTHCHECK`.
- The Azure Container Apps workflow pushes both a release tag and `latest`, deploys `latest` with `az containerapp update`, and assumes an existing app. No checked-in Container Apps definition records ingress, target port, environment variables, probes, or scaling; these must be made explicit in Kubernetes manifests.
