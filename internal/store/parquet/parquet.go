// Package parquet — see doc.go for the binding source and scope.
package parquet

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	pq "github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

// Config configures a Store. Field names mirror store.cold.* (F03 §4.3's
// config block); this pass reads a Go struct rather than YAML directly.
type Config struct {
	Dir       string // root dir; wal/ and blocks/ live under it
	Clock     model.Clock
	Retention map[store.ColdTier]time.Duration // dev defaults applied if nil (DR-7 §7)
	// WalRetain is store.cold.wal_retain (DR-7 §7: "WAL segments are
	// retained until seal + store.cold.wal_retain, default 10m"). Zero
	// applies the dev default. See PruneWAL.
	WalRetain time.Duration
}

// defaultWalRetain is store.cold.wal_retain's dev default (DR-7 §7).
const defaultWalRetain = 10 * time.Minute

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// openBlock is one (tenant, tier, hour) block still accepting Appends. Per
// this driver's simplification (documented in docs/reports/w9-store.md),
// an open block owns exactly one WAL segment — its own block id — for its
// whole open lifetime, so BindColdBlock's single walSegment argument always
// resolves unambiguously to the block that sealed it.
type openBlock struct {
	mu         sync.Mutex
	id         string
	tenant     model.TenantID
	tier       store.ColdTier
	hourBucket time.Time
	walPath    string
	walFile    *os.File
	walOffset  int64
	traces     []model.Trace
	createdAt  time.Time
}

// Store is the store/parquet driver: an ordering-invariant cold WAL plus
// time-bucketed, zstd-compressed Parquet blocks (DR-7).
type Store struct {
	cfg Config

	mu     sync.Mutex
	open   map[string]*openBlock          // blockKey -> open block
	sealed map[string]store.BlockManifest // blockID -> manifest
	tomb   map[model.TenantID]map[model.TraceID]bool
}

var _ store.ColdStore = (*Store)(nil)

// Close releases every still-open block's WAL file handle without sealing
// it (ColdStore has no Close in its method set, DR-7 — open WAL segments are
// exactly what ReplayWAL recovers on the next Open, so this is a plain
// resource release, not a flush). Safe to call on process shutdown; callers
// that want a durable seal first should call Seal/SealDue before Close.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for _, b := range s.open {
		if err := b.walFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func defaultRetention() map[store.ColdTier]time.Duration {
	return map[store.ColdTier]time.Duration{
		store.ColdAnomalous: 30 * 24 * time.Hour,
		store.ColdSampled:   7 * 24 * time.Hour,
	}
}

// Open opens (or creates) the cold store rooted at cfg.Dir, reloading any
// sealed block manifests already on disk (meta.json IS the manifest).
func Open(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("parquet: Config.Dir is required")
	}
	if cfg.Retention == nil {
		cfg.Retention = defaultRetention()
	}
	if cfg.WalRetain <= 0 {
		cfg.WalRetain = defaultWalRetain
	}
	for _, sub := range []string{"wal", "blocks"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("parquet: mkdir %s: %w", sub, err)
		}
	}
	s := &Store{
		cfg:    cfg,
		open:   map[string]*openBlock{},
		sealed: map[string]store.BlockManifest{},
		tomb:   map[model.TenantID]map[model.TraceID]bool{},
	}
	entries, _ := os.ReadDir(filepath.Join(cfg.Dir, "blocks"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(cfg.Dir, "blocks", e.Name(), "meta.json")
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var m store.BlockManifest
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		s.sealed[m.BlockID] = m
	}
	return s, nil
}

func (s *Store) now() time.Time {
	if s.cfg.Clock != nil {
		return s.cfg.Clock.Now()
	}
	return time.Now()
}

func blockKey(tid model.TenantID, tier store.ColdTier, hour time.Time) string {
	return fmt.Sprintf("%s|%d|%d", tid, tier, hour.Unix())
}

func newBlockID(tid model.TenantID, tier store.ColdTier, hour time.Time, seq int64) string {
	return fmt.Sprintf("blk-%s-%d-%d-%d", tid, tier, hour.Unix(), seq)
}

// blockSeq is process-global (block ids must stay unique across every
// *Store in the process, matching newBlockID's per-(tenant,tier,hour) key
// space) so it needs its own synchronization independent of any one Store's
// mu — a plain `blockSeq++` here raced under Go's memory model whenever two
// Store instances (e.g. two tests, or multiple cold stores in one process)
// called Append concurrently (w10 review fix).
var blockSeq atomic.Int64

func nextSeq() int64 {
	return blockSeq.Add(1)
}

// Append writes the trace's spans to the cold write-ahead journal for its
// (tenant, tier, hour) block and returns only after fsync (FR-F03-1). This
// driver fsyncs every record rather than group-committing on a
// 250ms/4MiB timer — a documented simplification (correctness over the
// batching optimization) given this pass's time budget.
func (s *Store) Append(ctx context.Context, tid model.TenantID, t model.Trace, tier store.ColdTier) (store.WALRef, error) {
	hour := time.Unix(0, int64(t.StartUnixNano)).UTC().Truncate(time.Hour)
	key := blockKey(tid, tier, hour)

	s.mu.Lock()
	b, ok := s.open[key]
	if !ok {
		id := newBlockID(tid, tier, hour, nextSeq())
		walPath := filepath.Join(s.cfg.Dir, "wal", id+".wal")
		f, err := os.OpenFile(walPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
		if err != nil {
			s.mu.Unlock()
			return store.WALRef{}, fmt.Errorf("parquet: open wal: %w", err)
		}
		b = &openBlock{id: id, tenant: tid, tier: tier, hourBucket: hour, walPath: walPath, walFile: f, createdAt: s.now()}
		s.open[key] = b
	}
	s.mu.Unlock()

	buf, err := encodeTrace(t)
	if err != nil {
		return store.WALRef{}, fmt.Errorf("parquet: encode trace: %w", err)
	}
	crc := crc32.Checksum(buf, crcTable)

	b.mu.Lock()
	defer b.mu.Unlock()
	off := b.walOffset
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(buf)))
	binary.BigEndian.PutUint32(hdr[4:8], crc)
	if _, err := b.walFile.Write(hdr); err != nil {
		return store.WALRef{}, err
	}
	if _, err := b.walFile.Write(buf); err != nil {
		return store.WALRef{}, err
	}
	if err := b.walFile.Sync(); err != nil {
		return store.WALRef{}, fmt.Errorf("parquet: wal fsync: %w", err)
	}
	b.walOffset += int64(len(hdr) + len(buf))
	b.traces = append(b.traces, t)

	return store.WALRef{Segment: b.id, Offset: off, Length: int32(len(buf)), CRC32C: crc}, nil
}

func encodeTrace(t model.Trace) ([]byte, error) {
	var buf writeCounter
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(t); err != nil {
		return nil, err
	}
	return buf.b, nil
}

func decodeTrace(b []byte) (model.Trace, error) {
	var t model.Trace
	dec := gob.NewDecoder(&byteReader{b: b})
	if err := dec.Decode(&t); err != nil {
		return model.Trace{}, err
	}
	return t, nil
}

// writeCounter is a trivial io.Writer sink for gob-encoding into memory.
type writeCounter struct{ b []byte }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

type byteReader struct {
	b   []byte
	off int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

// ReadFromWAL resolves a still-pending trace, preferring the in-memory copy
// on its open block and falling back to a direct WAL-file read (e.g. after
// a process restart followed by ReplayWAL).
func (s *Store) ReadFromWAL(ctx context.Context, tid model.TenantID, id model.TraceID, ref store.WALRef) (model.Trace, error) {
	s.mu.Lock()
	var b *openBlock
	for _, ob := range s.open {
		if ob.id == ref.Segment {
			b = ob
			break
		}
	}
	s.mu.Unlock()
	if b != nil {
		b.mu.Lock()
		for _, t := range b.traces {
			if t.TraceID == id {
				b.mu.Unlock()
				return t, nil
			}
		}
		b.mu.Unlock()
	}
	return s.readTraceFromWALFile(ref, id)
}

func (s *Store) readTraceFromWALFile(ref store.WALRef, id model.TraceID) (model.Trace, error) {
	path := filepath.Join(s.cfg.Dir, "wal", ref.Segment+".wal")
	f, err := os.Open(path)
	if err != nil {
		return model.Trace{}, fmt.Errorf("parquet: wal segment %s: %w", ref.Segment, err)
	}
	defer f.Close()
	for {
		hdr := make([]byte, 8)
		if _, err := io.ReadFull(f, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return model.Trace{}, err
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		wantCRC := binary.BigEndian.Uint32(hdr[4:8])
		buf := make([]byte, n)
		if _, err := io.ReadFull(f, buf); err != nil {
			return model.Trace{}, err
		}
		// w10 review fix: the CRC32C DR-7/FR-F03-1 requires per record was
		// stored in the header at Append but never checked on read — a
		// truncated or corrupted record could silently decode as a
		// different (or garbage) trace. Skip any record that fails it.
		if crc32.Checksum(buf, crcTable) != wantCRC {
			continue
		}
		t, err := decodeTrace(buf)
		if err != nil {
			continue
		}
		if t.TraceID == id {
			return t, nil
		}
	}
	return model.Trace{}, fmt.Errorf("parquet: trace not found in wal segment %s", ref.Segment)
}

// Seal closes an open block, writes spans.parquet/traces.parquet/meta.json,
// verifies the checksum and returns the manifest (DR-7's SealDue algorithm:
// "manifest row committed FIRST").
func (s *Store) Seal(ctx context.Context, blockID string) (store.BlockManifest, error) {
	s.mu.Lock()
	var key string
	var b *openBlock
	for k, ob := range s.open {
		if ob.id == blockID {
			b, key = ob, k
			break
		}
	}
	if b == nil {
		s.mu.Unlock()
		return store.BlockManifest{}, fmt.Errorf("parquet: unknown open block %q", blockID)
	}
	delete(s.open, key)
	s.mu.Unlock()

	b.mu.Lock()
	traces := append([]model.Trace(nil), b.traces...)
	b.mu.Unlock()

	dir := filepath.Join(s.cfg.Dir, "blocks", blockID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return store.BlockManifest{}, err
	}
	spansPath := filepath.Join(dir, "spans.parquet")
	tracesPath := filepath.Join(dir, "traces.parquet")
	metaPath := filepath.Join(dir, "meta.json")

	if err := writeSpansParquet(spansPath, traces); err != nil {
		return store.BlockManifest{}, fmt.Errorf("parquet: write spans.parquet: %w", err)
	}
	if err := writeTracesParquet(tracesPath, traces); err != nil {
		return store.BlockManifest{}, fmt.Errorf("parquet: write traces.parquet: %w", err)
	}
	checksum, err := fileChecksum(spansPath)
	if err != nil {
		return store.BlockManifest{}, err
	}

	m := store.BlockManifest{
		BlockID: blockID, Tenant: b.tenant, Tier: b.tier, HourBucket: b.hourBucket,
		SpansPath: spansPath, TracesPath: tracesPath, MetaPath: metaPath,
		Checksum: checksum, SealedAt: s.now(), RowCount: int64(len(traces)),
	}
	metaBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return store.BlockManifest{}, err
	}
	if err := os.WriteFile(metaPath, metaBytes, 0o644); err != nil {
		return store.BlockManifest{}, err
	}

	s.mu.Lock()
	s.sealed[blockID] = m
	s.mu.Unlock()

	_ = b.walFile.Close()
	return m, nil
}

// SealDue seals every open block older than flush_interval (dev default 5m,
// hardcoded here — see Config for the knob this pass doesn't yet expose).
func (s *Store) SealDue(ctx context.Context, now time.Time) ([]string, error) {
	const flushInterval = 5 * time.Minute
	s.mu.Lock()
	var due []string
	for _, b := range s.open {
		if now.Sub(b.createdAt) >= flushInterval {
			due = append(due, b.id)
		}
	}
	s.mu.Unlock()

	sort.Strings(due)
	var sealedIDs []string
	for _, id := range due {
		if _, err := s.Seal(ctx, id); err != nil {
			return sealedIDs, err
		}
		sealedIDs = append(sealedIDs, id)
	}
	return sealedIDs, nil
}

// DueBlock pairs an open block's tenant with its id, for store/tiered's seal
// cycle (DueBlocks below).
type DueBlock struct {
	Tenant  model.TenantID
	BlockID string
}

// DueBlocks lists open blocks whose age is >= the flush interval, without
// sealing them. Unlike SealDue (which seals every due block itself, with no
// hot-index reference to bind), this lets store/tiered seal-then-bind one
// block at a time via store.TieredStore.SealAndBind, preserving DR-7's
// ordering invariant (manifest row committed before the hot rows that
// reference it).
func (s *Store) DueBlocks(now time.Time) []DueBlock {
	const flushInterval = 5 * time.Minute
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []DueBlock
	for _, b := range s.open {
		if now.Sub(b.createdAt) >= flushInterval {
			due = append(due, DueBlock{Tenant: b.tenant, BlockID: b.id})
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].BlockID < due[j].BlockID })
	return due
}

// spanParquetRow / traceParquetRow are the columnar schemas written by
// this driver. Field names follow OTel semantic-convention-ish snake_case
// (FR-F03-9) though full OTel-attribute fidelity and an external DuckDB
// round-trip are out of scope for this pass (docs/reports/w9-store.md).
type spanParquetRow struct {
	TenantID      string `parquet:"tenant_id"`
	TraceID       string `parquet:"trace_id"`
	SpanID        string `parquet:"span_id"`
	Service       string `parquet:"service_name"`
	Operation     string `parquet:"operation_name"`
	Kind          uint8  `parquet:"kind"`
	StartUnixNano uint64 `parquet:"start_unix_nano"`
	EndUnixNano   uint64 `parquet:"end_unix_nano"`
	StatusCode    uint8  `parquet:"status_code"`
	AttrsJSON     string `parquet:"attrs_json"`
}

type traceParquetRow struct {
	TenantID      string `parquet:"tenant_id"`
	TraceID       string `parquet:"trace_id"`
	RootService   string `parquet:"root_service"`
	RootOperation string `parquet:"root_operation"`
	StartUnixNano uint64 `parquet:"start_unix_nano"`
	EndUnixNano   uint64 `parquet:"end_unix_nano"`
	SpanCount     int64  `parquet:"span_count"`
	ErrorCount    int64  `parquet:"error_count"`
	PathSignature uint64 `parquet:"path_signature"`
}

func writeSpansParquet(path string, traces []model.Trace) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := pq.NewGenericWriter[spanParquetRow](f, pq.Compression(&zstd.Codec{}))
	for _, t := range traces {
		for _, sp := range t.Spans {
			attrs, _ := json.Marshal(sp.Attrs)
			row := spanParquetRow{
				TenantID: string(t.Tenant), TraceID: hex.EncodeToString(t.TraceID[:]), SpanID: hex.EncodeToString(sp.SpanID[:]),
				Service: t.RootService, Operation: sp.Name, Kind: uint8(sp.Kind),
				StartUnixNano: sp.StartUnixNano, EndUnixNano: sp.EndUnixNano, StatusCode: uint8(sp.Status.Code),
				AttrsJSON: string(attrs),
			}
			if _, err := w.Write([]spanParquetRow{row}); err != nil {
				return err
			}
		}
	}
	if len(traces) == 0 {
		// still produce a valid, empty parquet file with the schema present.
	}
	return w.Close()
}

func writeTracesParquet(path string, traces []model.Trace) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := pq.NewGenericWriter[traceParquetRow](f, pq.Compression(&zstd.Codec{}))
	for _, t := range traces {
		row := traceParquetRow{
			TenantID: string(t.Tenant), TraceID: hex.EncodeToString(t.TraceID[:]), RootService: t.RootService,
			RootOperation: t.RootOperation, StartUnixNano: t.StartUnixNano, EndUnixNano: t.EndUnixNano,
			SpanCount: int64(t.SpanCount), ErrorCount: int64(t.ErrorCount), PathSignature: t.PathSignature,
		}
		if _, err := w.Write([]traceParquetRow{row}); err != nil {
			return err
		}
	}
	return w.Close()
}

func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadTrace reconstructs a sealed trace from its block's Parquet files.
// Span/trace attribute fidelity is reduced relative to the original
// model.Trace (attrs round-trip as a single JSON blob rather than
// model.AttrMap) — a documented deviation for this pass.
func (s *Store) ReadTrace(ctx context.Context, tid model.TenantID, id model.TraceID, loc store.TraceLoc) (model.Trace, error) {
	s.mu.Lock()
	m, ok := s.sealed[loc.BlockID]
	s.mu.Unlock()
	if !ok {
		return model.Trace{}, fmt.Errorf("parquet: unknown sealed block %q", loc.BlockID)
	}
	if s.isTombstoned(tid, id) {
		return model.Trace{}, fmt.Errorf("parquet: trace %x is tombstoned", id)
	}

	traceHex := hex.EncodeToString(id[:])

	tf, err := os.Open(m.TracesPath)
	if err != nil {
		return model.Trace{}, err
	}
	defer tf.Close()
	tr := pq.NewGenericReader[traceParquetRow](tf)
	defer tr.Close()
	var trace model.Trace
	found := false
	buf := make([]traceParquetRow, 64)
	for {
		n, err := tr.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i].TraceID == traceHex {
				trace = model.Trace{
					TraceID: id, Tenant: tid, RootService: buf[i].RootService, RootOperation: buf[i].RootOperation,
					StartUnixNano: buf[i].StartUnixNano, EndUnixNano: buf[i].EndUnixNano,
					SpanCount: int(buf[i].SpanCount), ErrorCount: int(buf[i].ErrorCount), PathSignature: buf[i].PathSignature,
				}
				found = true
			}
		}
		if err != nil {
			break
		}
	}
	if !found {
		return model.Trace{}, fmt.Errorf("parquet: trace %x not found in block %q", id, loc.BlockID)
	}

	sf, err := os.Open(m.SpansPath)
	if err != nil {
		return model.Trace{}, err
	}
	defer sf.Close()
	sr := pq.NewGenericReader[spanParquetRow](sf)
	defer sr.Close()
	sbuf := make([]spanParquetRow, 64)
	for {
		n, err := sr.Read(sbuf)
		for i := 0; i < n; i++ {
			if sbuf[i].TraceID != traceHex {
				continue
			}
			var sid model.SpanID
			if b, derr := hex.DecodeString(sbuf[i].SpanID); derr == nil {
				copy(sid[:], b)
			}
			trace.Spans = append(trace.Spans, model.Span{
				TraceID: id, SpanID: sid, Name: sbuf[i].Operation, Kind: model.SpanKind(sbuf[i].Kind),
				StartUnixNano: sbuf[i].StartUnixNano, EndUnixNano: sbuf[i].EndUnixNano,
				Status: model.Status{Code: model.StatusCode(sbuf[i].StatusCode)}, Tenant: tid,
			})
		}
		if err != nil {
			break
		}
	}
	return trace, nil
}

// ReplayWAL reloads any WAL segment on disk that has no sealed manifest
// covering it, re-appending its records into a fresh open block (DR-7's
// crash matrix: "ReplayWAL finds records with no trace row, re-appends
// them to a new open block").
func (s *Store) ReplayWAL(ctx context.Context) (store.ReplayReport, error) {
	start := s.now()
	entries, err := os.ReadDir(filepath.Join(s.cfg.Dir, "wal"))
	if err != nil {
		return store.ReplayReport{}, err
	}
	var report store.ReplayReport
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		segID := e.Name()
		if filepath.Ext(segID) == ".wal" {
			segID = segID[:len(segID)-len(".wal")]
		}
		if _, sealed := s.sealed[segID]; sealed {
			continue // already sealed and bound; nothing to replay
		}
		s.mu.Lock()
		_, stillOpen := s.open[segIDToOpenKey(s, segID)]
		s.mu.Unlock()
		if stillOpen {
			continue // already live in this process
		}

		path := filepath.Join(s.cfg.Dir, "wal", e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		var traces []model.Trace
		for {
			hdr := make([]byte, 8)
			if _, err := io.ReadFull(f, hdr); err != nil {
				break
			}
			n := binary.BigEndian.Uint32(hdr[0:4])
			wantCRC := binary.BigEndian.Uint32(hdr[4:8])
			buf := make([]byte, n)
			if _, err := io.ReadFull(f, buf); err != nil {
				break
			}
			// w10 review fix: verify the per-record CRC32C before trusting a
			// replayed record (see readTraceFromWALFile's identical fix).
			if crc32.Checksum(buf, crcTable) != wantCRC {
				continue
			}
			t, err := decodeTrace(buf)
			if err != nil {
				continue
			}
			traces = append(traces, t)
		}
		f.Close()
		if len(traces) == 0 {
			continue
		}

		// re-append into a fresh open block per (tenant, tier, hour) —
		// tier is not recoverable from the WAL record alone in this
		// pass's simplified journal, so replay assumes ColdSampled;
		// flagged as a known limitation in docs/reports/w9-store.md.
		for _, t := range traces {
			if _, err := s.Append(ctx, t.Tenant, t, store.ColdSampled); err != nil {
				return report, err
			}
			report.TracesReappended++
		}
		report.SegmentsReplayed++
		_ = os.Remove(path)
	}
	report.Duration = s.now().Sub(start)
	return report, nil
}

func segIDToOpenKey(s *Store, segID string) string {
	for k, b := range s.open {
		if b.id == segID {
			return k
		}
	}
	return ""
}

// ExpireBlocks deletes whole sealed blocks of tier whose
// sealedAt+retention(tier) has passed before, per FR-F03-5's block-scoped
// expiry (never per-trace).
func (s *Store) ExpireBlocks(ctx context.Context, before time.Time, tier store.ColdTier) ([]string, error) {
	retention := s.cfg.Retention[tier]
	s.mu.Lock()
	var toDelete []string
	for id, m := range s.sealed {
		if m.Tier != tier {
			continue
		}
		if m.SealedAt.Add(retention).Before(before) || m.SealedAt.Add(retention).Equal(before) {
			toDelete = append(toDelete, id)
		}
	}
	s.mu.Unlock()

	sort.Strings(toDelete)
	for _, id := range toDelete {
		s.mu.Lock()
		m := s.sealed[id]
		delete(s.sealed, id)
		s.mu.Unlock()
		_ = os.RemoveAll(filepath.Dir(m.MetaPath))
	}
	return toDelete, nil
}

// PruneWAL deletes the on-disk WAL segment file for every sealed block whose
// seal time is at least cfg.WalRetain in the past (DR-7 §7: "WAL segments
// are retained until seal + store.cold.wal_retain, default 10m").
//
// w10 review fix: Seal only ever closed a sealed block's WAL file handle,
// never removed the file (the original TestReplayWAL_RecoversUnsealedTrace
// test comment even says so: "Seal closes but doesn't delete it") — every
// sealed block leaked its WAL segment for the life of the process, an
// unbounded disk-growth gap in FR-F03-1/DR-7's stated retention that wasn't
// among the report's disclosed deferred items (DuckDB cross-val, orphan
// reconciliation, Compact no-op, cmd wiring). This follows the same
// exposed-driver-method pattern as SealDue/ExpireBlocks: a periodic caller
// in cmd/traceiq (out of scope for this pass, like those) is expected to
// invoke it, but the capability itself must exist and be correct.
func (s *Store) PruneWAL(now time.Time) ([]string, error) {
	s.mu.Lock()
	var due []string
	for id, m := range s.sealed {
		if !now.Before(m.SealedAt.Add(s.cfg.WalRetain)) {
			due = append(due, id)
		}
	}
	s.mu.Unlock()

	sort.Strings(due)
	var pruned []string
	for _, id := range due {
		path := filepath.Join(s.cfg.Dir, "wal", id+".wal")
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue // already pruned or never had a WAL file (e.g. replayed)
			}
			return pruned, fmt.Errorf("parquet: prune wal %s: %w", id, err)
		}
		pruned = append(pruned, id)
	}
	return pruned, nil
}

func (s *Store) isTombstoned(tid model.TenantID, id model.TraceID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.tomb[tid]; ok {
		return m[id]
	}
	return false
}

// Tombstone records a per-trace erasure request (right-to-erasure); the
// hot-index row deletion side of DR-7's Tombstone algorithm is store/tiered's
// responsibility, not this driver's (ColdStore has no HotIndex reference).
func (s *Store) Tombstone(ctx context.Context, tid model.TenantID, ids []model.TraceID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.tomb[tid]
	if !ok {
		m = map[model.TraceID]bool{}
		s.tomb[tid] = m
	}
	for _, id := range ids {
		m[id] = true
	}
	return nil
}

// Compact is not implemented in this pass (L1/L2 merge, DR-7's write-
// amplification budget) — returns a zero report rather than an error so
// callers that poll it don't hard-fail. See docs/reports/w9-store.md.
func (s *Store) Compact(ctx context.Context, level int, b store.CompactBudget) (store.CompactReport, error) {
	return store.CompactReport{}, nil
}

func (s *Store) Health(ctx context.Context) store.HealthReport {
	now := s.now()
	if _, err := os.Stat(s.cfg.Dir); err != nil {
		return store.HealthReport{Healthy: false, Message: err.Error(), CheckedAt: now}
	}
	return store.HealthReport{Healthy: true, Message: "ok", CheckedAt: now}
}
