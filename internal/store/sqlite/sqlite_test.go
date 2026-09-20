package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "traceiq.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

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

// AC-F03-2-shaped: WriteBatch then GetTrace round-trips a trace+span.
func TestWriteBatchAndGetTrace_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := store.TraceIndex{
		Tenant: tid, TraceID: traceID(1), RootService: "checkout", StartUnixNano: 1000,
		DurationNanos: 5_000_000, ErrorCount: 0, PathSignature: 42, KeepReason: model.KeepProbabilistic,
		ColdState: 0, WALSegment: "seg-1",
	}
	sp := store.SpanIndex{
		Tenant: tid, TraceID: tr.TraceID, SpanID: spanID(1), Service: "checkout", Operation: "POST /checkout",
		StartUnixNano: 1000, DurationNanos: 5_000_000,
	}
	receipt, err := s.WriteBatch(ctx, tid, store.HotBatch{Traces: []store.TraceIndex{tr}, Spans: []store.SpanIndex{sp}})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if receipt.Rows != 2 {
		t.Fatalf("receipt.Rows = %d, want 2", receipt.Rows)
	}

	got, err := s.GetTrace(ctx, tid, tr.TraceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if got.RootService != "checkout" || got.DurationNanos != 5_000_000 || got.ColdState != 0 || got.WALSegment != "seg-1" {
		t.Fatalf("GetTrace mismatch: %+v", got)
	}

	page, err := s.SearchSpans(ctx, tid, store.SpanQuery{Service: "checkout", Operation: "POST /checkout", Window: model.Window{Start: time.Unix(0, 0), End: time.Unix(0, 1_000_000_000)}})
	if err != nil {
		t.Fatalf("SearchSpans: %v", err)
	}
	if len(page.Spans) != 1 || page.Spans[0].SpanID != sp.SpanID {
		t.Fatalf("SearchSpans got %+v", page)
	}
}

// AC-F03-3-shaped: SearchSpans by an allowlisted attribute key returns the
// expected span; a key outside the allowlist is rejected, not silently slow.
func TestSearchSpans_AttributeIndex(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")

	tr := store.TraceIndex{Tenant: tid, TraceID: traceID(2), RootService: "api", StartUnixNano: 2000, KeepReason: model.KeepError}
	sp := store.SpanIndex{Tenant: tid, TraceID: tr.TraceID, SpanID: spanID(2), Service: "api", Operation: "GET /x", StartUnixNano: 2000, DurationNanos: 1000}
	attr := store.AttrIndexRow{Tenant: tid, KeyID: KeyID("http.route"), ValueHash: hashValue("/x"), Bucket: 200, TraceID: tr.TraceID, SpanID: sp.SpanID}

	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{Traces: []store.TraceIndex{tr}, Spans: []store.SpanIndex{sp}, AttrRows: []store.AttrIndexRow{attr}}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	page, err := s.SearchSpans(ctx, tid, store.SpanQuery{AttrKey: "http.route", AttrValue: "/x", Limit: 10})
	if err != nil {
		t.Fatalf("SearchSpans by attr: %v", err)
	}
	if len(page.Spans) != 1 || page.Spans[0].SpanID != sp.SpanID {
		t.Fatalf("SearchSpans by attr got %+v", page)
	}

	if _, err := s.SearchSpans(ctx, tid, store.SpanQuery{AttrKey: "not.allowlisted", AttrValue: "x"}); err == nil {
		t.Fatalf("SearchSpans on non-allowlisted key should error, got nil")
	}
}

// AC-F03-4-shaped: QueryRED is answered entirely from the rollup table.
func TestQueryRED(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")
	bucket := time.Unix(1_000, 0).UTC()
	sample := model.REDSample{
		Tenant: tid, Service: "api", Operation: "GET /x", BucketStart: bucket, Resolution: model.Res10s,
		Calls: 10, Errors: 1, DurationSumNanos: 10_000_000,
	}
	sample.Hist[0] = 5
	sample.Hist[1] = 5
	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{RED: []model.REDSample{sample}}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	series, err := s.QueryRED(ctx, tid, "api", "GET /x", model.Window{Start: bucket.Add(-time.Minute), End: bucket.Add(time.Minute)})
	if err != nil {
		t.Fatalf("QueryRED: %v", err)
	}
	if len(series.Samples) != 1 || series.Samples[0].Calls != 10 || series.Samples[0].Errors != 1 {
		t.Fatalf("QueryRED got %+v", series.Samples)
	}
}

// AC-F03-5-shaped: retention cutoff boundary — exactly-at-cutoff and
// one-second-before/after.
func TestExpireBefore_CutoffBoundary(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")

	cutoff := time.Unix(1_000_000, 0)
	before := store.TraceIndex{Tenant: tid, TraceID: traceID(10), RootService: "svc", StartUnixNano: cutoff.Add(-time.Second).UnixNano(), KeepReason: model.KeepProbabilistic}
	atCutoff := store.TraceIndex{Tenant: tid, TraceID: traceID(11), RootService: "svc", StartUnixNano: cutoff.UnixNano(), KeepReason: model.KeepProbabilistic}
	after := store.TraceIndex{Tenant: tid, TraceID: traceID(12), RootService: "svc", StartUnixNano: cutoff.Add(time.Second).UnixNano(), KeepReason: model.KeepProbabilistic}

	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{Traces: []store.TraceIndex{before, atCutoff, after}}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	expired, err := s.ExpireBefore(ctx, tid, store.TableTrace, cutoff)
	if err != nil {
		t.Fatalf("ExpireBefore: %v", err)
	}
	// only strictly-before-cutoff rows are deleted.
	if expired.Rows != 1 {
		t.Fatalf("expired.Rows = %d, want 1", expired.Rows)
	}
	if _, err := s.GetTrace(ctx, tid, before.TraceID); err == nil {
		t.Fatalf("expected before-cutoff trace to be gone")
	}
	if _, err := s.GetTrace(ctx, tid, atCutoff.TraceID); err != nil {
		t.Fatalf("at-cutoff trace should survive: %v", err)
	}
	if _, err := s.GetTrace(ctx, tid, after.TraceID); err != nil {
		t.Fatalf("after-cutoff trace should survive: %v", err)
	}
}

// Hot-index query latency sanity (not a real perf test): a batch of writes
// followed by reads should complete comfortably under a generous bound on
// a dev box / CI runner, catching any accidental O(n^2) or missing-index path.
func TestQueryLatency_Sanity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")

	var traces []store.TraceIndex
	var spans []store.SpanIndex
	for i := 0; i < 500; i++ {
		var id model.TraceID
		id[0], id[1] = byte(i), byte(i>>8)
		traces = append(traces, store.TraceIndex{Tenant: tid, TraceID: id, RootService: "svc", StartUnixNano: int64(i) * 1000, KeepReason: model.KeepProbabilistic})
		var sid model.SpanID
		sid[0], sid[1] = byte(i), byte(i>>8)
		spans = append(spans, store.SpanIndex{Tenant: tid, TraceID: id, SpanID: sid, Service: "svc", Operation: "op", StartUnixNano: int64(i) * 1000, DurationNanos: 1000})
	}
	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{Traces: traces, Spans: spans}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	start := time.Now()
	if _, err := s.SearchTraces(ctx, tid, store.TraceQuery{Window: model.Window{Start: time.Unix(0, 0), End: time.Unix(0, 1_000_000_000)}, Limit: 100}); err != nil {
		t.Fatalf("SearchTraces: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("SearchTraces took %v, want comfortably under 2s for 500 rows", d)
	}
}

// w10 review fix regression test: attr_index and span_text_fts previously
// never expired via ExpireBefore (both silently no-op'd), so DR-7's T0 tier
// ("Span rows + attr_index + FTS", 24h dev retention) only ever pruned
// `span`. This asserts both tables now honor the same cutoff-boundary rule
// AC-F03-5 already covers for `trace`.
func TestExpireBefore_AttrIndexAndFTS(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")

	cutoff := time.Unix(1_000_000, 0)

	// attr_index: bucket is a 10s bucket of start_unix_nano (DR-6 §6.2), so
	// the boundary is bucket-granular rather than nanosecond-exact.
	beforeBucket := cutoff.Add(-time.Minute).UnixNano() / int64(10*time.Second)
	afterBucket := cutoff.Add(time.Minute).UnixNano() / int64(10*time.Second)
	attrBefore := store.AttrIndexRow{Tenant: tid, KeyID: 0, ValueHash: 1, Bucket: beforeBucket, TraceID: traceID(30), SpanID: spanID(30)}
	attrAfter := store.AttrIndexRow{Tenant: tid, KeyID: 0, ValueHash: 2, Bucket: afterBucket, TraceID: traceID(31), SpanID: spanID(31)}

	ftsBefore := store.FTSRow{Tenant: tid, TraceID: traceID(32), SpanID: spanID(32), Text: "before", Timestamp: cutoff.Add(-time.Second)}
	ftsAt := store.FTSRow{Tenant: tid, TraceID: traceID(33), SpanID: spanID(33), Text: "at", Timestamp: cutoff}
	ftsAfter := store.FTSRow{Tenant: tid, TraceID: traceID(34), SpanID: spanID(34), Text: "after", Timestamp: cutoff.Add(time.Second)}

	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{
		AttrRows: []store.AttrIndexRow{attrBefore, attrAfter},
		FTSRows:  []store.FTSRow{ftsBefore, ftsAt, ftsAfter},
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if _, err := s.ExpireBefore(ctx, tid, store.TableAttrIndex, cutoff); err != nil {
		t.Fatalf("ExpireBefore(attr_index): %v", err)
	}
	var attrCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attr_index WHERE tenant_id=?`, string(tid)).Scan(&attrCount); err != nil {
		t.Fatalf("count attr_index: %v", err)
	}
	if attrCount != 1 {
		t.Fatalf("attr_index rows after ExpireBefore = %d, want 1 (only the after-cutoff bucket survives)", attrCount)
	}

	expired, err := s.ExpireBefore(ctx, tid, store.TableSpanFTS, cutoff)
	if err != nil {
		t.Fatalf("ExpireBefore(span_text_fts): %v", err)
	}
	if expired.Rows != 1 {
		t.Fatalf("ExpireBefore(span_text_fts).Rows = %d, want 1 (only strictly-before-cutoff)", expired.Rows)
	}
	var ftsCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM span_text_fts WHERE tenant_id=?`, string(tid)).Scan(&ftsCount); err != nil {
		t.Fatalf("count span_text_fts: %v", err)
	}
	if ftsCount != 2 {
		t.Fatalf("span_text_fts rows after ExpireBefore = %d, want 2 (at-cutoff and after survive)", ftsCount)
	}
}

func TestBindColdBlock(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tid := model.TenantID("t1")

	tr := store.TraceIndex{Tenant: tid, TraceID: traceID(20), RootService: "svc", StartUnixNano: 1, KeepReason: model.KeepProbabilistic, ColdState: 0, WALSegment: "seg-A"}
	if _, err := s.WriteBatch(ctx, tid, store.HotBatch{Traces: []store.TraceIndex{tr}}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	n, err := s.BindColdBlock(ctx, tid, "seg-A", store.BlockManifest{BlockID: "block-1"})
	if err != nil {
		t.Fatalf("BindColdBlock: %v", err)
	}
	if n != 1 {
		t.Fatalf("BindColdBlock rowsBound = %d, want 1", n)
	}

	got, err := s.GetTrace(ctx, tid, tr.TraceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if got.ColdState != 1 || got.BlockID != "block-1" {
		t.Fatalf("GetTrace after bind = %+v", got)
	}
}
