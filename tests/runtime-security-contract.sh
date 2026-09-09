#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

grep -Fq 'golang.org/x/crypto v0.55.0' "$repo_root/go.mod" || {
  echo "runtime-security-contract: golang.org/x/crypto must include the CVE-2026-56854 fix" >&2
  exit 1
}
grep -Fq 'google.golang.org/grpc v1.83.2' "$repo_root/go.mod" || {
  echo "runtime-security-contract: google.golang.org/grpc must include the 1.83.x security fixes" >&2
  exit 1
}

echo "runtime-security-contract: PASS"
