package anomaly

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// fakeTopo is a minimal TopologyReader stub: neighbors[service] gives the
// fixed neighbor set Neighbors(service, hops, dir) returns, independent of
// hops/dir -- sufficient to test grouping behavior at a chosen topology_hops
// value without needing a real graph traversal.
type fakeTopo struct {
	neighbors map[string][]string
}

func (f *fakeTopo) Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir uint8) ([]string, error) {
	return f.neighbors[service], nil
}
func (f *fakeTopo) Has(ctx context.Context, tid model.TenantID, service string) bool { return true }

func evAt(service string, kind model.AnomalyKind, score float64, at time.Time) model.AnomalyEvent {
	return model.AnomalyEvent{
		ID: service + "-" + at.String(), Tenant: "t1", Kind: kind, Service: service,
		Score: score, CreatedAt: at,
	}
}

// TestGrouper_MergesAdjacentServicesWithinWindow is AC-F05-3's positive
// case: 2 events on adjacent services (hop distance 1) within
// grouping.window (5m) merge into 1 incident.
func TestGrouper_MergesAdjacentServicesWithinWindow(t *testing.T) {
	cfg := testConfig()
	topo := &fakeTopo{neighbors: map[string][]string{"A": {"B"}, "B": {"A"}}}
	g := NewGrouper(cfg, topo)
	ctx := context.Background()
	now := time.Now()

	inc1, isNew1, err := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.8, now))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !isNew1 {
		t.Fatal("expected first event to open a new incident")
	}

	inc2, isNew2, err := g.Add(ctx, "t1", evAt("B", model.AnomalyErrorBurst, 0.7, now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if inc2.ID != inc1.ID {
		t.Fatalf("expected event on adjacent service B to merge into incident %s, got new incident %s (isNew=%v)", inc1.ID, inc2.ID, isNew2)
	}

	active, err := g.ActiveIncidents(ctx, "t1")
	if err != nil {
		t.Fatalf("ActiveIncidents: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected exactly 1 active incident after merge, got %d", len(active))
	}
	if len(active[0].Services) != 2 {
		t.Fatalf("expected incident to carry both services, got %v", active[0].Services)
	}
}

// TestGrouper_DoesNotMergeBeyondHopDistance is AC-F05-3's negative case:
// services beyond topology_hops must not merge.
func TestGrouper_DoesNotMergeBeyondHopDistance(t *testing.T) {
	cfg := testConfig()
	cfg.Grouping.TopologyHops = 1
	// A's 1-hop neighborhood is {B} only; D is unreachable within 1 hop.
	topo := &fakeTopo{neighbors: map[string][]string{"A": {"B"}, "B": {"A", "C"}}}
	g := NewGrouper(cfg, topo)
	ctx := context.Background()
	now := time.Now()

	inc1, _, err := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.8, now))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	inc2, isNew2, err := g.Add(ctx, "t1", evAt("D", model.AnomalyErrorBurst, 0.7, now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !isNew2 {
		t.Fatal("expected event on out-of-neighborhood service D to open a new incident")
	}
	if inc2.ID == inc1.ID {
		t.Fatalf("expected D (beyond topology_hops) to NOT merge with A's incident %s", inc1.ID)
	}

	active, err := g.ActiveIncidents(ctx, "t1")
	if err != nil {
		t.Fatalf("ActiveIncidents: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 separate active incidents, got %d", len(active))
	}
}

// TestGrouper_DoesNotMergeOutsideWindow: adjacent services but the second
// event arrives after grouping.window has elapsed -- must not merge.
func TestGrouper_DoesNotMergeOutsideWindow(t *testing.T) {
	cfg := testConfig()
	topo := &fakeTopo{neighbors: map[string][]string{"A": {"B"}, "B": {"A"}}}
	g := NewGrouper(cfg, topo)
	ctx := context.Background()
	now := time.Now()

	inc1, _, _ := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.8, now))
	inc2, isNew2, _ := g.Add(ctx, "t1", evAt("B", model.AnomalyErrorBurst, 0.7, now.Add(cfg.Grouping.Window+time.Minute)))
	if !isNew2 {
		t.Fatal("expected event outside grouping.window to open a new incident")
	}
	if inc2.ID == inc1.ID {
		t.Fatal("expected events separated by more than grouping.window not to merge")
	}
}

// TestGrouper_DedupeRepeatFingerprintAttachesWithSuppressedBy is AC-F05-3's
// third clause: a repeat Fingerprint within dedupe_ttl attaches with
// SuppressedBy set rather than opening a new incident.
func TestGrouper_DedupeRepeatFingerprintAttachesWithSuppressedBy(t *testing.T) {
	cfg := testConfig()
	g := NewGrouper(cfg, nil)
	ctx := context.Background()
	now := time.Now()

	e1 := evAt("A", model.AnomalyLatencyShift, 0.8, now)
	inc1, isNew1, err := g.Add(ctx, "t1", e1)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !isNew1 {
		t.Fatal("expected first event to open new incident")
	}

	// Force the incident closed (e.g. Resolved) but within dedupe_ttl.
	g.(*memGrouper).incidents["t1"][inc1.ID].Status = model.IncidentResolved
	g.(*memGrouper).closedAt["t1"][inc1.ID] = now

	// Same service+kind -> same fingerprint, arriving 1 minute later, well
	// within dedupe_ttl (30m).
	e2 := evAt("A", model.AnomalyLatencyShift, 0.9, now.Add(time.Minute))
	inc2, isNew2, err := g.Add(ctx, "t1", e2)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if isNew2 {
		t.Fatal("expected repeat fingerprint within dedupe_ttl to attach, not open a new incident (isNew should be false)")
	}
	if inc2.ID != inc1.ID {
		t.Fatalf("expected dedupe to attach to the original incident %s, got %s", inc1.ID, inc2.ID)
	}
	if inc2.SuppressedBy != inc1.ID {
		t.Fatalf("expected SuppressedBy=%s, got %q", inc1.ID, inc2.SuppressedBy)
	}
}

// TestGrouper_DedupeExpiresAfterTTL: a repeat fingerprint arriving after
// dedupe_ttl has elapsed since closure must open a fresh incident.
func TestGrouper_DedupeExpiresAfterTTL(t *testing.T) {
	cfg := testConfig()
	g := NewGrouper(cfg, nil)
	ctx := context.Background()
	now := time.Now()

	e1 := evAt("A", model.AnomalyLatencyShift, 0.8, now)
	inc1, _, _ := g.Add(ctx, "t1", e1)
	g.(*memGrouper).incidents["t1"][inc1.ID].Status = model.IncidentResolved
	g.(*memGrouper).closedAt["t1"][inc1.ID] = now

	e2 := evAt("A", model.AnomalyLatencyShift, 0.9, now.Add(cfg.Grouping.DedupeTTL+time.Minute))
	inc2, isNew2, _ := g.Add(ctx, "t1", e2)
	if !isNew2 {
		t.Fatal("expected a new incident once dedupe_ttl has elapsed")
	}
	if inc2.ID == inc1.ID {
		t.Fatal("expected a distinct incident ID after dedupe_ttl expiry")
	}
}

// TestGrouper_ForceClosesLowestScoreOverMaxOpenIncidents (DR-14 §14.7):
// exceeding max_open_incidents force-closes the lowest-Score open incident
// with Status=Expired.
func TestGrouper_ForceClosesLowestScoreOverMaxOpenIncidents(t *testing.T) {
	cfg := testConfig()
	cfg.Grouping.MaxOpenIncidents = 2
	g := NewGrouper(cfg, nil)
	ctx := context.Background()
	now := time.Now()

	incLow, _, _ := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.56, now))
	incMid, _, _ := g.Add(ctx, "t1", evAt("B", model.AnomalyLatencyShift, 0.70, now))
	// 3rd distinct incident exceeds MaxOpenIncidents=2 -> force-close lowest.
	g.Add(ctx, "t1", evAt("C", model.AnomalyLatencyShift, 0.90, now))

	mg := g.(*memGrouper)
	if mg.incidents["t1"][incLow.ID].Status != model.IncidentExpired {
		t.Errorf("expected lowest-Score incident %s to be force-closed Expired, got status %v", incLow.ID, mg.incidents["t1"][incLow.ID].Status)
	}
	if mg.incidents["t1"][incMid.ID].Status == model.IncidentExpired {
		t.Errorf("did not expect mid-Score incident %s to be force-closed", incMid.ID)
	}
}

// TestGrouper_ProvisionalIncidentScoreCappedAt069 (DR-14 §14.3): a
// Provisional incident's Score never exceeds 0.69, so it can never reach
// Critical and can never satisfy DR-21's P3.
func TestGrouper_ProvisionalIncidentScoreCappedAt069(t *testing.T) {
	cfg := testConfig()
	g := NewGrouper(cfg, nil)
	ctx := context.Background()
	now := time.Now()

	e := evAt("A", model.AnomalyLatencyShift, 0.95, now)
	e.Provisional = true
	inc, _, err := g.Add(ctx, "t1", e)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if inc.Score > 0.69 {
		t.Errorf("Provisional incident Score = %v, want <= 0.69 (DR-14 §14.3)", inc.Score)
	}
	if inc.Severity == model.SeverityCritical {
		t.Errorf("Provisional incident must never reach Critical severity, got %v", inc.Severity)
	}
	if !inc.Provisional {
		t.Errorf("expected Incident.Provisional=true once a Provisional event attaches")
	}
}

// TestGrouper_IncidentScoreSeverityFormula spot-checks DR-14 §14.5's score
// and severity table directly.
func TestGrouper_IncidentScoreSeverityFormula(t *testing.T) {
	cfg := testConfig()
	topo := &fakeTopo{neighbors: map[string][]string{"A": {"B"}, "B": {"A"}}}
	g := NewGrouper(cfg, topo)
	ctx := context.Background()
	now := time.Now()

	// Single service, score 0.60 -> Low (0.55-0.69, 1 service).
	inc1, _, _ := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.60, now))
	if inc1.Severity != model.SeverityLow {
		t.Errorf("single-service score 0.60: Severity = %v, want SeverityLow", inc1.Severity)
	}

	// Add a second distinct service to the SAME incident: distinctServices=2,
	// max event score still 0.60 -> Incident.Score = clamp01(0.60*(1+0.05*1))=0.63 -> Medium.
	inc2, _, _ := g.Add(ctx, "t1", evAt("B", model.AnomalyLatencyShift, 0.60, now.Add(time.Second)))
	wantScore := clamp01(0.60 * (1 + 0.05*1))
	if inc2.Score != wantScore {
		t.Errorf("2-service Incident.Score = %v, want %v", inc2.Score, wantScore)
	}
	if inc2.Severity != model.SeverityMedium {
		t.Errorf("2-service score %v: Severity = %v, want SeverityMedium", inc2.Score, inc2.Severity)
	}
}

// TestGrouper_LinearChainStarDisconnected exercises three synthetic graph
// shapes (F05 §7's explicit list): a linear chain, a star, and disconnected
// components.
func TestGrouper_LinearChainStarDisconnected(t *testing.T) {
	cfg := testConfig()
	cfg.Grouping.TopologyHops = 1

	t.Run("linear_chain", func(t *testing.T) {
		// A-B-C-D chain, hops=1: A and B merge, but A and D (3 hops apart)
		// must not merge directly through a single event on each end.
		topo := &fakeTopo{neighbors: map[string][]string{
			"A": {"B"}, "B": {"A", "C"}, "C": {"B", "D"}, "D": {"C"},
		}}
		g := NewGrouper(cfg, topo)
		ctx := context.Background()
		now := time.Now()
		incA, _, _ := g.Add(ctx, "t1", evAt("A", model.AnomalyLatencyShift, 0.8, now))
		incD, isNewD, _ := g.Add(ctx, "t1", evAt("D", model.AnomalyLatencyShift, 0.8, now.Add(time.Second)))
		if !isNewD || incD.ID == incA.ID {
			t.Errorf("expected A and D (3 hops apart, topology_hops=1) to be separate incidents")
		}
	})

	t.Run("star", func(t *testing.T) {
		// Hub H with spokes S1..S3, hops=1: any spoke merges with the hub's
		// incident since H is a 1-hop neighbor of every spoke.
		topo := &fakeTopo{neighbors: map[string][]string{
			"H": {"S1", "S2", "S3"}, "S1": {"H"}, "S2": {"H"}, "S3": {"H"},
		}}
		g := NewGrouper(cfg, topo)
		ctx := context.Background()
		now := time.Now()
		incH, _, _ := g.Add(ctx, "t1", evAt("H", model.AnomalyLatencyShift, 0.8, now))
		incS1, _, _ := g.Add(ctx, "t1", evAt("S1", model.AnomalyLatencyShift, 0.7, now.Add(time.Second)))
		if incS1.ID != incH.ID {
			t.Errorf("expected spoke S1 to merge into hub H's incident")
		}
	})

	t.Run("disconnected", func(t *testing.T) {
		// Two disjoint components: events must never merge across them.
		topo := &fakeTopo{neighbors: map[string][]string{
			"X1": {"X2"}, "X2": {"X1"}, "Y1": {"Y2"}, "Y2": {"Y1"},
		}}
		g := NewGrouper(cfg, topo)
		ctx := context.Background()
		now := time.Now()
		incX, _, _ := g.Add(ctx, "t1", evAt("X1", model.AnomalyLatencyShift, 0.8, now))
		incY, _, _ := g.Add(ctx, "t1", evAt("Y1", model.AnomalyLatencyShift, 0.8, now.Add(time.Second)))
		if incX.ID == incY.ID {
			t.Errorf("expected disconnected components to never merge into one incident")
		}
	})
}

// --- AC-F05-16 / DR-21 §21.1: no severity-/tier-derived paging bypass ---

// TestAC_F05_16_NoServiceMetaTierReference is the internal/anomaly-scoped
// proxy for AC-F05-16's AST/route test ("no code path from
// model.ServiceMeta.Tier to api.AlertRouter"). api.AlertRouter itself lives
// outside this package's scope lock; the contract this package owns is that
// it never reads model.ServiceMeta.Tier at all (grouping/scoring/paging
// inputs), so no such code path can originate here. This scans the
// package's own non-test source for any reference to ServiceMeta or a
// ".Tier" selector.
func TestAC_F05_16_NoServiceMetaTierReference(t *testing.T) {
	assertSourceFree(t, []string{"ServiceMeta", ".Tier"})
}
