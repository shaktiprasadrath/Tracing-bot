# TraceIQ build/test entry points. All targets pin the Set B toolchain
# (DR-1): GOTOOLCHAIN=go1.27.1, CGO_ENABLED=0, GOFLAGS=-mod=readonly.

export GOTOOLCHAIN := go1.27.1
export CGO_ENABLED := 0
export GOFLAGS := -mod=readonly

.PHONY: build vet test fmt fmt-check vulncheck staticcheck ci clean

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

fmt:
	gofmt -w .

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

# ci runs the full gate exactly as scripts/ci.sh does (gofmt, vet, build,
# test, conditional -race, govulncheck, staticcheck). Use scripts/ci.ps1
# directly on Windows PowerShell.
ci:
	bash scripts/ci.sh

clean:
	go clean ./...
