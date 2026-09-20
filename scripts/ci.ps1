# scripts/ci.ps1 -- TraceIQ CI gate: gofmt check, vet, build, test,
# govulncheck, staticcheck. Mirrors scripts/ci.sh for POSIX shells.
#
# Every Go invocation runs pinned to Set B (DR-1): GOTOOLCHAIN=go1.27.1,
# CGO_ENABLED=0, GOFLAGS=-mod=readonly. -race additionally needs CGO, so it
# is attempted in its own CGO_ENABLED=1 sub-step only when a C compiler is
# on PATH; otherwise the race step is skipped with a warning rather than
# failing the gate.

$ErrorActionPreference = "Stop"

$env:GOTOOLCHAIN = "go1.27.1"
$env:CGO_ENABLED = "0"
$env:GOFLAGS = "-mod=readonly"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $repoRoot

function Step($name) {
    Write-Host ""
    Write-Host "==> $name"
}

Step "gofmt check"
$unformatted = gofmt -l . | Where-Object { $_ -ne "" }
if ($unformatted) {
    Write-Error "gofmt: the following files are not formatted:`n$($unformatted -join "`n")"
    exit 1
}

Step "go vet ./..."
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "go build ./..."
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "go test ./... (no -race, CGO_ENABLED=0)"
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "go test ./... -race"
$cc = Get-Command cc, gcc, clang -ErrorAction SilentlyContinue | Select-Object -First 1
if ($cc) {
    $env:CGO_ENABLED = "1"
    go test -race ./...
    $raceExit = $LASTEXITCODE
    $env:CGO_ENABLED = "0"
    if ($raceExit -ne 0) { exit $raceExit }
} else {
    Write-Warning "no C compiler on PATH; skipping -race (needs CGO_ENABLED=1)."
}

Step "govulncheck"
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "staticcheck"
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Step "all checks passed"
