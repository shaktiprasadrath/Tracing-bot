package api

import (
	"context"

	"traceiq/internal/model"
	"traceiq/internal/topology"
)

// anomalyTopologyReader implements anomaly.TopologyReader in terms of
// topology.LiveGraph's real methods. anomaly.TopologyReader (internal/
// anomaly/anomaly.go) and topology.Graph.Neighbors (internal/topology/
// topology.go, DR-13) were declared independently — one per DR-2's
// consumer-declared-interface rule, the other as topology's own published
// contract — and disagree on shape: TopologyReader.Neighbors takes/returns
// (dir uint8) -> []string, while Graph.Neighbors takes/returns
// (dir topology.Direction) -> topology.Neighborhood. Neither package may be
// changed to fix this (topology and anomaly are separately reviewed
// packages, waves 10 and 12); this adapter is the seam api owns instead,
// per api being "the sole package permitted to import every feature
// package" (api.go's Deps doc). See docs/reports/w15-api-web.md for the
// bug this closes.
//
// topologyNeighborHas is a narrow, consumer-declared (DR-2-style) view of
// what the adapter needs, satisfied structurally by *topology.LiveGraph:
// its real Neighbors signature plus Has (added to LiveGraph alongside this
// adapter — see livegraph.go's Has doc comment — since neither Graph nor
// LiveGraph had any way to answer "has this service been seen" before).
type topologyNeighborHas interface {
	Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir topology.Direction) (topology.Neighborhood, error)
	Has(ctx context.Context, tid model.TenantID, service string) bool
}

type anomalyTopologyReader struct {
	g topologyNeighborHas
}

// newAnomalyTopologyReader adapts g (typically a *topology.LiveGraph) to
// anomaly.TopologyReader.
func newAnomalyTopologyReader(g topologyNeighborHas) anomalyTopologyReader {
	return anomalyTopologyReader{g: g}
}

func (a anomalyTopologyReader) Has(ctx context.Context, tid model.TenantID, service string) bool {
	return a.g.Has(ctx, tid, service)
}

// Neighbors translates anomaly.TopologyReader's flat-uint8/[]string shape to
// topology.Graph's Direction/Neighborhood shape. The uint8 values
// TopologyReader.Neighbors documents by caller convention (internal/anomaly/
// grouper.go's `3 /* Both */` call site) are numerically identical to
// topology.Upstream/Downstream/Both (1/2/3), so the direction converts by a
// direct cast rather than a lookup table. Upstream and downstream neighbors
// are merged into one deduplicated slice, matching how
// internal/anomaly/grouper_test.go's fakeTopo stub (and grouper.go's only
// call site, which always passes Both) already treat "neighbors" as a flat
// set rather than two sides.
func (a anomalyTopologyReader) Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir uint8) ([]string, error) {
	nb, err := a.g.Neighbors(ctx, tid, service, hops, topology.Direction(dir))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(nb.Upstream)+len(nb.Downstream))
	out := make([]string, 0, len(nb.Upstream)+len(nb.Downstream))
	for _, lists := range [][]string{nb.Upstream, nb.Downstream} {
		for _, s := range lists {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}
