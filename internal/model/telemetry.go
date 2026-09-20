package model

import "time"

// TraceID and SpanID are OTLP-shaped fixed-size identifiers (01 §4.1).
type TraceID [16]byte
type SpanID [8]byte

type SpanKind uint8 // 0 Unspecified, 1 Internal, 2 Server, 3 Client, 4 Producer, 5 Consumer
const (
	SpanKindUnspecified SpanKind = 0
	SpanKindInternal    SpanKind = 1
	SpanKindServer      SpanKind = 2
	SpanKindClient      SpanKind = 3
	SpanKindProducer    SpanKind = 4
	SpanKindConsumer    SpanKind = 5
)

type StatusCode uint8 // 0 Unset, 1 Ok, 2 Error
const (
	StatusUnset StatusCode = 0
	StatusOk    StatusCode = 1
	StatusError StatusCode = 2
)

// SourceFormat records which receiver decoded the span (01 §4.1; carried
// per DR-4's "fields feature docs dropped" list).
type SourceFormat uint8

const (
	SourceOTLP         SourceFormat = 0
	SourceJaegerProto  SourceFormat = 1
	SourceJaegerThrift SourceFormat = 2
	SourceZipkinV2     SourceFormat = 3
)

type AttrKind uint8 // 0 Str, 1 Bool, 2 Int, 3 Float, 4 Bytes, 5 Slice, 6 Map
const (
	AttrStr   AttrKind = 0
	AttrBool  AttrKind = 1
	AttrInt   AttrKind = 2
	AttrFloat AttrKind = 3
	AttrBytes AttrKind = 4
	AttrSlice AttrKind = 5
	AttrMapK  AttrKind = 6
)

type AttrValue struct {
	Kind  AttrKind
	Str   string
	Num   int64 // Int, and Bool as 0/1
	Float float64
	Bytes []byte
	List  []AttrValue
	Map   map[string]AttrValue
}

type AttrMap map[string]AttrValue

// KV is the allocation-free, sorted view of a span's attributes built by the
// ingest hot path (DR-4's attribute representation decision). Key is
// interned via ingest.KeyInterner.
type KV struct {
	Key string
	Val AttrValue
}

type Resource struct {
	ID             string // xxh3 of canonical attrs; interned, shared by pointer
	ServiceName    string // service.name
	ServiceVersion string // service.version
	Namespace      string // service.namespace
	Env            string // deployment.environment.name
	Attrs          AttrMap
}

type Scope struct {
	Name    string
	Version string
	Attrs   AttrMap
}

type SpanEvent struct {
	Name         string
	TimeUnixNano uint64
	Attrs        AttrMap
}

type SpanLink struct {
	TraceID TraceID
	SpanID  SpanID
	Attrs   AttrMap
}

type Status struct {
	Code    StatusCode
	Message string
}

// Span is the canonical telemetry unit (01 §4.1, amended by DR-4). Tenant is
// model.TenantID rather than 01's bare string, matching DR-4's introduction
// of TenantID as "the tenancy parameter type used in every signature" and
// DR-5's propagation rule — see docs/reports/scaffold-report.md for the note
// on this deviation from 01's literal field type.
type Span struct {
	TraceID       TraceID
	SpanID        SpanID
	ParentSpanID  SpanID // zero value = root
	TraceState    string
	Flags         uint32
	Name          string // operation
	Kind          SpanKind
	StartUnixNano uint64
	EndUnixNano   uint64
	Status        Status
	Attrs         AttrMap
	Resource      *Resource
	Scope         *Scope
	Events        []SpanEvent
	Links         []SpanLink

	DroppedAttrsCount  uint32
	DroppedEventsCount uint32
	DroppedLinksCount  uint32

	Tenant           TenantID
	SourceFormat     SourceFormat
	ReceivedUnixNano uint64
	SizeBytes        uint32 // post-normalization, used by limiter and memory accounting

	Truncated bool // DR-9 §14: span survived only via router-side RED extraction
}

// Service returns Resource.ServiceName.
func (s *Span) Service() string { panic("not implemented") }

// DurationNanos returns EndUnixNano - StartUnixNano.
func (s *Span) DurationNanos() uint64 { panic("not implemented") }

// IsError reports Status.Code == StatusError or an "error.type" attribute.
func (s *Span) IsError() bool { panic("not implemented") }

// IsRoot reports whether ParentSpanID is the zero value.
func (s *Span) IsRoot() bool { panic("not implemented") }

// AttrSorted returns an allocation-free, sorted view of Attrs for the
// limiter and the indexer (DR-4's attribute representation decision). The
// backing slice is drawn from a sync.Pool by the real implementation.
func (s *Span) AttrSorted() []KV { panic("not implemented") }

type Trace struct {
	TraceID       TraceID
	Tenant        TenantID
	Spans         []Span
	RootSpanID    SpanID
	RootService   string
	RootOperation string
	StartUnixNano uint64
	EndUnixNano   uint64
	DurationNanos uint64
	SpanCount     int
	ErrorCount    int
	Services      []string // sorted, deduped
	PathSignature uint64   // xxh3 of ordered (service, operation) edge list (DR-10 §10.4)
	Complete      bool     // root observed and no dangling parents
	Truncated     bool     // hit max_spans_per_trace
	SizeBytes     uint64
	AssembledAt   uint64
}

type StorageTier uint8

const (
	TierDrop   StorageTier = 0 // nothing persisted
	TierRollup StorageTier = 1 // RED rollup only
	TierIndex  StorageTier = 2 // hot-index rows, no Parquet body
	TierFull   StorageTier = 3 // hot index + full Parquet block
)

// Batch is the ingest unit shared by every receiver and by sampler.Consume
// (DR-4, new).
type Batch struct {
	Tenant           TenantID
	Spans            []Span
	SourceFormat     SourceFormat
	ReceivedUnixNano uint64
	SourceAddr       string
	SizeBytes        uint32
}

// ServiceMeta carries service ownership/SLO metadata (DR-4, new; DR-38
// "service metadata in memory" promise).
type ServiceMeta struct {
	Tenant          TenantID
	Service         string
	Tier            uint8 // 0 unknown; 1 most critical. Display/grouping ONLY — never a paging input (DR-21)
	Owners          []string
	SLOTargetMillis uint32
	Escalation      string
	Source          string // "resource_attribute" | "tenant_policy" | "api"
}

// DeployMarker records a deploy event correlated against anomalies (DR-4,
// new; consumed by anomaly.DeployIndex, DR-14 §14.6).
type DeployMarker struct {
	ID              string
	Tenant          TenantID
	Service         string
	Version         string
	PreviousVersion string
	Source          string
	At              time.Time
	RolloutFraction float64 // [P2], unused in v1 (DR-14 §14.6)
}
