package sampler

import (
	"context"
	"encoding/binary"
	"time"

	"traceiq/internal/model"
)

// fakeClock is a minimal model.Clock for deterministic tests (DR-31), mirroring
// internal/topology/livegraph_test.go's fakeClock: only Now/Since are backed
// by real behavior, NewTicker/NewTimer/Sleep are unused by the code paths
// under test here (Manager.Tick/DrainRED/FlushAll and DefaultPolicy.Evaluate
// are driven directly, never through Manager.Run's ticker loop).
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time                                   { return f.now }
func (f *fakeClock) Since(t time.Time) time.Duration                  { return f.now.Sub(t) }
func (f *fakeClock) NewTicker(d time.Duration) model.Ticker           { return nil }
func (f *fakeClock) NewTimer(d time.Duration) model.Timer             { return nil }
func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

// mkTraceID builds a deterministic, distinct model.TraceID from a small
// integer seed (last 8 bytes; first 8 bytes zero). Good enough for
// uniqueness/ordering tests; statistical tests that need real distribution
// use randomTraceID instead.
func mkTraceID(n uint64) model.TraceID {
	var id model.TraceID
	binary.BigEndian.PutUint64(id[8:], n)
	return id
}

func mkSpanID(n uint64) model.SpanID {
	var id model.SpanID
	binary.BigEndian.PutUint64(id[:], n)
	return id
}

// mkSpan builds a minimal model.Span. parent == model.SpanID{} marks it root
// (spanutil.go's spanIsRoot). durNanos is added to startNanos for EndUnixNano.
func mkSpan(traceID model.TraceID, spanID, parent model.SpanID, service, op string, startNanos, durNanos uint64, isError bool) model.Span {
	status := model.Status{Code: model.StatusOk}
	if isError {
		status.Code = model.StatusError
	}
	return model.Span{
		TraceID:       traceID,
		SpanID:        spanID,
		ParentSpanID:  parent,
		Name:          op,
		StartUnixNano: startNanos,
		EndUnixNano:   startNanos + durNanos,
		Status:        status,
		Resource:      &model.Resource{ServiceName: service},
	}
}
