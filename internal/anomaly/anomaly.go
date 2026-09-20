package anomaly

import (
	"context"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/topology"
)

// Kind is the closed set of anomaly detector kinds (DR-14 §14.1).
// deploy_regression is NOT a sixth kind — DR-14 §14.6 demotes it to an
// Event enrichment instead.
type Kind uint8

const (
	KindLatencyShift      Kind = 1
	KindErrorBurst        Kind = 2
	KindNewErrorSignature Kind = 3
	KindThroughputDrop    Kind = 4
	KindTopologyChange    Kind = 5
)

// Detector is DR-14 §14.1, verbatim. Evaluate is pure with respect to
// (in, clock): no I/O, no store call, so the eval harness (DR-36) can drive
// it deterministically.
type Detector interface {
	Kind() Kind
	Evaluate(ctx context.Context, tid model.TenantID, in EvalInput) ([]model.AnomalyEvent, error)
}

// EvalInput is DR-14 §14.1, verbatim.
type EvalInput struct {
	Now       time.Time
	Window    model.Window
	Samples   []model.REDSample // Res10s buckets only (DR-39)
	ErrorSigs []store.ErrorSignature
	Baselines BaselineReader
	Topology  TopologyReader // consumer-declared; topology.LiveGraph satisfies it
	Deploys   DeployIndex
}

// TopologyReader is DR-14 §14.1, verbatim: consumer-declared per DR-2's
// signature rule (method signatures reference only model/stdlib), satisfied
// structurally by topology.LiveGraph without anomaly importing topology.
type TopologyReader interface {
	Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir uint8) ([]string, error)
	Has(ctx context.Context, tid model.TenantID, service string) bool
}

// BaselineReader is DR-14 §14.1, verbatim.
type BaselineReader interface {
	Get(tid model.TenantID, service, operation string, at time.Time) (Baseline, bool)
}

// Baseline is DR-14 §14.1, verbatim.
type Baseline struct {
	Service, Operation string
	Bucket             uint8 // 0..23 hour-of-day; 24..30 weekday slots; 255 global
	Q                  model.Quantiles
	ErrorEWMA          float64
	RPSEWMA            float64
	Samples            uint32
	Warmed             bool
	Provisional        bool // warmed by decree at max_cold_start; stamped onto every derived event
	UpdatedAt          time.Time
}

// BaselineStore is DR-14 §14.1, verbatim.
type BaselineStore interface {
	Observe(ctx context.Context, tid model.TenantID, s model.REDSample) error
	Reader() BaselineReader
	Checkpoint(ctx context.Context) (CheckpointReport, error)  // incremental, dirty keys only
	Load(ctx context.Context, tid model.TenantID) (int, error) // warm start from anomaly_baseline
	Stats() BaselineStats
}

// CheckpointReport is BaselineStore.Checkpoint's return type. The register
// never gives an explicit field list.
// TODO(DR-14): under-specified.
type CheckpointReport struct {
	RowsWritten int
	At          time.Time
}

// BaselineStats is BaselineStore.Stats's return type. The register never
// gives an explicit field list.
// TODO(DR-14): under-specified.
type BaselineStats struct {
	Keys            int
	DirtyKeys       int
	EvictedKeys     int64
	ProvisionalKeys int
}

// QuantileEstimator is DR-14 §14.1, verbatim: the seam between P2 (default)
// and t-digest (global slot only).
type QuantileEstimator interface {
	Add(v float64)
	Quantile(q float64) float64
	Merge(other QuantileEstimator) error
	SizeBytes() int
	MarshalBinary() ([]byte, error)
	UnmarshalBinary([]byte) error
}

// P2Estimator is the P² algorithm implementation of QuantileEstimator: three
// tracked quantiles (0.50/0.95/0.99), 5 markers each, fixed 240 B, zero
// allocation after construction (DR-14 §14.1 prose). The register gives no
// explicit field list.
//
// TODO(DR-14): under-specified — the register's memory table (§14.2) implies
// the exported Markers[3][5]{Position,Height} shape alone carries the full
// per-quantile marker state, but the classic P² recurrence also needs each
// marker's floating desired-position (np) and a 5-sample init buffer before
// the recurrence starts; neither has room in the printed shape. Both are
// added here as unexported bookkeeping (quantile.go) rather than resolved by
// guessing a different exported shape, so the printed struct is preserved
// verbatim.
type P2Estimator struct {
	Markers [3][5]struct{ Position, Height float64 }

	np    [3][5]float64
	count [3]int
	init  [3][]float64
}

// TDigest is the t-digest implementation of QuantileEstimator, used only for
// the global slot (bucket 255) and red_rollup_1h.digest (DR-14 §14.1 prose:
// compression 100, <= 512 B serialized). The register gives no explicit
// field list.
//
// TODO(DR-14): under-specified — reconstructed as a bounded sorted-value
// digest (quantile.go) rather than the centroid-based t-digest structure,
// which the register never prints a field list for either. Compression
// still bounds the retained/merged size.
type TDigest struct {
	Compression int

	values []float64
}

// SeasonalBaseline is 02 §2's SeasonalTDigest, renamed (DR-14 §14.1 prose):
// holds 31 P2Estimator triples plus one TDigest.
// TODO(DR-14): under-specified.
type SeasonalBaseline struct {
	Slots  [31][3]P2Estimator
	Global TDigest
}

// Grouper is DR-14 §14.1, verbatim.
type Grouper interface {
	Add(ctx context.Context, tid model.TenantID, e model.AnomalyEvent) (model.Incident, bool, error)
	ActiveIncidents(ctx context.Context, tid model.TenantID) ([]model.Incident, error)
	Suppress(ctx context.Context, tid model.TenantID, id, reason string, ttl time.Duration) error
	Tick(ctx context.Context, now time.Time) ([]model.Incident, error)
	Stats() GrouperStats
}

// GrouperStats is DR-14 §14.1, verbatim.
type GrouperStats struct {
	OpenIncidents         int
	LastTickAt            time.Time // read by the deadman (DR-21)
	NeighborCacheHitRatio float64
}

// DeployIndex is DR-14 §14.6, verbatim: the one owner of the deploy-window
// abstraction. F05's EvalDeployCorrelation and F06's private pre/post split
// are both deleted; both call this.
type DeployIndex interface {
	Near(ctx context.Context, tid model.TenantID, service string, at time.Time, window time.Duration) ([]model.DeployMarker, error)
	PrePostSplit(ctx context.Context, tid model.TenantID, m model.DeployMarker) (pre, post model.Window, err error)
	Record(ctx context.Context, tid model.TenantID, m model.DeployMarker) error
	ListWindow(ctx context.Context, tid model.TenantID, w model.Window) ([]model.DeployMarker, error)
}

// EdgeMetaReader is DR-14 §14.7, verbatim: satisfied by store/sqlite over
// topology_edge_meta(is_new = 1, first_seen > ?).
type EdgeMetaReader interface {
	NewEdgesSince(ctx context.Context, tid model.TenantID, since time.Time) ([]topology.Edge, error)
}
