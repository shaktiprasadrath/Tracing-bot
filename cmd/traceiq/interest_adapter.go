package main

import (
	"context"

	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/sampler"
)

// samplerInterestSink adapts a real sampler.Sampler into rca.InterestSink
// (DR-11/D-X5). rca deliberately cannot import internal/sampler (DR-2's
// adjacency table; see internal/rca/interest.go's doc comment: "cmd/traceiq
// ... adapts between the two one-for-one when it wires sampler.Sampler into
// an rca.InterestSink"). This is that adapter: cmd/traceiq is on the import
// path of both internal/rca and internal/sampler, so it is the only package
// allowed to mention both InterestPredicate types in one file.
//
// The two structs (rca.InterestPredicate, sampler.InterestPredicate) are
// declared field-for-field identical on purpose (both sides say so in their
// own doc comments), so this adapter is a pure one-for-one copy, never a
// re-derivation — if the two struct shapes ever drift, this function is
// exactly where that would need to be revisited.
type samplerInterestSink struct {
	s sampler.Sampler
}

// newSamplerInterestSink constructs the adapter. s is the running sampler
// instance (cmd/traceiq's System.samp in production) that should learn what
// the RCA engine is investigating.
func newSamplerInterestSink(s sampler.Sampler) rca.InterestSink {
	return &samplerInterestSink{s: s}
}

// SetInterestPredicate implements rca.InterestSink by translating rca's
// locally-declared InterestPredicate into sampler's and delegating to the
// real sampler.Sampler.
func (a *samplerInterestSink) SetInterestPredicate(ctx context.Context, tid model.TenantID, p rca.InterestPredicate) (string, error) {
	return a.s.SetInterestPredicate(ctx, tid, toSamplerInterestPredicate(p))
}

// RemoveInterestPredicate implements rca.InterestSink by delegating directly
// -- the ID is an opaque string on both sides, no translation needed.
func (a *samplerInterestSink) RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error {
	return a.s.RemoveInterestPredicate(ctx, tid, id)
}

// toSamplerInterestPredicate is the one-for-one field mapping between rca's
// and sampler's independently-declared, structurally-identical
// InterestPredicate types (DR-11, verbatim on both sides). ID is left for
// the sampler to assign (mirrors rca.scopePredicate/recurrencePredicate,
// which also leave ID unset and rely on the sink to return the real one --
// see rca/interest.go's InterestSink doc comment).
func toSamplerInterestPredicate(p rca.InterestPredicate) sampler.InterestPredicate {
	return sampler.InterestPredicate{
		Tenant:      p.Tenant,
		Scope:       sampler.PredicateScope(p.Scope),
		Source:      p.Source,
		Services:    p.Services,
		Operations:  p.Operations,
		AttrEquals:  p.AttrEquals,
		ErrorSigIDs: p.ErrorSigIDs,
		PathSigs:    p.PathSigs,
		TraceIDs:    p.TraceIDs,
		MinDuration: p.MinDuration,
		ErrorsOnly:  p.ErrorsOnly,
		NarrowLevel: p.NarrowLevel,
		Hits:        p.Hits,
		CreatedAt:   p.CreatedAt,
		ExpiresAt:   p.ExpiresAt,
	}
}

var _ rca.InterestSink = (*samplerInterestSink)(nil)
