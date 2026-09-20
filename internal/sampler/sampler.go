package sampler

import (
	"context"
	"time"

	"github.com/cespare/xxhash/v2"

	"traceiq/internal/model"
)

// --- DR-8: rendezvous-hash sharding. The register's Go block for the ring
// and ShardFor is itself `package sampler` (sharding's routing contract
// lives with the sampler that owns Consume, not in store or a shared
// package), stated once in 01 §3.2 and cited everywhere else.

// MemberID identifies one sampler shard owner (a StatefulSet pod's stable
// identity in Kubernetes, DR-8 §"the resize protocol").
type MemberID string

// RingState distinguishes a stable membership from a drain-in-progress
// resize (DR-8's drain-then-move protocol; ownership of an open trace never
// moves mid-drain).
type RingState uint8

const (
	RingStable   RingState = 1
	RingDraining RingState = 2
)

// Ring is the sharding membership snapshot. Members is sorted and
// immutable; len(Members) == shard count.
type Ring struct {
	Epoch   uint64
	Members []MemberID
	State   RingState
}

// ShardFor is the ONLY routing function in the system (DR-8): rendezvous
// (HRW) hashing, argmax_i xxh3(Members[i] || id[:]). fnv64a(TraceID) mod N
// is deleted. A Kafka custom partitioner computes
// partition = ShardFor(ring, traceID) mod partitions, so ShardFor and the
// partitioner agree on 100% of assignments at equal counts.
//
// DEVIATION (w9-sampler): DR-8 specifies xxh3; this module only has
// github.com/cespare/xxhash/v2 (XXH64) in the dependency graph (already an
// indirect requirement, so no go.mod addition is needed). XXH64 is used as
// a drop-in stand-in — it preserves the rendezvous-hash property (uniform,
// independent per-member scores) that FR-F02-1/AC-F02-1 actually test, but
// is NOT bit-identical to a real xxh3 implementation. Swap in a true xxh3
// package before relying on cross-process/cross-language hash agreement
// (e.g. the Kafka custom partitioner in §4.2's "Bus/transport" note).
func ShardFor(r Ring, id model.TraceID) int {
	best := -1
	var bestScore uint64
	for i, m := range r.Members {
		h := xxhash.New()
		_, _ = h.Write([]byte(m))
		_, _ = h.Write(id[:])
		score := h.Sum64()
		if best == -1 || score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// Trace is the shard-local assembly state for one TraceID (F02 §4.2, missing
// from the types-only pass — added per this file's reconstruction license).
// model.KeepReason (01 §4.1) is stamped at finalize; there is no
// package-local Reason type (DR-4).
type Trace struct {
	TraceID       model.TraceID
	Tenant        model.TenantID
	Spans         []model.Span
	FirstSeen     time.Time
	LastSeen      time.Time
	ByteSize      int
	Truncated     bool   // set by the router's span-cap rule (DR-9)
	ShardEpoch    uint64 // stamped at open, for RED dedupe (DR-8)
	PathSignature uint64 // computed at finalize (DR-10 §4.2's ordered edge list)

	// seenSpanIDs implements DR-9's "RED before every discard" hole (c):
	// partialTrace's map[model.SpanID]struct{}, ~8B/span. A SpanID already in
	// this set is a duplicate delivery (F01's at-least-once semantics) and
	// MUST be dropped before both assembly and RED (FR-F02-5(c)); added in
	// w10-review as a real gap (this map did not exist at all — every
	// duplicate span was being double-counted into both the trace buffer and
	// RED).
	seenSpanIDs map[model.SpanID]struct{}
}

// --- DR-10: the canonical sampler interface set.

// Sampler is the canonical sampler interface (DR-10, binding renames from
// F02: ClearInterestPredicate -> RemoveInterestPredicate, Snapshot ->
// Stats, BaselineLookup -> BaselineSource).
type Sampler interface {
	// Consume satisfies ingest.SpanSink structurally (DR-2).
	Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error

	SetInterestPredicate(ctx context.Context, tid model.TenantID, p InterestPredicate) (string, error)
	RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error
	ListInterestPredicates(ctx context.Context, tid model.TenantID) ([]InterestPredicate, error)

	AdjustFloor(ctx context.Context, tid model.TenantID, floor float64) error

	Decisions() <-chan Decision         // cap 512, blocking send
	Traces() <-chan model.Trace         // cap 512, blocking send
	REDSamples() <-chan model.REDSample // cap 8192, drop-oldest + counter

	FlushAll(ctx context.Context) error // shutdown step 04 §X4
	ReplayWAL(ctx context.Context) (ReplayReport, error)
	Stats() Stats
}

// Stats is Sampler.Stats's return shape (DR-10, verbatim).
type Stats struct {
	Shards           []ShardStats
	BytesInFlight    int64
	OpenTraces       int64
	KeepRate60s      float64
	KeepRateByReason map[model.KeepReason]float64
	PredicatesActive int
	TracesLostTotal  int64
	RebalanceSplits  int64
	RingEpoch        uint64
}

// ShardStats is one shard's row inside Stats.Shards (DR-10, verbatim).
type ShardStats struct {
	ShardID       int
	OpenTraces    int
	BytesInFlight int64
	WALSegment    string
}

// PolicyEvaluator is the keep/drop decision function (DR-10, verbatim).
type PolicyEvaluator interface {
	Evaluate(ctx context.Context, tid model.TenantID, t *model.Trace, b KeyBaseline, m InterestMatch, g Governor) Decision
}

// BaselineSource is the consumer-declared read interface satisfied by
// store/sqlite. It is NEVER called from the decision path — baselines are
// read from an in-memory BaselineSnapshot instead (DR-10 §"baselines are
// never read from SQLite on the decision path").
type BaselineSource interface {
	Quantile(ctx context.Context, tid model.TenantID, service, operation string, q float64) (uint64, bool, error)
	LoadSnapshot(ctx context.Context, tid model.TenantID) (BaselineSnapshot, error)
	PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error)
	CallRate(ctx context.Context, tid model.TenantID, service string) (float64, error)
}

// PredicateSet is the bounded interest-predicate matcher (DR-10/DR-11,
// verbatim).
type PredicateSet interface {
	Match(t *model.Trace) InterestMatch
	Add(p InterestPredicate) (string, error)
	Remove(id string) error
	ExpireDue(now time.Time) int
	Narrow(level int) int
}

// InterestMatch is PredicateSet.Match's return shape (DR-10, verbatim).
type InterestMatch struct {
	Matched     bool
	PredicateID string
	Scope       PredicateScope
}

// ServiceOp keys a baseline lookup (DR-10, verbatim).
type ServiceOp struct {
	Service, Operation string
}

// KeyBaseline is one (service, operation) baseline entry (DR-10, verbatim).
type KeyBaseline struct {
	P95Nanos, P99Nanos uint64
	Warmed             bool
	Samples            uint32
}

// BaselineSnapshot is the immutable, atomically-swapped baseline table
// built at startup from red_rollup via LoadSnapshot and refreshed on
// sampler.baseline.refresh_interval (DR-10, verbatim). Stated staleness
// tolerance <= 120s.
type BaselineSnapshot struct {
	At    time.Time
	ByKey map[ServiceOp]KeyBaseline
}

// Decision is the outcome of PolicyEvaluator.Evaluate, delivered on
// Sampler.Decisions() (DR-10's six keep classes; DR-11's
// MatchedPredicateID/Hits/SecondaryReasons behavior). The register
// describes Decision's fields in prose (Reason, MatchedPredicateID,
// SecondaryReasons) but never prints a struct literal for it.
//
// TODO(DR-10/DR-11): under-specified — reconstructed minimally from
// "Decision.Reason follows DR-10's precedence... Decision.MatchedPredicateID
// is set... Decision.SecondaryReasons (a uint16 bitmask of
// model.KeepReason) records every other class that fired."
type Decision struct {
	Tenant  model.TenantID
	TraceID model.TraceID
	// Keep fills a gap left by the types-only pass: F02 §4.2's Decision
	// prints an explicit Keep bool and Sampler.Traces()'s doc comment
	// ("Keep == true only") presupposes it, but the committed struct above
	// never declared the field. Added here per this file's own
	// reconstruction license rather than left implicit in Reason, since
	// KeepTruncated is keep=true (truncated body, still kept) while
	// KeepDropped/KeepShed are keep=false — that split is not a pure
	// function of Reason's numeric value alone without a lookup table.
	Keep               bool
	Reason             model.KeepReason
	MatchedPredicateID string
	SecondaryReasons   uint16 // bitmask of model.KeepReason
	DecidedAt          time.Time
}

// Governor is PolicyEvaluator.Evaluate's rate-limiting collaborator: it
// carries the post-hoc, all-classes hard cap (sampler.policy.max_keep_rate)
// and the shed order (Probabilistic -> Floor -> Interest(Recurrence) ->
// Rare -> Slow; Error is never shed). The register names the parameter but
// never prints its interface.
//
// TODO(DR-10): under-specified — reconstructed minimally from "max_keep_rate
// is a hard cap in Policy.Evaluate, applied after all six classes... shed in
// this fixed order and no other."
type Governor interface {
	// Allow reports whether a keep of the given class is still permitted
	// under the rolling-60s max_keep_rate cap; a false result means the
	// caller must shed per the fixed order.
	Allow(class model.KeepReason) bool

	// CurrentFloor fills a second gap: §4.4's Evaluate pseudocode reads
	// governor.CurrentFloor(tid) for the deterministic probabilistic class
	// (FR-F02-4's AdjustFloor-overridden healthy_sample_rate), but the
	// register's Governor block only ever named Allow. Added per this
	// file's reconstruction license.
	CurrentFloor(tid model.TenantID) float64
}

// ReplayReport is Sampler.ReplayWAL's return shape (DR-9's spill WAL:
// "On start, sampler.ReplayWAL re-assembles every undecided trace before
// receivers bind"). The register never prints a struct for the report.
//
// TODO(DR-9): under-specified — reconstructed minimally; narrow against
// AC-F02-11's assertions (traces replayed, traceiq_sampler_traces_lost_total)
// before treating as final.
type ReplayReport struct {
	SegmentsReplayed int
	TracesReplayed   int
	TracesLost       int64
	Duration         time.Duration
}

// TimerWheel is the normative finalize mechanism (DR-9 §"finalize"):
// FinalizeTick's full scan is deleted in favor of a hashed timer wheel (256
// slots x 250ms = 64s span, two levels to cover hard_timeout); it is the
// only periodic work on the shard goroutine. The register names it by
// exported identifier ("sampler.TimerWheel is normative") but gives no
// field list — construction and scheduling are business logic, out of
// scope for this types-only pass.
//
// TODO(DR-9): under-specified — no Go block given; minimal placeholder.
type TimerWheel struct {
	Slots  int           // 256
	Tick   time.Duration // 250ms
	Levels int           // 2, to cover hard_timeout
}

// --- DR-11: interest predicate, two phases.

// PredicateScope distinguishes phase A (this investigation) from phase B
// (the next occurrence) interest (DR-11, verbatim).
type PredicateScope uint8

const (
	ScopeInvestigation PredicateScope = 1 // phase A
	ScopeRecurrence    PredicateScope = 2 // phase B
)

// InterestPredicate is the exact struct DR-11 prints, verbatim.
type InterestPredicate struct {
	ID          string
	Tenant      model.TenantID
	Scope       PredicateScope
	Source      string // "rca:<investigationID>" | "user:<subjectID>"
	Services    []string
	Operations  []string
	AttrEquals  map[string]string
	ErrorSigIDs []string
	PathSigs    []uint64
	TraceIDs    map[model.TraceID]struct{} // a SET, never a slice
	MinDuration time.Duration
	ErrorsOnly  bool
	NarrowLevel uint8 // 0 = as pushed; 1..4 progressively narrowed
	Hits        uint64
	CreatedAt   time.Time
	ExpiresAt   time.Time
}
