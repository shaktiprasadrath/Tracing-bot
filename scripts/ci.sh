#!/usr/bin/env bash
# scripts/ci.sh — TraceIQ CI gate: gofmt check, vet, build, test,
# govulncheck, staticcheck. Mirrors scripts/ci.ps1 for Windows.
#
# Every Go invocation runs pinned to Set B (DR-1): GOTOOLCHAIN=go1.27.1,
# CGO_ENABLED=0, GOFLAGS=-mod=readonly. -race additionally needs CGO, so it
# is attempted in its own CGO_ENABLED=1 sub-step only when a C compiler is
# on PATH; otherwise the race step is skipped with a warning rather than
# failing the gate.
set -euo pipefail

export GOTOOLCHAIN=go1.27.1
export CGO_ENABLED=0
export GOFLAGS=-mod=readonly

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

step() { printf '\n==> %s\n' "$1"; }

step "gofmt check"
unformatted="$(gofmt -l . | grep -v '^$' || true)"
if [ -n "$unformatted" ]; then
  echo "gofmt: the following files are not formatted:" >&2
  echo "$unformatted" >&2
  exit 1
fi

step "go vet ./..."
go vet ./...

step "go build ./..."
go build ./...

step "go test ./... (no -race, CGO_ENABLED=0)"
go test ./...

step "go test ./... -race"
if command -v cc >/dev/null 2>&1 || command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then
  CGO_ENABLED=1 go test -race ./...
else
  echo "WARNING: no C compiler on PATH; skipping -race (needs CGO_ENABLED=1)." >&2
fi

step "govulncheck"
go run golang.org/x/vuln/cmd/govulncheck@latest ./...

step "staticcheck"
go run honnef.co/go/tools/cmd/staticcheck@latest ./...

step "all checks passed"
