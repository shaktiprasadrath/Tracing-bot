package model

import "time"

// HealthReport is the shared health-check shape used where a consumer
// package declares its own dependency interface with an explicitly
// model-qualified return (e.g. DR-20's `correlate.LogAdapter.Health(ctx)
// model.HealthReport`). internal/store's HotIndex/ColdStore.Health methods
// (DR-6, DR-7) use a bare, package-local `HealthReport` instead — the
// register never gives either one a field list. See
// docs/reports/scaffold-report.md.
type HealthReport struct {
	Healthy   bool
	Message   string
	CheckedAt time.Time
	Details   map[string]string
}

// LogLine is one correlated log record (DR-20 §20.1's LogAdapter,
// §20.5's file-adapter NDJSON shape: "{ts, trace_id, span_id, service,
// level, body, attrs}"). Field-level definition reconstructed from that
// prose — see docs/reports/scaffold-report.md.
type LogLine struct {
	Timestamp time.Time
	TraceID   TraceID
	SpanID    SpanID
	Service   string
	Level     string
	Body      string
	Attrs     AttrMap
}

// LogBundle is correlate.Correlator.LogsForTrace's return type (DR-20 §20.1).
// Reconstructed — the register never gives its fields.
type LogBundle struct {
	TraceID   TraceID
	Lines     []LogLine
	Truncated bool
}

// SeriesPoint is one (timestamp, value) sample of a Series.
type SeriesPoint struct {
	Timestamp time.Time
	Value     float64
}

// Series is correlate.MetricAdapter.Range's return type (DR-20 §20.1).
// Reconstructed — the register never gives its fields.
type Series struct {
	Labels map[string]string
	Points []SeriesPoint
}

// Exemplar is a single sampled trace tying a metric value back to a trace
// (DR-20 §20.1's `correlate.MetricAdapter.ExemplarsFor(...) ([]model.Exemplar,
// error)`). internal/store's HotIndex.ExemplarsFor (DR-6) returns a bare,
// package-local `Exemplar` instead — the two are intentionally distinct
// types (see docs/reports/scaffold-report.md), matching correlate's explicit
// "model." qualification versus store's unqualified interface block.
type Exemplar struct {
	TraceID   TraceID
	SpanID    SpanID
	Timestamp time.Time
	Value     float64
	Labels    map[string]string
}

// MetricBundle is correlate.Correlator.MetricsForSpan's return type
// (DR-20 §20.1). Reconstructed — the register never gives its fields.
type MetricBundle struct {
	Series []Series
	Window Window
}
