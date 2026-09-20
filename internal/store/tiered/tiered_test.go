package tiered

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/store/parquet"
	"traceiq/internal/store/sqlite"
	"traceiq/internal/topology"
)

// --- shared test helpers ---

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

// callLog records the order in which spy methods are invoked, across both
// the spyHot and spyCold instances sharing it, so tests can assert
// cross-component ordering (DR-7's invariant) directly.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// --- spyCold: a minimal store.ColdStore recording call order ---

type spyCold struct {
	log      *callLog
	ref      store.WALRef
	manifest store.BlockManifest
	sealErr  error
}

var _ store.ColdStore = (*spyCold)(nil)

func (c *spyCold) Append(ctx context.Context, tid model.TenantID, t model.Trace, tier store.ColdTier) (store.WALRef, error) {
	c.log.add("cold.Append")
	return c.ref, nil
}
func (c *spyCold) Seal(ctx context.Context, blockID string) (store.BlockManifest, error) {
	c.log.add("cold.Seal")
	if c.sealErr != nil {
		return store.BlockManifest{}, c.sealErr
	}
	m := c.manifest
	m.BlockID = blockID
	return m, nil
}
func (c *spyCold) SealDue(ctx context.Context, now time.Time) ([]string, error) {
	c.log.add("cold.SealDue")
	return nil, nil
}
func (c *spyCold) ReadTrace(ctx context.Context, tid model.TenantID, id model.TraceID, loc store.TraceLoc) (model.Trace, error) {
	c.log.add("cold.ReadTrace")
	return model.Trace{TraceID: id, Tenant: tid, RootService: "from-cold"}, nil
}
func (c *spyCold) ReadFromWAL(ctx context.Context, tid model.TenantID, id model.TraceID, ref store.WALRef) (model.Trace, error) {
	c.log.add("cold.ReadFromWAL")
	return model.Trace{TraceID: id, Tenant: tid, RootService: "from-wal"}, nil
}
func (c *spyCold) ReplayWAL(ctx context.Context) (store.ReplayReport, error) {
	c.log.add("cold.ReplayWAL")
	return store.ReplayReport{}, nil
}
func (c *spyCold) ExpireBlocks(ctx context.Context, before time.Time, tier store.ColdTier) ([]string, error) {
	c.log.add("cold.ExpireBlocks")
	return nil, nil
}
func (c *spyCold) Tombstone(ctx context.Context, tid model.TenantID, ids []model.TraceID, reason string) error {
	c.log.add("cold.Tombstone")
	return nil
}
func (c *spyCold) Compact(ctx context.Context, level int, b store.CompactBudget) (store.CompactReport, error) {
	c.log.add("cold.Compact")
	return store.CompactReport{}, nil
}
func (c *spyCold) Health(ctx context.Context) store.HealthReport {
	c.log.add("cold.Health")
	return store.HealthReport{Healthy: true}
}

// --- spyHot: a minimal store.HotIndex recording call order ---

type spyHot struct {
	log        *callLog
	bindErr    error
	traceIndex store.TraceIndex
	ingested   int64
	diskReport store.DiskReport
}

var _ store.HotIndex = (*spyHot)(nil)

func (h *spyHot) WriteBatch(ctx context.Context, tid model.TenantID, b store.HotBatch) (store.BatchReceipt, error) {
	h.log.add("hot.WriteBatch")
	return store.BatchReceipt{Rows: len(b.Traces) + len(b.Spans)}, nil
}
func (h *spyHot) BindColdBlock(ctx context.Context, tid model.TenantID, walSegment string, m store.BlockManifest) (int, error) {
	h.log.add("hot.BindColdBlock")
	if h.bindErr != nil {
		return 0, h.bindErr
	}
	return 1, nil
}
func (h *spyHot) GetTrace(ctx context.Context, tid model.TenantID, id model.TraceID) (store.TraceIndex, error) {
	h.log.add("hot.GetTrace")
	return h.traceIndex, nil
}
func (h *spyHot) SearchSpans(ctx context.Context, tid model.TenantID, q store.SpanQuery) (store.SpanPage, error) {
	h.log.add("hot.SearchSpans")
	return store.SpanPage{}, nil
}
func (h *spyHot) SearchTraces(ctx context.Context, tid model.TenantID, q store.TraceQuery) (store.TracePage, error) {
	h.log.add("hot.SearchTraces")
	return store.TracePage{}, nil
}
func (h *spyHot) QueryRED(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) (store.REDSeries, error) {
	h.log.add("hot.QueryRED")
	return store.REDSeries{}, nil
}
func (h *spyHot) QueryEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error) {
	h.log.add("hot.QueryEdges")
	return nil, nil
}
func (h *spyHot) QueryErrorSignatures(ctx context.Context, tid model.TenantID, service string, w model.Window) ([]store.ErrorSignature, error) {
	h.log.add("hot.QueryErrorSignatures")
	return nil, nil
}
func (h *spyHot) ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window, n int) ([]store.Exemplar, error) {
	h.log.add("hot.ExemplarsFor")
	return nil, nil
}
func (h *spyHot) PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error) {
	h.log.add("hot.PathSeen")
	return time.Time{}, false, nil
}
func (h *spyHot) IngestedBytes(ctx context.Context, tid model.TenantID, w model.Window) (int64, error) {
	h.log.add("hot.IngestedBytes")
	return h.ingested, nil
}
func (h *spyHot) PendingColdRows(ctx context.Context, tid model.TenantID, olderThan time.Time) ([]store.PendingCold, error) {
	h.log.add("hot.PendingColdRows")
	return nil, nil
}
func (h *spyHot) ExpireBefore(ctx context.Context, tid model.TenantID, tbl store.TableID, cutoff time.Time) (store.Expired, error) {
	h.log.add("hot.ExpireBefore")
	return store.Expired{}, nil
}
func (h *spyHot) CascadeRED(ctx context.Context, tid model.TenantID, from, to model.Resolution, before time.Time) (int, error) {
	h.log.add("hot.CascadeRED")
	return 0, nil
}
func (h *spyHot) DiskUsage(ctx context.Context) (store.DiskReport, error) {
	h.log.add("hot.DiskUsage")
	return h.diskReport, nil
}
func (h *spyHot) Capabilities() store.HotCapabilities {
	return store.HotCapabilities{}
}
func (h *spyHot) Health(ctx context.Context) store.HealthReport {
	h.log.add("hot.Health")
	return store.HealthReport{Healthy: true}
}
func (h *spyHot) Close() error {
	h.log.add("hot.Close")
	return nil
}

// --- crash-consistency ordering invariant (DR-7) ---

// Append must fsync the cold WAL before the hot-index row commits — the
// trace is searchable at ack, never before the WAL write it points at
// exists.
func TestAppend_OrderingInvariant_ColdBeforeHot(t *testing.T) {
	log := &callLog{}
	cold := &spyCold{log: log, ref: store.WALRef{Segment: "seg-1"}}
	hot := &spyHot{log: log}
	ts := New(hot, cold, nil)

	tid := model.TenantID("t1")
	tr := model.Trace{TraceID: traceID(1), Tenant: tid, RootService: "checkout"}
	if _, err := ts.Append(context.Background(), tid, tr, store.ColdSampled, model.KeepProbabilistic); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := log.snapshot()
	if len(got) != 2 || got[0] != "cold.Append" || got[1] != "hot.WriteBatch" {
		t.Fatalf("call order = %v, want [cold.Append hot.WriteBatch]", got)
	}
}

// RunSealCycle must commit the cold manifest (Cold.Seal) before binding the
// hot-index rows that reference it (Hot.BindColdBlock) — a hot row may never
// carry a block_id for a manifest that doesn't exist yet.
func TestRunSealCycle_SealCommitsBeforeBind(t *testing.T) {
	log := &callLog{}
	cold := &spyCold{log: log, manifest: store.BlockManifest{Tenant: "t1"}}
	hot := &spyHot{log: log}
	ts := New(hot, cold, nil)

	due := []DueBlock{{Tenant: "t1", BlockID: "blk-1"}}
	manifests, err := ts.RunSealCycle(context.Background(), due)
	if err != nil {
		t.Fatalf("RunSealCycle: %v", err)
	}
	if len(manifests) != 1 || manifests[0].BlockID != "blk-1" {
		t.Fatalf("RunSealCycle manifests = %+v", manifests)
	}

	got := log.snapshot()
	if len(got) != 2 || got[0] != "cold.Seal" || got[1] != "hot.BindColdBlock" {
		t.Fatalf("call order = %v, want [cold.Seal hot.BindColdBlock]", got)
	}
}

// If Cold.Seal fails, Hot.BindColdBlock must never run: a hot row must never
// be bound to a manifest that didn't commit (DR-7's crash matrix).
func TestRunSealCycle_NeverBinds_WhenSealFails(t *testing.T) {
	log := &callLog{}
	cold := &spyCold{log: log, sealErr: errors.New("seal: disk full")}
	hot := &spyHot{log: log}
	ts := New(hot, cold, nil)

	due := []DueBlock{{Tenant: "t1", BlockID: "blk-1"}}
	if _, err := ts.RunSealCycle(context.Background(), due); err == nil {
		t.Fatalf("expected RunSealCycle to propagate the Seal error")
	}

	for _, c := range log.snapshot() {
		if c == "hot.BindColdBlock" {
			t.Fatalf("hot.BindColdBlock ran despite a failed Seal; calls = %v", log.snapshot())
		}
	}
}

// A failure partway through a multi-block cycle returns the manifests
// already bound, and stops before touching the failing block's hot rows.
func TestRunSealCycle_PartialFailureStopsAndReturnsProgress(t *testing.T) {
	log := &callLog{}
	hot := &spyHot{log: log}
	cold := &sealOnceThenFailCold{spyCold: spyCold{log: log}}
	ts := New(hot, cold, nil)

	due := []DueBlock{{Tenant: "t1", BlockID: "blk-1"}, {Tenant: "t1", BlockID: "blk-2"}}
	manifests, err := ts.RunSealCycle(context.Background(), due)
	if err == nil {
		t.Fatalf("expected an error from the second block's Seal")
	}
	if len(manifests) != 1 || manifests[0].BlockID != "blk-1" {
		t.Fatalf("manifests after partial failure = %+v, want exactly [blk-1]", manifests)
	}
}

// sealOnceThenFailCold seals its first block normally and fails every
// subsequent Seal, to test RunSealCycle's partial-progress behavior.
type sealOnceThenFailCold struct {
	spyCold
	sealed int
}

func (c *sealOnceThenFailCold) Seal(ctx context.Context, blockID string) (store.BlockManifest, error) {
	c.log.add("cold.Seal")
	c.sealed++
	if c.sealed > 1 {
		return store.BlockManifest{}, errors.New("seal: boom")
	}
	return store.BlockManifest{BlockID: blockID, Tenant: "t1"}, nil
}

// --- basic write+query round-trip through the real composite ---

func realTiered(t *testing.T) (*Store, model.TenantID) {
	t.Helper()
	dir := t.TempDir()
	hot, err := sqlite.Open(filepath.Join(dir, "traceiq.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	cold, err := parquet.Open(parquet.Config{Dir: filepath.Join(dir, "cold")})
	if err != nil {
		t.Fatalf("parquet.Open: %v", err)
	}
	ts := New(hot, cold, nil)
	t.Cleanup(func() {
		ts.Close()
		cold.Close()
	})
	return ts, model.TenantID("t1")
}

func TestStore_AppendAndGetTrace_RoundTrip(t *testing.T) {
	ts, tid := realTiered(t)
	ctx := context.Background()

	tr := model.Trace{
		TraceID:       traceID(1),
		Tenant:        tid,
		RootService:   "checkout",
		RootOperation: "POST /checkout",
		StartUnixNano: 1_700_000_000_000_000_000,
		EndUnixNano:   1_700_000_000_005_000_000,
		SpanCount:     1,
		Spans: []model.Span{{
			TraceID: traceID(1), SpanID: spanID(1), Name: "POST /checkout",
			StartUnixNano: 1_700_000_000_000_000_000, EndUnixNano: 1_700_000_000_005_000_000,
			Tenant: tid,
		}},
	}

	if _, err := ts.Append(ctx, tid, tr, store.ColdSampled, model.KeepProbabilistic); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := ts.GetTrace(ctx, tid, tr.TraceID)
	if err != nil {
		t.Fatalf("GetTrace (pending, via WAL): %v", err)
	}
	if got.TraceID != tr.TraceID || got.RootService != "checkout" {
		t.Fatalf("GetTrace mismatch: %+v", got)
	}
}

// After a block seals and its hot rows are bound, GetTrace must resolve via
// the sealed Parquet block rather than the (by-then-stale) WAL reference —
// the observable half of DR-7's ordering invariant.
func TestStore_SealAndBind_SwitchesReadPathToCold(t *testing.T) {
	ts, tid := realTiered(t)
	ctx := context.Background()

	tr := model.Trace{
		TraceID: traceID(2), Tenant: tid, RootService: "api", RootOperation: "GET /x",
		StartUnixNano: 1_700_000_000_000_000_000, EndUnixNano: 1_700_000_000_001_000_000,
	}
	receipt, err := ts.Append(ctx, tid, tr, store.ColdSampled, model.KeepProbabilistic)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(receipt.WALSegments) != 1 {
		t.Fatalf("receipt.WALSegments = %v, want 1 segment", receipt.WALSegments)
	}
	blockID := receipt.WALSegments[0]

	manifest, rowsBound, err := ts.SealAndBind(ctx, tid, blockID)
	if err != nil {
		t.Fatalf("SealAndBind: %v", err)
	}
	if rowsBound != 1 {
		t.Fatalf("SealAndBind rowsBound = %d, want 1", rowsBound)
	}
	if manifest.BlockID != blockID {
		t.Fatalf("manifest.BlockID = %q, want %q", manifest.BlockID, blockID)
	}

	got, err := ts.GetTrace(ctx, tid, tr.TraceID)
	if err != nil {
		t.Fatalf("GetTrace (sealed, via block): %v", err)
	}
	if got.TraceID != tr.TraceID || got.RootService != "api" {
		t.Fatalf("GetTrace after seal mismatch: %+v", got)
	}
}
