package anomaly

import (
	"context"
	"sort"
	"sync"
	"time"

	"traceiq/internal/model"
)

// globalBucket is Baseline.Bucket's sentinel for the global (all-time,
// season-independent) slot (DR-14 §14.1).
const globalBucket uint8 = 255

// coldState is the four-state cold-start machine of DR-14 §14.3/FR-F05-4.
// It is derived, not stored: Baseline.Warmed/Provisional (already part of
// the exported contract) encode it losslessly as a 2-bit tuple, so no new
// exported field is needed:
//
//	Cold                 -> Warmed=false, Provisional=false
//	GlobalOnly            -> Warmed=false, Provisional=true
//	Warm                  -> Warmed=true,  Provisional=false
//	ProvisionalByDecree   -> Warmed=true,  Provisional=true
type coldState uint8

const (
	stateCold coldState = iota
	stateGlobalOnly
	stateWarm
	stateProvisionalByDecree
)

func classifyState(globalSamples, seasonSamples uint32, firstSeen, now time.Time, cfg BaselineConfig) coldState {
	warmGlobal := globalSamples >= cfg.WarmupSamples
	if !warmGlobal {
		if !firstSeen.IsZero() && now.Sub(firstSeen) > cfg.MaxColdStart {
			return stateProvisionalByDecree
		}
		return stateCold
	}
	if seasonSamples < cfg.WarmupSamplesPerBucket {
		return stateGlobalOnly
	}
	return stateWarm
}

// seasonSlot is one (tenant, service, operation, season bucket)'s streaming
// state. Latency is tracked as three independent P2Estimators, one per
// exposed quantile time-series (the bucket-level P50/P95/P99 fed in by
// Observe), each read back via Quantile(0.5): a robust running center of
// that quantile's recent bucket-to-bucket values. This is a deliberate
// simplification of DR-14 §14.1's "one P2Estimator per season slot,
// 5 markers x 3 quantiles" shape: REDSample only ever hands Observe an
// already-bucket-aggregated {P50,P95,P99} triple (DR-39 §39.3, Res10s), not
// raw per-span durations, so there is no raw sample stream to feed a single
// tri-quantile P² instance in the way the classic algorithm assumes.
// NEEDS_CONTEXT if a byte-exact 288 B/slot layout is required downstream.
type seasonSlot struct {
	p50, p95, p99 *P2Estimator
	errorEWMA     float64
	rpsEWMA       float64
	samples       uint32
	updatedAt     time.Time
}

func newSeasonSlot() *seasonSlot {
	return &seasonSlot{p50: NewP2Estimator(), p95: NewP2Estimator(), p99: NewP2Estimator()}
}

type baselineKey struct {
	tenant    model.TenantID
	service   string
	operation string
}

type baselineEntry struct {
	key       baselineKey
	firstSeen time.Time
	updatedAt time.Time

	seasons [31]*seasonSlot

	// globalDigest is TDigest per DR-14 §14.1 ("used only for the global
	// slot"), fed the bucket P95 stream — the single value the latency
	// detectors actually widen/compare against in Cold/GlobalOnly state.
	globalDigest    *TDigest
	globalErrorEWMA float64
	globalRPSEWMA   float64
	globalSamples   uint32

	dirty map[uint8]bool
}

// memBaselineStore is an in-memory BaselineStore/BaselineReader bounded per
// DR-14 §14.2's cardinality cap. Checkpoint/Load are minimal, correctly-
// shaped stubs: batch 1 does not wire a durable anomaly_baseline table
// (that is store/sqlite's job, out of this package's scope), so
// Checkpoint drains the dirty set without I/O and Load is a no-op.
// NEEDS_CONTEXT: durable persistence wiring.
type memBaselineStore struct {
	mu       sync.Mutex
	cfg      Config
	byTenant map[model.TenantID]map[baselineKey]*baselineEntry
	evicted  int64
}

// NewBaselineStore returns an in-memory BaselineStore/BaselineReader.
func NewBaselineStore(cfg Config) BaselineStore {
	return &memBaselineStore{
		cfg:      cfg,
		byTenant: make(map[model.TenantID]map[baselineKey]*baselineEntry),
	}
}

func hourSlot(t time.Time) uint8 { return uint8(t.UTC().Hour()) }

func weekdaySlot(t time.Time) uint8 {
	wd := int(t.UTC().Weekday()) // Sunday=0..Saturday=6
	wd = (wd + 6) % 7            // Monday=0..Sunday=6
	return uint8(24 + wd)
}

func (s *memBaselineStore) Observe(ctx context.Context, tid model.TenantID, sample model.REDSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenantMap, ok := s.byTenant[tid]
	if !ok {
		tenantMap = make(map[baselineKey]*baselineEntry)
		s.byTenant[tid] = tenantMap
	}
	key := baselineKey{tenant: tid, service: sample.Service, operation: sample.Operation}
	entry, ok := tenantMap[key]
	if !ok {
		if len(tenantMap) >= s.cfg.Baseline.MaxKeys {
			s.evictOne(tenantMap)
		}
		entry = &baselineEntry{
			key:          key,
			firstSeen:    sample.BucketStart,
			globalDigest: NewTDigest(s.cfg.Baseline.EstimatorTDigestCompression),
			dirty:        make(map[uint8]bool),
		}
		tenantMap[key] = entry
	}

	const bucketSeconds = 10.0 // Res10s (DR-39)
	errRate := 0.0
	if sample.Calls > 0 {
		errRate = float64(sample.Errors) / float64(sample.Calls)
	}
	rps := float64(sample.Calls) / bucketSeconds
	alpha := s.cfg.Baseline.EWMAAlpha

	updateSlot := func(idx uint8) {
		slot := entry.seasons[idx]
		if slot == nil {
			slot = newSeasonSlot()
			entry.seasons[idx] = slot
		}
		slot.p50.Add(float64(sample.Q.P50Nanos))
		slot.p95.Add(float64(sample.Q.P95Nanos))
		slot.p99.Add(float64(sample.Q.P99Nanos))
		slot.errorEWMA = alpha*errRate + (1-alpha)*slot.errorEWMA
		slot.rpsEWMA = alpha*rps + (1-alpha)*slot.rpsEWMA
		slot.samples++
		slot.updatedAt = sample.BucketStart
		entry.dirty[idx] = true
	}
	updateSlot(hourSlot(sample.BucketStart))
	updateSlot(weekdaySlot(sample.BucketStart))

	entry.globalDigest.Add(float64(sample.Q.P95Nanos))
	entry.globalErrorEWMA = alpha*errRate + (1-alpha)*entry.globalErrorEWMA
	entry.globalRPSEWMA = alpha*rps + (1-alpha)*entry.globalRPSEWMA
	entry.globalSamples++
	entry.dirty[globalBucket] = true
	entry.updatedAt = sample.BucketStart

	return nil
}

func (s *memBaselineStore) Reader() BaselineReader { return s }

func (s *memBaselineStore) Get(tid model.TenantID, service, operation string, at time.Time) (Baseline, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenantMap, ok := s.byTenant[tid]
	if !ok {
		return Baseline{}, false
	}
	entry, ok := tenantMap[baselineKey{tenant: tid, service: service, operation: operation}]
	if !ok {
		return Baseline{}, false
	}

	idx := hourSlot(at)
	slot := entry.seasons[idx]
	var seasonSamples uint32
	if slot != nil {
		seasonSamples = slot.samples
	}

	state := classifyState(entry.globalSamples, seasonSamples, entry.firstSeen, at, s.cfg.Baseline)

	b := Baseline{
		Service:   service,
		Operation: operation,
		Bucket:    idx,
		UpdatedAt: entry.updatedAt,
	}

	globalQ := func() model.Quantiles {
		v := entry.globalDigest.Quantile(0.95)
		return model.Quantiles{P50Nanos: uint64(entry.globalDigest.Quantile(0.5)), P95Nanos: uint64(v), P99Nanos: uint64(entry.globalDigest.Quantile(0.99))}
	}

	switch state {
	case stateCold:
		b.Samples = entry.globalSamples
		b.Warmed = false
		b.Provisional = false
		b.Q = globalQ()
		b.ErrorEWMA = entry.globalErrorEWMA
		b.RPSEWMA = entry.globalRPSEWMA
	case stateGlobalOnly:
		b.Samples = seasonSamples
		b.Warmed = false
		b.Provisional = true
		b.Q = globalQ()
		b.ErrorEWMA = entry.globalErrorEWMA
		b.RPSEWMA = entry.globalRPSEWMA
	case stateWarm, stateProvisionalByDecree:
		b.Samples = seasonSamples
		b.Warmed = true
		b.Provisional = state == stateProvisionalByDecree
		if slot != nil {
			b.Q = model.Quantiles{
				P50Nanos: uint64(slot.p50.Quantile(0.5)),
				P95Nanos: uint64(slot.p95.Quantile(0.5)),
				P99Nanos: uint64(slot.p99.Quantile(0.5)),
			}
			b.ErrorEWMA = slot.errorEWMA
			b.RPSEWMA = slot.rpsEWMA
		} else {
			b.Q = globalQ()
			b.ErrorEWMA = entry.globalErrorEWMA
			b.RPSEWMA = entry.globalRPSEWMA
		}
	}
	return b, true
}

// evictOne implements DR-14 §14.2's cardinality cap: LRU-by-traffic among
// the oldest-UpdatedAt decile.
func (s *memBaselineStore) evictOne(tenantMap map[baselineKey]*baselineEntry) {
	if len(tenantMap) == 0 {
		return
	}
	entries := make([]*baselineEntry, 0, len(tenantMap))
	for _, e := range tenantMap {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].updatedAt.Before(entries[j].updatedAt) })
	decileSize := len(entries)/10 + 1
	if decileSize > len(entries) {
		decileSize = len(entries)
	}
	victim := entries[0]
	for _, e := range entries[:decileSize] {
		if e.globalRPSEWMA < victim.globalRPSEWMA {
			victim = e
		}
	}
	delete(tenantMap, victim.key)
	s.evicted++
}

func (s *memBaselineStore) Checkpoint(ctx context.Context) (CheckpointReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := 0
	remaining := s.cfg.Baseline.CheckpointMaxRows
	for _, tenantMap := range s.byTenant {
		for _, e := range tenantMap {
			for bucket := range e.dirty {
				if remaining <= 0 {
					break
				}
				delete(e.dirty, bucket)
				rows++
				remaining--
			}
		}
	}
	return CheckpointReport{RowsWritten: rows, At: time.Now()}, nil
}

// Load is a no-op: no durable anomaly_baseline reader is wired in batch 1
// (NEEDS_CONTEXT — store/sqlite side).
func (s *memBaselineStore) Load(ctx context.Context, tid model.TenantID) (int, error) {
	return 0, nil
}

func (s *memBaselineStore) Stats() BaselineStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, dirty, provisional := 0, 0, 0
	now := time.Now()
	for _, tenantMap := range s.byTenant {
		keys += len(tenantMap)
		for _, e := range tenantMap {
			dirty += len(e.dirty)
			st := classifyState(e.globalSamples, 0, e.firstSeen, now, s.cfg.Baseline)
			if st == stateGlobalOnly || st == stateProvisionalByDecree {
				provisional++
			}
		}
	}
	return BaselineStats{Keys: keys, DirtyKeys: dirty, EvictedKeys: s.evicted, ProvisionalKeys: provisional}
}
