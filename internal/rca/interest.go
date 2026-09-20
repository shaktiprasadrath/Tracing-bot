package rca

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// InterestPredicate mirrors sampler.InterestPredicate (DR-11, verbatim field
// list) but is declared locally in rca rather than imported from package
// sampler. rca "PRODUCES predicates the sampler consumes" (this wave's
// brief) and DR-2's adjacency table does not put sampler on rca's allowed
// import list in the reverse direction needed here: a parameter of a
// sampler-defined struct type can only structurally satisfy a consumer
// interface if the consumer imports that package, which is exactly the
// import rca must not take. Consumer-declared interfaces elsewhere in this
// file (TopologyReader) get away with primitives-only signatures; a
// multi-field DTO cannot. The resolution used here: rca owns its own
// InterestPredicate/InterestSink pair; cmd/traceiq (out of this wave's
// scope) adapts between the two one-for-one when it wires sampler.Sampler
// into an rca.InterestSink. See docs/reports/w11-rca-rules.md.
type PredicateScope uint8

const (
	ScopeInvestigation PredicateScope = 1 // phase A: data for THIS investigation's next step (FR-F06-12a)
	ScopeRecurrence    PredicateScope = 2 // phase B: data for the NEXT occurrence (FR-F06-12b)
)

// InterestPredicate is DR-11's struct, field-for-field, redeclared in rca
// per the doc comment above.
type InterestPredicate struct {
	ID          string
	Tenant      model.TenantID
	Scope       PredicateScope
	Source      string // "rca:<investigationID>"
	Services    []string
	Operations  []string
	AttrEquals  map[string]string
	ErrorSigIDs []string
	PathSigs    []uint64
	TraceIDs    map[model.TraceID]struct{}
	MinDuration time.Duration
	ErrorsOnly  bool
	NarrowLevel uint8
	Hits        uint64
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// InterestSink is rca's consumer-declared interface for pushing/removing
// predicates (DR-11's SetInterestPredicate/RemoveInterestPredicate,
// signature-compatible with sampler.Sampler's own method set once the
// InterestPredicate types are unified by an adapter). A nil InterestSink is
// valid — Engine.Investigate treats predicate push/remove as best-effort and
// skips it — so tests and early wiring don't need a fake for it.
type InterestSink interface {
	SetInterestPredicate(ctx context.Context, tid model.TenantID, p InterestPredicate) (string, error)
	RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error
}

// scopePredicate builds the FR-F06-12a phase-A predicate, pushed at
// Contextualize before the first Hypothesize step.
func scopePredicate(tid model.TenantID, inv model.Investigation, inc model.Incident, now time.Time, ttl time.Duration) InterestPredicate {
	services := append([]string{inc.EpicenterService}, inc.BlastRadius...)
	services = dedupeTruncate(services, 32)
	return InterestPredicate{
		Tenant:     tid,
		Scope:      ScopeInvestigation,
		Source:     "rca:" + inv.ID,
		Services:   services,
		ErrorsOnly: false,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}
}

// recurrencePredicate builds the FR-F06-12b phase-B predicate, pushed only
// when Status == Concluded && Confidence >= rca.confidence_threshold.
// Scoped by error signatures / path signatures only, never by service alone
// (DR-11: "a service-scoped 24h predicate is a store-everything switch").
func recurrencePredicate(tid model.TenantID, inv model.Investigation, errSigs []string, pathSigs []uint64, now time.Time, ttl time.Duration) InterestPredicate {
	return InterestPredicate{
		Tenant:      tid,
		Scope:       ScopeRecurrence,
		Source:      "rca:" + inv.ID,
		ErrorSigIDs: errSigs,
		PathSigs:    pathSigs,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
}

func dedupeTruncate(in []string, max int) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}

const (
	defaultScopeTTL      = 30 * time.Minute
	defaultRecurrenceTTL = 24 * time.Hour
)
