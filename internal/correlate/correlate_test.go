package correlate_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"traceiq/internal/correlate"
	"traceiq/internal/model"
)

// fakeClock is a minimal, local model.Clock stand-in (correlate is not
// permitted to import internal/eval per DR-2's adjacency table, so this is
// hand-rolled rather than reusing eval.VirtualClock).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
func (c *fakeClock) NewTicker(d time.Duration) model.Ticker           { panic("not used") }
func (c *fakeClock) NewTimer(d time.Duration) model.Timer             { panic("not used") }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

func mkTraceID(b byte) model.TraceID {
	var t model.TraceID
	for i := range t {
		t[i] = b
	}
	return t
}

func mkSpanID(b byte) model.SpanID {
	var s model.SpanID
	for i := range s {
		s[i] = b
	}
	return s
}

func TestFileLogAdapter_LogsForTrace_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	tid := model.TenantID("acme")
	trace := mkTraceID(0xAB)
	other := mkTraceID(0xCD)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	must(t, correlate.WriteDevLogLine(dir, tid, "a.ndjson", now, trace, mkSpanID(1), "checkout", "error", "payment timeout", map[string]string{"k": "v"}))
	must(t, correlate.WriteDevLogLine(dir, tid, "a.ndjson", now.Add(time.Second), trace, mkSpanID(2), "checkout", "info", "retrying", nil))
	must(t, correlate.WriteDevLogLine(dir, tid, "a.ndjson", now, other, mkSpanID(3), "checkout", "info", "unrelated line", nil))

	ad := correlate.NewFileLogAdapter(dir)
	w := model.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	lines, err := ad.LogsForTrace(context.Background(), tid, trace, w, 100)
	if err != nil {
		t.Fatalf("LogsForTrace: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 matching lines, got %d: %+v", len(lines), lines)
	}
	for _, l := range lines {
		if l.TraceID != trace {
			t.Fatalf("returned line with wrong trace id: %x", l.TraceID)
		}
	}
	if lines[0].Body != "payment timeout" || lines[1].Body != "retrying" {
		t.Fatalf("unexpected ordering/content: %+v", lines)
	}
}

func TestFileLogAdapter_TenantIsolation(t *testing.T) {
	dir := t.TempDir()
	trace := mkTraceID(0x11)
	now := time.Now().UTC()

	must(t, correlate.WriteDevLogLine(dir, "tenant-a", "a.ndjson", now, trace, mkSpanID(1), "svc", "info", "tenant A line", nil))
	must(t, correlate.WriteDevLogLine(dir, "tenant-b", "a.ndjson", now, trace, mkSpanID(1), "svc", "info", "tenant B line", nil))

	ad := correlate.NewFileLogAdapter(dir)
	w := model.Window{}
	lines, err := ad.LogsForTrace(context.Background(), "tenant-a", trace, w, 100)
	if err != nil {
		t.Fatalf("LogsForTrace: %v", err)
	}
	if len(lines) != 1 || lines[0].Body != "tenant A line" {
		t.Fatalf("cross-tenant leak: got %+v", lines)
	}
}

func TestFileMetricAdapter_ExemplarsFor(t *testing.T) {
	dir := t.TempDir()
	tid := model.TenantID("acme")
	trace := mkTraceID(0x42)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	must(t, correlate.WriteDevExemplar(dir, tid, "m.ndjson", now, trace, mkSpanID(9), "checkout", "POST /pay", 123.4, map[string]string{"le": "0.5"}))
	must(t, correlate.WriteDevExemplar(dir, tid, "m.ndjson", now.Add(time.Second), trace, mkSpanID(9), "checkout", "POST /pay", 456.7, nil))
	must(t, correlate.WriteDevExemplar(dir, tid, "m.ndjson", now, trace, mkSpanID(9), "other-service", "GET /x", 1.0, nil))

	ad := correlate.NewFileMetricAdapter(dir)
	w := model.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	exs, err := ad.ExemplarsFor(context.Background(), tid, "checkout", "POST /pay", w)
	if err != nil {
		t.Fatalf("ExemplarsFor: %v", err)
	}
	if len(exs) != 2 {
		t.Fatalf("want 2 exemplars, got %d: %+v", len(exs), exs)
	}
	if exs[0].TraceID != trace {
		t.Fatalf("exemplar trace id mismatch: %x", exs[0].TraceID)
	}
}

func newTestCorrelator(t *testing.T, dir string, cfg correlate.Config, clock model.Clock) correlate.Correlator {
	t.Helper()
	logAd := correlate.NewFileLogAdapter(dir)
	metAd := correlate.NewFileMetricAdapter(dir)
	c, err := correlate.New(cfg, clock, []correlate.LogAdapter{logAd}, []correlate.MetricAdapter{metAd})
	if err != nil {
		t.Fatalf("correlate.New: %v", err)
	}
	return c
}

func TestCorrelator_LogsForTrace_JoinsDevAdapter(t *testing.T) {
	dir := t.TempDir()
	tid := model.TenantID("acme")
	trace := mkTraceID(0x77)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	must(t, correlate.WriteDevLogLine(dir, tid, "a.ndjson", now, trace, mkSpanID(1), "checkout", "error", "boom", nil))

	clk := newFakeClock(now)
	c := newTestCorrelator(t, dir, correlate.DefaultConfig(), clk)
	w := model.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	bundle, err := c.LogsForTrace(context.Background(), tid, trace, w, 10)
	if err != nil {
		t.Fatalf("LogsForTrace: %v", err)
	}
	if len(bundle.Lines) != 1 || bundle.Lines[0].Body != "boom" {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
}

func TestCorrelator_MetricsForSpan_JoinsDevAdapter(t *testing.T) {
	dir := t.TempDir()
	tid := model.TenantID("acme")
	trace := mkTraceID(0x88)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	must(t, correlate.WriteDevExemplar(dir, tid, "m.ndjson", now, trace, mkSpanID(2), "checkout", "POST /pay", 99.0, nil))

	clk := newFakeClock(now)
	c := newTestCorrelator(t, dir, correlate.DefaultConfig(), clk)
	span := model.Span{
		SpanID:   mkSpanID(2),
		Name:     "POST /pay",
		Resource: &model.Resource{ServiceName: "checkout"},
	}
	w := model.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	bundle, err := c.MetricsForSpan(context.Background(), tid, span, w)
	if err != nil {
		t.Fatalf("MetricsForSpan: %v", err)
	}
	if len(bundle.Series) != 1 || len(bundle.Series[0].Points) != 1 || bundle.Series[0].Points[0].Value != 99.0 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
}

// countingLogAdapter wraps a FileLogAdapter and counts calls, to prove a
// cache hit avoids re-fetch.
type countingLogAdapter struct {
	*correlate.FileLogAdapter
	calls int64
}

func (a *countingLogAdapter) LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error) {
	atomic.AddInt64(&a.calls, 1)
	return a.FileLogAdapter.LogsForTrace(ctx, tid, traceID, w, limit)
}

func TestCorrelator_CacheHitAvoidsRefetch(t *testing.T) {
	dir := t.TempDir()
	tid := model.TenantID("acme")
	trace := mkTraceID(0x99)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	must(t, correlate.WriteDevLogLine(dir, tid, "a.ndjson", now, trace, mkSpanID(1), "checkout", "error", "boom", nil))

	clk := newFakeClock(now)
	counting := &countingLogAdapter{FileLogAdapter: correlate.NewFileLogAdapter(dir)}
	c, err := correlate.New(correlate.DefaultConfig(), clk, []correlate.LogAdapter{counting}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := model.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}

	if _, err := c.LogsForTrace(context.Background(), tid, trace, w, 10); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := c.LogsForTrace(context.Background(), tid, trace, w, 10); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := atomic.LoadInt64(&counting.calls); got != 1 {
		t.Fatalf("want exactly 1 adapter call (second served from cache), got %d", got)
	}

	stats := c.Stats()
	if stats.CacheHitRatio <= 0 {
		t.Fatalf("expected nonzero cache hit ratio, got %v", stats.CacheHitRatio)
	}

	// After TTL expiry, the adapter is re-queried.
	clk.Advance(2 * correlate.DefaultConfig().CacheTTL)
	if _, err := c.LogsForTrace(context.Background(), tid, trace, w, 10); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if got := atomic.LoadInt64(&counting.calls); got != 2 {
		t.Fatalf("want 2 adapter calls after TTL expiry, got %d", got)
	}
}

// blockingLogAdapter blocks in LogsForTrace until release is closed, so the
// test can hold max_inflight calls in flight and assert the next one is
// rejected immediately with ErrAdapterBusy rather than queued.
type blockingLogAdapter struct {
	release chan struct{}
}

func (a *blockingLogAdapter) Name() string { return "blocking" }
func (a *blockingLogAdapter) LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error) {
	<-a.release
	return nil, nil
}
func (a *blockingLogAdapter) QueryByServiceWindow(ctx context.Context, tid model.TenantID, service string, w model.Window, contains string, limit int) ([]model.LogLine, error) {
	return nil, nil
}
func (a *blockingLogAdapter) Capabilities() correlate.LogCapabilities {
	return correlate.LogCapabilities{TenantScoped: true, TraceIDIndexed: true}
}
func (a *blockingLogAdapter) Health(ctx context.Context) model.HealthReport {
	return model.HealthReport{Healthy: true}
}
func (a *blockingLogAdapter) Close() error { return nil }

func TestCorrelator_BoundedFanOut_DoesNotExceedCap(t *testing.T) {
	release := make(chan struct{})
	ad := &blockingLogAdapter{release: release}
	cfg := correlate.DefaultConfig()
	cfg.MaxInflightPerTenant = 2
	cfg.MaxInflightGlobal = 2
	clk := newFakeClock(time.Now())
	c, err := correlate.New(cfg, clk, []correlate.LogAdapter{ad}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tid := model.TenantID("acme")
	w := model.Window{}

	var wg sync.WaitGroup
	started := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started <- struct{}{}
			// Distinct traceIDs so the cache key differs and each call must
			// actually acquire a semaphore slot rather than being served
			// from cache.
			tr := mkTraceID(byte(i + 1))
			_, _ = c.LogsForTrace(context.Background(), tid, tr, w, 10)
		}(i)
	}
	<-started
	<-started
	time.Sleep(50 * time.Millisecond) // let both goroutines block inside the adapter

	// The cap is saturated (2/2 in flight); a third call must fail fast with
	// ErrAdapterBusy, never queue.
	done := make(chan error, 1)
	go func() {
		_, err := c.LogsForTrace(context.Background(), tid, mkTraceID(0x09), w, 10)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, correlate.ErrAdapterBusy) {
			t.Fatalf("want ErrAdapterBusy, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("third call was queued instead of failing fast")
	}

	close(release)
	wg.Wait()
}

// failingLogAdapter fails every call until succeedAfter calls have been
// made, then succeeds; it counts how many times it was actually invoked so
// the test can prove the breaker skips calling it while open.
type failingLogAdapter struct {
	calls int64
}

func (a *failingLogAdapter) Name() string { return "failing" }
func (a *failingLogAdapter) LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error) {
	atomic.AddInt64(&a.calls, 1)
	return nil, errors.New("boom")
}
func (a *failingLogAdapter) QueryByServiceWindow(ctx context.Context, tid model.TenantID, service string, w model.Window, contains string, limit int) ([]model.LogLine, error) {
	return nil, nil
}
func (a *failingLogAdapter) Capabilities() correlate.LogCapabilities {
	return correlate.LogCapabilities{TenantScoped: true, TraceIDIndexed: true}
}
func (a *failingLogAdapter) Health(ctx context.Context) model.HealthReport {
	return model.HealthReport{Healthy: false}
}
func (a *failingLogAdapter) Close() error { return nil }

// TestCorrelator_BreakerOpensAfterConsecutiveFailures verifies FR-F07-7 /
// AC-F07-3: after breaker_failures consecutive failures, the adapter is
// skipped entirely (fails fast with ErrAdapterUnavailable, never actually
// called) for breaker_open, then resumes being called once that elapses.
func TestCorrelator_BreakerOpensAfterConsecutiveFailures(t *testing.T) {
	ad := &failingLogAdapter{}
	cfg := correlate.DefaultConfig()
	cfg.BreakerFailures = 3
	cfg.BreakerOpenDuration = 30 * time.Second
	clk := newFakeClock(time.Now())
	c, err := correlate.New(cfg, clk, []correlate.LogAdapter{ad}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tid := model.TenantID("acme")
	w := model.Window{}

	// 3 consecutive failures, each against a distinct trace id so the
	// cache never short-circuits the adapter call.
	for i := 0; i < 3; i++ {
		if _, err := c.LogsForTrace(context.Background(), tid, mkTraceID(byte(i+1)), w, 10); err == nil {
			t.Fatalf("call %d: expected an error from the failing adapter", i)
		}
	}
	if got := atomic.LoadInt64(&ad.calls); got != 3 {
		t.Fatalf("want 3 adapter calls before the breaker opens, got %d", got)
	}
	if !c.Stats().BreakerOpen {
		t.Fatalf("expected Stats().BreakerOpen=true after %d consecutive failures", cfg.BreakerFailures)
	}

	// Breaker is now open: a further call must fail immediately WITHOUT
	// reaching the adapter (calls count stays at 3).
	if _, err := c.LogsForTrace(context.Background(), tid, mkTraceID(0xEE), w, 10); err == nil {
		t.Fatalf("expected an error while the breaker is open")
	} else if !errors.Is(err, correlate.ErrAdapterUnavailable) && !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected ErrAdapterUnavailable while breaker is open, got %v", err)
	}
	if got := atomic.LoadInt64(&ad.calls); got != 3 {
		t.Fatalf("breaker-open call must not reach the adapter: want 3 calls, got %d", got)
	}

	// After breaker_open elapses, the adapter is retried.
	clk.Advance(cfg.BreakerOpenDuration + time.Second)
	if _, err := c.LogsForTrace(context.Background(), tid, mkTraceID(0xFF), w, 10); err == nil {
		t.Fatalf("expected an error (adapter still fails), but it should have been CALLED again")
	}
	if got := atomic.LoadInt64(&ad.calls); got != 4 {
		t.Fatalf("want 4 adapter calls after breaker_open elapses, got %d", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
