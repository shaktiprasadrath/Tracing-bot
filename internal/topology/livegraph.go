package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// ServiceNode is a graph node (F04 §4.2). Not redeclared by topology.go's
// types-only pass, so it is added here alongside the implementation.
type ServiceNode struct {
	Tenant     model.TenantID
	Name       string
	Attributes map[string]string
	FirstSeen  time.Time
	LastSeen   time.Time
}

// ExportFormat values (DR-13 leaves the enum open; §8 of F04 flags this for
// lead-architect confirmation). json/dot are the two formats D-Y4 implies.
const (
	ExportFormatJSON ExportFormat = "json"
	ExportFormatDOT  ExportFormat = "dot"
)

type edgeKey struct {
	Tenant   model.TenantID
	Caller   string
	Callee   string
	Protocol string
}

type pendingKey struct {
	Tenant  model.TenantID
	TraceID model.TraceID
	SpanID  model.SpanID
}

// pendingClientSpan is the correlation-buffer entry for FR-F04-8's join.
type pendingClientSpan struct {
	Service   string
	Protocol  string
	Timestamp time.Time
}

// LiveGraph is the in-memory implementation of Graph (DR-13). It never
// imports internal/store: persistence flows through the consumer-declared
// EdgeSink/EdgeSource seams, satisfied by store/sqlite and wired by
// cmd/traceiq.
//
// Deviation from F04 §4.3's literal constructor
// (`NewLiveGraph(ctx, tid, cfg, sink, source, clock)`): that signature scopes
// construction to a single tenant, which conflicts with every other Graph
// method taking `tid` per call (Neighbors/Edges/Snapshot/etc all serve every
// tenant from one instance). This implementation keeps LiveGraph
// multi-tenant — constructed once, tenant-scoped only per call — and drops
// ctx/tid from the constructor; warm-start is its own WarmStart(ctx, tid, w)
// method instead of being baked into construction. Flagged for
// lead-architect confirmation alongside F04 §8's other open shape questions.
//
// Also simplified vs DR-13's "hash(caller,callee) -> shard" sharded design:
// this pass uses one RWMutex-guarded map rather than N shards. Functionally
// equivalent (same Graph contract, same eviction/change-detection
// semantics); the AC-F04-3 p99-at-20k-edges perf target is not validated
// here — flagged as a follow-up, not attempted within this pass's time box.
type LiveGraph struct {
	cfg           config.TopologyConfig
	clock         model.Clock
	sink          EdgeSink
	source        EdgeSource
	newEdgeWindow time.Duration // F04's change_detection.new_edge_window (24h); not present on
	// config.TopologyConfig (only VanishedAfter is), so it is a package
	// constant here rather than a config-sourced value. Deviation, noted.
	vanishWindow time.Duration
	pendingTTL   time.Duration

	mu          sync.RWMutex
	edges       map[edgeKey]*Edge
	services    map[model.TenantID]map[string]*ServiceNode
	pending     map[pendingKey]pendingClientSpan
	ops         map[string]map[string]*EdgeOp // edgeID -> operation -> EdgeOp
	lastNewEmit map[edgeKey]time.Time
	evicted     int64

	// insertSeq records the monotonically increasing insertion order of every
	// live edge, and nextSeq is its counter. They exist solely to give
	// evictIfNeeded a deterministic final tie-break (see that function): Calls
	// and LastSeen can both be exactly equal between two edges (trivially so
	// under a fake/virtual clock, but also in production when two edges are
	// first seen inside the same Consume batch), and without a total order the
	// victim was chosen by Go's randomized map iteration order. Kept as a
	// side map rather than a field on Edge so the exported, cross-package
	// Edge value type (compared by reflect.DeepEqual/cmp in other packages'
	// tests) gains no hidden state.
	insertSeq map[edgeKey]uint64
	nextSeq   uint64

	changes chan ChangeEvent
}

// NewLiveGraph constructs a multi-tenant LiveGraph. clock is required
// (DR-31): every bucket boundary and vanish/new-edge window comparison reads
// model.Clock rather than time.Now, so internal/archtest's time-ban check
// passes and eval.VirtualClock can drive topology deterministically.
func NewLiveGraph(cfg config.TopologyConfig, sink EdgeSink, source EdgeSource, clock model.Clock) *LiveGraph {
	vanish := cfg.VanishedAfter
	if vanish <= 0 {
		vanish = time.Hour
	}
	return &LiveGraph{
		cfg:           cfg,
		clock:         clock,
		sink:          sink,
		source:        source,
		newEdgeWindow: 24 * time.Hour,
		vanishWindow:  vanish,
		pendingTTL:    30 * time.Second,
		edges:         make(map[edgeKey]*Edge),
		services:      make(map[model.TenantID]map[string]*ServiceNode),
		pending:       make(map[pendingKey]pendingClientSpan),
		ops:           make(map[string]map[string]*EdgeOp),
		lastNewEmit:   make(map[edgeKey]time.Time),
		insertSeq:     make(map[edgeKey]uint64),
		changes:       make(chan ChangeEvent, 256),
	}
}

// Consume implements ingest.SpanSink structurally (DR-2): the signature
// mentions only model types, so this package is never imported by
// internal/ingest and never imports it. Edge derivation follows F04 §4.4's
// algorithm: a CLIENT/PRODUCER span with a direct peer attribute
// self-encodes its edge; otherwise it is buffered until the matching
// SERVER/CONSUMER span arrives (bounded correlation window), and an
// unmatched SERVER/CONSUMER span is recorded as an "external" caller edge
// rather than dropped (FR-F04-8).
func (g *LiveGraph) Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error {
	now := g.clock.Now()
	for i := range spans {
		span := &spans[i]
		service := ""
		if span.Resource != nil {
			service = span.Resource.ServiceName
		}
		g.ensureService(tid, service, now)

		switch span.Kind {
		case model.SpanKindClient, model.SpanKindProducer:
			callee, protocol := extractPeer(span.Attrs)
			if callee != "" {
				g.upsertEdge(tid, service, callee, protocol, span.Name, now, span)
				continue
			}
			key := pendingKey{Tenant: tid, TraceID: span.TraceID, SpanID: span.SpanID}
			g.mu.Lock()
			g.pending[key] = pendingClientSpan{Service: service, Protocol: protocolOf(span.Attrs), Timestamp: now}
			g.mu.Unlock()

		case model.SpanKindServer, model.SpanKindConsumer:
			key := pendingKey{Tenant: tid, TraceID: span.TraceID, SpanID: span.ParentSpanID}
			g.mu.Lock()
			entry, ok := g.pending[key]
			if ok {
				delete(g.pending, key)
			}
			g.mu.Unlock()
			if ok {
				g.upsertEdge(tid, entry.Service, service, entry.Protocol, span.Name, now, span)
			} else {
				// FR-F04-8: external/unattributed caller, counted not dropped.
				g.upsertEdge(tid, "external", service, "unknown", span.Name, now, span)
			}

		default:
			// INTERNAL spans: node presence only, no edge (F04 §4.4).
		}
	}
	return nil
}

func (g *LiveGraph) ensureService(tid model.TenantID, name string, now time.Time) {
	if name == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	tset, ok := g.services[tid]
	if !ok {
		tset = make(map[string]*ServiceNode)
		g.services[tid] = tset
	}
	n, ok := tset[name]
	if !ok {
		n = &ServiceNode{Tenant: tid, Name: name, FirstSeen: now}
		tset[name] = n
	}
	n.LastSeen = now
}

// upsertEdge is DR-13/F04 §4.4's upsertEdge, extended to also feed
// EdgeOps (FR-F04-9) and RED (Calls/Errors/DurationSumNanos/Hist) from the
// triggering span.
func (g *LiveGraph) upsertEdge(tid model.TenantID, caller, callee, protocol, operation string, ts time.Time, span *model.Span) {
	if caller != "" && caller == callee {
		protocol = "internal"
	}
	key := edgeKey{Tenant: tid, Caller: caller, Callee: callee, Protocol: protocol}

	var duration uint64
	if span.EndUnixNano > span.StartUnixNano {
		duration = span.EndUnixNano - span.StartUnixNano
	}
	isErr := span.Status.Code == model.StatusError

	g.mu.Lock()
	e, exists := g.edges[key]
	if !exists {
		e = &Edge{
			ID:         edgeIDFor(caller, callee, protocol),
			Tenant:     tid,
			Caller:     caller,
			Callee:     callee,
			Protocol:   protocol,
			Resolution: model.Res10s,
			FirstSeen:  ts,
		}
		g.edges[key] = e
		g.nextSeq++
		g.insertSeq[key] = g.nextSeq
	}
	e.Calls++
	if isErr {
		e.Errors++
	}
	e.DurationSumNanos += duration
	addToHist(&e.Hist, duration)
	e.LastSeen = ts
	e.BucketStart = ts.Truncate(10 * time.Second)
	e.IsVanished = false
	if !exists {
		g.evictIfNeeded()
	}

	lastEmit, seen := g.lastNewEmit[key]
	isNew := !seen || ts.Sub(lastEmit) > g.newEdgeWindow
	e.IsNew = isNew
	// FIXED (review pass): §4.4's addWithTTL(key, newEdgeWindow) refreshes the
	// TTL on EVERY upsert, not only when a NewEdge fires. The prior code only
	// updated lastNewEmit inside `if isNew`, so an edge under continuous
	// traffic never advanced its anchor timestamp and would spuriously
	// re-trigger ChangeEvent{NewEdge} ~24h after its original first sighting
	// even though it never actually went quiet — a false "new edge" signal
	// that would reach F05's anomaly detector. Refreshing unconditionally
	// restores the intended "not seen in the prior rolling 24h window"
	// semantics: only an edge that is genuinely absent for > newEdgeWindow
	// re-emits NewEdge on its next sighting.
	g.lastNewEmit[key] = ts
	edgeCopy := *e

	if operation != "" {
		opsForEdge, ok := g.ops[e.ID]
		if !ok {
			opsForEdge = make(map[string]*EdgeOp)
			g.ops[e.ID] = opsForEdge
		}
		op, ok := opsForEdge[operation]
		if !ok {
			op = &EdgeOp{Tenant: tid, EdgeID: e.ID, CalleeOperation: operation, BucketStart: ts.Truncate(time.Hour)}
			opsForEdge[operation] = op
		}
		op.Calls++
		if isErr {
			op.Errors++
		}
		if duration > op.P99Nanos { // placeholder max, not a true quantile (§8 flags EdgeOp shape as inferred)
			op.P99Nanos = duration
		}
	}
	g.mu.Unlock()

	if isNew {
		g.emit(ChangeEvent{Kind: "NewEdge", Edge: edgeCopy, Detected: ts})
	}
}

// evictIfNeeded is DR-13's LRU-by-Calls eviction at topology.max_edges.
// Caller must hold g.mu.
//
// w17 final-review fix (the TestEvictionAtCap flake): the victim is chosen by
// a TOTAL order, not by "strictly fewer Calls than the best seen so far"
// evaluated in Go's randomized map iteration order. The old comparison
// (`e.Calls < victim.Calls`) left every Calls-tie resolved by whichever key
// the runtime happened to yield first, so at cap a brand-new edge could evict
// *itself* half the time and then immediately re-trigger eviction on its next
// sighting — observed as a ~1-in-10 `want EdgesEvicted=1, got 2` failure, and
// in production as nondeterministic, non-LRU edge loss.
//
// The order is (coldest first): fewest Calls, then least-recently-seen, then
// earliest inserted. Calls is DR-13's stated criterion; LastSeen is the "LRU"
// half of "LRU-by-Calls" and is the meaningful discriminator between two
// equally-quiet edges; insertSeq is the final, always-unique tie-break that
// makes the choice deterministic even when a batch creates several edges at
// one identical clock reading (always true under eval.VirtualClock).
//
// It also evicts in a loop rather than once, and drops the victim's companion
// per-edge state (`ops`, `lastNewEmit`). Those two maps were previously never
// pruned, so under high edge cardinality they grew without bound even though
// `edges` itself was capped — max_edges bounded only one of the three maps.
func (g *LiveGraph) evictIfNeeded() {
	max := g.cfg.MaxEdges
	if max <= 0 {
		return
	}
	for len(g.edges) > max {
		var victimKey edgeKey
		var victim *Edge
		for k, e := range g.edges {
			if victim == nil || g.colder(k, e, victimKey, victim) {
				victimKey, victim = k, e
			}
		}
		if victim == nil {
			return
		}
		delete(g.edges, victimKey)
		delete(g.insertSeq, victimKey)
		delete(g.lastNewEmit, victimKey)
		delete(g.ops, victim.ID)
		g.evicted++
	}
}

// colder reports whether edge a is a strictly better eviction victim than b
// under the total order documented on evictIfNeeded. Caller must hold g.mu.
func (g *LiveGraph) colder(aKey edgeKey, a *Edge, bKey edgeKey, b *Edge) bool {
	if a.Calls != b.Calls {
		return a.Calls < b.Calls
	}
	if !a.LastSeen.Equal(b.LastSeen) {
		return a.LastSeen.Before(b.LastSeen)
	}
	// insertSeq is unique per live edge, so this branch always decides.
	return g.insertSeq[aKey] < g.insertSeq[bKey]
}

// sweepVanished is F04 §4.4's VanishSweep. Exported test hook via same-package
// tests; production use is a background ticker (see Run).
func (g *LiveGraph) sweepVanished(now time.Time) []ChangeEvent {
	g.mu.Lock()
	var events []ChangeEvent
	for _, e := range g.edges {
		if e.IsVanished {
			continue
		}
		if now.Sub(e.LastSeen) > g.vanishWindow {
			e.IsVanished = true
			events = append(events, ChangeEvent{Kind: "VanishedEdge", Edge: *e, Detected: now})
		}
	}
	g.mu.Unlock()
	for _, ev := range events {
		g.emit(ev)
	}
	return events
}

// sweepPending bounds the correlation buffer's growth (§4.4's SweepPending).
func (g *LiveGraph) sweepPending(now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for k, p := range g.pending {
		if now.Sub(p.Timestamp) > g.pendingTTL {
			delete(g.pending, k)
			n++
		}
	}
	return n
}

// emit is Changes()'s bounded (cap 256), non-blocking, drop-oldest send.
func (g *LiveGraph) emit(ev ChangeEvent) {
	select {
	case g.changes <- ev:
		return
	default:
	}
	select {
	case <-g.changes:
	default:
	}
	select {
	case g.changes <- ev:
	default:
	}
}

// Has reports whether service has ever been observed for tid — true once
// ensureService has recorded it (from any span, including INTERNAL spans
// that never produce an edge), independent of whether it currently
// participates in any edge. Added (additive, no signature elsewhere
// changed) so callers with only the per-tenant service set in hand — not a
// full edge scan — can answer service-presence without inferring it from
// Edges()/Neighbors(), which would wrongly report "absent" for a
// freshly-seen, edge-less service. internal/api's anomaly.TopologyReader
// adapter (see topology_adapter.go) is the first consumer: DR-14 §14.1's
// TopologyReader requires Has, and Graph/LiveGraph had no way to answer it
// before this method (docs/reports/w15-api-web.md).
func (g *LiveGraph) Has(ctx context.Context, tid model.TenantID, service string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	tset, ok := g.services[tid]
	if !ok {
		return false
	}
	_, ok = tset[service]
	return ok
}

func (g *LiveGraph) Neighbors(ctx context.Context, tid model.TenantID, service string, hops int, dir Direction) (Neighborhood, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	visited := map[string]int{service: 0}
	var upstream, downstream []string
	var edgesOut []Edge
	frontier := []string{service}
	for h := 1; h <= hops && len(frontier) > 0; h++ {
		var next []string
		for _, s := range frontier {
			for k, e := range g.edges {
				if k.Tenant != tid {
					continue
				}
				if (dir == Downstream || dir == Both) && e.Caller == s {
					edgesOut = append(edgesOut, *e)
					if _, ok := visited[e.Callee]; !ok {
						visited[e.Callee] = h
						downstream = append(downstream, e.Callee)
						next = append(next, e.Callee)
					}
				}
				if (dir == Upstream || dir == Both) && e.Callee == s {
					edgesOut = append(edgesOut, *e)
					if _, ok := visited[e.Caller]; !ok {
						visited[e.Caller] = h
						upstream = append(upstream, e.Caller)
						next = append(next, e.Caller)
					}
				}
			}
		}
		frontier = next
	}
	return Neighborhood{Root: service, Upstream: upstream, Downstream: downstream, Edges: edgesOut, HopOf: visited}, nil
}

func (g *LiveGraph) Distance(ctx context.Context, tid model.TenantID, a, b string, maxHops int) (int, bool, error) {
	if a == b {
		return 0, true, nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	visited := map[string]bool{a: true}
	frontier := []string{a}
	for h := 1; h <= maxHops; h++ {
		var next []string
		for _, s := range frontier {
			for k, e := range g.edges {
				if k.Tenant != tid {
					continue
				}
				var other string
				switch s {
				case e.Caller:
					other = e.Callee
				case e.Callee:
					other = e.Caller
				default:
					continue
				}
				if other == b {
					return h, true, nil
				}
				if !visited[other] {
					visited[other] = true
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	return 0, false, nil
}

func (g *LiveGraph) Edges(ctx context.Context, tid model.TenantID, w model.Window) ([]Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Edge
	for k, e := range g.edges {
		if k.Tenant != tid {
			continue
		}
		if !w.Start.IsZero() && e.LastSeen.Before(w.Start) {
			continue
		}
		if !w.End.IsZero() && e.FirstSeen.After(w.End) {
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

func (g *LiveGraph) EdgeOps(ctx context.Context, tid model.TenantID, edgeID string, w model.Window, topN int) ([]EdgeOp, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	opsForEdge, ok := g.ops[edgeID]
	if !ok {
		return nil, nil
	}
	out := make([]EdgeOp, 0, len(opsForEdge))
	for _, op := range opsForEdge {
		if op.Tenant != tid {
			continue
		}
		out = append(out, *op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

func (g *LiveGraph) Snapshot(ctx context.Context, tid model.TenantID) (Snapshot, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var edges []Edge
	for k, e := range g.edges {
		if k.Tenant != tid {
			continue
		}
		edges = append(edges, *e)
	}
	return Snapshot{Tenant: tid, At: g.clock.Now(), Edges: edges}, nil
}

func (g *LiveGraph) Export(ctx context.Context, tid model.TenantID, w io.Writer, f ExportFormat) error {
	snap, err := g.Snapshot(ctx, tid)
	if err != nil {
		return err
	}
	switch f {
	case ExportFormatDOT:
		fmt.Fprintln(w, "digraph topology {")
		for _, e := range snap.Edges {
			fmt.Fprintf(w, "  %q -> %q [protocol=%q calls=%d];\n", e.Caller, e.Callee, e.Protocol, e.Calls)
		}
		fmt.Fprintln(w, "}")
		return nil
	case ExportFormatJSON, "":
		return json.NewEncoder(w).Encode(snap)
	default:
		return fmt.Errorf("topology: unsupported export format %q", f)
	}
}

func (g *LiveGraph) Changes() <-chan ChangeEvent { return g.changes }

// Flush is the shutdown step (04 §X6) and the periodic (flush_interval)
// persistence path through EdgeSink (DR-13, DR-2 — never store.HotIndex
// directly).
func (g *LiveGraph) Flush(ctx context.Context) error {
	if g.sink == nil {
		return nil
	}
	g.mu.RLock()
	byTenant := map[model.TenantID][]Edge{}
	for _, e := range g.edges {
		byTenant[e.Tenant] = append(byTenant[e.Tenant], *e)
	}
	g.mu.RUnlock()
	for tid, edges := range byTenant {
		if err := g.sink.WriteEdges(ctx, tid, edges); err != nil {
			return err
		}
	}
	return nil
}

func (g *LiveGraph) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Stats{EdgeCount: len(g.edges), EdgesEvicted: g.evicted, OpenBuckets: len(g.pending)}
}

// Run drives the background work F04 §4.1/§4.4 requires a live process to
// perform — periodic Flush (10s, §4.1's "Periodic Flush"), VanishSweep
// (1m, change_detection.sweep_interval) and the pending-correlation-buffer
// sweep (pendingTTL/2, §4.4's SweepPending) — none of which had a driver
// before this pass (sweepVanished/sweepPending/Flush were reachable only
// from tests calling them directly; the package doc for sweepVanished even
// referenced "a background ticker (see Run)" that did not exist). FIXED
// (review pass): without this, FR-F04-6's VanishedEdge detection and the
// EdgeSink-backed durability/warm-start path never ran outside tests.
//
// config.TopologyConfig does not carry flush_interval or
// change_detection.sweep_interval (only VanishedAfter is present, same gap
// already noted on newEdgeWindow/pendingTTL above), so the intervals below
// are the package constants matching F04 §4.3's documented defaults;
// promoting them to config fields is a follow-up, not attempted here.
//
// Run blocks until ctx is cancelled. cmd/traceiq is expected to start it in
// its own goroutine once per LiveGraph instance, alongside Start-equivalent
// wiring for the rest of the ingest -> topology pipeline (not yet assembled
// in cmd/traceiq as of this pass).
func (g *LiveGraph) Run(ctx context.Context) error {
	const (
		flushInterval = 10 * time.Second
		sweepInterval = time.Minute
	)
	pendingSweepInterval := g.pendingTTL / 2
	if pendingSweepInterval <= 0 {
		pendingSweepInterval = 15 * time.Second
	}

	flushT := g.clock.NewTicker(flushInterval)
	vanishT := g.clock.NewTicker(sweepInterval)
	pendingT := g.clock.NewTicker(pendingSweepInterval)
	defer flushT.Stop()
	defer vanishT.Stop()
	defer pendingT.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flushT.C():
			_ = g.Flush(ctx)
		case <-vanishT.C():
			g.sweepVanished(g.clock.Now())
		case <-pendingT.C():
			g.sweepPending(g.clock.Now())
		}
	}
}

// WarmStart is FR-F04-7: reconstruct in-memory state from EdgeSource on
// restart. Not part of the Graph interface (DR-13 doesn't print a Go
// signature for it beyond prose); exposed so cmd/traceiq can call it once at
// boot per tenant.
func (g *LiveGraph) WarmStart(ctx context.Context, tid model.TenantID, w model.Window) error {
	if g.source == nil {
		return nil
	}
	loaded, err := g.source.LoadEdges(ctx, tid, w)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range loaded {
		e := loaded[i]
		key := edgeKey{Tenant: e.Tenant, Caller: e.Caller, Callee: e.Callee, Protocol: e.Protocol}
		ec := e
		if _, exists := g.edges[key]; !exists {
			g.nextSeq++
			g.insertSeq[key] = g.nextSeq
		}
		g.edges[key] = &ec
	}
	// w17 final-review fix: warm start previously bypassed topology.max_edges
	// entirely — whatever EdgeSource.LoadEdges returned was installed wholesale,
	// so a restart could re-inflate the graph far past the configured memory
	// bound and stay there until organic churn happened to trigger an upsert of
	// a *new* edge. Re-applying the cap here makes max_edges hold across
	// restarts, which is the only thing that makes it a real bound.
	g.evictIfNeeded()
	return nil
}

// extractPeer / protocolOf implement F04 §4.4's semantic-convention
// precedence table for FR-F04-2/FR-F04-8.
func extractPeer(attrs model.AttrMap) (callee, protocol string) {
	if v, ok := strAttr(attrs, "peer.service"); ok {
		return v, protocolOf(attrs)
	}
	if v, ok := strAttr(attrs, "server.address"); ok {
		return v, protocolOf(attrs)
	}
	if v, ok := strAttr(attrs, "db.system"); ok {
		return "db:" + v, "db"
	}
	if v, ok := strAttr(attrs, "messaging.destination.name"); ok {
		return v, "messaging"
	}
	return "", "unknown"
}

func protocolOf(attrs model.AttrMap) string {
	if _, ok := attrs["rpc.system"]; ok {
		return "grpc"
	}
	if _, ok := attrs["http.request.method"]; ok {
		return "http"
	}
	if _, ok := attrs["http.method"]; ok {
		return "http"
	}
	if _, ok := attrs["db.system"]; ok {
		return "db"
	}
	if _, ok := attrs["messaging.system"]; ok {
		return "messaging"
	}
	return "unknown"
}

func strAttr(attrs model.AttrMap, key string) (string, bool) {
	v, ok := attrs[key]
	if !ok || v.Kind != model.AttrStr || v.Str == "" {
		return "", false
	}
	return v.Str, true
}

func edgeIDFor(caller, callee, protocol string) string {
	return caller + "\x00" + callee + "\x00" + protocol
}

// addToHist buckets a span duration into model.LatencyHist's 16 log-spaced
// (doubling) boundaries, 1ms .. 32768ms (DR-13, DR-39 §39.1).
func addToHist(hist *model.LatencyHist, durationNanos uint64) {
	hist[bucketIndex(durationNanos)]++
}

func bucketIndex(durationNanos uint64) int {
	ms := durationNanos / 1_000_000
	if ms < 1 {
		return 0
	}
	idx := 0
	bound := uint64(1)
	for idx < 15 && ms >= bound*2 {
		bound *= 2
		idx++
	}
	return idx
}
