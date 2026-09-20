package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/config"
	"traceiq/internal/ingest"
	"traceiq/internal/model"
	"traceiq/internal/rca"
	"traceiq/internal/rca/rules"
	"traceiq/internal/sampler"
	"traceiq/internal/store"
	"traceiq/internal/store/parquet"
	"traceiq/internal/store/sqlite"
	"traceiq/internal/tenant"
	"traceiq/internal/topology"
)

// System is the assembled dev-mode single-binary process (X-OPS §4.1's
// DevMode box): store -> topology -> sampler -> ingest -> anomaly -> rca,
// per the startup order in X-OPS §4.6's mirror-image note and this wave's
// task instructions. internal/api is deliberately NOT wired here — see the
// TODO on Start below and docs/reports/w15-cmd-wiring.md.
type System struct {
	cfg   *config.Config
	clock model.Clock
	logs  *log.Logger

	hot    *sqlite.Store
	cold   *parquet.Store
	tiered *store.TieredStore

	topo     *topology.LiveGraph
	samp     *sampler.Impl
	resolver tenant.Resolver
	ing      *ingest.Server

	baseline anomaly.BaselineStore
	rcaEng   rca.Engine

	registry *simpleRegistry
	httpSrv  *http.Server

	readyIngest  *staticReadiness
	readyStore   *staticReadiness
	readySampler *staticReadiness

	// tickerCancel/tickerDone let Shutdown stop and fully JOIN the finalize
	// ticker before touching anything else (w16 review fix; see Shutdown's
	// X4 comment): it must be independent of bridgeCancel/bg so Shutdown can
	// guarantee the ticker has stopped producing decisions/traces/RED
	// samples before FlushAll runs and before the store/baseline bridges
	// below are told to drain and stop.
	tickerCancel context.CancelFunc
	tickerDone   chan struct{}

	bridgeCancel context.CancelFunc
	bg           sync.WaitGroup

	// shutdownCtx is set by Shutdown before it cancels bridgeCtx, and read
	// by runStoreBridge/runBaselineFeed's post-cancellation drain (via
	// drainContext) to finish outstanding writes with a live context
	// instead of the already-cancelled bridgeCtx. Safe without a mutex: the
	// write below happens-before bridgeCancel() in program order, and each
	// reader only observes it after its own ctx.Done() fires, which is
	// itself sequenced after that cancel call.
	shutdownCtx context.Context
}

// newSystem constructs every component but starts nothing (mirrors
// ingest.New/topology.NewLiveGraph's own construct-then-Start split).
func newSystem(cfg *config.Config) (*System, error) {
	clock := newRealClock()
	logs := log.New(os.Stderr, "traceiq: ", log.LstdFlags|log.Lmicroseconds)

	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("system: creating data_dir %q: %w", cfg.Server.DataDir, err)
	}

	// --- store (config -> store -> ...; X-OPS §4.6 startup order) ---
	if cfg.Store.Hot.Driver != "sqlite" {
		return nil, fmt.Errorf("system: store.hot.driver %q not supported by this dev-mode wiring (only sqlite)", cfg.Store.Hot.Driver)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Store.Hot.SQLite.Path), 0o755); err != nil {
		return nil, fmt.Errorf("system: creating hot store dir: %w", err)
	}
	hot, err := sqlite.Open(cfg.Store.Hot.SQLite.Path)
	if err != nil {
		return nil, fmt.Errorf("system: opening hot store: %w", err)
	}

	if cfg.Store.Cold.Driver != "parquet_local" {
		hot.Close()
		return nil, fmt.Errorf("system: store.cold.driver %q not supported by this dev-mode wiring (only parquet_local)", cfg.Store.Cold.Driver)
	}
	cold, err := parquet.Open(parquet.Config{
		Dir:       cfg.Store.Cold.Parquet.Path,
		Clock:     clock,
		WalRetain: cfg.Store.Cold.Parquet.WALRetain,
	})
	if err != nil {
		hot.Close()
		return nil, fmt.Errorf("system: opening cold store: %w", err)
	}
	tiered := store.NewTieredStore(hot, cold, clock)

	// Startup reconciliation mirrors shutdown order (X-OPS §4.6): cold WAL
	// replay before receivers bind.
	if _, err := cold.ReplayWAL(context.Background()); err != nil {
		logs.Printf("cold store WAL replay: %v", err)
	}

	// --- topology ---
	topo := topology.NewLiveGraph(cfg.Topology, memEdgeSink{}, memEdgeSource{}, clock)

	// --- sampler ---
	samp := sampler.NewImpl(
		toSamplerAssemblyConfig(cfg.Sampler.Assembly),
		toSamplerPolicyConfig(cfg.Sampler.Policy),
		clock,
		sampler.NewMemWAL(),
		normalizeShards(cfg.Sampler.Shards),
	)

	// --- ingest (config -> ... -> ingest; fans out to sampler + topology) ---
	resolver := newTenantResolver(*cfg)
	ing, err := ingest.New(cfg.Ingest, clock, resolver, []ingest.SpanSink{samp, topo})
	if err != nil {
		hot.Close()
		cold.Close()
		return nil, fmt.Errorf("system: constructing ingest: %w", err)
	}

	// --- anomaly (baseline store only in this wave; see doc comment) ---
	baseline := anomaly.NewBaselineStore(toAnomalyConfig(cfg.Anomaly))

	// --- rca: rules reasoner wired unconditionally; an LLM reasoner is
	// optional/best-effort and config-gated (rca.reasoner: llm|auto), and
	// the sibling agent's internal/llm work may not be stable yet, so this
	// wave always falls back to rules-only regardless of cfg.RCA.Reasoner
	// (documented in the report; a real "auto" resolution that probes for
	// an API key and only then prefers llm is a follow-up).
	reasoner := rules.New()
	registry := rca.NewRegistry(noopTraceStore{}, noopLogStore{}, noopMetricStore{}, noopTopologyStore{}, noopMemoryStore{})
	// DR-11/D-X5: wire the real, running sampler instance into rca.Engine's
	// InterestSink via the adapter (interest_adapter.go) instead of passing
	// nil. This is what closes the agent-to-sampler feedback loop in the
	// deployed binary -- see docs/reports/w19-fix-interestsink-wiring.md.
	rcaEng := rca.NewEngine(rca.NewMemJournal(), registry, reasoner, rca.NewMemObjectStore(), newSamplerInterestSink(samp), clock)

	reg := newSimpleRegistry()

	return &System{
		cfg:          cfg,
		clock:        clock,
		logs:         logs,
		hot:          hot,
		cold:         cold,
		tiered:       tiered,
		topo:         topo,
		samp:         samp,
		resolver:     resolver,
		ing:          ing,
		baseline:     baseline,
		rcaEng:       rcaEng,
		registry:     reg,
		readyIngest:  newStaticReadiness("ingest"),
		readyStore:   newStaticReadiness("store"),
		readySampler: newStaticReadiness("sampler"),
	}, nil
}

func normalizeShards(n int) int {
	if n > 0 {
		return n
	}
	return 1 // GOMAXPROCS substitute kept at 1 for dev-mode determinism
}

// Start brings the system up in X-OPS §4.6's mirror-image startup order:
// store is already open (Replay ran in newSystem), so this starts the
// sampler's finalize ticker, the sampler->store bridge, ingest receivers,
// the RED->baseline feed, and the selfobs HTTP server (/healthz, /readyz,
// /metrics).
//
// TODO(internal/api): server.mode/api.endpoint's real REST/MCP/UI surface is
// internal/api. It has since stabilized (go test ./internal/api/... is
// green as of w16), but wiring it into this System is deliberately still
// NOT done here after re-assessing the gap in w16: api.Deps (internal/api/api.go)
// has ~20 fields spanning auth, tenant, remediate, memory, correlate,
// anomaly, eval, sampler, store, cold and k8s, and this System constructs
// concrete implementations for only a handful of those (store/cold/topology/
// sampler/rca). The rest -- most importantly auth.Authenticator/
// auth.Authorizer, which every route but /healthz, /readyz and the static
// SPA is gated on (middleware.go: FAIL CLOSED, nil -> 401/403) -- have NO
// concrete implementation anywhere in the repo yet (internal/auth ships only
// interfaces, per its own doc comments); api.Deps.Remediate/Memory/
// Correlate/Anomaly/Eval/K8s are similarly unconstructed here. Wiring
// api.HTTPServer today with only the fields this System already has would
// therefore produce a process that serves /healthz, /readyz and the static
// UI shell but 401s on every REST/MCP call -- not a working API surface, and
// arguably worse than no wiring at all (it would look configured while
// being non-functional). This is a multi-package, multi-interface wiring
// job, not the "small, well-scoped addition" this review's brief invited a
// direct fix for; it belongs in its own dedicated wave once
// internal/auth grows a concrete dev-mode Authenticator/Authorizer pair
// (and, ideally, config.Config gains a way to enable/disable it, per
// server.mode: api). This selfobs HTTP server remains a deliberate stand-in
// for /healthz and /readyz only.
func (s *System) Start(ctx context.Context) error {
	bridgeCtx, cancel := context.WithCancel(context.Background())
	s.bridgeCancel = cancel

	// The ticker runs under its own context/done-signal, not bridgeCtx/bg
	// (w16 review fix): Shutdown needs to stop and fully join it BEFORE
	// FlushAll and before cancelling the store/baseline bridges, to
	// guarantee those bridges are draining a producer that has genuinely
	// stopped rather than racing a still-running Tick goroutine.
	tickerCtx, tickerCancel := context.WithCancel(context.Background())
	s.tickerCancel = tickerCancel
	s.tickerDone = make(chan struct{})
	go func() {
		defer close(s.tickerDone)
		s.runSamplerTicker(tickerCtx)
	}()

	s.bg.Add(1)
	go s.runStoreBridge(bridgeCtx)

	s.bg.Add(1)
	go s.runBaselineFeed(bridgeCtx)

	if err := s.ing.Start(ctx); err != nil {
		tickerCancel()
		cancel()
		return fmt.Errorf("system: starting ingest: %w", err)
	}

	mux := s.registry.httpMux()
	s.httpSrv = &http.Server{Addr: s.cfg.SelfObs.MetricsEndpoint, Handler: mux}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logs.Printf("selfobs http server: %v", err)
		}
	}()

	s.readyStore.SetReady(true)
	s.readySampler.SetReady(true)
	s.readyIngest.SetReady(true)
	s.registry.RegisterReadiness(s.readyStore)
	s.registry.RegisterReadiness(s.readySampler)
	s.registry.RegisterReadiness(s.readyIngest)

	return nil
}

func (s *System) runSamplerTicker(ctx context.Context) {
	tick := s.cfg.Sampler.Assembly.WheelTick
	if tick <= 0 {
		tick = 250 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.samp.Tick(ctx)
			// w16 review fix: sampler.Manager.DrainRED is the only thing
			// that ever moves a sample out of the internal RED accumulator
			// (fed continuously by Consume's m.red.Accumulate) and onto the
			// REDSamples() channel runBaselineFeed reads -- sampler.Manager.Run
			// calls Tick+DrainRED together for exactly this reason, but this
			// package reimplements its own ticker instead of calling Run and
			// had dropped the DrainRED half, so anomaly's baseline was
			// silently never fed in this wave's wiring despite
			// runBaselineFeed existing and looking correct in isolation.
			s.samp.DrainRED()
		}
	}
}

// runStoreBridge drains Decisions()/Traces() and appends every kept trace
// to the tiered store. DESIGN NOTE (documented, not a hidden assumption):
// sampler.Manager.finalize sends on decisions and, only for Keep==true,
// immediately afterward on tracesCh, and finalize is only ever called
// sequentially from this process's own Tick/FlushAll goroutine (never
// concurrently) -- so reading one decision and then, iff Keep, exactly one
// value off Traces() reconstructs the pairing without sampler.Decision
// carrying its trace inline. This is called out in the report as a
// coupling this bridge relies on.
func (s *System) runStoreBridge(ctx context.Context) {
	defer s.bg.Done()
	decisions := s.samp.Decisions()
	traces := s.samp.Traces()
	for {
		select {
		case <-ctx.Done():
			// w16 review fix: Shutdown only cancels this context (X4) after
			// the ticker has been stopped and joined and FlushAll has
			// returned, so no further write to decisions/traces can happen
			// from here on -- drain whatever is already buffered instead of
			// returning immediately. The naive version of this select
			// raced ctx.Done() against a still-pending decisions/traces
			// read: when both were ready, Go's uniform pseudo-random case
			// selection could pick ctx.Done() and silently drop an
			// already-decided Keep=true trace that was sitting in the
			// channel at shutdown.
			s.drainStoreBridge(decisions, traces)
			return
		case dec, ok := <-decisions:
			if !ok {
				return
			}
			s.applyDecision(dec, traces)
		}
	}
}

// drainStoreBridge empties whatever is currently buffered in decisions/
// traces (guaranteed final by the caller's ctx.Done() precondition -- see
// runStoreBridge).
func (s *System) drainStoreBridge(decisions <-chan sampler.Decision, traces <-chan model.Trace) {
	for {
		select {
		case dec, ok := <-decisions:
			if !ok {
				return
			}
			s.applyDecision(dec, traces)
		default:
			return
		}
	}
}

// applyDecision is runStoreBridge's per-decision body, shared with
// drainStoreBridge. Two deliberate choices, both w16 review fixes closing
// the same class of race:
//
//   - The paired-trace read is a plain blocking receive, not a select
//     against ctx.Done(): sampler.Manager.finalize sends a Keep decision and
//     its trace back to back from the same single-threaded caller
//     (documented on runStoreBridge's doc comment), so once a Keep decision
//     has been dequeued its trace is guaranteed to already be in (or
//     arriving immediately into) the channel -- racing that read against
//     cancellation would reintroduce a drop.
//   - The store write always uses drainContext(), never a loop's own
//     (cancellable) ctx: cancellation only means "stop pulling NEW work off
//     the channel", not "abort a write already committed to". Passing the
//     loop's ctx straight through here meant a decision dequeued by
//     runStoreBridge's normal select case an instant before Shutdown calls
//     bridgeCancel could still have its Append aborted mid-flight with
//     "context canceled" -- observed while testing this fix -- even though
//     the channel-drain race itself was already closed above.
func (s *System) applyDecision(dec sampler.Decision, traces <-chan model.Trace) {
	if !dec.Keep {
		return
	}
	tr := <-traces
	tier := store.ColdSampled
	switch dec.Reason {
	case model.KeepError, model.KeepSlow, model.KeepRare, model.KeepInterest:
		tier = store.ColdAnomalous
	}
	if _, err := s.tiered.Append(s.drainContext(), dec.Tenant, tr, tier, dec.Reason); err != nil {
		s.logs.Printf("store append: %v", err)
	}
}

// drainContext is what applyDecision/baseline-Observe calls use once ctx
// has already been cancelled (the post-shutdown drain path): shutdownCtx is
// set by Shutdown before it cancels bridgeCtx, so it is still live (bounded
// by the shutdown grace deadline) when the drain runs. Falling back to
// context.Background() only matters for tests that call the bridge
// goroutines without going through Shutdown at all.
func (s *System) drainContext() context.Context {
	if s.shutdownCtx != nil {
		return s.shutdownCtx
	}
	return context.Background()
}

// runBaselineFeed drains sampler's RED stream into anomaly's baseline
// store. This is the extent of anomaly wiring in this wave: constructing
// and feeding BaselineStore, not running the detector/grouper eval loop
// (EvalInput construction from windowed RED aggregates is real business
// logic deferred to a follow-up — see docs/reports/w15-cmd-wiring.md).
func (s *System) runBaselineFeed(ctx context.Context) {
	defer s.bg.Done()
	red := s.samp.REDSamples()
	for {
		select {
		case <-ctx.Done():
			// Same reasoning as runStoreBridge's drain: by the time this
			// fires, the ticker is stopped and Shutdown has already called
			// samp.DrainRED() one last time (X4), so nothing further will
			// ever be written to red -- drain what's buffered rather than
			// dropping it on the ctx.Done()-vs-channel-read race.
			s.drainBaselineFeed(red)
			return
		case sample, ok := <-red:
			if !ok {
				return
			}
			// drainContext(), not the loop's own cancellable ctx -- see
			// applyDecision's doc comment for why: cancellation must not be
			// able to abort a write already dequeued and committed to.
			if err := s.baseline.Observe(s.drainContext(), sample.Tenant, sample); err != nil {
				s.logs.Printf("baseline observe: %v", err)
			}
		}
	}
}

func (s *System) drainBaselineFeed(red <-chan model.REDSample) {
	for {
		select {
		case sample, ok := <-red:
			if !ok {
				return
			}
			if err := s.baseline.Observe(s.drainContext(), sample.Tenant, sample); err != nil {
				s.logs.Printf("baseline observe: %v", err)
			}
		default:
			return
		}
	}
}

// Shutdown implements X-OPS §4.6's graceful shutdown order X1-X12,
// restricted to the components this wave actually wires (ingest, sampler,
// topology, cold/hot store, the selfobs HTTP server). rca/remediate
// (X7/X8) have no in-flight work to abort in this wave since neither is
// driven by a live event loop yet; audit (X11) is not wired (internal/api's
// concern). grace bounds the whole sequence; exceeding it still runs every
// remaining step (best effort) rather than aborting outright.
func (s *System) Shutdown(ctx context.Context, grace time.Duration) error {
	deadline := time.Now().Add(grace)
	shutdownCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// Exposed to the store/baseline bridge goroutines' post-cancellation
	// drain (drainContext) so they finish outstanding writes against a live,
	// grace-bounded context instead of the already-cancelled bridgeCtx. Safe
	// without a mutex: this write happens-before tickerCancel/bridgeCancel
	// below in program order, and each reader only observes it after its
	// own ctx.Done() fires, which is itself sequenced after those calls.
	s.shutdownCtx = shutdownCtx

	s.registry.beginShutdown() // X1: /readyz starts failing

	// X2/X3: receivers stop accepting; queues drain.
	if err := s.ing.Stop(shutdownCtx); err != nil {
		s.logs.Printf("shutdown: ingest stop: %v", err)
	}

	// X4: stop the finalize ticker FIRST and wait for it to fully exit
	// before force-completing every open trace (KeepShed). w16 review fix:
	// the ticker previously shared bridgeCancel/bg with the store/baseline
	// bridges and was only asked to stop AFTER FlushAll, with no join --
	// that left a real window where a Tick-driven finalize() could still be
	// producing decisions/traces/RED samples concurrently with, or shortly
	// after, FlushAll's own sweep, and (2) that same lack of ordering meant
	// the store/baseline bridges below could never safely tell "no more
	// writes are coming" from "the channel is momentarily empty" -- so their
	// own ctx.Done()-vs-channel-read select could race a still-arriving
	// write and silently drop it. Stopping and joining the ticker here first
	// makes FlushAll (called next, from this single goroutine) the sole
	// remaining producer, so once it returns the bridges' final drain
	// (runStoreBridge/runBaselineFeed's ctx.Done() case) is draining a
	// genuinely quiesced set of channels rather than guessing.
	if s.tickerCancel != nil {
		s.tickerCancel()
	}
	if s.tickerDone != nil {
		<-s.tickerDone
	}
	if err := s.samp.FlushAll(shutdownCtx); err != nil {
		s.logs.Printf("shutdown: sampler FlushAll: %v", err)
	}
	// Flush any RED samples accumulated since the last tick. w16 review fix:
	// sampler.Manager.DrainRED is the only thing that ever moves a sample
	// out of the internal RED accumulator onto the REDSamples() channel;
	// see runSamplerTicker's per-tick call for the steady-state half of this
	// fix -- without it runBaselineFeed's channel was never fed at all.
	s.samp.DrainRED()
	if s.bridgeCancel != nil {
		s.bridgeCancel()
	}

	// X5: topology flush.
	if err := s.topo.Flush(shutdownCtx); err != nil {
		s.logs.Printf("shutdown: topology flush: %v", err)
	}

	// X6: anomaly baseline checkpoint.
	if _, err := s.baseline.Checkpoint(shutdownCtx); err != nil {
		s.logs.Printf("shutdown: baseline checkpoint: %v", err)
	}

	// selfobs HTTP server.
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			s.logs.Printf("shutdown: selfobs http: %v", err)
		}
	}

	s.bg.Wait()

	// X9: seal open cold blocks and bind them into the hot index (each
	// manifest carries its own Tenant, so Seal + BindColdBlock is used
	// directly here rather than store.TieredStore.SealAndBind, which
	// requires the caller to already know the tenant up front).
	//
	// NOTE: this must list due blocks with DueBlocks (no side effect) and
	// then Seal each one itself, NOT call SealDue: SealDue already seals
	// every due block internally (removing it from the store's open-block
	// map) and returns the IDs it just sealed, so treating that return
	// value as a still-open list and calling Seal on it again fails with
	// "unknown open block" on every entry -- a double-seal, not a real
	// error, but Seal's second call aborts before BindColdBlock ever runs.
	for _, db := range s.cold.DueBlocks(time.Now().Add(24 * time.Hour)) {
		manifest, err := s.cold.Seal(shutdownCtx, db.BlockID)
		if err != nil {
			s.logs.Printf("shutdown: seal %s: %v", db.BlockID, err)
			continue
		}
		if _, err := s.hot.BindColdBlock(shutdownCtx, manifest.Tenant, db.BlockID, manifest); err != nil {
			s.logs.Printf("shutdown: bind %s: %v", db.BlockID, err)
		}
	}
	if err := s.cold.Close(); err != nil {
		s.logs.Printf("shutdown: cold close: %v", err)
	}

	// X10: hot store close (control writer / telemetry writer drain,
	// wal_checkpoint(TRUNCATE) are store/sqlite's own Close concern).
	if err := s.hot.Close(); err != nil {
		s.logs.Printf("shutdown: hot close: %v", err)
	}

	// X12: nothing else to close in this wave (no listeners of our own
	// beyond ingest/selfobs, both already stopped above).
	return nil
}
