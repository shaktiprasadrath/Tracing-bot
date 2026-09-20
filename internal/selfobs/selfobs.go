package selfobs

// Recorder is the metrics-emission seam every other package reports
// through — e.g. traceiq_ingest_spans_dropped_total (DR-28 §28.1),
// traceiq_sampler_traces_lost_total (DR-9), traceiq_component_degraded
// (DR-33 §33.1). The register names dozens of specific metric series across
// the DRs but never gives selfobs an explicit Go interface; this is a
// reasonable minimal seam reconstructed from those usages. See
// docs/reports/scaffold-report.md.
type Recorder interface {
	IncCounter(name string, delta float64, labels map[string]string)
	ObserveHistogram(name string, value float64, labels map[string]string)
	SetGauge(name string, value float64, labels map[string]string)
}

// ComponentHealth backs the traceiq_component_degraded{component=...}
// gauge DR-33 §33.1 requires ("those dependencies move to
// /readyz?verbose=true and to traceiq_component_degraded{component=...}
// gauges").
type ComponentHealth interface {
	SetDegraded(component string, degraded bool)
}

// ReadinessCheck is one contributor to /readyz (DR-33 §33.1's six
// conditions). The readiness aggregator (cmd/traceiq, DR-33) polls every
// registered check.
type ReadinessCheck interface {
	Name() string
	Ready() bool
}

// Registry is the process-wide selfobs surface: metrics recording,
// component-degraded gauges, and readiness-check registration for the
// /metrics, /healthz and /readyz endpoints (DR-26 §26.2's
// selfobs.metrics_endpoint, DR-33 §33.1).
type Registry interface {
	Recorder
	ComponentHealth
	RegisterReadiness(c ReadinessCheck)
}
