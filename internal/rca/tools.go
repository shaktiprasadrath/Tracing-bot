package rca

import (
	"context"
	"fmt"

	"traceiq/internal/model"
)

// --- consumer-declared backends (DR-2's signature rule) ---
//
// Each of the five closed tools (DR-16 §16.1) is backed by a single-method
// interface declared here, in rca, rather than imported from store/topology/
// correlate/memory's concrete types. This wave stubs the Tool bodies against
// these interfaces only — cmd/traceiq wires the real store/topology/
// correlate/memory implementations in a later wave, satisfying these
// interfaces structurally (DR-2). The signature of each method already
// returns model.ToolResult directly so a real backend can do its own
// projection/clamping (DR-16 §16.3's Project allowlist, max_rows_per_tool_
// call, max_evidence_bytes) close to the data instead of rca re-deriving it.
type TraceStore interface {
	TraceQuery(ctx context.Context, tid model.TenantID, a TraceQueryArgs) (model.ToolResult, error)
}

type LogStore interface {
	LogQuery(ctx context.Context, tid model.TenantID, a LogQueryArgs) (model.ToolResult, error)
}

type MetricStore interface {
	MetricQuery(ctx context.Context, tid model.TenantID, a MetricQueryArgs) (model.ToolResult, error)
}

type TopologyStore interface {
	TopologyQuery(ctx context.Context, tid model.TenantID, a TopologyQueryArgs) (model.ToolResult, error)
}

type MemoryStore interface {
	MemoryQuery(ctx context.Context, tid model.TenantID, a MemoryQueryArgs) (model.ToolResult, error)
}

// --- the five Tool implementations ---

type traceQueryTool struct{ backend TraceStore }
type logQueryTool struct{ backend LogStore }
type metricQueryTool struct{ backend MetricStore }
type topologyQueryTool struct{ backend TopologyStore }
type memoryQueryTool struct{ backend MemoryStore }

func NewTraceQueryTool(b TraceStore) Tool       { return traceQueryTool{backend: b} }
func NewLogQueryTool(b LogStore) Tool           { return logQueryTool{backend: b} }
func NewMetricQueryTool(b MetricStore) Tool     { return metricQueryTool{backend: b} }
func NewTopologyQueryTool(b TopologyStore) Tool { return topologyQueryTool{backend: b} }
func NewMemoryQueryTool(b MemoryStore) Tool     { return memoryQueryTool{backend: b} }

func (t traceQueryTool) Name() model.ToolName { return ToolTraceQuery }
func (t traceQueryTool) Schema() ArgSchema {
	return ArgSchema{Name: ToolTraceQuery, Description: "query traces by service/operation/status/attrs"}
}
func (t traceQueryTool) Cost() ToolCost { return defaultToolCost }
func (t traceQueryTool) Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	if a.Tool != ToolTraceQuery || a.Trace == nil {
		return model.ToolResult{}, fmt.Errorf("rca: trace_query invoked with mismatched args")
	}
	if t.backend == nil {
		return model.ToolResult{}, ErrToolUnavailable
	}
	r, err := t.backend.TraceQuery(ctx, tid, *a.Trace)
	r.Tool = ToolTraceQuery
	return r, err
}

func (t logQueryTool) Name() model.ToolName { return ToolLogQuery }
func (t logQueryTool) Schema() ArgSchema {
	return ArgSchema{Name: ToolLogQuery, Description: "query logs by trace or service/window"}
}
func (t logQueryTool) Cost() ToolCost { return defaultToolCost }
func (t logQueryTool) Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	if a.Tool != ToolLogQuery || a.Log == nil {
		return model.ToolResult{}, fmt.Errorf("rca: log_query invoked with mismatched args")
	}
	if t.backend == nil {
		return model.ToolResult{}, ErrToolUnavailable
	}
	r, err := t.backend.LogQuery(ctx, tid, *a.Log)
	r.Tool = ToolLogQuery
	return r, err
}

func (t metricQueryTool) Name() model.ToolName { return ToolMetricQuery }
func (t metricQueryTool) Schema() ArgSchema {
	return ArgSchema{Name: ToolMetricQuery, Description: "query a registered metric template or RED(service,operation,window)"}
}
func (t metricQueryTool) Cost() ToolCost { return defaultToolCost }
func (t metricQueryTool) Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	if a.Tool != ToolMetricQuery || a.Metric == nil {
		return model.ToolResult{}, fmt.Errorf("rca: metric_query invoked with mismatched args")
	}
	if t.backend == nil {
		return model.ToolResult{}, ErrToolUnavailable
	}
	r, err := t.backend.MetricQuery(ctx, tid, *a.Metric)
	r.Tool = ToolMetricQuery
	return r, err
}

func (t topologyQueryTool) Name() model.ToolName { return ToolTopologyQuery }
func (t topologyQueryTool) Schema() ArgSchema {
	return ArgSchema{Name: ToolTopologyQuery, Description: "query the service topology neighborhood"}
}
func (t topologyQueryTool) Cost() ToolCost { return defaultToolCost }
func (t topologyQueryTool) Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	if a.Tool != ToolTopologyQuery || a.Topology == nil {
		return model.ToolResult{}, fmt.Errorf("rca: topology_query invoked with mismatched args")
	}
	if t.backend == nil {
		return model.ToolResult{}, ErrToolUnavailable
	}
	r, err := t.backend.TopologyQuery(ctx, tid, *a.Topology)
	r.Tool = ToolTopologyQuery
	return r, err
}

func (t memoryQueryTool) Name() model.ToolName { return ToolMemoryQuery }
func (t memoryQueryTool) Schema() ArgSchema {
	return ArgSchema{Name: ToolMemoryQuery, Description: "query memory records by fingerprint/text"}
}
func (t memoryQueryTool) Cost() ToolCost { return defaultToolCost }
func (t memoryQueryTool) Invoke(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	if a.Tool != ToolMemoryQuery || a.Memory == nil {
		return model.ToolResult{}, fmt.Errorf("rca: memory_query invoked with mismatched args")
	}
	if t.backend == nil {
		return model.ToolResult{}, ErrToolUnavailable
	}
	r, err := t.backend.MemoryQuery(ctx, tid, *a.Memory)
	r.Tool = ToolMemoryQuery
	return r, err
}

// defaultToolCost is DR-17 §17.5's rca.budget row/byte ceilings, clamped
// server-side (rca.Tool.Cost's doc comment).
var defaultToolCost = ToolCost{
	MaxRows:      500,  // rca.budget.max_rows_per_tool_call
	MaxBytes:     8192, // rca.budget.max_evidence_bytes
	MaxWallClock: toolCallTimeout,
}

// ErrToolUnavailable is DR-16 §16.3's "unavailable" verdict cause: a tool
// dispatched with no backend wired (batch-2 store/topology/correlate/memory
// not yet plugged in, or a genuine backend outage).
var ErrToolUnavailable = fmt.Errorf("rca: tool backend unavailable")

// --- ToolRegistry ---

// Registry is the closed five-tool ToolRegistry (DR-15). NewRegistry always
// registers exactly the five DR-16 §16.1 tools; a nil backend for any of
// them yields a Tool whose Invoke returns ErrToolUnavailable (verdict
// "unavailable") rather than a registry with a missing entry — the set of
// Names() is closed regardless of what is wired behind it.
type Registry struct {
	tools map[model.ToolName]Tool
}

func NewRegistry(trace TraceStore, log LogStore, metric MetricStore, topo TopologyStore, mem MemoryStore) *Registry {
	return &Registry{tools: map[model.ToolName]Tool{
		ToolTraceQuery:    NewTraceQueryTool(trace),
		ToolLogQuery:      NewLogQueryTool(log),
		ToolMetricQuery:   NewMetricQueryTool(metric),
		ToolTopologyQuery: NewTopologyQueryTool(topo),
		ToolMemoryQuery:   NewMemoryQueryTool(mem),
	}}
}

func (r *Registry) Get(n model.ToolName) (Tool, bool) {
	t, ok := r.tools[n]
	return t, ok
}

func (r *Registry) Names() []model.ToolName {
	return []model.ToolName{ToolTraceQuery, ToolLogQuery, ToolMetricQuery, ToolTopologyQuery, ToolMemoryQuery}
}

func (r *Registry) Dispatch(ctx context.Context, tid model.TenantID, a ToolArgs) (model.ToolResult, error) {
	t, ok := r.Get(a.Tool)
	if !ok {
		return model.ToolResult{}, fmt.Errorf("rca: unknown tool %q", a.Tool)
	}
	if a.TenantID != tid {
		return model.ToolResult{}, fmt.Errorf("rca: tenant mismatch on dispatch")
	}
	return t.Invoke(ctx, tid, a)
}

var _ ToolRegistry = (*Registry)(nil)
