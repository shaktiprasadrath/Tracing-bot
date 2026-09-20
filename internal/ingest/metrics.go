package ingest

import (
	"sync"

	"traceiq/internal/model"
)

// metrics is a minimal, dependency-free counter surface satisfying FR-F01-9's
// metric *names* (ingest_spans_received_total{protocol,tenant},
// ingest_spans_rejected_total{protocol,reason}, ingest_batches_received_total{protocol});
// ingest_latency_seconds (a histogram) is out of scope for this pass — see
// docs/reports/w9-ingest-cont.md.
//
// SIMPLIFICATION (documented): FR-F01-9 asks for real Prometheus-style
// metrics. internal/selfobs.Recorder (internal/selfobs/selfobs.go) is the
// package that owns the actual /metrics exposition and is an allowed import
// per DR-2's adjacency table, but New()'s signature is fixed by F01 §4.3 to
// `New(cfg Config, clock model.Clock, resolver tenant.Resolver, sinks []SpanSink)`
// with no Recorder parameter. Rather than widen that signature (out of
// F01's register), this pass keeps an in-process counter map with the same
// label shape, so wiring a selfobs.Recorder behind it later is a pure
// addition, not a rewrite.
type metrics struct {
	mu       sync.Mutex
	received map[string]uint64 // "protocol|tenant" -> count
	rejected map[string]uint64 // "protocol|reason" -> count
	batches  map[Protocol]uint64
}

func newMetrics() *metrics {
	return &metrics{
		received: make(map[string]uint64),
		rejected: make(map[string]uint64),
		batches:  make(map[Protocol]uint64),
	}
}

func (m *metrics) spansReceived(p Protocol, tid model.TenantID, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received[string(p)+"|"+string(tid)] += uint64(n)
}

func (m *metrics) spansRejected(p Protocol, reason RejectReason, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected[string(p)+"|"+string(reason)] += uint64(n)
}

func (m *metrics) batchReceived(p Protocol) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches[p]++
}

// Received/Rejected/Batches are test/inspection accessors (FR-F01-9's AC:
// "all four metrics are present and correctly labeled").
func (m *metrics) Received(p Protocol, tid model.TenantID) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.received[string(p)+"|"+string(tid)]
}

func (m *metrics) Rejected(p Protocol, reason RejectReason) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rejected[string(p)+"|"+string(reason)]
}

func (m *metrics) Batches(p Protocol) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.batches[p]
}
