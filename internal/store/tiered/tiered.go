// Package tiered — see doc.go for the binding source and scope.
package tiered

import (
	"context"
	"fmt"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
)

// Store composes a store.HotIndex and store.ColdStore into a
// store.TieredStore (embedded, so every store.TieredStore method — Append,
// GetTrace, SealAndBind, EmitCostSignal, Signals, SweepRetention — is
// available directly on *Store) and adds the two pieces of driving logic
// doc.go calls out that store.TieredStore's types-only pass left to this
// package: a seal cycle that binds each newly-sealed block into the hot
// index (DR-7), and a cost-signal sample taken off the live hot index
// (DR-12). store/tiered depends only on the store.HotIndex/store.ColdStore
// interfaces (DR-2's adjacency table forbids importing store/sqlite or
// store/parquet directly) — cmd/traceiq constructs the concrete drivers and
// injects them here.
type Store struct {
	*store.TieredStore

	clock model.Clock
}

// New composes hot and cold into a *Store. clock may be nil, in which case
// SampleCostSignal falls back to the wall clock (matching store/parquet and
// store/sqlite's own Clock-optional convention).
func New(hot store.HotIndex, cold store.ColdStore, clock model.Clock) *Store {
	return &Store{
		TieredStore: store.NewTieredStore(hot, cold, clock),
		clock:       clock,
	}
}

// Close releases the hot index's resources (its dedicated writer goroutine
// and DB handle, per store.HotIndex.Close). ColdStore has no equivalent
// long-lived resource to release (DR-7: no Close in its method set).
func (s *Store) Close() error {
	return s.Hot.Close()
}

// Health reports both drivers' health; unhealthy if either is.
func (s *Store) Health(ctx context.Context) store.HealthReport {
	hh := s.Hot.Health(ctx)
	if !hh.Healthy {
		return hh
	}
	return s.Cold.Health(ctx)
}

// DueBlock names one cold block ready to be sealed and bound: the tenant it
// belongs to, and its block/WAL-segment id (store/parquet's documented
// simplification: an open block's own id doubles as its WAL segment name
// for its whole open lifetime, so blockID always resolves unambiguously —
// see docs/reports/w9-store.md). Discovering which blocks are due is the
// concrete cold-store driver's job (e.g. parquet.Store.DueBlocks); tiered
// only drives the seal-then-bind step over interfaces.
type DueBlock struct {
	Tenant  model.TenantID
	BlockID string
}

// RunSealCycle drives DR-7's ordering invariant end to end for every block
// in due, one at a time: store.TieredStore.SealAndBind calls Cold.Seal
// (which writes spans.parquet/traces.parquet/meta.json and commits the
// manifest row) and only then calls Hot.BindColdBlock — so a hot-index row
// is never bound to a manifest that hasn't committed. A failure partway
// through returns the manifests already bound alongside the error; retrying
// with the same (now-shorter) due list is safe because BindColdBlock is
// idempotent per DR-7's crash matrix ("re-runs on startup").
func (s *Store) RunSealCycle(ctx context.Context, due []DueBlock) ([]store.BlockManifest, error) {
	manifests := make([]store.BlockManifest, 0, len(due))
	for _, d := range due {
		m, _, err := s.SealAndBind(ctx, d.Tenant, d.BlockID)
		if err != nil {
			return manifests, fmt.Errorf("tiered: seal cycle: block %s: %w", d.BlockID, err)
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// SampleCostSignal runs one DR-12 cost-controller tick for tenant tid: it
// measures ingest rate over the trailing measureWindow and the hot index's
// on-disk footprint, then feeds both into store.TieredStore.EmitCostSignal
// (the pure proportional-controller step), which also pushes the result onto
// Signals(). Time is read only from s.clock (never time.Now directly, absent
// a clock), matching EmitCostSignal's own contract.
func (s *Store) SampleCostSignal(ctx context.Context, tid model.TenantID, budgetBytesPerSec, currentFloor float64, measureWindow time.Duration) (store.CostSignal, error) {
	if measureWindow <= 0 {
		return store.CostSignal{}, fmt.Errorf("tiered: measureWindow must be positive")
	}
	now := s.now()
	w := model.Window{Start: now.Add(-measureWindow), End: now}
	bytes, err := s.Hot.IngestedBytes(ctx, tid, w)
	if err != nil {
		return store.CostSignal{}, fmt.Errorf("tiered: ingested bytes: %w", err)
	}
	observedBytesPerSec := float64(bytes) / measureWindow.Seconds()

	disk, err := s.Hot.DiskUsage(ctx)
	if err != nil {
		return store.CostSignal{}, fmt.Errorf("tiered: disk usage: %w", err)
	}

	return s.EmitCostSignal(tid, observedBytesPerSec, budgetBytesPerSec, currentFloor, disk.Ratio), nil
}

func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock.Now()
	}
	return time.Now()
}
