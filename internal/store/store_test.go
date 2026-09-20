package store_test

// Regression test for W18-D1 (see Testing_defect.md): TieredStore.Append was
// stamping every SpanIndex.Service with the trace's t.RootService instead of
// the individual span's own originating service (internal/store/store.go's
// Append, around what was line 664). This was invisible in single-hop
// synthetic traces (root service == span service trivially) and only
// surfaced against a real multi-hop Istio mesh trace, where every span in a
// stored trace was mis-stamped with the root service regardless of which
// service actually produced it.
//
// This test lives in an external package (store_test) rather than package
// store so it can use the real sqlite.HotIndex implementation as the "hot
// index" it appends to and queries back — sqlite imports store, so a same-
// package test would be a import cycle.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/store/sqlite"
)

func traceID(b byte) model.TraceID {
	var id model.TraceID
	id[0] = b
	return id
}

func spanID(b byte) model.SpanID {
	var id model.SpanID
	id[0] = b
	return id
}

// fakeCold is a minimal store.ColdStore that only needs to make
// TieredStore.Append succeed; this test exercises the hot-index side of
// Append, not the cold tier.
type fakeCold struct {
	ref store.WALRef
}

var _ store.ColdStore = (*fakeCold)(nil)

func (c *fakeCold) Append(ctx context.Context, tid model.TenantID, t model.Trace, tier store.ColdTier) (store.WALRef, error) {
	return c.ref, nil
}
func (c *fakeCold) Seal(ctx context.Context, blockID string) (store.BlockManifest, error) {
	return store.BlockManifest{}, nil
}
func (c *fakeCold) SealDue(ctx context.Context, now time.Time) ([]string, error) { return nil, nil }
func (c *fakeCold) ReadTrace(ctx context.Context, tid model.TenantID, id model.TraceID, loc store.TraceLoc) (model.Trace, error) {
	return model.Trace{}, nil
}
func (c *fakeCold) ReadFromWAL(ctx context.Context, tid model.TenantID, id model.TraceID, ref store.WALRef) (model.Trace, error) {
	return model.Trace{}, nil
}
func (c *fakeCold) ReplayWAL(ctx context.Context) (store.ReplayReport, error) {
	return store.ReplayReport{}, nil
}
func (c *fakeCold) ExpireBlocks(ctx context.Context, before time.Time, tier store.ColdTier) ([]string, error) {
	return nil, nil
}
func (c *fakeCold) Tombstone(ctx context.Context, tid model.TenantID, ids []model.TraceID, reason string) error {
	return nil
}
func (c *fakeCold) Compact(ctx context.Context, level int, b store.CompactBudget) (store.CompactReport, error) {
	return store.CompactReport{}, nil
}
func (c *fakeCold) Health(ctx context.Context) store.HealthReport {
	return store.HealthReport{Healthy: true}
}

func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlite.Open(filepath.Join(dir, "traceiq.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestAppend_SpanServicePerSpan_NotRootService is the W18-D1 regression
// test: a synthetic multi-hop trace (root span from service A, child from
// service B, grandchild from service C, all sharing one trace_id) is
// appended via TieredStore.Append, then the hot index is queried back and
// each span row's Service column must reflect its OWN originating service,
// not the trace's root service.
func TestAppend_SpanServicePerSpan_NotRootService(t *testing.T) {
	hot := openTestStore(t)
	cold := &fakeCold{ref: store.WALRef{Segment: "seg-1"}}
	ts := store.NewTieredStore(hot, cold, nil)

	ctx := context.Background()
	tid := model.TenantID("tenant-1")
	tr := traceID(1)

	root := model.Span{
		TraceID:       tr,
		SpanID:        spanID(1),
		Name:          "POST /checkout",
		StartUnixNano: 1_000,
		EndUnixNano:   9_000,
		Resource:      &model.Resource{ServiceName: "mesh-client.bank"},
	}
	child := model.Span{
		TraceID:       tr,
		SpanID:        spanID(2),
		ParentSpanID:  spanID(1),
		Name:          "POST /payment",
		StartUnixNano: 2_000,
		EndUnixNano:   7_000,
		Resource:      &model.Resource{ServiceName: "transaction-orchestrator.bank"},
	}
	grandchild := model.Span{
		TraceID:       tr,
		SpanID:        spanID(3),
		ParentSpanID:  spanID(2),
		Name:          "POST /ledger",
		StartUnixNano: 3_000,
		EndUnixNano:   6_000,
		Resource:      &model.Resource{ServiceName: "ledger-service.bank"},
	}

	trace := model.Trace{
		TraceID:       tr,
		Tenant:        tid,
		Spans:         []model.Span{root, child, grandchild},
		RootSpanID:    root.SpanID,
		RootService:   "mesh-client.bank", // trace-level summary field, correct and untouched by the fix
		RootOperation: root.Name,
		StartUnixNano: root.StartUnixNano,
		EndUnixNano:   root.EndUnixNano,
		DurationNanos: root.EndUnixNano - root.StartUnixNano,
		SpanCount:     3,
	}

	if _, err := ts.Append(ctx, tid, trace, store.ColdSampled, model.KeepProbabilistic); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := hot.SearchSpans(ctx, tid, store.SpanQuery{
		Window: model.Window{
			Start: time.Unix(0, 0),
			End:   time.Unix(0, 100_000),
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}

	wantService := map[model.SpanID]string{
		root.SpanID:       "mesh-client.bank",
		child.SpanID:      "transaction-orchestrator.bank",
		grandchild.SpanID: "ledger-service.bank",
	}
	if len(got.Spans) != 3 {
		t.Fatalf("SearchSpans: got %d spans, want 3", len(got.Spans))
	}
	for _, sp := range got.Spans {
		want, ok := wantService[sp.SpanID]
		if !ok {
			t.Fatalf("SearchSpans: unexpected span_id %x", sp.SpanID)
		}
		if sp.Service != want {
			t.Errorf("span %x (operation %q): Service = %q, want %q (its own originating service, not the trace root)",
				sp.SpanID, sp.Operation, sp.Service, want)
		}
	}

	// The impact called out in Testing_defect.md: SearchSpans filtered by a
	// non-root Service must be able to find that service's own spans.
	page, err := hot.SearchSpans(ctx, tid, store.SpanQuery{Service: "transaction-orchestrator.bank", Limit: 10})
	if err != nil {
		t.Fatalf("SearchSpans(Service=transaction-orchestrator.bank): %v", err)
	}
	if len(page.Spans) != 1 || page.Spans[0].SpanID != child.SpanID {
		t.Fatalf("SearchSpans(Service=transaction-orchestrator.bank): got %+v, want exactly the child span", page.Spans)
	}
}
