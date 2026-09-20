# syntax=docker/dockerfile:1
#
# Multi-stage build for cmd/traceiq (per docs/architecture/05-tool-selection-adr.md
# D-13: gcr.io/distroless/static-debian12:nonroot base). CGO_ENABLED=0 static
# binary, so the runtime image needs no libc, no shell, no package manager.
#
# Builder stage: full Go toolchain, never shipped in the final image.
FROM golang:1.27-bookworm AS builder

ENV GOTOOLCHAIN=go1.27.1 \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags "-s -w" -o /out/traceiq ./cmd/traceiq

# Runtime stage: distroless static, nonroot (UID 65532). No shell, no package
# manager, no curl/wget/netcat — nothing beyond the binary itself.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /out/traceiq /traceiq

# UID 65532 ("nonroot") is already the image's default user; kept explicit
# rather than overridden.
USER 65532:65532

# Only the OTLP gRPC ingest listener is needed for this test. Other listeners
# (selfobs metrics/health, API) stay bound in-process per traceiq.yaml but are
# not EXPOSEd here since nothing outside the pod needs to reach them.
EXPOSE 4317

ENTRYPOINT ["/traceiq"]
CMD ["--config", "/etc/traceiq/traceiq.yaml"]
