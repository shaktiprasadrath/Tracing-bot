package main

import (
	"context"

	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/topology"
)

// memEdgeSink/memEdgeSource are placeholder EdgeSink/EdgeSource
// implementations (topology.go's consumer-declared persistence seam,
// DR-13). store/sqlite is the intended production implementation, but
// wiring topology's edge persistence through the hot index's actual
// attr_index/topology_edge tables is out of this wave's time box (this
// wave's priority, per task instructions, is the ingest -> sampler -> store
// smoke test). LiveGraph keeps the live edge set in memory regardless
// (topology.Snapshot/Edges don't depend on the sink), so this stub doesn't
// block topology from working within one process's lifetime — only
// warm-start-from-cold-storage on restart is deferred.
type memEdgeSink struct{}

func (memEdgeSink) WriteEdges(ctx context.Context, tid model.TenantID, edges []topology.Edge) error {
	return nil
}

type memEdgeSource struct{}

func (memEdgeSource) LoadEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error) {
	return nil, nil
}

// --- rca tool backends ---
//
// rca.tools.go declares five consumer-facing backend interfaces
// (TraceStore/LogStore/MetricStore/TopologyStore/MemoryStore) that the real
// store/correlate/topology/memory packages are meant to satisfy so
// rca.Engine's five closed tools (DR-16 §16.1) can actually query evidence.
// Wiring each one against the real backends (store.TieredStore's
// SearchSpans/SearchTraces, correlate.Correlator, topology.LiveGraph,
// memory.Store) is real work beyond this wave's time box; these no-op
// stubs let rca.NewRegistry/rca.NewEngine construct and the rules reasoner
// run end to end (NextStep/Conclude), just against empty tool results for
// now. Documented as a follow-up in docs/reports/w15-cmd-wiring.md.
type noopTraceStore struct{}

func (noopTraceStore) TraceQuery(ctx context.Context, tid model.TenantID, a rca.TraceQueryArgs) (model.ToolResult, error) {
	return model.ToolResult{}, nil
}

type noopLogStore struct{}

func (noopLogStore) LogQuery(ctx context.Context, tid model.TenantID, a rca.LogQueryArgs) (model.ToolResult, error) {
	return model.ToolResult{}, nil
}

type noopMetricStore struct{}

func (noopMetricStore) MetricQuery(ctx context.Context, tid model.TenantID, a rca.MetricQueryArgs) (model.ToolResult, error) {
	return model.ToolResult{}, nil
}

type noopTopologyStore struct{}

func (noopTopologyStore) TopologyQuery(ctx context.Context, tid model.TenantID, a rca.TopologyQueryArgs) (model.ToolResult, error) {
	return model.ToolResult{}, nil
}

type noopMemoryStore struct{}

func (noopMemoryStore) MemoryQuery(ctx context.Context, tid model.TenantID, a rca.MemoryQueryArgs) (model.ToolResult, error) {
	return model.ToolResult{}, nil
}

// rca.InterestSink is now backed by samplerInterestSink (interest_adapter.go),
// which wires the real, running sampler.Impl into rca.Engine -- the DR-11
// phase-A/B predicate push back into the sampler is no longer deferred. See
// docs/reports/w19-fix-interestsink-wiring.md.
