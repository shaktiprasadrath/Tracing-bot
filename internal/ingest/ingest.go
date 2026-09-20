package ingest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"traceiq/internal/config"
	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// Protocol identifies one of ingest's supported wire formats (F01 §4.3,
// verbatim).
type Protocol string

const (
	ProtocolOTLPGRPC Protocol = "otlp-grpc"
	ProtocolOTLPHTTP Protocol = "otlp-http"
	ProtocolJaeger   Protocol = "jaeger"
	ProtocolZipkin   Protocol = "zipkin"
)

// Receiver is the catalog-fixed interface: accepts spans in any supported
// format, emits model.Span batches via its configured SpanSink (F01 §4.3,
// verbatim).
type Receiver interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Protocol() Protocol
}

// SpanSink receives normalized, tenant-stamped span batches. Declared in
// this package per DR-2's structural-interface rule: the signature mentions
// only model/tenant/stdlib types, so sampler.Sampler.Consume and
// topology.LiveGraph.Consume satisfy it WITHOUT internal/ingest importing
// internal/sampler or internal/topology, and without either of them
// importing internal/ingest. Multiple sinks are registered; ingest fans out
// to all of them independently (FR-F01-11).
type SpanSink interface {
	Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error
}

// Normalizer converts a protocol-specific wire object into model.Batch.
// otlpgrpc/otlphttp implementations are near-identity transforms;
// jaeger/zipkin implementations perform field mapping (F01 §4.3, verbatim).
type Normalizer interface {
	Normalize(raw any) (model.Batch, error)
}

// Server is New's return type — the constructed receiver set (F01 §4.3's
// `func New(...) (*Server, error)`). The register gives the constructor
// signature but never a field list for the struct it returns; this pass
// completes the prior agent's reconstruction of the pipeline described in
// §4.3/§4.4: a bounded per-sink queue (FR-F01-6, DR-28), an injected-clock
// tenant/limits/fan-out pipeline (§4.4's HandleInbound), and a registered
// OTLP gRPC receiver wired to that pipeline.
//
// DESIGN CHOICE (this pass, documented per task instructions): kept the
// prior agent's Server/sinkQueue/metrics/otlpGRPCReceiver shape rather than
// simplifying it away — §4.4's HandleInbound pseudocode is genuinely a
// per-sink-queue, per-protocol-receiver design (FR-F01-6/11), so a Server
// aggregate that owns queues+receivers+metrics is the right shape, not
// overcomplication. What changed from the stub: sinkQueue, metrics and
// otlpGRPCReceiver are now real (queue.go, metrics.go, otlpgrpc.go) instead
// of forward-referenced names with no definition.
type Server struct {
	Receivers []Receiver

	cfg      config.IngestConfig
	clock    model.Clock
	resolver tenant.Resolver
	sinks    []SpanSink
	queues   []*sinkQueue
	metrics  *metrics

	normalizers map[Protocol]Normalizer

	otlpGRPC *otlpGRPCReceiver

	drainCancel func()
	drainWG     sync.WaitGroup
}

// Tenant resolution uses tenant.Resolver (internal/tenant, DR-3) directly —
// ingest declares no resolver interface of its own. ingest.TenantResolver
// is DELETED (DR-3): FromSubject(ctx, subjectTenant) is the sole production
// path; FromDevDefault is legal only under server.profile: dev + loopback +
// ingest.auth.mode: none (DR-3 §3, DR-26 §26.3 rule 3).

// New constructs a Receiver set with an injected clock (DR-31): every
// timeout in this package (enqueue_timeout, TLS reload debounce) reads
// model.Clock rather than calling time.Now/time.After directly, so
// internal/archtest's time-ban check passes and the eval harness's
// VirtualClock can drive ingest deterministically. cfg is
// config.IngestConfig (01 §7's `ingest` block, amended by DR-26/DR-28) —
// F01 §4.3 prints the parameter unqualified as `Config`, resolved to the
// package that actually owns those config keys per DR-2's adjacency table
// (ingest -> config is allowed; ingest defining a second, competing Config
// type would duplicate 01 §7).
func New(cfg config.IngestConfig, clock model.Clock, resolver tenant.Resolver, sinks []SpanSink) (*Server, error) {
	if clock == nil {
		return nil, errors.New("ingest.New: clock must not be nil (DR-31)")
	}
	if resolver == nil {
		return nil, errors.New("ingest.New: resolver must not be nil (DR-3)")
	}
	if len(sinks) == 0 {
		return nil, errors.New("ingest.New: at least one SpanSink must be registered (FR-F01-11)")
	}

	s := &Server{
		cfg:      cfg,
		clock:    clock,
		resolver: resolver,
		sinks:    sinks,
		metrics:  newMetrics(),
	}

	s.queues = make([]*sinkQueue, len(sinks))
	for i, sink := range sinks {
		s.queues[i] = newSinkQueue(sink, cfg.Queue, clock)
	}

	s.normalizers = map[Protocol]Normalizer{
		ProtocolOTLPGRPC: &OTLPNormalizer{},
		ProtocolOTLPHTTP: &OTLPNormalizer{},
		ProtocolZipkin:   &ZipkinNormalizer{},
	}

	s.otlpGRPC = newOTLPGRPCReceiver(s, cfg.OTLPGRPC)
	s.Receivers = []Receiver{s.otlpGRPC}

	return s, nil
}

// Start starts every registered Receiver and the drain loop for every
// sink's queue (§4.4's "one drain goroutine per sink queue, started at
// Receiver.Start").
func (s *Server) Start(ctx context.Context) error {
	drainCtx, cancel := context.WithCancel(ctx)
	s.drainCancel = cancel
	s.drainWG.Add(len(s.queues))
	for _, q := range s.queues {
		go func(q *sinkQueue) {
			defer s.drainWG.Done()
			q.drain(drainCtx)
		}(q)
	}
	for _, r := range s.Receivers {
		if err := r.Start(ctx); err != nil {
			cancel()
			return fmt.Errorf("ingest: starting receiver %s: %w", r.Protocol(), err)
		}
	}
	return nil
}

// Stop stops every registered Receiver and then the drain loops. Receivers
// are stopped (and thus every producer joined) BEFORE drainCancel fires, so
// sinkQueue.drain's post-cancellation flush (queue.go) is guaranteed no
// further push can race it; Stop then waits for every drain goroutine to
// actually finish that flush before returning, so a caller (e.g.
// cmd/traceiq's Shutdown) never observes Stop as complete while a
// already-enqueued batch is still sitting undelivered.
func (s *Server) Stop(ctx context.Context) error {
	var firstErr error
	for _, r := range s.Receivers {
		if err := r.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.drainCancel != nil {
		s.drainCancel()
	}
	s.drainWG.Wait()
	return firstErr
}

// Ingest is §4.4's HandleInbound, exported at the Server level so every
// protocol receiver (otlpGRPCReceiver.Export, a future otlphttp/jaeger/
// zipkin handler) shares one pipeline: normalize -> enforce 01 §8.4 limits
// -> resolve tenant (FR-F01-10) -> stamp -> fan out to every sink's bounded
// queue (FR-F01-6/11).
//
// subjectTenant is the tenant carried by the authenticated principal (API
// key or mTLS SAN) — never a header/body/query parameter (FR-F01-10).
// useDevDefault selects tenant.Resolver.FromDevDefault instead; the caller
// (the receiver, which has access to server.profile/listener/auth.mode) is
// responsible for deciding that per DR-3/DR-26 §26.3 rule 3 — Server here
// only owns config.IngestConfig, not the full config.Config server block.
func (s *Server) Ingest(ctx context.Context, protocol Protocol, raw any, subjectTenant model.TenantID, useDevDefault bool) (model.Batch, error) {
	norm := s.normalizers[protocol]
	if norm == nil {
		err := &IngestError{Reason: ReasonMalformed, Protocol: protocol, Err: fmt.Errorf("no normalizer registered for protocol %q", protocol)}
		s.metrics.spansRejected(protocol, ReasonMalformed, 1)
		return model.Batch{}, err
	}

	batch, err := norm.Normalize(raw)
	if err != nil {
		s.metrics.spansRejected(protocol, ReasonMalformed, 1)
		return model.Batch{}, &IngestError{Reason: ReasonMalformed, Protocol: protocol, Err: err}
	}
	s.metrics.batchReceived(protocol)
	batch.ReceivedUnixNano = uint64(s.clock.Now().UnixNano())

	if err := applyLimits(&batch, s.cfg.Limits); err != nil {
		var ie *IngestError
		reason := ReasonOversize
		if errors.As(err, &ie) {
			reason = ie.Reason
			ie.Protocol = protocol
		}
		s.metrics.spansRejected(protocol, reason, len(batch.Spans))
		return model.Batch{}, err
	}

	var tenantID model.TenantID
	if useDevDefault {
		tenantID, err = s.resolver.FromDevDefault(ctx)
	} else {
		tenantID, err = s.resolver.FromSubject(ctx, subjectTenant)
	}
	if err != nil {
		s.metrics.spansRejected(protocol, ReasonUnauthenticated, len(batch.Spans))
		return model.Batch{}, &IngestError{Reason: ReasonUnauthenticated, Protocol: protocol, Err: err}
	}
	// w17 final-review fix (BLOCKER): a resolver that returns ("", nil) must
	// not be able to open a tenant-less ingest path. otlpgrpc.go's
	// subjectFromContext deliberately returns an EMPTY subject for every auth
	// mode it does not implement, documenting that "tenant.Resolver.FromSubject
	// is expected to reject [it] as UNAUTHENTICATED -- fail-closed, not
	// fail-open" — but the only production Resolver (cmd/traceiq's
	// simpleResolver.FromSubject) was a bare pass-through returning
	// (subjectTenant, nil), so an unauthenticated gRPC caller's spans were
	// accepted and stamped with TenantID(""). Every downstream store, graph and
	// detector then keys that batch under a single shared empty-tenant bucket,
	// merging unrelated senders' telemetry and making it readable by any
	// subject that also carries an empty Tenant. The resolver is fixed too, but
	// the invariant is enforced HERE as well because Resolver is an injected
	// seam: ingest must not depend on which implementation it was handed to
	// stay tenant-isolated (DR-3, DR-5).
	if tenantID == "" {
		s.metrics.spansRejected(protocol, ReasonUnauthenticated, len(batch.Spans))
		return model.Batch{}, &IngestError{Reason: ReasonUnauthenticated, Protocol: protocol, Err: ErrNoTenant}
	}

	batch.Tenant = tenantID
	for i := range batch.Spans {
		batch.Spans[i].Tenant = tenantID
		batch.Spans[i].ReceivedUnixNano = batch.ReceivedUnixNano
	}

	for _, q := range s.queues {
		if err := q.push(ctx, tenantID, batch.Spans, batch.SizeBytes); err != nil {
			var ie *IngestError
			reason := ReasonQueueFull
			if errors.As(err, &ie) {
				reason = ie.Reason
				ie.Protocol = protocol
			}
			s.metrics.spansRejected(protocol, reason, len(batch.Spans))
			return model.Batch{}, err
		}
	}

	s.metrics.spansReceived(protocol, tenantID, len(batch.Spans))
	return batch, nil
}
