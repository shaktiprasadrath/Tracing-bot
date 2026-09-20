package correlate

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"traceiq/internal/model"
)

// Config is correlate's runtime configuration (DR-20 §20.2/§20.4's key
// paths; values owned by 01 §7, cited here only as Go fields for the
// in-process constructor).
type Config struct {
	MaxInflightPerTenant int64
	MaxInflightGlobal    int64
	CacheTTL             time.Duration
	CacheMaxEntries      int
	DefaultLimit         int
	// BreakerFailures is DR-20 §20.4's breaker_failures: consecutive
	// per-adapter failures before the breaker opens (FR-F07-7).
	BreakerFailures int
	// BreakerOpenDuration is DR-20 §20.4's breaker_open: how long a
	// tripped adapter fails fast before being retried (FR-F07-7).
	BreakerOpenDuration time.Duration
}

// DefaultConfig mirrors DR-20 §20.4's published defaults.
func DefaultConfig() Config {
	return Config{
		MaxInflightPerTenant: 4,
		MaxInflightGlobal:    16,
		CacheTTL:             60 * time.Second,
		CacheMaxEntries:      2048,
		DefaultLimit:         500,
		BreakerFailures:      5,
		BreakerOpenDuration:  30 * time.Second,
	}
}

type cacheEntry struct {
	logs     *model.LogBundle
	metrics  *model.MetricBundle
	storedAt time.Time
	tenant   model.TenantID
}

// breakerState is one adapter's circuit-breaker state (FR-F07-7, DR-20
// §20.4): breaker_failures consecutive failures opens the breaker for
// breaker_open; while open, calls fail fast with ErrAdapterUnavailable
// rather than reaching the adapter at all.
type breakerState struct {
	mu                  sync.Mutex
	consecutiveFailures int
	openUntil           time.Time
}

func (b *breakerState) isOpen(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return now.Before(b.openUntil)
}

func (b *breakerState) recordFailure(now time.Time, cfg Config) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures++
	if b.consecutiveFailures >= cfg.BreakerFailures {
		b.openUntil = now.Add(cfg.BreakerOpenDuration)
	}
}

func (b *breakerState) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.openUntil = time.Time{}
}

// correlator is the Correlator implementation: cache + bounded fan-out over
// LogAdapter/MetricAdapter (DR-20 §20.1, §20.4).
type correlator struct {
	cfg            Config
	clock          model.Clock
	logAdapters    []LogAdapter
	metricAdapters []MetricAdapter

	globalSem *semaphore.Weighted

	mu        sync.Mutex
	tenantSem map[model.TenantID]*semaphore.Weighted
	cache     map[string]cacheEntry
	// tenantOrder is the insertion order of cache keys per tenant, oldest
	// first, used for a simple bounded eviction (a scoped-down stand-in for
	// DR-20 §20.4's exact per-tenant-partitioned LRU — see
	// docs/reports/w11-correlate-memory.md).
	tenantOrder map[model.TenantID][]string
	// breakers is per-adapter (keyed by Adapter.Name()) circuit-breaker
	// state, shared across log and metric adapters (FR-F07-7).
	breakers map[string]*breakerState

	statsMu   sync.Mutex
	inflight  int
	cacheHits int
	cacheTot  int
}

// New constructs a Correlator (DR-20 §20.1). The production signature also
// threads an auth.EgressDialer through to network-backed adapters (Loki,
// Elasticsearch, Prometheus); those adapters are out of this pass's scope
// (see docs/reports/w11-correlate-memory.md), so New here takes already-
// constructed adapters directly — the dev/file adapters need no dialer.
func New(cfg Config, clock model.Clock, logAdapters []LogAdapter, metricAdapters []MetricAdapter) (Correlator, error) {
	if clock == nil {
		return nil, fmt.Errorf("correlate: clock is required")
	}
	if cfg.MaxInflightPerTenant <= 0 {
		cfg.MaxInflightPerTenant = DefaultConfig().MaxInflightPerTenant
	}
	if cfg.MaxInflightGlobal <= 0 {
		cfg.MaxInflightGlobal = DefaultConfig().MaxInflightGlobal
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = DefaultConfig().CacheTTL
	}
	if cfg.CacheMaxEntries <= 0 {
		cfg.CacheMaxEntries = DefaultConfig().CacheMaxEntries
	}
	if cfg.DefaultLimit <= 0 {
		cfg.DefaultLimit = DefaultConfig().DefaultLimit
	}
	if cfg.BreakerFailures <= 0 {
		cfg.BreakerFailures = DefaultConfig().BreakerFailures
	}
	if cfg.BreakerOpenDuration <= 0 {
		cfg.BreakerOpenDuration = DefaultConfig().BreakerOpenDuration
	}
	return &correlator{
		cfg:            cfg,
		clock:          clock,
		logAdapters:    logAdapters,
		metricAdapters: metricAdapters,
		globalSem:      semaphore.NewWeighted(cfg.MaxInflightGlobal),
		tenantSem:      map[model.TenantID]*semaphore.Weighted{},
		cache:          map[string]cacheEntry{},
		tenantOrder:    map[model.TenantID][]string{},
		breakers:       map[string]*breakerState{},
	}, nil
}

// breakerFor returns (creating if needed) the shared breakerState for an
// adapter name.
func (c *correlator) breakerFor(name string) *breakerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.breakers[name]
	if !ok {
		b = &breakerState{}
		c.breakers[name] = b
	}
	return b
}

// anyBreakerOpen reports whether any known adapter's breaker is currently
// open, for Stats().
func (c *correlator) anyBreakerOpen() bool {
	now := c.clock.Now()
	c.mu.Lock()
	breakers := make([]*breakerState, 0, len(c.breakers))
	for _, b := range c.breakers {
		breakers = append(breakers, b)
	}
	c.mu.Unlock()
	for _, b := range breakers {
		if b.isOpen(now) {
			return true
		}
	}
	return false
}

func (c *correlator) tenantSemaphore(tid model.TenantID) *semaphore.Weighted {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.tenantSem[tid]
	if !ok {
		s = semaphore.NewWeighted(c.cfg.MaxInflightPerTenant)
		c.tenantSem[tid] = s
	}
	return s
}

// acquire performs the bounded fan-out gate: per-tenant AND global caps,
// non-blocking (ErrAdapterBusy immediately, never queued) per FR-F07-7.
func (c *correlator) acquire(tid model.TenantID) (release func(), err error) {
	tsem := c.tenantSemaphore(tid)
	if !tsem.TryAcquire(1) {
		return nil, ErrAdapterBusy
	}
	if !c.globalSem.TryAcquire(1) {
		tsem.Release(1)
		return nil, ErrAdapterBusy
	}
	c.statsMu.Lock()
	c.inflight++
	c.statsMu.Unlock()
	return func() {
		c.globalSem.Release(1)
		tsem.Release(1)
		c.statsMu.Lock()
		c.inflight--
		c.statsMu.Unlock()
	}, nil
}

func (c *correlator) cacheGet(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsMu.Lock()
	c.cacheTot++
	c.statsMu.Unlock()
	e, ok := c.cache[key]
	if !ok {
		return cacheEntry{}, false
	}
	if c.clock.Since(e.storedAt) > c.cfg.CacheTTL {
		delete(c.cache, key)
		return cacheEntry{}, false
	}
	c.statsMu.Lock()
	c.cacheHits++
	c.statsMu.Unlock()
	return e, true
}

func (c *correlator) cacheSet(key string, e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[key]; !exists {
		order := c.tenantOrder[e.tenant]
		// Per-tenant partitioned bound: this tenant's share is
		// CacheMaxEntries / max(1, number of tenants seen) — evict this
		// tenant's own oldest entry first (DR-20 §20.4: "one tenant cannot
		// evict another tenant's entries beyond its partition share").
		share := c.cfg.CacheMaxEntries
		if n := len(c.tenantOrder); n > 1 {
			share = c.cfg.CacheMaxEntries / n
			if share < 1 {
				share = 1
			}
		}
		for len(order) >= share {
			oldest := order[0]
			order = order[1:]
			delete(c.cache, oldest)
		}
		order = append(order, key)
		c.tenantOrder[e.tenant] = order
	}
	c.cache[key] = e
}

func windowKey(w model.Window) string {
	return fmt.Sprintf("%d-%d", w.Start.UnixNano(), w.End.UnixNano())
}

// LogsForTrace is FR-F07-1: exact trace_id join across configured
// LogAdapters, cached, bounded, with a heuristic service+window fallback
// (FR-F07-5) when no adapter has any trace_id-tagged data.
func (c *correlator) LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) (model.LogBundle, error) {
	if limit <= 0 {
		limit = c.cfg.DefaultLimit
	}
	key := fmt.Sprintf("logs|%s|%x|%s|%d", tid, traceID, windowKey(w), limit)
	if e, ok := c.cacheGet(key); ok && e.logs != nil {
		return *e.logs, nil
	}

	release, err := c.acquire(tid)
	if err != nil {
		return model.LogBundle{}, err
	}
	defer release()

	var all []model.LogLine
	var adapterErrs []error
	for _, ad := range c.logAdapters {
		br := c.breakerFor(ad.Name())
		if br.isOpen(c.clock.Now()) {
			// Breaker open: fail fast, never reach the adapter (FR-F07-7).
			adapterErrs = append(adapterErrs, ErrAdapterUnavailable)
			continue
		}
		lines, err := ad.LogsForTrace(ctx, tid, traceID, w, limit)
		if err != nil {
			adapterErrs = append(adapterErrs, err)
			br.recordFailure(c.clock.Now(), c.cfg)
			continue
		}
		br.recordSuccess()
		all = append(all, lines...)
	}

	// TODO(FR-F07-5): the heuristic QueryByServiceWindow fallback needs a
	// service to query by, which LogsForTrace's DR-20 §20.1 signature does
	// not carry (Correlator has no store access under DR-2). Not implemented
	// this pass — see docs/reports/w11-correlate-memory.md "remaining".

	sort.Slice(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })
	truncated := false
	if len(all) > limit {
		all = all[:limit]
		truncated = true
	}
	bundle := model.LogBundle{TraceID: traceID, Lines: all, Truncated: truncated}
	c.cacheSet(key, cacheEntry{logs: &bundle, storedAt: c.clock.Now(), tenant: tid})

	if len(adapterErrs) > 0 && len(all) == 0 {
		return bundle, fmt.Errorf("correlate: %d adapter(s) failed: %v", len(adapterErrs), adapterErrs[0])
	}
	return bundle, nil
}

// MetricsForSpan is FR-F07-2: exemplar-linked metric samples for the span's
// (service, operation) within w, cached and bounded like LogsForTrace.
func (c *correlator) MetricsForSpan(ctx context.Context, tid model.TenantID, s model.Span, w model.Window) (model.MetricBundle, error) {
	key := fmt.Sprintf("metrics|%s|%x|%s", tid, s.SpanID, windowKey(w))
	if e, ok := c.cacheGet(key); ok && e.metrics != nil {
		return *e.metrics, nil
	}

	release, err := c.acquire(tid)
	if err != nil {
		return model.MetricBundle{}, err
	}
	defer release()

	service := ""
	if s.Resource != nil {
		service = s.Resource.ServiceName
	}
	operation := s.Name

	var series []model.Series
	var adapterErrs []error
	for _, ad := range c.metricAdapters {
		br := c.breakerFor(ad.Name())
		if br.isOpen(c.clock.Now()) {
			adapterErrs = append(adapterErrs, ErrAdapterUnavailable)
			continue
		}
		exs, err := ad.ExemplarsFor(ctx, tid, service, operation, w)
		if err != nil {
			adapterErrs = append(adapterErrs, err)
			br.recordFailure(c.clock.Now(), c.cfg)
			continue
		}
		br.recordSuccess()
		if len(exs) == 0 {
			continue
		}
		pts := make([]model.SeriesPoint, 0, len(exs))
		for _, ex := range exs {
			pts = append(pts, model.SeriesPoint{Timestamp: ex.Timestamp, Value: ex.Value})
		}
		series = append(series, model.Series{
			Labels: map[string]string{"adapter": ad.Name(), "service": service, "operation": operation},
			Points: pts,
		})
	}

	bundle := model.MetricBundle{Series: series, Window: w}
	c.cacheSet(key, cacheEntry{metrics: &bundle, storedAt: c.clock.Now(), tenant: tid})

	if len(adapterErrs) > 0 && len(series) == 0 {
		return bundle, fmt.Errorf("correlate: %d adapter(s) failed: %v", len(adapterErrs), adapterErrs[0])
	}
	return bundle, nil
}

func (c *correlator) Stats() Stats {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	ratio := 0.0
	if c.cacheTot > 0 {
		ratio = float64(c.cacheHits) / float64(c.cacheTot)
	}
	return Stats{InflightRequests: c.inflight, BreakerOpen: c.anyBreakerOpen(), CacheHitRatio: ratio}
}
