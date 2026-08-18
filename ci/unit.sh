#!/usr/bin/env bash
# Unit gate entrypoint (spec 006 / T007) consumed by the reusable CI `unit` job.
# Runs the Go unit suite with coverage and enforces a business-logic threshold,
# excluding bootstrap/infra files (main.go, tracing.go) from the denominator
# per research D2 ("exclude generated/bootstrap code"). Emits coverage.out for
# the SonarQube quality gate (which stays visibly skipped until its server exists).
set -euo pipefail

THRESHOLD="${UNIT_COVERAGE_THRESHOLD:-70}"

go test ./... -covermode=atomic -coverprofile=coverage.out

# Statement-weighted business-logic coverage: keep the mode header, drop the
# bootstrap/infra source lines, then let `go tool cover` recompute the total.
head -1 coverage.out >coverage.business.out
grep -vE '/(main|tracing)\.go:' coverage.out | grep -v '^mode:' >>coverage.business.out || true
covered="$(go tool cover -func=coverage.business.out | awk '/^total:/ {gsub(/%/,"",$NF); print $NF}')"

echo "Business-logic coverage: ${covered}% (threshold ${THRESHOLD}%)"
if awk -v c="$covered" -v t="$THRESHOLD" 'BEGIN { exit (c+0 >= t+0) ? 0 : 1 }'; then
  echo "unit gate: PASS"
else
  echo "::error::unit coverage ${covered}% is below threshold ${THRESHOLD}%"
  exit 1
fi
