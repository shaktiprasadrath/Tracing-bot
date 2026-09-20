package correlate

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// LogAdapter is DR-20 §20.1, verbatim.
type LogAdapter interface {
	Name() string
	LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error)
	QueryByServiceWindow(ctx context.Context, tid model.TenantID, service string, w model.Window, contains string, limit int) ([]model.LogLine, error)
	Capabilities() LogCapabilities
	Health(ctx context.Context) model.HealthReport
	Close() error
}

// MetricAdapter is DR-20 §20.1, verbatim.
type MetricAdapter interface {
	Name() string
	Range(ctx context.Context, tid model.TenantID, templateID string, params map[string]string, w model.Window, stepSeconds int) (model.Series, error)
	ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) ([]model.Exemplar, error)
	Capabilities() MetricCapabilities
	Health(ctx context.Context) model.HealthReport
	Close() error
}

// Correlator is DR-20 §20.1, verbatim. correlate never calls
// store.GetTrace; the caller supplies model.Window and the trace (DR-2).
type Correlator interface {
	LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) (model.LogBundle, error)
	MetricsForSpan(ctx context.Context, tid model.TenantID, s model.Span, w model.Window) (model.MetricBundle, error)
	Stats() Stats
}

// LogCapabilities is DR-20 §20.1, verbatim.
type LogCapabilities struct {
	TenantScoped   bool // false => refuses construction when tenancy.enabled
	TraceIDIndexed bool // false => QueryByServiceWindow heuristic is the only path; always labelled
	MaxLookback    time.Duration
}

// MetricCapabilities is MetricAdapter.Capabilities's return type. The
// register never gives an explicit field list (only LogCapabilities gets
// one, in §20.1); shaped by analogy to it.
// TODO(DR-20): under-specified.
type MetricCapabilities struct {
	TenantScoped bool
	MaxLookback  time.Duration
}

// Stats is Correlator.Stats's return type. The register never gives an
// explicit field list.
// TODO(DR-20): under-specified.
type Stats struct {
	InflightRequests int
	BreakerOpen      bool
	CacheHitRatio    float64
}
