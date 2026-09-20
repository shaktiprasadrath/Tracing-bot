package store

import (
	"context"
	"fmt"
	"io"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/topology"
)

// HealthReport is store's own package-local health-check shape, used by
// HotIndex.Health and ColdStore.Health (DR-6, DR-7). The register never
// gives either a field list; per internal/model/health.go's note, store
// keeps a bare, package-local HealthReport rather than importing
// model.HealthReport, matching the earlier scaffold's resolution of the
// same ambiguity for other packages.
type HealthReport struct {
	Healthy   bool
	Message   string
	CheckedAt time.Time
	Details   map[string]string
}

// --- DR-6: hot index.

// HotIndex is the canonical hot-index interface (DR-6 §6.1, verbatim):
// batch-oriented, insert-only, append-only rollups with read-time
// aggregation, so the same contract is implementable on SQLite now and
// ClickHouse in Phase 2.
type HotIndex interface {
	// --- writes: exactly one batch call per flush, never per row ---
	WriteBatch(ctx context.Context, tid model.TenantID, b HotBatch) (BatchReceipt, error)
	BindColdBlock(ctx context.Context, tid model.TenantID, walSegment string, m BlockManifest) (rowsBound int, err error)

	// --- reads ---
	GetTrace(ctx context.Context, tid model.TenantID, id model.TraceID) (TraceIndex, error)
	SearchSpans(ctx context.Context, tid model.TenantID, q SpanQuery) (SpanPage, error)
	SearchTraces(ctx context.Context, tid model.TenantID, q TraceQuery) (TracePage, error)
	QueryRED(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) (REDSeries, error)
	QueryEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error)
	QueryErrorSignatures(ctx context.Context, tid model.TenantID, service string, w model.Window) ([]ErrorSignature, error)
	ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window, n int) ([]Exemplar, error)
	PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error)
	IngestedBytes(ctx context.Context, tid model.TenantID, w model.Window) (int64, error)
	PendingColdRows(ctx context.Context, tid model.TenantID, olderThan time.Time) ([]PendingCold, error)

	// --- retention ---
	ExpireBefore(ctx context.Context, tid model.TenantID, tbl TableID, cutoff time.Time) (Expired, error)
	CascadeRED(ctx context.Context, tid model.TenantID, from, to model.Resolution, before time.Time) (int, error)
	DiskUsage(ctx context.Context) (DiskReport, error)

	Capabilities() HotCapabilities
	Health(ctx context.Context) HealthReport
	Close() error
}

// HotBatch is one flush's worth of rows across every hot table (DR-6 §6.1,
// verbatim). FTSRows are written only for traces whose KeepReason is
// Error/Slow/Rare/Interest.
type HotBatch struct {
	Traces    []TraceIndex
	Spans     []SpanIndex
	AttrRows  []AttrIndexRow
	AttrDict  []AttrDictRow
	FTSRows   []FTSRow // only for traces whose KeepReason is Error/Slow/Rare/Interest
	RED       []model.REDSample
	Edges     []topology.Edge
	EdgeOps   []EdgeOpRow
	ErrorSigs []ErrorSignature
	PathSigs  []PathSigRow
	Exemplars []Exemplar
	Resources []ResourceRow
}

// BatchReceipt is HotIndex.WriteBatch's return shape (DR-6 §6.1, verbatim).
type BatchReceipt struct {
	Rows         int
	Bytes        int64
	CommitMillis int64
	WALSegments  []string
}

// HotCapabilities lets store/tiered state per-backend semantics instead of
// assuming SQLite (DR-6 §6.1, verbatim).
type HotCapabilities struct {
	ReadYourWrites     bool // sqlite: true; clickhouse ReplacingMergeTree: false
	RowLevelDelete     bool // sqlite: true; clickhouse: false (partition drop only)
	FullTextSearch     bool // sqlite fts5: true
	MaxKeptSpansPerSec int  // sqlite (dev box): 1200; clickhouse [P2]: 60000
}

// ObjectStore is DR-6's renamed BlobStore (02's name, F03's method set,
// verbatim).
type ObjectStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) (ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	Delete(ctx context.Context, keys []string) error
	List(ctx context.Context, prefix string, after string, limit int) ([]ObjectInfo, string, error)
	Kind() string
}

// ObjectInfo is ObjectStore.Put/List's row shape. DR-6 names the type but
// never prints its fields.
//
// TODO(DR-6): under-specified — reconstructed minimally from the method
// set's own vocabulary (key, size, an opaque continuation cursor).
type ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// --- Row/query types HotIndex's method set references. DR-6 §6.2 gives
// full DDL for attr_index/attr_dict and an ALTER for trace's new columns,
// but not a complete column list for span/trace/resource/FTS rows within
// the excerpted range (01 §5.1 is cited, not reproduced) — those row types
// are reconstructed minimally and flagged.

// TraceIndex is the trace row shape read/written through HotIndex.
//
// TODO(DR-6): under-specified — DR-6 §6.2 gives only an ALTER (block_id,
// row_group, row_offset, cold_state, wal_segment, keep_reason) against a
// base 01 §5.1 `trace` schema not reproduced in the excerpted range;
// reconstructed minimally from that ALTER plus model.Trace/KeepReason.
type TraceIndex struct {
	Tenant        model.TenantID
	TraceID       model.TraceID
	RootService   string
	StartUnixNano int64
	DurationNanos uint64
	ErrorCount    int
	PathSignature uint64
	KeepReason    model.KeepReason
	ColdState     uint8 // 0 pending, 1 sealed, 2 rollup_only, 3 lost
	BlockID       string
	RowGroup      int32
	RowOffset     int64
	WALSegment    string
}

// SpanIndex is the span row shape read/written through HotIndex.
//
// TODO(DR-6): under-specified — no DDL for `span` is reproduced in the
// excerpted range; reconstructed minimally from model.Span and the covering
// indexes DR-6 §6.3 lists (service, operation, start_unix_nano,
// error_sig_id).
type SpanIndex struct {
	Tenant        model.TenantID
	TraceID       model.TraceID
	SpanID        model.SpanID
	Service       string
	Operation     string
	StartUnixNano int64
	DurationNanos uint64
	ErrorSigID    string
}

// AttrIndexRow is one row of the closed 8-key attribute index (DR-6 §6.2's
// `attr_index` DDL, verbatim column list).
type AttrIndexRow struct {
	Tenant    model.TenantID
	KeyID     int
	ValueHash int64
	Bucket    int64 // start_unix_nano / 10e9, 10s bucket
	TraceID   model.TraceID
	SpanID    model.SpanID
}

// AttrDictRow is one row of the attribute value dictionary (DR-6 §6.2's
// `attr_dict` DDL, verbatim column list).
type AttrDictRow struct {
	Tenant    model.TenantID
	KeyID     int
	ValueHash int64
	Value     string // normalized, <= 128 bytes
	FirstSeen time.Time
	LastSeen  time.Time
}

// FTSRow is one full-text row, written only for spans of anomalous traces
// (keep_reason in 1..4) per DR-6 §6.2's note on span_text_fts.
//
// TODO(DR-6): under-specified — DDL for span_text_fts is not reproduced in
// the excerpted range; reconstructed minimally.
//
// Timestamp is the row's span.start_unix_nano (w10 review fix): DR-7's T0
// tier ("Span rows + attr_index + FTS", 24h dev retention) requires
// span_text_fts to expire on the same schedule as span/attr_index; without a
// time column ExpireBefore(TableSpanFTS, ...) cannot honor that tier at all.
type FTSRow struct {
	Tenant    model.TenantID
	TraceID   model.TraceID
	SpanID    model.SpanID
	Text      string
	Timestamp time.Time
}

// PathSigRow is one path-signature observation, drained from sampler's
// pathSigCh by the single telemetry writer, dedup-on-write (DR-10
// §"RecordPathSignature is removed from the decision path").
//
// TODO(DR-6/DR-10): under-specified — no Go/DDL block given; reconstructed
// minimally.
type PathSigRow struct {
	Tenant    model.TenantID
	Signature uint64
	FirstSeen time.Time
	LastSeen  time.Time
}

// EdgeOpRow mirrors topology_edge_op's DDL (DR-13, verbatim column list) as
// carried through a HotBatch write.
type EdgeOpRow struct {
	Tenant          model.TenantID
	EdgeID          string
	CalleeOperation string
	BucketStart     time.Time // 1h buckets only
	Calls           uint64
	Errors          uint64
	P99Nanos        uint64
}

// ResourceRow is the `resource` table's row shape, referenced by DR-6 §6.2
// (`traceiq.db` table list) but not given a column list in the excerpted
// range.
//
// TODO(DR-6): under-specified — reconstructed minimally.
type ResourceRow struct {
	Tenant model.TenantID
	Attrs  model.AttrMap
}

// ErrorSignature is QueryErrorSignatures's element type and a HotBatch
// field (DR-6). Named throughout the register (error_signature table,
// DR-6/DR-7/DR-14) but never given a Go field list in the excerpted range.
//
// TODO(DR-6): under-specified — reconstructed minimally.
type ErrorSignature struct {
	Tenant    model.TenantID
	ID        string
	Service   string
	Signature uint64
	FirstSeen time.Time
	LastSeen  time.Time
	Samples   int64
}

// Exemplar is ExemplarsFor's element type and a HotBatch field (DR-6).
//
// TODO(DR-6): under-specified — reconstructed minimally from "exemplar"
// usage elsewhere (RecordExemplar deleted; exemplars are trace/span
// pointers attached to a RED bucket or edge).
type Exemplar struct {
	Tenant    model.TenantID
	TraceID   model.TraceID
	SpanID    model.SpanID
	Timestamp time.Time
}

// PendingCold is PendingColdRows's element type (DR-6): trace rows still
// `cold_state = 0 (pending)` older than a cutoff, the back-fill work list
// for BindColdBlock.
//
// TODO(DR-6): under-specified — reconstructed minimally from the
// trace_by_coldstate index's own columns (tenant_id, cold_state,
// wal_segment).
type PendingCold struct {
	Tenant     model.TenantID
	TraceID    model.TraceID
	WALSegment string
}

// TableID identifies one hot table for ExpireBefore (DR-6). Named as a
// parameter type but never given a concrete representation.
//
// TODO(DR-6): under-specified — reconstructed as a closed string enum over
// the traceiq.db table list in DR-6 §6.2.
type TableID string

const (
	TableTrace       TableID = "trace"
	TableSpan        TableID = "span"
	TableAttrIndex   TableID = "attr_index"
	TableAttrDict    TableID = "attr_dict"
	TableSpanFTS     TableID = "span_text_fts"
	TableRedRollup   TableID = "red_rollup"
	TableTopologyEdg TableID = "topology_edge"
)

// Expired is ExpireBefore's return shape (DR-6's retention sweep).
//
// TODO(DR-6): under-specified — reconstructed minimally.
type Expired struct {
	Rows int64
}

// DiskReport is DiskUsage's return shape (DR-6).
//
// TODO(DR-6): under-specified — reconstructed minimally from 01 §10.2's
// store_hot_index_ratio definition (hot bytes on disk vs. raw ingested).
type DiskReport struct {
	HotBytes  int64
	ColdBytes int64
	Ratio     float64 // hot_bytes_on_disk / raw_ingested_span_bytes
}

// SpanQuery / SpanPage / TraceQuery / TracePage / REDSeries are
// SearchSpans/SearchTraces/QueryRED's parameter and return shapes (DR-6
// §6.1/§6.3). The register describes the query predicates in prose (by
// indexed attribute, by service+operation+time, free text, by error
// signature; by time, by service+duration, errors-only, by path signature)
// but never prints Go structs for them.
//
// TODO(DR-6): under-specified — reconstructed minimally from §6.3's query
// table.
type SpanQuery struct {
	Window     model.Window
	Service    string
	Operation  string
	AttrKey    string
	AttrValue  string
	ErrorSigID string
	FullText   string
	Limit      int
	PageToken  string
}

type SpanPage struct {
	Spans         []SpanIndex
	NextPageToken string
}

type TraceQuery struct {
	Window        model.Window
	Service       string
	MinDuration   time.Duration
	ErrorsOnly    bool
	PathSignature uint64
	Limit         int
	PageToken     string
}

type TracePage struct {
	Traces        []TraceIndex
	NextPageToken string
}

// REDSeries is QueryRED's return shape: a time-ordered run of
// model.REDSample at the requested resolution.
type REDSeries struct {
	Samples []model.REDSample
}

// BlockManifest is the cold-block manifest row BindColdBlock consumes and
// ColdStore.Seal produces (DR-6/DR-7). Named throughout DR-7 (`block_manifest`
// table, "the manifest row is committed") but never given a Go field list.
//
// TODO(DR-7): under-specified — reconstructed minimally from DR-7's crash
// matrix and Seal's doc comment (block id, object paths, checksum, sealed
// time).
type BlockManifest struct {
	BlockID    string
	Tenant     model.TenantID
	Tier       ColdTier
	HourBucket time.Time
	SpansPath  string
	TracesPath string
	MetaPath   string
	Checksum   string
	SealedAt   time.Time
	RowCount   int64
}

// --- DR-7: cold store.

// ColdTier distinguishes anomalous vs. sampled cold bodies (DR-7, verbatim).
type ColdTier uint8

const (
	ColdAnomalous ColdTier = 1
	ColdSampled   ColdTier = 2
)

// ColdStore is DR-7's canonical cold-tier interface, verbatim. Per-trace
// Parquet rows are appended to time-bucketed blocks that seal on size or
// time; there is no per-trace row group and no ColdPointer.
type ColdStore interface {
	// Append writes the trace's spans into the open block for (tenant, tier, hour bucket).
	// It returns only after the write-ahead journal record is group-fsynced.
	Append(ctx context.Context, tid model.TenantID, t model.Trace, tier ColdTier) (WALRef, error)
	// Seal closes a block, writes spans.parquet/traces.parquet/meta.json, verifies the
	// checksum and returns the manifest. The caller commits the manifest row and then
	// calls HotIndex.BindColdBlock with the same WAL segment id.
	Seal(ctx context.Context, blockID string) (BlockManifest, error)
	SealDue(ctx context.Context, now time.Time) ([]string, error)
	ReadTrace(ctx context.Context, tid model.TenantID, id model.TraceID, loc TraceLoc) (model.Trace, error)
	ReadFromWAL(ctx context.Context, tid model.TenantID, id model.TraceID, ref WALRef) (model.Trace, error)
	ReplayWAL(ctx context.Context) (ReplayReport, error)
	ExpireBlocks(ctx context.Context, before time.Time, tier ColdTier) ([]string, error)
	Tombstone(ctx context.Context, tid model.TenantID, ids []model.TraceID, reason string) error
	Compact(ctx context.Context, level int, b CompactBudget) (CompactReport, error)
	Health(ctx context.Context) HealthReport
}

// WALRef locates one cold write-ahead journal record (DR-7, verbatim).
type WALRef struct {
	Segment string
	Offset  int64
	Length  int32
	CRC32C  uint32
}

// TraceLoc is resolved from the hot index; it is never returned
// synchronously by Append (DR-7, verbatim).
type TraceLoc struct {
	BlockID   string
	RowGroup  int32
	RowOffset int64
	ColdState uint8
	WAL       WALRef
}

// ReplayReport is ColdStore.ReplayWAL's return shape (DR-7's crash matrix:
// "ReplayWAL finds records with no trace row, re-appends them to a new open
// block"). The register never prints a struct for it.
//
// TODO(DR-7): under-specified — reconstructed minimally; narrow against the
// crash-matrix counters (traceiq_cold_wal_replayed_total) before treating
// as final.
type ReplayReport struct {
	SegmentsReplayed int
	TracesReappended int
	Duration         time.Duration
}

// CompactBudget bounds one Compact call (DR-7's compaction write-
// amplification budget, store.cold.compaction.max_bytes_per_hour).
//
// TODO(DR-7): under-specified — reconstructed minimally.
type CompactBudget struct {
	MaxBytes int64
}

// CompactReport is Compact's return shape (DR-7).
//
// TODO(DR-7): under-specified — reconstructed minimally from
// traceiq_cold_compaction_write_amplification.
type CompactReport struct {
	BlocksIn           int
	BlocksOut          int
	BytesWritten       int64
	WriteAmplification float64
	TombstonesPurged   int
}

// --- DR-12: cost control.

// Watermark is the disk-pressure escalation rung (DR-12, verbatim).
type Watermark uint8

const (
	WMNormal   Watermark = 0
	WMWarn     Watermark = 1
	WMHigh     Watermark = 2
	WMCritical Watermark = 3
)

// CostSignal is TieredStore.Signals's element type (DR-12, verbatim).
// store never imports sampler: cmd/traceiq wires this channel to
// sampler.AdjustFloor.
type CostSignal struct {
	Tenant              model.TenantID
	ObservedBytesPerSec float64 // 5-minute RATE, not a 24h cumulative
	BudgetBytesPerSec   float64 // tenant.Policy.ByteBudgetBytes / retention horizon
	CurrentFloor        float64
	RecommendedFloor    float64
	DiskUsedRatio       float64
	Watermark           Watermark
	EmittedAt           time.Time
}

// TieredStore is the composed hot+cold store that emits CostSignal (DR-12,
// verbatim: `func (s *TieredStore) Signals() <-chan CostSignal`). DR-8/DR-9
// also refer to store.TieredStore as the table-router across traceiq.db and
// control.db (DR-6 §6.2).
//
// The unexported fields below are store/tiered's own wiring (this pass's
// concrete implementation of the "business-logic wiring, out of scope for
// [the] types-only pass" note): a clock for DR-31's ban on time.Now inside
// this package, and a buffered CostSignal channel so EmitCostSignal never
// blocks the caller that drives it.
type TieredStore struct {
	Hot  HotIndex
	Cold ColdStore

	clock     model.Clock
	signalsCh chan CostSignal
}

// NewTieredStore composes a hot index and cold store into a TieredStore,
// wiring model.Clock per DR-31 so retention sweeps and cost-signal emission
// never call time.Now directly.
func NewTieredStore(hot HotIndex, cold ColdStore, clock model.Clock) *TieredStore {
	return &TieredStore{
		Hot:       hot,
		Cold:      cold,
		clock:     clock,
		signalsCh: make(chan CostSignal, 64),
	}
}

// Signals returns the cost-control channel the controller drains at
// store.cost.signal_interval (DR-12, verbatim signature).
func (s *TieredStore) Signals() <-chan CostSignal {
	return s.signalsCh
}

// EmitCostSignal runs one cost-controller tick (F03 §4.4: proportional
// controller, 0.15 deadband, 0.5x/2x per-interval step, watermark ladder)
// for tenant tid and pushes the result onto Signals(). It is a pure
// function of its inputs plus s.clock.Now(), so callers (store/tiered's
// background ticker in production, a test in this package) can drive it
// deterministically without a real-time goroutine.
func (s *TieredStore) EmitCostSignal(tid model.TenantID, observedBytesPerSec, budgetBytesPerSec, currentFloor, diskUsedRatio float64) CostSignal {
	const (
		deadband         = 0.15
		maxStep          = 0.5
		adaptiveFloorMin = 0.0001
		adaptiveFloorMax = 1.0
		maxKeepRateFloor = 0.05
	)
	ratio := 0.0
	if budgetBytesPerSec > 0 {
		ratio = observedBytesPerSec / budgetBytesPerSec
	}
	wm := watermarkFor(diskUsedRatio)
	newFloor := currentFloor
	if diff := ratio - 1; diff > deadband || diff < -deadband {
		step := currentFloor * maxStep
		if step <= 0 {
			step = adaptiveFloorMin
		}
		if ratio > 1 {
			newFloor = currentFloor - step
		} else {
			newFloor = currentFloor + step
		}
		if newFloor < adaptiveFloorMin {
			newFloor = adaptiveFloorMin
		}
		if newFloor > adaptiveFloorMax {
			newFloor = adaptiveFloorMax
		}
	}
	now := time.Now()
	if s.clock != nil {
		now = s.clock.Now()
	}
	sig := CostSignal{
		Tenant:              tid,
		ObservedBytesPerSec: observedBytesPerSec,
		BudgetBytesPerSec:   budgetBytesPerSec,
		CurrentFloor:        currentFloor,
		RecommendedFloor:    newFloor,
		DiskUsedRatio:       diskUsedRatio,
		Watermark:           wm,
		EmittedAt:           now,
	}
	select {
	case s.signalsCh <- sig:
	default:
		// channel full: drop rather than block the controller tick.
	}
	return sig
}

func watermarkFor(diskUsedRatio float64) Watermark {
	switch {
	case diskUsedRatio >= 0.95:
		return WMCritical
	case diskUsedRatio >= 0.85:
		return WMHigh
	case diskUsedRatio >= 0.80:
		return WMWarn
	default:
		return WMNormal
	}
}

// SweepRetention runs one retention pass for tenant tid against policy
// (F03 §4.4's block-scoped reaper): ranged deletes on every hot table this
// package knows how to expire, then whole-block cold expiry for the
// anomalous and sampled tiers. now is read from s.clock so tests can drive
// it with a fake clock.
func (s *TieredStore) SweepRetention(ctx context.Context, tid model.TenantID, policy RetentionPolicy) error {
	now := time.Now()
	if s.clock != nil {
		now = s.clock.Now()
	}
	hotTables := []struct {
		tbl     TableID
		horizon time.Duration
	}{
		{TableSpan, policy.HotSpanRows},
		{TableAttrIndex, policy.HotSpanRows},
		{TableSpanFTS, policy.HotSpanRows},
		{TableTrace, policy.HotTraceRows},
		{TableRedRollup, policy.REDRollups},
		{TableTopologyEdg, policy.HotRollups},
	}
	for _, h := range hotTables {
		if h.horizon <= 0 {
			continue
		}
		if _, err := s.Hot.ExpireBefore(ctx, tid, h.tbl, now.Add(-h.horizon)); err != nil {
			return fmt.Errorf("store: expire %s: %w", h.tbl, err)
		}
	}
	if policy.ColdAnomalous > 0 {
		if _, err := s.Cold.ExpireBlocks(ctx, now.Add(-policy.ColdAnomalous), ColdAnomalous); err != nil {
			return fmt.Errorf("store: expire anomalous blocks: %w", err)
		}
	}
	if policy.ColdSampled > 0 {
		if _, err := s.Cold.ExpireBlocks(ctx, now.Add(-policy.ColdSampled), ColdSampled); err != nil {
			return fmt.Errorf("store: expire sampled blocks: %w", err)
		}
	}
	return nil
}

// spanService returns sp's own originating service (sp.Resource.ServiceName),
// not the trace-level root service. W18-D1: model.Span.Service() is still a
// panic("not implemented") stub in internal/model/telemetry.go, and this
// package may not edit internal/model (scope lock), so — mirroring
// internal/sampler/spanutil.go's spanService, which hit the same
// constraint — a local equivalent lives here instead.
func spanService(sp *model.Span) string {
	if sp.Resource != nil {
		return sp.Resource.ServiceName
	}
	return ""
}

// Append implements the DR-7 ordering invariant end to end: the cold-WAL
// group fsync commits first, then the hot-index trace row commits with
// cold_state=0 (pending) and wal_segment set — this is what makes an
// appended trace searchable via ReadFromWAL before its block ever seals
// (FR-F03-1, FR-F03-10's "every write routed to the correct tier by table").
func (s *TieredStore) Append(ctx context.Context, tid model.TenantID, t model.Trace, tier ColdTier, keep model.KeepReason) (BatchReceipt, error) {
	ref, err := s.Cold.Append(ctx, tid, t, tier)
	if err != nil {
		return BatchReceipt{}, fmt.Errorf("store: cold append: %w", err)
	}
	ti := TraceIndex{
		Tenant:        tid,
		TraceID:       t.TraceID,
		RootService:   t.RootService,
		StartUnixNano: int64(t.StartUnixNano),
		DurationNanos: t.DurationNanos,
		ErrorCount:    t.ErrorCount,
		PathSignature: t.PathSignature,
		KeepReason:    keep,
		ColdState:     0,
		WALSegment:    ref.Segment,
	}
	spans := make([]SpanIndex, 0, len(t.Spans))
	for _, sp := range t.Spans {
		spans = append(spans, SpanIndex{
			Tenant:        tid,
			TraceID:       t.TraceID,
			SpanID:        sp.SpanID,
			Service:       spanService(&sp),
			Operation:     sp.Name,
			StartUnixNano: int64(sp.StartUnixNano),
			DurationNanos: sp.EndUnixNano - sp.StartUnixNano,
		})
	}
	receipt, err := s.Hot.WriteBatch(ctx, tid, HotBatch{Traces: []TraceIndex{ti}, Spans: spans})
	if err != nil {
		return receipt, fmt.Errorf("store: hot write: %w", err)
	}
	receipt.WALSegments = append(receipt.WALSegments, ref.Segment)
	return receipt, nil
}

// GetTrace resolves a trace through the hot index first, then the cheapest
// cold path able to answer: a pending trace resolves via ReadFromWAL, a
// sealed one via TraceLoc (FR-F03-2).
func (s *TieredStore) GetTrace(ctx context.Context, tid model.TenantID, id model.TraceID) (model.Trace, error) {
	ti, err := s.Hot.GetTrace(ctx, tid, id)
	if err != nil {
		return model.Trace{}, err
	}
	if ti.ColdState == 0 {
		return s.Cold.ReadFromWAL(ctx, tid, id, WALRef{Segment: ti.WALSegment})
	}
	loc := TraceLoc{BlockID: ti.BlockID, RowGroup: ti.RowGroup, RowOffset: ti.RowOffset, ColdState: ti.ColdState}
	return s.Cold.ReadTrace(ctx, tid, id, loc)
}

// SealAndBind seals a due cold block and, only after the manifest row is
// durable, back-fills the hot index's pointer rows — the DR-7 ordering a
// block is "sealed before hot row references it".
func (s *TieredStore) SealAndBind(ctx context.Context, tid model.TenantID, blockID string) (BlockManifest, int, error) {
	m, err := s.Cold.Seal(ctx, blockID)
	if err != nil {
		return BlockManifest{}, 0, err
	}
	// store/parquet's open block uses its own block id as the WAL segment
	// name for the block's whole open lifetime (documented simplification,
	// see docs/reports/w9-store.md): a block always has exactly one
	// wal_segment value to bind, so blockID doubles as that segment name.
	n, err := s.Hot.BindColdBlock(ctx, tid, blockID, m)
	if err != nil {
		return m, 0, err
	}
	return m, n, nil
}

// RetentionPolicy is DR-12's per-tenant materialized retention/budget
// policy (verbatim), distinct from config.StoreRetentionConfig: it merges
// the config defaults with tenant.Policy overrides (DR-5's
// tenant-first-column rule) and is what F03's retention sweep and DR-12's
// cost controller both read.
type RetentionPolicy struct {
	Tenant          model.TenantID
	HotSpanRows     time.Duration
	HotTraceRows    time.Duration
	HotRollups      time.Duration
	ColdAnomalous   time.Duration
	ColdSampled     time.Duration
	REDRollups      time.Duration
	Investigations  time.Duration
	Audit           time.Duration
	MaxDiskBytes    int64   // NEW
	HighWatermark   float64 // NEW, default 0.85
	ActionOnFull    string  // NEW, "shed_sampled" | "stop_ingest"
	ByteBudgetBytes int64   // per-tenant, from tenant.Policy
}
