package anomaly

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"traceiq/internal/model"
)

// memGrouper is an in-memory Grouper implementing DR-14 §14.7's O(1)
// amortized grouping: byService indexes each open incident under every
// service in its K-hop neighborhood (computed once per newly-added
// service, memoised in neighborCache), so Add is a map lookup plus a small
// slice scan, never a scan over all open incidents.
type memGrouper struct {
	mu   sync.Mutex
	cfg  Config
	topo TopologyReader

	incidents     map[model.TenantID]map[string]*model.Incident
	byService     map[model.TenantID]map[string][]string // service -> incident IDs indexed under it (self or neighbor)
	byFingerprint map[model.TenantID]map[string]string   // fingerprint -> incident ID
	closedAt      map[model.TenantID]map[string]time.Time

	epicenterScore map[string]map[string]float64 // incidentID -> service -> best event score
	neighborCache  map[string]neighborCacheEntry

	stats GrouperStats
	seq   int
}

type neighborCacheEntry struct {
	neighbors []string
	expiresAt time.Time
}

// NewGrouper returns an in-memory Grouper. topo may be nil (grouping then
// falls back to same-service-only matching).
func NewGrouper(cfg Config, topo TopologyReader) Grouper {
	return &memGrouper{
		cfg:            cfg,
		topo:           topo,
		incidents:      make(map[model.TenantID]map[string]*model.Incident),
		byService:      make(map[model.TenantID]map[string][]string),
		byFingerprint:  make(map[model.TenantID]map[string]string),
		closedAt:       make(map[model.TenantID]map[string]time.Time),
		epicenterScore: make(map[string]map[string]float64),
		neighborCache:  make(map[string]neighborCacheEntry),
	}
}

var _ Grouper = (*memGrouper)(nil)

func (g *memGrouper) ensureTenant(tid model.TenantID) {
	if g.incidents[tid] == nil {
		g.incidents[tid] = make(map[string]*model.Incident)
	}
	if g.byService[tid] == nil {
		g.byService[tid] = make(map[string][]string)
	}
	if g.byFingerprint[tid] == nil {
		g.byFingerprint[tid] = make(map[string]string)
	}
	if g.closedAt[tid] == nil {
		g.closedAt[tid] = make(map[string]time.Time)
	}
}

func isOpen(s model.IncidentStatus) bool {
	switch s {
	case model.IncidentCandidate, model.IncidentInvestigating, model.IncidentReported, model.IncidentPaging:
		return true
	default:
		return false
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// computeFingerprint mirrors DR-14 §14.5's construction (tenant + sorted
// tokens) using FNV-64a rather than xxh3, since the register never
// publishes xxh3 as a dependency choice for this package and no external
// oracle needs a byte-identical hash for these tests — only deterministic,
// collision-stable dedupe behavior. NEEDS_CONTEXT if an exact hash function
// match to memory.Fingerprint.Compute is required.
func computeFingerprint(tid model.TenantID, e model.AnomalyEvent) string {
	tokens := []string{
		"svc:" + e.Service,
		fmt.Sprintf("kind:%d", e.Kind),
	}
	if e.Operation != "" {
		tokens = append(tokens, "op:"+e.Operation)
	}
	if e.ErrorSigID != "" {
		tokens = append(tokens, "errsig:"+e.ErrorSigID)
	}
	sort.Strings(tokens)
	h := fnv.New64a()
	h.Write([]byte(tid))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(tokens, "\x01")))
	return fmt.Sprintf("fp1:%x", h.Sum64())
}

func severityFor(score float64, distinctServices int) model.Severity {
	switch {
	case score < 0.55:
		return model.SeverityUnknown
	case score <= 0.69:
		if distinctServices >= 2 {
			return model.SeverityMedium
		}
		return model.SeverityLow
	case score <= 0.84:
		return model.SeverityHigh
	default:
		return model.SeverityCritical
	}
}

func (g *memGrouper) neighborsOf(ctx context.Context, tid model.TenantID, service string) []string {
	cacheKey := string(tid) + "|" + service
	if e, ok := g.neighborCache[cacheKey]; ok && time.Now().Before(e.expiresAt) {
		return e.neighbors
	}
	if g.topo == nil {
		return nil
	}
	neighbors, err := g.topo.Neighbors(ctx, tid, service, g.cfg.Grouping.TopologyHops, 3 /* Both */)
	if err != nil {
		return nil
	}
	g.neighborCache[cacheKey] = neighborCacheEntry{neighbors: neighbors, expiresAt: time.Now().Add(g.cfg.Grouping.NeighborCacheTTL)}
	return neighbors
}

func (g *memGrouper) addToByService(tid model.TenantID, service, incidentID string) {
	if contains(g.byService[tid][service], incidentID) {
		return
	}
	g.byService[tid][service] = append(g.byService[tid][service], incidentID)
}

// registerServiceForIncident is FR-F05-10/DR-14 §14.7: called once per
// newly-added service in an incident, computing (and caching) its K-hop
// neighborhood and indexing the incident under every neighbor too, so a
// later event on any of those neighbors resolves via a single map lookup.
func (g *memGrouper) registerServiceForIncident(ctx context.Context, tid model.TenantID, incidentID, service string) {
	g.addToByService(tid, service, incidentID)
	for _, n := range g.neighborsOf(ctx, tid, service) {
		g.addToByService(tid, n, incidentID)
	}
}

func (g *memGrouper) attachEvent(ctx context.Context, tid model.TenantID, inc *model.Incident, e model.AnomalyEvent) {
	inc.EventIDs = append(inc.EventIDs, e.ID)
	if len(inc.EventIDs) > g.cfg.Grouping.MaxEventsPerIncident {
		inc.EventIDs = inc.EventIDs[len(inc.EventIDs)-g.cfg.Grouping.MaxEventsPerIncident:]
	}

	if !contains(inc.Services, e.Service) {
		inc.Services = append(inc.Services, e.Service)
		g.registerServiceForIncident(ctx, tid, inc.ID, e.Service)
	}
	if e.Provisional {
		inc.Provisional = true
	}
	for _, id := range e.DeployMarkerIDs {
		if !contains(inc.DeployMarkerIDs, id) {
			inc.DeployMarkerIDs = append(inc.DeployMarkerIDs, id)
		}
	}
	for _, tr := range e.ExemplarTraceIDs {
		if len(inc.ExemplarTraceIDs) < 4 {
			inc.ExemplarTraceIDs = append(inc.ExemplarTraceIDs, tr)
		}
	}

	// Score/Severity (DR-14 §14.5).
	if g.epicenterScore[inc.ID] == nil {
		g.epicenterScore[inc.ID] = make(map[string]float64)
	}
	if e.Score > g.epicenterScore[inc.ID][e.Service] {
		g.epicenterScore[inc.ID][e.Service] = e.Score
	}
	maxScore := 0.0
	for _, sc := range g.epicenterScore[inc.ID] {
		if sc > maxScore {
			maxScore = sc
		}
	}
	distinct := len(inc.Services)
	score := clamp01(maxScore * (1 + 0.05*float64(distinct-1)))
	if inc.Provisional && score > 0.69 {
		score = 0.69 // DR-14 §14.3: provisional incidents never reach Critical
	}
	inc.Score = score
	inc.Severity = severityFor(score, distinct)

	// EpicenterService = highest event score seen so far, tie by lexical.
	best, bestScore := inc.EpicenterService, -1.0
	for svc, sc := range g.epicenterScore[inc.ID] {
		if sc > bestScore || (sc == bestScore && (best == "" || svc < best)) {
			best, bestScore = svc, sc
		}
	}
	inc.EpicenterService = best

	// BlastRadius = services within topology_hops of the epicenter that
	// carry an event.
	neighborSet := map[string]bool{inc.EpicenterService: true}
	for _, n := range g.neighborsOf(ctx, tid, inc.EpicenterService) {
		neighborSet[n] = true
	}
	var blast []string
	for _, s := range inc.Services {
		if neighborSet[s] {
			blast = append(blast, s)
		}
	}
	sort.Strings(blast)
	inc.BlastRadius = blast

	if e.CreatedAt.After(inc.LastSeen) {
		inc.LastSeen = e.CreatedAt
	}
	inc.UpdatedAt = e.CreatedAt
}

func (g *memGrouper) Add(ctx context.Context, tid model.TenantID, e model.AnomalyEvent) (model.Incident, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureTenant(tid)

	fp := computeFingerprint(tid, e)

	if existingID, ok := g.byFingerprint[tid][fp]; ok {
		if inc, ok2 := g.incidents[tid][existingID]; ok2 {
			eligible := isOpen(inc.Status)
			if !eligible {
				if closedAt, ok3 := g.closedAt[tid][existingID]; ok3 && e.CreatedAt.Sub(closedAt) <= g.cfg.Grouping.DedupeTTL {
					eligible = true
				}
			}
			if eligible {
				g.attachEvent(ctx, tid, inc, e)
				inc.SuppressedBy = existingID
				return *inc, false, nil
			}
		}
	}

	var target *model.Incident
	for _, id := range g.byService[tid][e.Service] {
		inc := g.incidents[tid][id]
		if inc != nil && isOpen(inc.Status) && !inc.UpdatedAt.IsZero() && e.CreatedAt.Sub(inc.UpdatedAt) <= g.cfg.Grouping.Window {
			target = inc
			break
		}
	}

	if target == nil {
		g.seq++
		inc := &model.Incident{
			ID:        fmt.Sprintf("inc-%d", g.seq),
			Tenant:    tid,
			Status:    model.IncidentCandidate,
			FirstSeen: e.CreatedAt,
			LastSeen:  e.CreatedAt,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.CreatedAt,
		}
		g.incidents[tid][inc.ID] = inc
		g.attachEvent(ctx, tid, inc, e)
		inc.Fingerprint = fp
		g.byFingerprint[tid][fp] = inc.ID
		g.enforceMaxOpenIncidents(tid)
		return *inc, true, nil
	}

	g.attachEvent(ctx, tid, target, e)
	if target.Fingerprint == "" {
		target.Fingerprint = fp
	}
	g.byFingerprint[tid][fp] = target.ID
	return *target, true, nil
}

func (g *memGrouper) enforceMaxOpenIncidents(tid model.TenantID) {
	var lowest *model.Incident
	open := 0
	for _, inc := range g.incidents[tid] {
		if !isOpen(inc.Status) {
			continue
		}
		open++
		if lowest == nil || inc.Score < lowest.Score {
			lowest = inc
		}
	}
	if open > g.cfg.Grouping.MaxOpenIncidents && lowest != nil {
		lowest.Status = model.IncidentExpired
		g.closedAt[tid][lowest.ID] = time.Now()
	}
}

func (g *memGrouper) ActiveIncidents(ctx context.Context, tid model.TenantID) ([]model.Incident, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []model.Incident
	for _, inc := range g.incidents[tid] {
		if isOpen(inc.Status) {
			out = append(out, *inc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (g *memGrouper) Suppress(ctx context.Context, tid model.TenantID, id, reason string, ttl time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensureTenant(tid)
	inc, ok := g.incidents[tid][id]
	if !ok {
		return fmt.Errorf("anomaly: incident %s not found for tenant %s", id, tid)
	}
	inc.Status = model.IncidentSuppressed
	inc.SuppressedBy = reason
	g.closedAt[tid][id] = time.Now()
	return nil
}

func (g *memGrouper) Tick(ctx context.Context, now time.Time) ([]model.Incident, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stats.LastTickAt = now
	open := 0
	for _, tenantIncs := range g.incidents {
		for _, inc := range tenantIncs {
			if isOpen(inc.Status) {
				open++
			}
		}
	}
	g.stats.OpenIncidents = open
	return nil, nil
}

func (g *memGrouper) Stats() GrouperStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stats
}
