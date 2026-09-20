package ingest

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	otlpcollectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// otlpGRPCReceiver is F01 §4.1's `otlpgrpc.Receiver` (":4317
// TraceService.Export"), implementing both ingest.Receiver and the
// generated otlpcollectortracev1.TraceServiceServer -- Export is the "OTLP
// receiver (gRPC handler accepting ExportTraceServiceRequest)" the
// SCOPE_LOCK task calls for, wired to Server.Ingest (§4.4's HandleInbound).
type otlpGRPCReceiver struct {
	otlpcollectortracev1.UnimplementedTraceServiceServer

	parent *Server
	cfg    config.IngestReceiverConfig

	grpcServer *grpc.Server
	listener   net.Listener
}

func newOTLPGRPCReceiver(parent *Server, cfg config.IngestReceiverConfig) *otlpGRPCReceiver {
	return &otlpGRPCReceiver{parent: parent, cfg: cfg}
}

func (r *otlpGRPCReceiver) Protocol() Protocol { return ProtocolOTLPGRPC }

// Start binds the configured listener and serves TraceService.Export. A
// disabled receiver (cfg.Enabled == false, e.g. the migration-only
// protocols' empty-endpoint-disables convention extended here for
// uniformity) is a no-op, matching the Portability NFR's "no code fork
// between modes" -- the same Receiver either binds a real socket
// (gateway/single mode) or stays dormant.
func (r *otlpGRPCReceiver) Start(ctx context.Context) error {
	if !r.cfg.Enabled || r.cfg.Endpoint == "" {
		return nil
	}
	lis, err := net.Listen("tcp", r.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("ingest: otlpgrpc: listen %s: %w", r.cfg.Endpoint, err)
	}
	r.listener = lis
	// TLS (§6 "Transport": TLS 1.3 minimum on every listener) is not wired
	// in this pass -- see docs/reports/w9-ingest-cont.md.
	r.grpcServer = grpc.NewServer()
	otlpcollectortracev1.RegisterTraceServiceServer(r.grpcServer, r)
	go func() {
		_ = r.grpcServer.Serve(lis)
	}()
	return nil
}

func (r *otlpGRPCReceiver) Stop(ctx context.Context) error {
	if r.grpcServer != nil {
		r.grpcServer.GracefulStop()
	}
	return nil
}

// Export is the TraceService.Export gRPC handler (FR-F01-1). It resolves
// the tenant-bearing principal from the request context (auth is NOT fully
// wired in this pass -- see subjectFromContext below and the report) and
// delegates everything else to Server.Ingest, the shared §4.4 pipeline.
func (r *otlpGRPCReceiver) Export(ctx context.Context, req *otlpcollectortracev1.ExportTraceServiceRequest) (*otlpcollectortracev1.ExportTraceServiceResponse, error) {
	subjectTenant, useDevDefault := subjectFromContext(ctx, r.parent.cfg.Auth)

	if _, err := r.parent.Ingest(ctx, ProtocolOTLPGRPC, req, subjectTenant, useDevDefault); err != nil {
		var ie *IngestError
		if errors.As(err, &ie) {
			return nil, ie.GRPCStatus().Err()
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &otlpcollectortracev1.ExportTraceServiceResponse{}, nil
}

// subjectFromContext extracts the authenticated principal's tenant from ctx.
//
// SIMPLIFICATION (documented, out of this pass's scope): full principal
// extraction (bearer-token lookup against auth.tokens_file, or mTLS SAN via
// peer.FromContext) belongs to X-SEC's auth package, not yet built. This
// stub only implements the `auth.mode: none` dev-default branch
// (DR-3/DR-26 §26.3 rule 3's "server.profile: dev + loopback +
// ingest.auth.mode: none" -- the loopback/profile check itself is the
// caller/deployment's responsibility, not ingest's, per this file's Start
// having no access to config.ServerConfig). Every other mode currently
// returns an empty subject, which tenant.Resolver.FromSubject is expected
// to reject as UNAUTHENTICATED -- fail-closed, not fail-open.
func subjectFromContext(ctx context.Context, auth config.IngestAuthConfig) (model.TenantID, bool) {
	if auth.Mode == "none" {
		return "", true
	}
	return "", false
}
