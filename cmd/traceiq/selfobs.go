package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"traceiq/internal/selfobs"
)

// simpleRegistry is a minimal selfobs.Registry implementation. selfobs
// itself is interface-only in this scaffold (internal/selfobs/selfobs.go
// declares Recorder/ComponentHealth/ReadinessCheck/Registry with no
// concrete type behind any of them — confirmed by reading the package
// before writing this file) and is out of this wave's edit scope, so the
// composition root supplies a small Prometheus-client-backed implementation
// here rather than leaving /metrics unimplemented. This covers FR-XOPS-4's
// /metrics existence requirement at counter/gauge granularity; the
// dozens of specific named series the register lists
// (traceiq_ingest_spans_dropped_total etc.) are NOT individually wired by
// every feature package in this wave — only the generic Recorder surface
// exists, so a caller CAN emit them, not that every call site does yet.
type simpleRegistry struct {
	reg *prometheus.Registry

	mu       sync.Mutex
	counters map[string]*prometheus.CounterVec
	gauges   map[string]*prometheus.GaugeVec
	hists    map[string]*prometheus.HistogramVec
	degraded sync.Map // component string -> bool

	checksMu sync.Mutex
	checks   []selfobs.ReadinessCheck

	shuttingDown atomic.Bool
}

func newSimpleRegistry() *simpleRegistry {
	return &simpleRegistry{
		reg:      prometheus.NewRegistry(),
		counters: map[string]*prometheus.CounterVec{},
		gauges:   map[string]*prometheus.GaugeVec{},
		hists:    map[string]*prometheus.HistogramVec{},
	}
}

var _ selfobs.Registry = (*simpleRegistry)(nil)

func labelNames(labels map[string]string) []string {
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	return names
}

func (r *simpleRegistry) IncCounter(name string, delta float64, labels map[string]string) {
	r.mu.Lock()
	c, ok := r.counters[name]
	if !ok {
		c = prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: name}, labelNames(labels))
		r.reg.MustRegister(c)
		r.counters[name] = c
	}
	r.mu.Unlock()
	c.With(labels).Add(delta)
}

func (r *simpleRegistry) ObserveHistogram(name string, value float64, labels map[string]string) {
	r.mu.Lock()
	h, ok := r.hists[name]
	if !ok {
		h = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: name}, labelNames(labels))
		r.reg.MustRegister(h)
		r.hists[name] = h
	}
	r.mu.Unlock()
	h.With(labels).Observe(value)
}

func (r *simpleRegistry) SetGauge(name string, value float64, labels map[string]string) {
	r.mu.Lock()
	g, ok := r.gauges[name]
	if !ok {
		g = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: name}, labelNames(labels))
		r.reg.MustRegister(g)
		r.gauges[name] = g
	}
	r.mu.Unlock()
	g.With(labels).Set(value)
}

func (r *simpleRegistry) SetDegraded(component string, degraded bool) {
	r.degraded.Store(component, degraded)
	v := 0.0
	if degraded {
		v = 1.0
	}
	r.SetGauge("traceiq_component_degraded", v, map[string]string{"component": component})
}

func (r *simpleRegistry) RegisterReadiness(c selfobs.ReadinessCheck) {
	r.checksMu.Lock()
	defer r.checksMu.Unlock()
	r.checks = append(r.checks, c)
}

// readyStatus implements FR-XOPS-4's /readyz semantics at the subset this
// wave wires readiness checks for: every registered ReadinessCheck ready,
// and shutdown not begun. The full six-condition list (migrations applied,
// receivers bound, a 30s write probe on both databases, disk headroom) is
// only partially represented — each wired component registers one check
// (see system.go) rather than this file re-deriving all six conditions
// itself.
func (r *simpleRegistry) readyStatus() (bool, map[string]bool) {
	r.checksMu.Lock()
	checks := append([]selfobs.ReadinessCheck(nil), r.checks...)
	r.checksMu.Unlock()

	detail := make(map[string]bool, len(checks))
	ok := !r.shuttingDown.Load()
	for _, c := range checks {
		ready := c.Ready()
		detail[c.Name()] = ready
		if !ready {
			ok = false
		}
	}
	return ok, detail
}

func (r *simpleRegistry) beginShutdown() { r.shuttingDown.Store(true) }

// httpMux builds the bare http.ServeMux X-OPS §3.1's FR-XOPS-4 calls for:
// /healthz (alive + config loaded, no dependency checks, ever), /readyz
// (every registered ReadinessCheck plus "shutdown has not begun"), and
// /metrics (Prometheus exposition). This is deliberately not internal/api —
// the sibling agent's concurrent work owns that package; this is the
// "minimal http.ServeMux" the task instructions explicitly permit as a
// stand-in until internal/api is stable enough to import.
func (r *simpleRegistry) httpMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ok, detail := r.readyStatus()
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(detail)
	})
	mux.Handle("/metrics", promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{}))
	return mux
}

// staticReadiness is a trivial selfobs.ReadinessCheck backed by an
// atomic.Bool a component flips once it's actually up.
type staticReadiness struct {
	name  string
	ready atomic.Bool
}

func newStaticReadiness(name string) *staticReadiness { return &staticReadiness{name: name} }
func (c *staticReadiness) Name() string               { return c.name }
func (c *staticReadiness) Ready() bool                { return c.ready.Load() }
func (c *staticReadiness) SetReady(v bool)            { c.ready.Store(v) }
