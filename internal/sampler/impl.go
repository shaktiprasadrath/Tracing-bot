package sampler

import (
	"context"

	"traceiq/internal/model"
)

// Impl is the concrete type combining Manager + DefaultPolicy + PredicateSet
// into the full DR-10 Sampler interface (F02 §4.3's `func New(...) (*Sampler,
// error)`, which names its return type after the *interface* — an interface
// cannot itself be constructed, so this wiring type is what that constructor
// actually returns in practice).
//
// FIXED (w10-review): this type did not exist. Manager alone implements
// Consume/Decisions/Traces/REDSamples/FlushAll/ReplayWAL/Stats but not
// SetInterestPredicate/RemoveInterestPredicate/ListInterestPredicates/
// AdjustFloor (those live on the separately-constructed PredicateSet and
// DefaultPolicy passed into NewManager) — so nothing in the package actually
// satisfied its own canonical `sampler.Sampler` interface. That is judged a
// Blocker for this pass: F06's rca.Engine (DR-11, D-X5) and cmd/traceiq both
// depend on a single object exposing the whole interface, and the package's
// own doc.go states the catalog interface is `sampler.Sampler` — a package
// that cannot produce its own declared interface doesn't yet implement
// itself. Impl closes that gap by delegating predicate/floor calls to the
// same policy/preds instances already wired into the embedded Manager.
type Impl struct {
	*Manager
	policy *DefaultPolicy
	preds  PredicateSet
}

// NewImpl constructs an Impl: a DefaultPolicy and PredicateSet are built
// from cfg, then wired into a Manager the same way newTestManager (this
// package's tests) already does by hand. clock and wal follow DR-31's clock
// injection and DR-9's spill-WAL contracts respectively; pass NewMemWAL() for
// wal until a disk-backed implementation exists (wal.go's own deviation
// note).
func NewImpl(acfg AssemblyConfig, pcfg PolicyConfig, clock model.Clock, wal WAL, nShards int) *Impl {
	policy := NewDefaultPolicy(pcfg, clock)
	preds := NewPredicateSet()
	mgr := NewManager(acfg, clock, wal, policy, policy, preds, nShards)
	return &Impl{Manager: mgr, policy: policy, preds: preds}
}

// SetInterestPredicate implements Sampler (DR-10/DR-11's phase A/B push).
func (im *Impl) SetInterestPredicate(ctx context.Context, tid model.TenantID, p InterestPredicate) (string, error) {
	p.Tenant = tid
	return im.preds.Add(p)
}

// RemoveInterestPredicate implements Sampler (DR-10's binding rename of
// ClearInterestPredicate).
func (im *Impl) RemoveInterestPredicate(ctx context.Context, tid model.TenantID, id string) error {
	return im.preds.Remove(id)
}

// ListInterestPredicates implements Sampler (DR-10, new). The canonical
// PredicateSet interface has no listing method (predicate.go's List is an
// addition beyond it, same reconstruction license as Evicted()), so this
// goes through a type assertion; a PredicateSet implementation that doesn't
// provide List returns an empty result rather than failing the call.
func (im *Impl) ListInterestPredicates(ctx context.Context, tid model.TenantID) ([]InterestPredicate, error) {
	if lister, ok := im.preds.(interface {
		List(model.TenantID) []InterestPredicate
	}); ok {
		return lister.List(tid), nil
	}
	return nil, nil
}

// AdjustFloor implements Sampler (DR-12's cost-control entry point).
func (im *Impl) AdjustFloor(ctx context.Context, tid model.TenantID, floor float64) error {
	im.policy.AdjustFloor(tid, floor)
	return nil
}

// var _ Sampler = (*Impl)(nil) is a compile-time proof that Impl satisfies
// the package's own canonical interface — the assertion this pass's gap
// left nothing in the package to make.
var _ Sampler = (*Impl)(nil)
