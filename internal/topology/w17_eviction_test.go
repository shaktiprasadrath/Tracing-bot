package topology

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// TestEvictionTieBreak_IsDeterministic is the regression test for the w17
// final review's fix to TestEvictionAtCap's ~1-in-10 flake.
//
// evictIfNeeded used to select its victim with `e.Calls < victim.Calls`
// evaluated over Go's randomized map iteration order, so any Calls tie was
// resolved by whichever key the runtime yielded first. At cap that let a
// freshly inserted edge evict ITSELF about half the time and then
// immediately re-trigger eviction on its next sighting. Under a fake clock
// every edge shares one identical LastSeen, so Calls and LastSeen are BOTH
// tied here and only the insertion-order tie-break can decide — which is
// exactly the case the old code got wrong.
//
// Running the whole scenario many times inside one test makes a
// map-iteration-order regression fail essentially every run rather than
// one run in ten.
func TestEvictionTieBreak_IsDeterministic(t *testing.T) {
	const iterations = 200

	for i := 0; i < iterations; i++ {
		clock := &fakeClock{now: time.Unix(7000, 0)}
		cfg := testCfg()
		cfg.MaxEdges = 2
		g := NewLiveGraph(cfg, nil, nil, clock)

		// Three edges, each with exactly one call: Calls and LastSeen are
		// tied three ways, so insertion order is the only discriminator.
		for n := 0; n < 3; n++ {
			s := mkSpan(9, byte(40+n), 0, model.SpanKindClient,
				serviceName(n), model.AttrMap{"peer.service": strVal(calleeName(n))},
				0, 1_000_000, model.StatusOk)
			_ = g.Consume(context.Background(), tenantA, []model.Span{s})
		}

		stats := g.Stats()
		if stats.EdgeCount != 2 {
			t.Fatalf("iteration %d: EdgeCount=%d, want 2", i, stats.EdgeCount)
		}
		if stats.EdgesEvicted != 1 {
			t.Fatalf("iteration %d: EdgesEvicted=%d, want exactly 1 — a nondeterministic tie-break makes the newest edge evict itself and then evict again", i, stats.EdgesEvicted)
		}

		// The FIRST-inserted edge is the coldest under the documented total
		// order (equal Calls, equal LastSeen, lowest insertion sequence), so
		// it is the one that must go — never the edge just added.
		edges, err := g.Edges(context.Background(), tenantA, model.Window{})
		if err != nil {
			t.Fatalf("iteration %d: Edges: %v", i, err)
		}
		for _, e := range edges {
			if e.Callee == calleeName(0) {
				t.Fatalf("iteration %d: the earliest-inserted edge must be the eviction victim, but it survived: %+v", i, e)
			}
		}
		var sawNewest bool
		for _, e := range edges {
			if e.Callee == calleeName(2) {
				sawNewest = true
			}
		}
		if !sawNewest {
			t.Fatalf("iteration %d: the most recently inserted edge was evicted; eviction must be LRU-by-Calls, not map-order", i)
		}
	}
}

// TestWarmStart_RespectsMaxEdges covers the companion fix: WarmStart
// installed whatever EdgeSource.LoadEdges returned without re-applying
// topology.max_edges, so a restart could re-inflate the graph past its
// configured memory bound and stay there.
func TestWarmStart_RespectsMaxEdges(t *testing.T) {
	clock := &fakeClock{now: time.Unix(7100, 0)}
	cfg := testCfg()
	cfg.MaxEdges = 3
	src := &staticEdgeSource{}
	for n := 0; n < 10; n++ {
		src.edges = append(src.edges, Edge{
			ID:       edgeIDFor(serviceName(n), calleeName(n), "http"),
			Tenant:   tenantA,
			Caller:   serviceName(n),
			Callee:   calleeName(n),
			Protocol: "http",
			Calls:    uint64(n + 1),
		})
	}

	g := NewLiveGraph(cfg, nil, src, clock)
	if err := g.WarmStart(context.Background(), tenantA, model.Window{}); err != nil {
		t.Fatalf("WarmStart: %v", err)
	}
	if got := g.Stats().EdgeCount; got != cfg.MaxEdges {
		t.Fatalf("WarmStart left %d edges in the graph, exceeding topology.max_edges=%d", got, cfg.MaxEdges)
	}
}

type staticEdgeSource struct{ edges []Edge }

func (s *staticEdgeSource) LoadEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error) {
	return s.edges, nil
}

func serviceName(n int) string { return "w17svc" + string(rune('A'+n)) }
func calleeName(n int) string  { return "w17callee" + string(rune('A'+n)) }
