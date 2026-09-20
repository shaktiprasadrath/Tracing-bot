package model

import "time"

// Resolution is the RED/topology rollup cadence. DR-13's topology.Edge block
// and DR-6's HotIndex.CascadeRED also print a bare, unqualified `Resolution`
// with the identical three constants; earlier passes read that as license
// for each of topology/store to keep a separate package-local copy (see
// docs/reports/w2-scaffold-a.md). DR-39 ("One RED record, one quantile set")
// is the decision that actually owns this type — it prints `package model`
// explicitly and its whole point is collapsing exactly this kind of
// triplication (model.REDSample absorbs sampler.REDSample/anomaly.REDSample/
// store.SpanRollup/etc. the same way). topology.Edge.Resolution and
// store.HotIndex.CascadeRED's parameter both reference this type directly;
// the topology- and store-local copies were deleted.
type Resolution uint8

const (
	Res10s Resolution = 1
	Res5m  Resolution = 2
	Res1h  Resolution = 3
)

// Quantiles is the one stored quantile set, {p50, p95, p99, max} (DR-4, DR-39
// §39.2 — p90 is deleted everywhere).
type Quantiles struct {
	P50Nanos, P95Nanos, P99Nanos, MaxNanos uint64
}

// LatencyHist is a fixed-boundary, mergeable-by-addition latency histogram:
// 16 log-spaced boundaries from 1 ms to 32 s, 64 bytes, quantiles
// interpolated at read time (DR-13, DR-39 §39.1).
type LatencyHist [16]uint32

// REDSample is the only RED type in the system (DR-39 §39.1). It replaces
// sampler.REDSample, anomaly.REDSample, store.SpanRollup, store.REDResult,
// store.REDBucket and topology.EdgeRED.
type REDSample struct {
	Tenant           TenantID
	Service          string
	Operation        string
	BucketStart      time.Time
	Resolution       Resolution
	Calls            uint64
	Errors           uint64
	DurationSumNanos uint64
	Hist             LatencyHist // mergeable by addition
	Q                Quantiles   // interpolated from Hist at READ time
	ExemplarTraceIDs []TraceID   // <= 4, first-wins reservoir, ties by lowest TraceID (DR-38 §38.2)
	TraceID          TraceID     // dedupe key component (DR-8)
	ShardEpoch       uint64      // dedupe key component (DR-8)
	KeptCount        uint32
}
