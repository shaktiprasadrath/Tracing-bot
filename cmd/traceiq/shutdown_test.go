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
	"traceiq/internal/sampler"
	"traceiq/internal/store"
	"traceiq/internal/store/parquet"
	"traceiq/internal/store/sqlite"
)

// w16 review: cmd/traceiq had exactly one test (the happy-path smoke test,
// which only calls Shutdown after every trace has already been confirmed
// readable). Nothing exercised shutdown racing active work. That gap hid two
// real bugs, both fixed alongside these tests:
//
//  1. runStoreBridge/runBaselineFeed selected on `<-ctx.Done()` against a
//     pending `<-decisions`/`<-red` read; when Shutdown cancelled bridgeCtx
//     while a decision/sample was already buffered, Go's random case
//     selection could silently drop an already-decided Keep=true trace (or a
//     RED sample) instead of persisting it.
//  2. sampler.Manager.DrainRED was never called anywhere in cmd/traceiq (only
//     sampler.Manager.Run -- which this package doesn't use -- calls it
//     alongside Tick), so runBaselineFeed's REDSamples() channel was never
//     fed at all: the anomaly baseline was silently empty in the real
//     binary regardless of shutdown timing.

// newTestSystemForShutdown builds a System with fast timers, wired to a temp
// dir, without starting receivers (tests drive ingest.Server.Ingest
// directly, same as the existing smoke test).
func newTestSystemForShutdown(t *testing.T) *System {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Server.DataDir = dir
	cfg.Store.Hot.SQLite.Path = dir + "/hot/traceiq.db"
	cfg.Store.Cold.Parquet.Path = dir + "/cold"
	cfg.Store.Cold.Parquet.WALDir = dir + "/cold/wal"
	cfg.Sampler.Assembly.IdleTimeout = time.Millisecond
	cfg.Sampler.Assembly.HardTimeout = 5 * time.Millisecond
	cfg.Sampler.Assembly.WheelTick = time.Millisecond
	cfg.Sampler.Policy.KeepErrors = true
	cfg.Ingest.OTLPGRPC.Enabled = false
	cfg.Ingest.OTLPHTTP.Enabled = false
	cfg.SelfObs.MetricsEndpoint = "127.0.0.1:0"

	sys, err := newSystem(cfg)
	if err != nil {
		t.Fatalf("newSystem: %v", err)
	}
	return sys
}

// reopenTieredStore opens fresh hot/cold store handles against the same
// on-disk paths sys was configured with, for verifying durable persistence
// after sys.Shutdown has already closed sys.hot/sys.cold. The returned
// closer must be called to release the new handles.
func reopenTieredStore(t *testing.T, sys *System) (*store.TieredStore, func()) {
	t.Helper()
	hot, err := sqlite.Open(sys.cfg.Store.Hot.SQLite.Path)
	if err != nil {
		t.Fatalf("reopen hot store: %v", err)
	}
	cold, err := parquet.Open(parquet.Config{
		Dir:       sys.cfg.Store.Cold.Parquet.Path,
		Clock:     sys.clock,
		WalRetain: sys.cfg.Store.Cold.Parquet.WALRetain,
	})
	if err != nil {
		_ = hot.Close()
		t.Fatalf("reopen cold store: %v", err)
	}
	tiered := store.NewTieredStore(hot, cold, sys.clock)
	return tiered, func() {
		_ = cold.Close()
		_ = hot.Close()
	}
}

func errorSpanRequest(traceID [16]byte, spanID [8]byte, service string) *otlpcollectortracev1.ExportTraceServiceRequest {
	now := time.Now()
	return &otlpcollectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{
			{
				Resource: &otlpresourcev1.Resource{
					Attributes: []*otlpcommonv1.KeyValue{
						{Key: "service.name", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: service}}},
					},
				},
				ScopeSpans: []*otlptracev1.ScopeSpans{
					{
						Spans: []*otlptracev1.Span{
							{
								TraceId:           traceID[:],
								SpanId:            spanID[:],
								Name:              "op",
								Kind:              otlptracev1.Span_SPAN_KIND_SERVER,
								StartTimeUnixNano: uint64(now.UnixNano()),
								EndTimeUnixNano:   uint64(now.Add(time.Millisecond).UnixNano()),
								Status:            &otlptracev1.Status{Code: otlptracev1.Status_STATUS_CODE_ERROR, Message: "boom"},
							},
						},
					},
				},
			},
		},
	}
}

// TestSystem_DrainStoreBridge_DrainsBufferedDecisionsWithoutLoss is a
// deterministic, white-box regression test for bug (1) above: it feeds
// drainStoreBridge exactly the situation Shutdown's X4 step guarantees it
// will see (decisions/traces already fully written, channel then closed by
// a producer that has genuinely stopped) and asserts every already-decided
// Keep=true trace reaches the store. Unlike a timing-based end-to-end test,
// this does not depend on winning a race against the real ticker to
// reproduce the bug, so it fails reliably if the drain logic regresses.
func TestSystem_DrainStoreBridge_DrainsBufferedDecisionsWithoutLoss(t *testing.T) {
	sys := newTestSystemForShutdown(t)
	defer func() {
		_ = sys.cold.Close()
		_ = sys.hot.Close()
	}()
	sys.shutdownCtx = context.Background()

	decisions := make(chan sampler.Decision, 8)
	traces := make(chan model.Trace, 8)
	const tid = model.TenantID("drain-tenant")

	want := make([]model.TraceID, 4)
	for i := range want {
		var traceID model.TraceID
		traceID[15] = byte(i + 1)
		want[i] = traceID
		decisions <- sampler.Decision{Tenant: tid, TraceID: traceID, Keep: true, Reason: model.KeepError}
		traces <- model.Trace{TraceID: traceID, Tenant: tid, RootService: "svc"}
	}
	// Matches Shutdown's real invariant: by the time drainStoreBridge is
	// called, the ticker has been stopped+joined and FlushAll has already
	// returned, so no further write to these channels can occur.
	close(decisions)

	sys.drainStoreBridge(decisions, traces)

	for i, traceID := range want {
		if _, err := sys.tiered.GetTrace(context.Background(), tid, traceID); err != nil {
			t.Errorf("trace %d (%x) missing after drainStoreBridge: %v", i, traceID, err)
		}
	}
}

// TestSystem_RunSamplerTicker_FeedsBaselineViaDrainRED is a regression test
// for bug (2) above: it starts the real System (real ticker, real bridge
// goroutines) and asserts the anomaly baseline actually receives an
// observation from an ingested span. Before the fix, s.baseline.Stats().Keys
// would stay 0 forever because nothing ever called sampler.Manager.DrainRED.
func TestSystem_RunSamplerTicker_FeedsBaselineViaDrainRED(t *testing.T) {
	sys := newTestSystemForShutdown(t)

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

	var traceID [16]byte
	traceID[15] = 0x01
	var spanID [8]byte
	spanID[7] = 0x01
	req := errorSpanRequest(traceID, spanID, "red-service")

	if _, err := sys.ing.Ingest(ctx, ingest.ProtocolOTLPGRPC, req, model.TenantID("red-tenant"), false); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sys.baseline.Stats().Keys > 0 {
			return // success: DrainRED actually moved a sample into the baseline
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("anomaly baseline never received an observation; sampler.Manager.DrainRED is not being called")
}

// TestSystem_Shutdown_MidIngest_NoHangOrGrossLoss drives real concurrent
// work (spans landing in the sampler's shard buffers, the real ticker
// finalizing them, the real store bridge appending them) right up to the
// moment Shutdown is called, rather than the smoke test's pattern of
// waiting for everything to settle first, and asserts Shutdown still
// returns promptly with most already-decided traces intact.
//
// This drives the sampler directly via sys.samp.Consume (the same
// ingest.SpanSink method ingest.Server's queue drain calls) instead of going
// through sys.ing.Ingest: internal/ingest's own sinkQueue.drain has the
// identical `select { case <-queue: ...; case <-ctx.Done(): return }` race
// this wave's system.go fix closes for the sampler->store bridge (queue.go,
// drain, around the ctx.Done() case), and Server.Stop cancels that queue's
// context without waiting for it to drain -- so routing this test through
// the real ingest queue would conflate that separate, out-of-scope (package
// internal/ingest, not cmd/traceiq) bug with the one this test targets.
// Flagged separately rather than fixed here; see the w16 review report.
//
// Traces still genuinely open (not yet past idle_timeout) at the instant
// FlushAll runs are legitimately shed (KeepReason=KeepShed, X-OPS X4) --
// that's shutdown working as designed, not data loss -- so this only bounds
// the loss rather than requiring zero, while still catching a systemic drop
// (the pre-fix bug could lose most or all already-decided traces, not just
// the odd one at the boundary).
func TestSystem_Shutdown_MidIngest_NoHangOrGrossLoss(t *testing.T) {
	sys := newTestSystemForShutdown(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sys.Start(ctx); err != nil {
		t.Fatalf("System.Start: %v", err)
	}

	const tenant = model.TenantID("shutdown-tenant")
	const n = 200
	traceIDs := make([]model.TraceID, n)
	for i := 0; i < n; i++ {
		id := i + 1 // avoid an all-zero trace_id at i==0
		var traceID model.TraceID
		traceID[14] = byte(id >> 8)
		traceID[15] = byte(id)
		var spanID model.SpanID
		spanID[6] = byte(id >> 8)
		spanID[7] = byte(id)
		now := time.Now()
		span := model.Span{
			TraceID:       traceID,
			SpanID:        spanID,
			Name:          "op",
			Kind:          model.SpanKindServer,
			StartUnixNano: uint64(now.UnixNano()),
			EndUnixNano:   uint64(now.Add(time.Millisecond).UnixNano()),
			Status:        model.Status{Code: model.StatusError, Message: "boom"},
			Resource:      &model.Resource{ServiceName: "shutdown-service"},
			Tenant:        tenant,
		}
		if err := sys.samp.Consume(ctx, tenant, []model.Span{span}); err != nil {
			t.Fatalf("sampler Consume %d: %v", i, err)
		}
		traceIDs[i] = traceID
	}

	// A head start lets the ticker actually fire and decide (Keep=true) some
	// of these before Shutdown races in -- without this, every trace would
	// still be open and this test would only exercise the documented
	// KeepShed path, not the bridge-drain fix. Measured on this runner: all
	// 200 finalize within ~20ms of the consume loop finishing (idle_timeout
	// is 1ms), but the store bridge only drains ~13 decisions per 20ms
	// (real sqlite+parquet disk I/O per Append), so at 60ms in there is
	// still a large, genuinely in-flight backlog sitting in the buffered
	// decisions/traces channel -- exactly the situation Shutdown's X4
	// ordering and the bridge's post-cancellation drain need to handle
	// without loss.
	time.Sleep(60 * time.Millisecond)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, sc := context.WithTimeout(context.Background(), 10*time.Second)
		defer sc()
		shutdownDone <- sys.Shutdown(shutdownCtx, 10*time.Second)
	}()

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("System.Shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("System.Shutdown did not return within 15s -- shutdown hang")
	}

	// Shutdown already closed sys.hot/sys.cold (X9/X10), so verify against
	// freshly-reopened store handles on the same on-disk files rather than
	// sys.tiered -- this checks what actually got durably persisted, not
	// just what the (now-closed) in-process handle would report.
	tiered2, closeStores := reopenTieredStore(t, sys)
	defer closeStores()

	var missing int
	for _, traceID := range traceIDs {
		if _, err := tiered2.GetTrace(context.Background(), tenant, traceID); err != nil {
			missing++
		}
	}
	if missing > n/4 {
		t.Fatalf("%d/%d traces missing from the store after shutdown mid-ingest -- want most already-decided traces to survive", missing, n)
	}
}
