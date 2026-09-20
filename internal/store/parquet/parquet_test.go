package parquet

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

// fakeClock is a minimal model.Clock for deterministic Seal/SealDue/
// ExpireBlocks tests; only Now()/Since() are exercised by this driver.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) Since(t time.Time) time.Duration                  { return c.Now().Sub(t) }
func (c *fakeClock) NewTicker(d time.Duration) model.Ticker           { return nil }
func (c *fakeClock) NewTimer(d time.Duration) model.Timer             { return nil }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

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

// closeAllOpenWAL closes every still-open WAL file handle so t.TempDir()
// cleanup never races a live handle (matters on Windows).
func closeAllOpenWAL(t *testing.T, s *Store) {
	t.Helper()
	_ = s.Close()
}

func testTrace(tid model.TenantID, id model.TraceID, startUnixNano uint64) model.Trace {
	sid := spanID(id[0])
	return model.Trace{
		TraceID:       id,
		Tenant:        tid,
		RootService:   "checkout",
		RootOperation: "POST /checkout",
		StartUnixNano: startUnixNano,
		EndUnixNano:   startUnixNano + 5_000_000,
		SpanCount:     1,
		ErrorCount:    0,
		PathSignature: 42,
		Spans: []model.Span{
			{
				TraceID:       id,
				SpanID:        sid,
				Name:          "POST /checkout",
				Kind:          model.SpanKind(2),
				StartUnixNano: startUnixNano,
				EndUnixNano:   startUnixNano + 5_000_000,
				Status:        model.Status{Code: model.StatusCode(1)},
				Tenant:        tid,
			},
		},
	}
}

// AC-F03-1-shaped: Append fsyncs the WAL and the trace is immediately
// readable via ReadFromWAL, before any Seal.
func TestAppend_TraceReadableBeforeSeal(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(1), 1_700_000_000_000_000_000)

	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ref.Segment == "" {
		t.Fatalf("Append: empty WAL segment")
	}

	got, err := s.ReadFromWAL(ctx, tid, tr.TraceID, ref)
	if err != nil {
		t.Fatalf("ReadFromWAL: %v", err)
	}
	if got.TraceID != tr.TraceID || got.RootService != "checkout" {
		t.Fatalf("ReadFromWAL mismatch: %+v", got)
	}
}

// Block write/seal + manifest correctness.
func TestSeal_WritesManifestAndFiles(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(2), 1_700_000_000_000_000_000)

	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	m, err := s.Seal(ctx, ref.Segment)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if m.BlockID != ref.Segment {
		t.Fatalf("manifest.BlockID = %q, want %q", m.BlockID, ref.Segment)
	}
	if m.Tenant != tid || m.Tier != store.ColdSampled {
		t.Fatalf("manifest tenant/tier mismatch: %+v", m)
	}
	if m.RowCount != 1 {
		t.Fatalf("manifest.RowCount = %d, want 1", m.RowCount)
	}
	if m.Checksum == "" {
		t.Fatalf("manifest.Checksum is empty")
	}
	for _, p := range []string{m.SpansPath, m.TracesPath, m.MetaPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected file %s to exist: %v", p, err)
		}
	}

	// Sealing again should fail: the block is no longer open.
	if _, err := s.Seal(ctx, ref.Segment); err == nil {
		t.Fatalf("Seal on an already-sealed block should error")
	}
}

// Read-back of a written trace after seal (cold-store round trip).
func TestReadTrace_AfterSeal_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(3), 1_700_000_000_000_000_000)

	ref, err := s.Append(ctx, tid, tr, store.ColdAnomalous)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	m, err := s.Seal(ctx, ref.Segment)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	got, err := s.ReadTrace(ctx, tid, tr.TraceID, store.TraceLoc{BlockID: m.BlockID})
	if err != nil {
		t.Fatalf("ReadTrace: %v", err)
	}
	if got.TraceID != tr.TraceID {
		t.Fatalf("ReadTrace.TraceID = %x, want %x", got.TraceID, tr.TraceID)
	}
	if got.RootService != tr.RootService || got.RootOperation != tr.RootOperation {
		t.Fatalf("ReadTrace service/operation mismatch: %+v", got)
	}
	if got.StartUnixNano != tr.StartUnixNano || got.EndUnixNano != tr.EndUnixNano {
		t.Fatalf("ReadTrace timing mismatch: %+v", got)
	}
	if len(got.Spans) != 1 {
		t.Fatalf("ReadTrace got %d spans, want 1", len(got.Spans))
	}
	sp := got.Spans[0]
	if sp.SpanID != tr.Spans[0].SpanID || sp.Name != tr.Spans[0].Name {
		t.Fatalf("ReadTrace span mismatch: %+v", sp)
	}
	if sp.Status.Code != tr.Spans[0].Status.Code {
		t.Fatalf("ReadTrace span status mismatch: %+v", sp.Status)
	}
}

// SealDue seals only blocks whose age has crossed the flush interval.
func TestSealDue_OnlySealsOldEnoughBlocks(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(4), 1_700_000_000_000_000_000)
	if _, err := s.Append(ctx, tid, tr, store.ColdSampled); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Not due yet.
	sealed, err := s.SealDue(ctx, clock.Now())
	if err != nil {
		t.Fatalf("SealDue: %v", err)
	}
	if len(sealed) != 0 {
		t.Fatalf("SealDue too early sealed %v, want none", sealed)
	}

	clock.Advance(5 * time.Minute)
	sealed, err = s.SealDue(ctx, clock.Now())
	if err != nil {
		t.Fatalf("SealDue: %v", err)
	}
	if len(sealed) != 1 {
		t.Fatalf("SealDue after flush interval sealed %v, want 1", sealed)
	}
}

// DueBlocks (store/tiered's seal-cycle discovery step) must not itself seal
// anything, and must report the same set SealDue would seal.
func TestDueBlocks_DoesNotSeal(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(5), 1_700_000_000_000_000_000)
	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	clock.Advance(5 * time.Minute)
	due := s.DueBlocks(clock.Now())
	if len(due) != 1 || due[0].BlockID != ref.Segment || due[0].Tenant != tid {
		t.Fatalf("DueBlocks = %+v, want one entry for %s/%s", due, tid, ref.Segment)
	}

	// still open: a direct Seal must still succeed (DueBlocks didn't seal it).
	if _, err := s.Seal(ctx, ref.Segment); err != nil {
		t.Fatalf("Seal after DueBlocks: %v", err)
	}
}

// Manifest reload on Open: sealed blocks survive a process restart (a fresh
// Store pointed at the same dir).
func TestOpen_ReloadsSealedManifests(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s1, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(6), 1_700_000_000_000_000_000)
	ref, err := s1.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	m, err := s1.Seal(ctx, ref.Segment)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	closeAllOpenWAL(t, s1)

	s2, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s2) })

	got, err := s2.ReadTrace(ctx, tid, tr.TraceID, store.TraceLoc{BlockID: m.BlockID})
	if err != nil {
		t.Fatalf("ReadTrace on reloaded store: %v", err)
	}
	if got.TraceID != tr.TraceID {
		t.Fatalf("ReadTrace after reload mismatch: %+v", got)
	}
}

// AC-F03-5-shaped, block granularity: a block is deleted exactly when
// now >= sealed_at + retention(tier); one-second-before survives,
// exactly-at and one-second-after are deleted.
func TestExpireBlocks_RetentionBoundary(t *testing.T) {
	sealedAt := time.Unix(1_700_000_000, 0).UTC()
	retention := time.Hour

	newSealedStore := func(t *testing.T, id byte) (*Store, store.BlockManifest, *fakeClock) {
		t.Helper()
		dir := t.TempDir()
		clock := newFakeClock(sealedAt)
		s, err := Open(Config{Dir: dir, Clock: clock, Retention: map[store.ColdTier]time.Duration{store.ColdSampled: retention}})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { closeAllOpenWAL(t, s) })
		ctx := context.Background()
		tid := model.TenantID("t1")
		tr := testTrace(tid, traceID(id), uint64(sealedAt.UnixNano()))
		ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		m, err := s.Seal(ctx, ref.Segment)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		return s, m, clock
	}

	t.Run("one second before cutoff survives", func(t *testing.T) {
		s, m, _ := newSealedStore(t, 20)
		before := sealedAt.Add(retention).Add(-time.Second)
		deleted, err := s.ExpireBlocks(context.Background(), before, store.ColdSampled)
		if err != nil {
			t.Fatalf("ExpireBlocks: %v", err)
		}
		if len(deleted) != 0 {
			t.Fatalf("ExpireBlocks deleted %v one second before cutoff, want none", deleted)
		}
		if _, err := os.Stat(m.MetaPath); err != nil {
			t.Fatalf("expected block to survive: %v", err)
		}
	})

	t.Run("exactly at cutoff is deleted", func(t *testing.T) {
		s, m, _ := newSealedStore(t, 21)
		before := sealedAt.Add(retention)
		deleted, err := s.ExpireBlocks(context.Background(), before, store.ColdSampled)
		if err != nil {
			t.Fatalf("ExpireBlocks: %v", err)
		}
		if len(deleted) != 1 || deleted[0] != m.BlockID {
			t.Fatalf("ExpireBlocks at cutoff = %v, want [%s]", deleted, m.BlockID)
		}
		if _, err := os.Stat(m.MetaPath); !os.IsNotExist(err) {
			t.Fatalf("expected block dir removed, stat err = %v", err)
		}
	})

	t.Run("one second after cutoff is deleted", func(t *testing.T) {
		s, m, _ := newSealedStore(t, 22)
		before := sealedAt.Add(retention).Add(time.Second)
		deleted, err := s.ExpireBlocks(context.Background(), before, store.ColdSampled)
		if err != nil {
			t.Fatalf("ExpireBlocks: %v", err)
		}
		if len(deleted) != 1 || deleted[0] != m.BlockID {
			t.Fatalf("ExpireBlocks after cutoff = %v, want [%s]", deleted, m.BlockID)
		}
	})

	// A different tier is never touched by ExpireBlocks(tier=ColdSampled).
	t.Run("other tier untouched", func(t *testing.T) {
		dir := t.TempDir()
		clock := newFakeClock(sealedAt)
		s, err := Open(Config{Dir: dir, Clock: clock, Retention: map[store.ColdTier]time.Duration{
			store.ColdSampled:   retention,
			store.ColdAnomalous: retention,
		}})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { closeAllOpenWAL(t, s) })
		ctx := context.Background()
		tid := model.TenantID("t1")
		tr := testTrace(tid, traceID(23), uint64(sealedAt.UnixNano()))
		ref, err := s.Append(ctx, tid, tr, store.ColdAnomalous)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		m, err := s.Seal(ctx, ref.Segment)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		deleted, err := s.ExpireBlocks(ctx, sealedAt.Add(retention).Add(time.Hour), store.ColdSampled)
		if err != nil {
			t.Fatalf("ExpireBlocks: %v", err)
		}
		if len(deleted) != 0 {
			t.Fatalf("ExpireBlocks(tier=Sampled) touched an anomalous block: %v", deleted)
		}
		if _, err := os.Stat(m.MetaPath); err != nil {
			t.Fatalf("anomalous block should survive a sampled-tier sweep: %v", err)
		}
	})
}

// Tombstone (right-to-erasure) makes a sealed trace unreadable immediately,
// independent of block expiry.
func TestTombstone_MakesTraceUnreadable(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(7), 1_700_000_000_000_000_000)
	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	m, err := s.Seal(ctx, ref.Segment)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := s.ReadTrace(ctx, tid, tr.TraceID, store.TraceLoc{BlockID: m.BlockID}); err != nil {
		t.Fatalf("ReadTrace before tombstone: %v", err)
	}

	if err := s.Tombstone(ctx, tid, []model.TraceID{tr.TraceID}, "gdpr-erasure"); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}

	if _, err := s.ReadTrace(ctx, tid, tr.TraceID, store.TraceLoc{BlockID: m.BlockID}); err == nil {
		t.Fatalf("ReadTrace after tombstone should error")
	}
}

// w10 review fix regression test: Seal used to only close a sealed block's
// WAL file handle and never remove it, so every sealed block leaked its WAL
// segment on disk forever, contradicting DR-7 §7's "WAL segments are
// retained until seal + wal_retain (default 10m)". PruneWAL now removes a
// sealed segment once its retention window has elapsed, and leaves it alone
// before that.
func TestPruneWAL_RemovesAfterRetention(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock, WalRetain: 10 * time.Minute})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(40), 1_700_000_000_000_000_000)
	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Seal(ctx, ref.Segment); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	walPath := filepath.Join(dir, "wal", ref.Segment+".wal")
	if _, err := os.Stat(walPath); err != nil {
		t.Fatalf("expected wal file to still exist right after seal: %v", err)
	}

	// Not yet due: retention window hasn't elapsed.
	pruned, err := s.PruneWAL(clock.Now().Add(9 * time.Minute))
	if err != nil {
		t.Fatalf("PruneWAL (early): %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("PruneWAL pruned %v before wal_retain elapsed, want none", pruned)
	}
	if _, err := os.Stat(walPath); err != nil {
		t.Fatalf("wal file should still exist before wal_retain elapses: %v", err)
	}

	// Due: exactly at sealed_at + wal_retain.
	pruned, err = s.PruneWAL(clock.Now().Add(10 * time.Minute))
	if err != nil {
		t.Fatalf("PruneWAL (due): %v", err)
	}
	if len(pruned) != 1 || pruned[0] != ref.Segment {
		t.Fatalf("PruneWAL = %v, want [%s]", pruned, ref.Segment)
	}
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Fatalf("expected wal file removed after wal_retain elapsed, stat err = %v", err)
	}

	// Idempotent: pruning again finds nothing left to remove.
	pruned, err = s.PruneWAL(clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PruneWAL (repeat): %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("PruneWAL repeat = %v, want none (already pruned)", pruned)
	}
}

// w10 review fix regression test: the per-record CRC32C stored at Append
// time was never checked on read, so a corrupted/truncated WAL record could
// silently decode instead of being rejected. A flipped byte in the record's
// payload must make it unreadable (via ReadFromWAL's file-fallback path)
// rather than returning corrupted data.
func TestReadFromWAL_RejectsCorruptedRecord(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(41), 1_700_000_000_000_000_000)
	ref, err := s.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Force the file-fallback read path (rather than the in-memory open-block
	// copy) so the corrupted bytes on disk are what gets read back.
	closeAllOpenWAL(t, s)

	walPath := filepath.Join(dir, "wal", ref.Segment+".wal")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("read wal file: %v", err)
	}
	if len(data) <= 8 {
		t.Fatalf("wal record too short to corrupt: %d bytes", len(data))
	}
	data[8] ^= 0xFF // flip a byte in the gob-encoded payload, past the 8-byte header
	if err := os.WriteFile(walPath, data, 0o644); err != nil {
		t.Fatalf("rewrite corrupted wal file: %v", err)
	}

	s2, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s2) })

	if _, err := s2.ReadFromWAL(ctx, tid, tr.TraceID, ref); err == nil {
		t.Fatalf("ReadFromWAL should reject a CRC-mismatched record, got nil error")
	}
}

// ReplayWAL crash-consistency (DR-7 crash matrix, unit-level approximation):
// a trace appended but never sealed by the process that wrote it is
// recovered by a freshly-opened Store pointed at the same directory.
func TestReplayWAL_RecoversUnsealedTrace(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock(time.Unix(1_700_000_000, 0).UTC())
	s1, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open (pre-crash): %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s1) })

	ctx := context.Background()
	tid := model.TenantID("t1")
	tr := testTrace(tid, traceID(8), 1_700_000_000_000_000_000)
	origRef, err := s1.Append(ctx, tid, tr, store.ColdSampled)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	// s1 is never Sealed here — simulating a crash between the WAL fsync and
	// any seal (DR-7 crash-matrix rows 1/2). Its WAL file handle is closed
	// (as a real process's open fds are on exit/kill) so the replaying
	// process below can remove the consumed segment on every OS.
	closeAllOpenWAL(t, s1)

	s2, err := Open(Config{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatalf("Open (post-crash): %v", err)
	}
	t.Cleanup(func() { closeAllOpenWAL(t, s2) })

	report, err := s2.ReplayWAL(ctx)
	if err != nil {
		t.Fatalf("ReplayWAL: %v", err)
	}
	if report.SegmentsReplayed != 1 || report.TracesReappended != 1 {
		t.Fatalf("ReplayWAL report = %+v, want 1 segment / 1 trace", report)
	}

	// The recovered trace lives in a new open block; seal everything due and
	// confirm it reads back correctly.
	clock.Advance(10 * time.Minute)
	sealedIDs, err := s2.SealDue(ctx, clock.Now())
	if err != nil {
		t.Fatalf("SealDue: %v", err)
	}
	if len(sealedIDs) != 1 {
		t.Fatalf("SealDue after replay = %v, want 1 block", sealedIDs)
	}

	got, err := s2.ReadTrace(ctx, tid, tr.TraceID, store.TraceLoc{BlockID: sealedIDs[0]})
	if err != nil {
		t.Fatalf("ReadTrace after replay+seal: %v", err)
	}
	if got.TraceID != tr.TraceID {
		t.Fatalf("recovered trace mismatch: %+v", got)
	}

	// The original crashed WAL segment is consumed (removed) by ReplayWAL;
	// only the fresh block's own segment (Seal closes but doesn't delete it)
	// remains.
	origWAL := filepath.Join(dir, "wal", origRef.Segment+".wal")
	if _, err := os.Stat(origWAL); !os.IsNotExist(err) {
		t.Fatalf("expected original wal segment %s to be removed by replay, stat err = %v", origWAL, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("ReadDir wal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wal dir = %v after replay+seal, want exactly the new block's segment", entries)
	}
}
