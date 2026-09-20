package main

import (
	"context"
	"testing"
	"time"

	otlpcollectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"traceiq/internal/config"
	"traceiq/internal/ingest"
	"traceiq/internal/model"
)

// TestSystem_EndToEnd_OTLPSpanReachesStore is the wave's headline
// integration test: it assembles the real System (store, topology,
// sampler, ingest -- exactly what cmd/traceiq wires in production), drives
// a synthetic OTLP ExportTraceServiceRequest through ingest.Server.Ingest
// (the same HandleInbound path the gRPC receiver calls), and asserts the
// span's trace is retrievable back out of the tiered store once the
// sampler finalizes it. Per-package unit tests already cover sampler
// policy/assembly, store hot/cold and ingest normalization individually;
// this test is the thing none of those alone prove: that the wiring in
// system.go actually connects them into one working pipeline.
func TestSystem_EndToEnd_OTLPSpanReachesStore(t *testing.T) {
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Server.DataDir = dir
	cfg.Store.Hot.SQLite.Path = dir + "/hot/traceiq.db"
	cfg.Store.Cold.Parquet.Path = dir + "/cold"
	cfg.Store.Cold.Parquet.WALDir = dir + "/cold/wal"
	// Finalize almost immediately so the test doesn't wait out the 8s dev
	// default idle timeout: the smoke test's own trace has no further
	// spans arriving, so a short idle timeout is what actually triggers
	// Manager.Tick's finalize path (assembly.go's Tick: idle-since-
	// LastSeen or hard-since-FirstSeen).
	cfg.Sampler.Assembly.IdleTimeout = 20 * time.Millisecond
	cfg.Sampler.Assembly.HardTimeout = 200 * time.Millisecond
	cfg.Sampler.Assembly.WheelTick = 10 * time.Millisecond
	// keep_errors keeps this span (it carries an ERROR status below)
	// regardless of the healthy_sample_rate floor, so the test doesn't
	// depend on sampler.Impl's probabilistic path at all.
	cfg.Sampler.Policy.KeepErrors = true
	// Ingest receivers bind real sockets by default; the smoke test drives
	// ingest.Server.Ingest directly (the same method otlpGRPCReceiver.Export
	// calls), so no listener needs to be bound at all.
	cfg.Ingest.OTLPGRPC.Enabled = false
	cfg.Ingest.OTLPHTTP.Enabled = false
	cfg.SelfObs.MetricsEndpoint = "127.0.0.1:0"

	sys, err := newSystem(cfg)
	if err != nil {
		t.Fatalf("newSystem: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sys.Start(ctx); err != nil {
		t.Fatalf("System.Start: %v", err)
	}
	defer func() {
		shutdownCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		if err := sys.Shutdown(shutdownCtx, 5*time.Second); err != nil {
			t.Errorf("System.Shutdown: %v", err)
		}
	}()

	traceID := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := [8]byte{0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8}
	now := time.Now()

	req := &otlpcollectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{
			{
				Resource: &otlpresourcev1.Resource{
					Attributes: []*otlpcommonv1.KeyValue{
						{Key: "service.name", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "checkout-service"}}},
					},
				},
				ScopeSpans: []*otlptracev1.ScopeSpans{
					{
						Spans: []*otlptracev1.Span{
							{
								TraceId:           traceID[:],
								SpanId:            spanID[:],
								Name:              "POST /checkout",
								Kind:              otlptracev1.Span_SPAN_KIND_SERVER,
								StartTimeUnixNano: uint64(now.UnixNano()),
								EndTimeUnixNano:   uint64(now.Add(5 * time.Millisecond).UnixNano()),
								Status:            &otlptracev1.Status{Code: otlptracev1.Status_STATUS_CODE_ERROR, Message: "boom"},
							},
						},
					},
				},
			},
		},
	}

	const tenant = model.TenantID("smoke-tenant")
	if _, err := sys.ing.Ingest(ctx, ingest.ProtocolOTLPGRPC, req, tenant, false); err != nil {
		t.Fatalf("ingest.Server.Ingest: %v", err)
	}

	// The sampler finalizes on its own ticker (runSamplerTicker); poll the
	// tiered store rather than sleeping a fixed guess.
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		tr, err := sys.tiered.GetTrace(ctx, tenant, model.TraceID(traceID))
		if err == nil {
			if tr.RootService != "checkout-service" {
				t.Fatalf("trace RootService = %q, want checkout-service", tr.RootService)
			}
			if tr.ErrorCount != 1 {
				t.Fatalf("trace ErrorCount = %d, want 1", tr.ErrorCount)
			}
			if len(tr.Spans) != 1 || tr.Spans[0].Name != "POST /checkout" {
				t.Fatalf("unexpected spans in stored trace: %+v", tr.Spans)
			}
			return // success
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("trace never reached the store within the deadline; last GetTrace error: %v", lastErr)
}
