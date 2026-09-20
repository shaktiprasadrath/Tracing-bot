package anomaly

import (
	"context"
	"sync"
	"time"

	"traceiq/internal/model"
)

// memDeployIndex is an in-memory DeployIndex (DR-14 §14.6): the single
// owner of the deploy-window abstraction. No durable store is wired in
// batch 1, so Record/ListWindow/Near operate purely over an in-process
// per-tenant slice. NEEDS_CONTEXT: durable persistence wiring.
type memDeployIndex struct {
	mu      sync.Mutex
	cfg     Config
	markers map[model.TenantID][]model.DeployMarker
}

// NewDeployIndex returns an in-memory DeployIndex.
func NewDeployIndex(cfg Config) DeployIndex {
	return &memDeployIndex{cfg: cfg, markers: make(map[model.TenantID][]model.DeployMarker)}
}

func (idx *memDeployIndex) Record(ctx context.Context, tid model.TenantID, m model.DeployMarker) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.markers[tid] = append(idx.markers[tid], m)
	return nil
}

func (idx *memDeployIndex) Near(ctx context.Context, tid model.TenantID, service string, at time.Time, window time.Duration) ([]model.DeployMarker, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	var out []model.DeployMarker
	for _, m := range idx.markers[tid] {
		if m.Service != service {
			continue
		}
		d := at.Sub(m.At)
		if d < 0 {
			d = -d
		}
		if d <= window {
			out = append(out, m)
		}
	}
	return out, nil
}

// PrePostSplit is defined exactly once (DR-14 §14.6): rca (F06) calls this
// same method for its own pre/post correlation rather than owning a private
// implementation.
func (idx *memDeployIndex) PrePostSplit(ctx context.Context, tid model.TenantID, m model.DeployMarker) (pre, post model.Window, err error) {
	w := idx.cfg.DeployMarkers.CorrelationWindow
	settle := idx.cfg.DeployMarkers.Settle
	pre = model.Window{Start: m.At.Add(-w), End: m.At}
	post = model.Window{Start: m.At.Add(settle), End: m.At.Add(settle).Add(w)}
	return pre, post, nil
}

func (idx *memDeployIndex) ListWindow(ctx context.Context, tid model.TenantID, w model.Window) ([]model.DeployMarker, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	var out []model.DeployMarker
	for _, m := range idx.markers[tid] {
		if !m.At.Before(w.Start) && !m.At.After(w.End) {
			out = append(out, m)
		}
	}
	return out, nil
}

var _ DeployIndex = (*memDeployIndex)(nil)

// applyDeployEnrichment is FR-F05-9/DR-14 §14.6's deploy-window enrichment:
// a latency_shift or error_burst event whose window intersects a nearby
// deploy is tagged in place, never dispatched as a separate Kind/event.
func applyDeployEnrichment(ctx context.Context, tid model.TenantID, deploys DeployIndex, cfg Config, ev *model.AnomalyEvent) {
	if deploys == nil || !cfg.DeployMarkers.Enabled {
		return
	}
	markers, err := deploys.Near(ctx, tid, ev.Service, ev.WindowStart, cfg.DeployMarkers.CorrelationWindow)
	if err != nil || len(markers) == 0 {
		return
	}
	for _, m := range markers {
		ev.DeployMarkerIDs = append(ev.DeployMarkerIDs, m.ID)
	}
	ev.Score = clamp01(ev.Score + 0.10)
}
