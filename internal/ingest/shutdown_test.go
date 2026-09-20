package ingest

import (
	"context"
	"sync"
	"testing"
	"time"

	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// TestSinkQueue_DrainRemaining_DrainsBufferedBatchesWithoutLoss is a
// deterministic, white-box regression test for the w16 review finding: it
// feeds drainRemaining exactly the situation drain's ctx.Done() case
// guarantees it will see (batches already sitting in q.ch, with no further
// push possible because Server.Stop only cancels drainCtx after every
// Receiver has already been stopped) and asserts every already-enqueued
// batch still reaches the sink. Calling drainRemaining directly -- rather
// than driving it via drain(ctx) with an already-cancelled ctx -- avoids
// racing Go's own select case selection the way the pre-fix code did, so
// this fails reliably if drainRemaining regresses, instead of only
// occasionally (mirrors cmd/traceiq/shutdown_test.go's
// TestSystem_DrainStoreBridge_DrainsBufferedDecisionsWithoutLoss, which
// tests drainStoreBridge directly for the same reason).
func TestSinkQueue_DrainRemaining_DrainsBufferedBatchesWithoutLoss(t *testing.T) {
	sink := newFakeSink()
	clock := &fakeClock{now: time.Unix(1000, 0)}
	q := newSinkQueue(sink, config.IngestQueueConfig{Capacity: 8}, clock)

	const tid = model.TenantID("drain-tenant")
	const want = 4
	for i := 0; i < want; i++ {
		if !q.tryEnqueue(queuedBatch{tenant: tid, spans: []model.Span{{}}, bytes: 1}) {
			t.Fatalf("tryEnqueue %d: queue unexpectedly full", i)
		}
	}

	q.drainRemaining()

	if _, _, calls := sink.snapshot(); calls != want {
		t.Fatalf("sink.calls = %d, want %d (buffered batches dropped instead of drained)", calls, want)
	}
}

// blockingSink delays each Consume call so a realistic backlog builds up in
// the queue before Stop races in -- playing the role real disk I/O plays in
// cmd/traceiq's TestSystem_Shutdown_MidIngest_NoHangOrGrossLoss.
type blockingSink struct {
	mu       sync.Mutex
	delay    time.Duration
	consumed int
}

func (s *blockingSink) Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.consumed += len(spans)
	s.mu.Unlock()
	return nil
}

func (s *blockingSink) snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumed
}

// TestServerStop_MidIngest_DrainsBufferedBatchesWithoutLoss drives real
// concurrent work (spans landing in the queue, the real drain goroutine
// consuming them) right up to the moment Stop is called, rather than
// waiting for everything to settle first, and asserts Stop still returns
// with every already-enqueued batch delivered instead of dropped.
func TestServerStop_MidIngest_DrainsBufferedBatchesWithoutLoss(t *testing.T) {
	sink := &blockingSink{delay: 5 * time.Millisecond}
	resolver := &fakeResolver{subjectOK: map[model.TenantID]bool{"tenant-a": true}}
	s, err := New(config.IngestConfig{
		Queue:  config.IngestQueueConfig{Capacity: 200, OverflowPolicy: "shed"},
		Limits: config.IngestLimitsConfig{MaxSpansPerBatch: 1000},
	}, &fakeClock{now: time.Unix(1000, 0)}, resolver, []SpanSink{sink})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const n = 50
	for i := 0; i < n; i++ {
		id := byte(i + 1)
		req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(id), SpanId: mkSpanID(id), Name: "op"})
		if _, err := s.Ingest(ctx, ProtocolOTLPGRPC, req, "tenant-a", false); err != nil {
			t.Fatalf("Ingest %d: %v", i, err)
		}
	}

	// A brief head start lets the drain goroutine consume only a handful of
	// batches before Stop races in, leaving most of the other 49 still
	// sitting in q.ch, undrained -- exactly the situation Server.Stop's
	// receivers-then-drainCancel ordering and drain's post-cancellation
	// flush (queue.go) need to handle without loss.
	time.Sleep(5 * time.Millisecond)

	stopDone := make(chan error, 1)
	go func() { stopDone <- s.Stop(context.Background()) }()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Server.Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Stop did not return within 5s -- shutdown hang")
	}

	if got := sink.snapshot(); got != n {
		t.Fatalf("sink consumed %d spans, want %d (batches still buffered at Stop were dropped instead of drained)", got, n)
	}
}
