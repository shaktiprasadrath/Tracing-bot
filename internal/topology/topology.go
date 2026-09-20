package topology

import (
	"context"
	"io"
	"time"

	"traceiq/internal/model"
)

// Edge is topology's canonical edge record (DR-13, replaces the
// per-callee-operation shape). ID = xxh3(caller "\x00" callee "\x00"
// protocol). Hist is model.LatencyHist so P50/P95/P99 are interpolated at
// read time instead of estimated live per bucket.
type Edge struct {
	ID               string
	Tenant           model.TenantID
	Caller           string // "" for ingress
	Callee           string
	Protocol         string // http | grpc | db | messaging | internal | unknown
	BucketStart      time.Time
	Resolution       model.Resolution
	Calls            uint64
	Errors           uint64
	DurationSumNanos uint64
	Hist             model.LatencyHist // fixed-boundary, mergeable, 16 buckets, 64 bytes
	ExemplarTraceIDs []model.TraceID   // <= 2
	FirstSeen        time.Time
	LastSeen         time.Time
	IsNew            bool
	IsVanished       bool
}

// EdgeOp is the bounded per-callee-operation top-N row backing
// `topology_edge_op` (DR-13): per-edge operation visibility survives at
// bounded cardinality in an hourly table, top
// topology.max_operations_per_edge (default 20) operations by calls, per
// edge per hour. The register gives the SQL DDL but not a Go struct for the
// read-side row; reconstructed 1:1 from the DDL columns.
type EdgeOp struct {
	Tenant          model.TenantID
	EdgeID          string
	CalleeOperation string
	BucketStart     time.Time // 1h buckets only
	Calls           uint64
	Errors          uint64
	P99Nanos        uint64
}

// Direction selects which side of a Neighbors traversal to return (DR-13).
type Direction uint8

const (
	Upstream   Direction = 1
	Downstream Direction = 2
	Both       Direction = 3
)

// ExportFormat selects Graph.Export's serialization (DR-13, closes the
// topology half of D-Y4). The register names the method but never
// enumerates the format set.
//
// TODO(DR-13): under-specified — no closed enum is printed for
// ExportFormat's values; only "json"/"dot"-shaped exports are implied by
// D-Y4's graph-export intent elsewhere in the register.
type ExportFormat string

// Neighborhood is Graph.Neighbors's return shape (DR-13, verbatim).
type Neighborhood struct {
	Root                 string
	Upstream, Downstream []string
	Edges                []Edge
	HopOf                map[string]int
}

// ChangeEvent is the element type of Graph.Changes() (DR-13 §"Changes() is
// lossless for NewEdge"). The register describes NewEdge (durably recorded
// via topology_edge_meta.is_new/first_seen, reconciled every 30s tick) and
// VanishedEdge (channel-only, best-effort, re-derivable from last_seen) as
// the two kinds but never prints a Go struct for the envelope.
//
// TODO(DR-13): under-specified — event Kind enum and payload reconstructed
// minimally from the surrounding prose.
type ChangeEvent struct {
	Kind     string // "NewEdge" | "VanishedEdge"
	Edge     Edge
	Detected time.Time
}

// Snapshot is Graph.Snapshot's return shape (DR-13). The register calls it
// out by name (a warm-start / export source) but never prints its fields.
//
// TODO(DR-13): under-specified — reconstructed minimally as the edge set at
// a point in time; narrow before treating as final.
type Snapshot struct {
	Tenant model.TenantID
	At     time.Time
	Edges  []Edge
}

// Stats is Graph.Stats's return shape (DR-13). Named but not field-defined
// by the register.
//
// TODO(DR-13): under-specified — reconstructed minimally from the package's
// own published memory-budget note (edge count, evictions).
type Stats struct {
	EdgeCount    int
	EdgesEvicted int64
	OpenBuckets  int
}

// Graph is the full topology interface DR-13 restores (what F04 had
// dropped, and what 02, 03 §2, 04 §X6 and F06.TopologyQuery all call).
// Consume satisfies ingest.SpanSink structurally (DR-2's signature rule:
// the method mentions only model/tenant/stdlib types) — this package never
// imports internal/ingest.
type Graph interface {
	Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error

	Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir Direction) (Neighborhood, error)
	Distance(ctx context.Context, tid model.TenantID, a, b string, maxHops int) (int, bool, error)
	Edges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error)
	EdgeOps(ctx context.Context, tid model.TenantID, edgeID string, w model.Window, topN int) ([]EdgeOp, error)
	Snapshot(ctx context.Context, tid model.TenantID) (Snapshot, error)
	Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error // D-Y4

	Changes() <-chan ChangeEvent
	Flush(ctx context.Context) error // shutdown step 04 §X6
	Stats() Stats
}

// EdgeSink is the consumer-declared persistence seam (DR-13): F04 persists
// through EdgeSink and warm-starts through EdgeSource, never by calling
// store.HotIndex directly (CC-1c, PD-15(4)). store/sqlite satisfies both;
// cmd/traceiq injects it. Declared here per DR-2's structural-interface
// rule, so internal/topology never imports internal/store.
type EdgeSink interface {
	WriteEdges(ctx context.Context, tid model.TenantID, edges []Edge) error
}

// EdgeSource is the consumer-declared warm-start read seam (DR-13),
// satisfied by store/sqlite.
type EdgeSource interface {
	LoadEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error)
}
