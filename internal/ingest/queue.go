package ingest

import (
	"context"
	"errors"
	"sync/atomic"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

var errQueueFull = errors.New("ingest: sink queue full")

// queuedBatch is one bounded-queue entry (FR-F01-6, DR-28).
type queuedBatch struct {
	tenant model.TenantID
	spans  []model.Span
	bytes  int64
}

// sinkQueue is FR-F01-6's per-sink bounded output queue: bounded in BOTH
// batches (channel capacity) and bytes (an atomic counter checked before
// every send), with a clock-driven enqueue_timeout and an overflow_policy
// of shed (default) or block_up_to_timeout — "block" (unbounded) is DELETED
// per DR-28 and never implemented here.
type sinkQueue struct {
	sink  SpanSink
	cfg   config.IngestQueueConfig
	clock model.Clock
	ch    chan queuedBatch
	bytes int64 // atomic
}

func newSinkQueue(sink SpanSink, cfg config.IngestQueueConfig, clock model.Clock) *sinkQueue {
	capacity := cfg.Capacity
	if capacity <= 0 {
		capacity = 1
	}
	return &sinkQueue{sink: sink, cfg: cfg, clock: clock, ch: make(chan queuedBatch, capacity)}
}

// tryEnqueue makes one non-blocking attempt, honoring the byte bound as
// well as the channel's own (batch-count) bound.
func (q *sinkQueue) tryEnqueue(b queuedBatch) bool {
	if q.cfg.MaxBytes > 0 && atomic.LoadInt64(&q.bytes)+b.bytes > q.cfg.MaxBytes {
		return false
	}
	select {
	case q.ch <- b:
		atomic.AddInt64(&q.bytes, b.bytes)
		return true
	default:
		return false
	}
}

// push is §4.4's per-sink `select { case outputQueue[sink].push(batch): ...
// case <-clock.After(enqueue_timeout): ... case <-ctx.Done(): ... }`,
// expressed with model.Clock (DR-31) rather than time.After so
// internal/archtest's time-ban check passes and eval.VirtualClock can drive
// it deterministically.
func (q *sinkQueue) push(ctx context.Context, tenant model.TenantID, spans []model.Span, sizeBytes uint32) error {
	b := queuedBatch{tenant: tenant, spans: spans, bytes: int64(sizeBytes)}
	if q.tryEnqueue(b) {
		return nil
	}
	if q.cfg.OverflowPolicy == "block_up_to_timeout" && q.cfg.EnqueueTimeout > 0 {
		timer := q.clock.NewTimer(q.cfg.EnqueueTimeout)
		defer timer.Stop()
		select {
		case <-timer.C():
			if q.tryEnqueue(b) {
				return nil
			}
			return &IngestError{Reason: ReasonQueueFullAfterWait, Err: errQueueFull}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// overflow_policy: shed (default) — return RESOURCE_EXHAUSTED immediately.
	return &IngestError{Reason: ReasonQueueFull, Err: errQueueFull}
}

// drain is §4.4's "one drain goroutine per sink queue, started at
// Receiver.Start": for batch in q: sink.Consume(ctx, batch.Tenant, batch.Spans).
//
// w16 review fix (mirrors cmd/traceiq/system.go's runStoreBridge/
// applyDecision fix for the identical race): ctx is only Server.Stop's
// drainCtx, cancelled after every receiver has already been stopped
// (Server.Stop stops receivers first, then calls drainCancel) — so by the
// time ctx.Done() fires, nothing can push onto q.ch again. The naive
// version of this select raced ctx.Done() against a still-pending q.ch
// read: when a batch was already sitting in the channel at cancellation,
// Go's uniform pseudo-random case selection could pick ctx.Done() and
// return without ever consuming it, silently dropping an
// already-enqueued, should-be-durable batch. Draining what's left
// non-blockingly before returning closes that window.
func (q *sinkQueue) drain(ctx context.Context) {
	for {
		select {
		case b := <-q.ch:
			q.consume(b)
		case <-ctx.Done():
			q.drainRemaining()
			return
		}
	}
}

// drainRemaining empties whatever is currently buffered in q.ch. Safe to
// call non-blockingly only because the caller (drain, on ctx.Done()) is
// guaranteed no further push can arrive.
func (q *sinkQueue) drainRemaining() {
	for {
		select {
		case b := <-q.ch:
			q.consume(b)
		default:
			return
		}
	}
}

// consume is drain/drainRemaining's shared per-batch body. It always uses
// context.Background(), never the drain loop's own cancellable ctx: once a
// batch is dequeued we're committed to delivering it, and cancellation
// should only mean "stop pulling NEW work off the queue", not "abort a
// write already in flight" — passing the loop's ctx straight through here
// would let Server.Stop's drainCancel abort a Consume call an instant
// after it was dequeued from the normal (non-drain) select case, the same
// failure mode applyDecision's drainContext() fix closes in
// cmd/traceiq/system.go.
func (q *sinkQueue) consume(b queuedBatch) {
	atomic.AddInt64(&q.bytes, -b.bytes)
	// At-least-once delivery (§3.2): a Consume error is not retried
	// here — retry/dedupe safety lives downstream (sampler.partialTrace,
	// DR-9 §9), not in ingest itself.
	_ = q.sink.Consume(context.Background(), b.tenant, b.spans)
}
