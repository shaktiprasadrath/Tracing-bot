package topology

import (
	"context"
	"sync"
	"testing"
	"time"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// fakeClock is a minimal model.Clock for deterministic tests (DR-31). Most
// tests only exercise Now() (sweep* is invoked directly rather than via a
// ticker), but TestRun_DrivesFlushAndVanishSweepAndPendingSweep (review-pass
// regression for the Run method) needs real, individually-triggerable
// tickers keyed by the requested duration, so NewTicker is a small fake
// registry rather than returning nil.
type fakeClock struct {
	now time.Time

	mu      sync.Mutex
	tickers map[time.Duration]*fakeTicker
}

type fakeTicker struct{ ch chan time.Time }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}

func (f *fakeClock) Now() time.Time                  { return f.now }
func (f *fakeClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }

func (f *fakeClock) NewTicker(d time.Duration) model.Ticker {
	ft := &fakeTicker{ch: make(chan time.Time, 1)}
	f.mu.Lock()
	if f.tickers == nil {
		f.tickers = make(map[time.Duration]*fakeTicker)
	}
	f.tickers[d] = ft
	f.mu.Unlock()
	return ft
}

func (f *fakeClock) tickerFor(t *testing.T, d time.Duration) *fakeTicker {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		ft := f.tickers[d]
		f.mu.Unlock()
		if ft != nil {
			return ft
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no ticker registered for duration %s within timeout", d)
	return nil
}

func (f *fakeClock) NewTimer(d time.Duration) model.Timer             { return nil }
func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

// fakeEdgeSink is a minimal EdgeSink recording WriteEdges calls, used to
// verify Run's periodic Flush actually reaches the sink.
type fakeEdgeSink struct {
	mu    sync.Mutex
	calls int
	done  chan struct{}
}

func newFakeEdgeSink() *fakeEdgeSink { return &fakeEdgeSink{done: make(chan struct{}, 8)} }

func (s *fakeEdgeSink) WriteEdges(ctx context.Context, tid model.TenantID, edges []Edge) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.done <- struct{}{}
	return nil
}

func testCfg() config.TopologyConfig {
	return config.TopologyConfig{
		MaxEdges:             20000,
		MaxOperationsPerEdge: 20,
		VanishedAfter:        time.Hour,
	}
}

func strVal(s string) model.AttrValue { return model.AttrValue{Kind: model.AttrStr, Str: s} }

func mkSpan(traceID byte, spanID, parentID byte, kind model.SpanKind, service string, attrs model.AttrMap, startNanos, endNanos uint64, status model.StatusCode) model.Span {
	var tid model.TraceID
	tid[0] = traceID
	var sid model.SpanID
	sid[0] = spanID
	var pid model.SpanID
	if parentID != 0 {
		pid[0] = parentID
	}
	return model.Span{
		TraceID:       tid,
		SpanID:        sid,
		ParentSpanID:  pid,
		Kind:          kind,
		Name:          "op",
		Resource:      &model.Resource{ServiceName: service},
		Attrs:         attrs,
		StartUnixNano: startNanos,
		EndUnixNano:   endNanos,
		Status:        model.Status{Code: status},
	}
}

const tenantA model.TenantID = "tenant-a"

func TestConsume_DirectPeerAttributeCreatesEdge(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	span := mkSpan(1, 1, 0, model.SpanKindClient, "checkout", model.AttrMap{
		"peer.service":        strVal("payments"),
		"http.request.method": strVal("GET"),
	}, 1_000_000, 2_000_000, model.StatusOk)

	if err := g.Consume(context.Background(), tenantA, []model.Span{span}); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	edges, err := g.Edges(context.Background(), tenantA, model.Window{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if e.Caller != "checkout" || e.Callee != "payments" || e.Protocol != "http" {
		t.Errorf("unexpected edge %+v", e)
	}
	if e.Calls != 1 {
		t.Errorf("want Calls=1, got %d", e.Calls)
	}

	select {
	case ev := <-g.Changes():
		if ev.Kind != "NewEdge" {
			t.Errorf("want NewEdge, got %s", ev.Kind)
		}
	default:
		t.Errorf("expected a NewEdge ChangeEvent")
	}
}

func TestConsume_JoinedViaPendingCorrelation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(2000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	client := mkSpan(2, 1, 0, model.SpanKindClient, "checkout", model.AttrMap{}, 1_000_000, 2_000_000, model.StatusOk)
	server := mkSpan(2, 2, 1, model.SpanKindServer, "payments", model.AttrMap{}, 1_100_000, 1_900_000, model.StatusOk)

	if err := g.Consume(context.Background(), tenantA, []model.Span{client}); err != nil {
		t.Fatalf("Consume(client): %v", err)
	}
	if err := g.Consume(context.Background(), tenantA, []model.Span{server}); err != nil {
		t.Fatalf("Consume(server): %v", err)
	}

	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	if len(edges) != 1 {
		t.Fatalf("want 1 joined edge, got %d: %+v", len(edges), edges)
	}
	if edges[0].Caller != "checkout" || edges[0].Callee != "payments" {
		t.Errorf("unexpected joined edge %+v", edges[0])
	}
}

func TestConsume_UnattributedInboundIsRecordedNotDropped(t *testing.T) {
	clock := &fakeClock{now: time.Unix(3000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	server := mkSpan(3, 1, 0, model.SpanKindServer, "orphan-service", model.AttrMap{}, 1_000_000, 2_000_000, model.StatusOk)
	if err := g.Consume(context.Background(), tenantA, []model.Span{server}); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	if len(edges) != 1 {
		t.Fatalf("want 1 edge for unattributed inbound, got %d", len(edges))
	}
	if edges[0].Caller != "external" || edges[0].Callee != "orphan-service" {
		t.Errorf("unexpected unattributed edge %+v", edges[0])
	}
}

func TestConsume_REDCountersAccumulate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(4000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	ok := mkSpan(4, 1, 0, model.SpanKindClient, "a", model.AttrMap{"peer.service": strVal("b")}, 0, 10_000_000, model.StatusOk)
	errSpan := mkSpan(4, 2, 0, model.SpanKindClient, "a", model.AttrMap{"peer.service": strVal("b")}, 0, 20_000_000, model.StatusError)

	_ = g.Consume(context.Background(), tenantA, []model.Span{ok, errSpan})

	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if e.Calls != 2 {
		t.Errorf("want Calls=2, got %d", e.Calls)
	}
	if e.Errors != 1 {
		t.Errorf("want Errors=1, got %d", e.Errors)
	}
	if e.DurationSumNanos != 30_000_000 {
		t.Errorf("want DurationSumNanos=30ms, got %d", e.DurationSumNanos)
	}
}

func TestEvictionAtCap(t *testing.T) {
	clock := &fakeClock{now: time.Unix(5000, 0)}
	cfg := testCfg()
	cfg.MaxEdges = 2
	g := NewLiveGraph(cfg, nil, nil, clock)

	// Edge 1 gets 3 calls (survives), edge 2 gets 1 call, edge 3 arrives last
	// and should trigger eviction of the lowest-Calls edge.
	for i := 0; i < 3; i++ {
		s := mkSpan(6, byte(10+i), 0, model.SpanKindClient, "svc1", model.AttrMap{"peer.service": strVal("callee1")}, 0, 1_000_000, model.StatusOk)
		_ = g.Consume(context.Background(), tenantA, []model.Span{s})
	}
	s2 := mkSpan(6, 20, 0, model.SpanKindClient, "svc2", model.AttrMap{"peer.service": strVal("callee2")}, 0, 1_000_000, model.StatusOk)
	_ = g.Consume(context.Background(), tenantA, []model.Span{s2})

	for i := 0; i < 2; i++ {
		s3 := mkSpan(6, byte(30+i), 0, model.SpanKindClient, "svc3", model.AttrMap{"peer.service": strVal("callee3")}, 0, 1_000_000, model.StatusOk)
		_ = g.Consume(context.Background(), tenantA, []model.Span{s3})
	}

	stats := g.Stats()
	if stats.EdgeCount != 2 {
		t.Fatalf("want EdgeCount=2 at cap, got %d", stats.EdgeCount)
	}
	if stats.EdgesEvicted != 1 {
		t.Fatalf("want EdgesEvicted=1, got %d", stats.EdgesEvicted)
	}

	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	for _, e := range edges {
		if e.Callee == "callee2" {
			t.Errorf("expected the single-call svc2->callee2 edge to be evicted, still present: %+v", e)
		}
	}
}

func TestChanges_NewEdgeThenVanished(t *testing.T) {
	clock := &fakeClock{now: time.Unix(6000, 0)}
	cfg := testCfg()
	cfg.VanishedAfter = time.Minute
	g := NewLiveGraph(cfg, nil, nil, clock)

	s := mkSpan(7, 1, 0, model.SpanKindClient, "a", model.AttrMap{"peer.service": strVal("b")}, 0, 1_000_000, model.StatusOk)
	_ = g.Consume(context.Background(), tenantA, []model.Span{s})

	select {
	case ev := <-g.Changes():
		if ev.Kind != "NewEdge" {
			t.Fatalf("want NewEdge, got %s", ev.Kind)
		}
	default:
		t.Fatal("expected NewEdge event")
	}

	future := clock.now.Add(2 * time.Minute)
	events := g.sweepVanished(future)
	if len(events) != 1 || events[0].Kind != "VanishedEdge" {
		t.Fatalf("want 1 VanishedEdge event, got %+v", events)
	}

	select {
	case ev := <-g.Changes():
		if ev.Kind != "VanishedEdge" {
			t.Fatalf("want VanishedEdge on channel, got %s", ev.Kind)
		}
	default:
		t.Fatal("expected VanishedEdge event on Changes() channel")
	}

	// No double-vanish emission on a second sweep.
	events2 := g.sweepVanished(future.Add(time.Second))
	if len(events2) != 0 {
		t.Fatalf("want no re-emission, got %+v", events2)
	}
}

func TestExtractPeerProtocolFamilies(t *testing.T) {
	cases := []struct {
		name         string
		attrs        model.AttrMap
		wantCallee   string
		wantProtocol string
	}{
		{"http", model.AttrMap{"peer.service": strVal("svc"), "http.request.method": strVal("GET")}, "svc", "http"},
		{"grpc", model.AttrMap{"peer.service": strVal("svc"), "rpc.system": strVal("grpc")}, "svc", "grpc"},
		{"db", model.AttrMap{"db.system": strVal("postgresql")}, "db:postgresql", "db"},
		{"messaging", model.AttrMap{"messaging.destination.name": strVal("orders")}, "orders", "messaging"},
		{"unknown", model.AttrMap{}, "", "unknown"},
		{"server_address_fallback", model.AttrMap{"server.address": strVal("svc2"), "http.method": strVal("POST")}, "svc2", "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			callee, protocol := extractPeer(tc.attrs)
			if callee != tc.wantCallee || protocol != tc.wantProtocol {
				t.Errorf("extractPeer(%v) = (%q, %q), want (%q, %q)", tc.attrs, callee, protocol, tc.wantCallee, tc.wantProtocol)
			}
		})
	}
}

func TestUpsertEdge_InternalProtocolWhenCallerEqualsCallee(t *testing.T) {
	clock := &fakeClock{now: time.Unix(7000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)
	s := mkSpan(8, 1, 0, model.SpanKindClient, "svc", model.AttrMap{"peer.service": strVal("svc")}, 0, 1_000_000, model.StatusOk)
	_ = g.Consume(context.Background(), tenantA, []model.Span{s})
	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	if len(edges) != 1 || edges[0].Protocol != "internal" {
		t.Fatalf("want protocol=internal for self-call, got %+v", edges)
	}
}

func TestNeighbors_UpstreamAndDownstream(t *testing.T) {
	clock := &fakeClock{now: time.Unix(8000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	spans := []model.Span{
		mkSpan(9, 1, 0, model.SpanKindClient, "A", model.AttrMap{"peer.service": strVal("B")}, 0, 1_000_000, model.StatusOk),
		mkSpan(9, 2, 0, model.SpanKindClient, "B", model.AttrMap{"peer.service": strVal("C")}, 0, 1_000_000, model.StatusOk),
	}
	for _, s := range spans {
		_ = g.Consume(context.Background(), tenantA, []model.Span{s})
	}

	down, err := g.Neighbors(context.Background(), tenantA, "A", 1, Downstream)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(down.Downstream) != 1 || down.Downstream[0] != "B" {
		t.Fatalf("want downstream [B] from A, got %+v", down.Downstream)
	}

	up, err := g.Neighbors(context.Background(), tenantA, "C", 1, Upstream)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(up.Upstream) != 1 || up.Upstream[0] != "B" {
		t.Fatalf("want upstream [B] from C, got %+v", up.Upstream)
	}

	dist, ok, err := g.Distance(context.Background(), tenantA, "A", "C", 5)
	if err != nil || !ok || dist != 2 {
		t.Fatalf("Distance(A,C) = (%d, %v, %v), want (2, true, nil)", dist, ok, err)
	}
}

func TestSnapshotAndStats(t *testing.T) {
	clock := &fakeClock{now: time.Unix(9000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)
	s := mkSpan(10, 1, 0, model.SpanKindClient, "A", model.AttrMap{"peer.service": strVal("B")}, 0, 1_000_000, model.StatusOk)
	_ = g.Consume(context.Background(), tenantA, []model.Span{s})

	snap, err := g.Snapshot(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Edges) != 1 || snap.Tenant != tenantA {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	stats := g.Stats()
	if stats.EdgeCount != 1 {
		t.Errorf("want EdgeCount=1, got %d", stats.EdgeCount)
	}
}

func TestEdgeOps_TopN(t *testing.T) {
	clock := &fakeClock{now: time.Unix(11000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	mk := func(spanID byte, op string, calls int) {
		for i := 0; i < calls; i++ {
			s := mkSpan(12, spanID, 0, model.SpanKindClient, "A", model.AttrMap{"peer.service": strVal("B")}, 0, 1_000_000, model.StatusOk)
			s.Name = op
			_ = g.Consume(context.Background(), tenantA, []model.Span{s})
		}
	}
	mk(1, "GetUser", 5)
	mk(2, "ListUsers", 2)

	edges, _ := g.Edges(context.Background(), tenantA, model.Window{})
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	ops, err := g.EdgeOps(context.Background(), tenantA, edges[0].ID, model.Window{}, 1)
	if err != nil {
		t.Fatalf("EdgeOps: %v", err)
	}
	if len(ops) != 1 || ops[0].CalleeOperation != "GetUser" || ops[0].Calls != 5 {
		t.Fatalf("want top-1 op GetUser/5, got %+v", ops)
	}
}

// FR-F04-6/AC-F04-6 regression (review-pass fix): an edge under CONTINUOUS
// traffic must NOT re-emit ChangeEvent{NewEdge} just because more than
// newEdgeWindow (24h) has elapsed since its ORIGINAL first sighting. §4.4's
// upsertEdge pseudocode refreshes the recentEdgeSet TTL on every touch
// (addWithTTL, unconditional); the prior code only refreshed lastNewEmit
// inside `if isNew`, so a continuously-active edge would spuriously look
// "new" again ~24h after its first sighting even though it was never
// actually absent.
func TestUpsertEdge_ContinuousTrafficDoesNotReemitNewEdgeAcrossWindow(t *testing.T) {
	clock := &fakeClock{now: time.Unix(50000, 0)}
	g := NewLiveGraph(testCfg(), nil, nil, clock)

	mk := func() model.Span {
		return mkSpan(14, 1, 0, model.SpanKindClient, "a", model.AttrMap{"peer.service": strVal("b")}, 0, 1_000_000, model.StatusOk)
	}

	// First sighting: NewEdge fires.
	if err := g.Consume(context.Background(), tenantA, []model.Span{mk()}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	select {
	case ev := <-g.Changes():
		if ev.Kind != "NewEdge" {
			t.Fatalf("want NewEdge, got %s", ev.Kind)
		}
	default:
		t.Fatal("expected initial NewEdge event")
	}

	// Continuous traffic every hour for 30 hours straight — well past the
	// 24h newEdgeWindow, but the edge was NEVER absent.
	for h := 1; h <= 30; h++ {
		clock.now = clock.now.Add(time.Hour)
		if err := g.Consume(context.Background(), tenantA, []model.Span{mk()}); err != nil {
			t.Fatalf("Consume at hour %d: %v", h, err)
		}
		select {
		case ev := <-g.Changes():
			t.Fatalf("hour %d: unexpected re-emission of %s for a continuously-active edge", h, ev.Kind)
		default:
		}
	}
}

// Review-pass regression: LiveGraph.Run must actually drive the periodic
// Flush/VanishSweep/SweepPending work the design calls for (the prior code
// left sweepVanished/sweepPending/Flush reachable only from direct test
// calls, with no production driver despite a comment promising one).
func TestRun_DrivesFlushAndVanishSweep(t *testing.T) {
	clock := &fakeClock{now: time.Unix(60000, 0)}
	sink := newFakeEdgeSink()
	cfg := testCfg()
	cfg.VanishedAfter = time.Minute
	g := NewLiveGraph(cfg, sink, nil, clock)

	s := mkSpan(15, 1, 0, model.SpanKindClient, "a", model.AttrMap{"peer.service": strVal("b")}, 0, 1_000_000, model.StatusOk)
	if err := g.Consume(context.Background(), tenantA, []model.Span{s}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	<-g.Changes() // drain the initial NewEdge

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- g.Run(ctx) }()

	// Fire the flush ticker (10s) and confirm it reaches EdgeSink.WriteEdges.
	flushTicker := clock.tickerFor(t, 10*time.Second)
	flushTicker.ch <- clock.now
	select {
	case <-sink.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drive a Flush within timeout")
	}

	// Fire the vanish-sweep ticker (1m) after advancing the clock past
	// VanishedAfter, and confirm a VanishedEdge reaches Changes().
	vanishTicker := clock.tickerFor(t, time.Minute)
	clock.now = clock.now.Add(2 * time.Minute)
	vanishTicker.ch <- clock.now
	select {
	case ev := <-g.Changes():
		if ev.Kind != "VanishedEdge" {
			t.Fatalf("want VanishedEdge, got %s", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drive a VanishSweep within timeout")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}
