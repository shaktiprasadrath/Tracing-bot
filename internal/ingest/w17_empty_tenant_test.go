package ingest

import (
	"context"
	"errors"
	"testing"

	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"traceiq/internal/model"
)

// passthroughResolver is exactly the shape cmd/traceiq's simpleResolver had
// before the w17 final review: an unconditional `return subjectTenant, nil`.
// otlpgrpc.go's subjectFromContext hands Ingest an EMPTY subject tenant for
// every auth mode it does not implement, documenting that FromSubject is
// "expected to reject [it] as UNAUTHENTICATED -- fail-closed, not
// fail-open". A pass-through resolver breaks that expectation silently.
type passthroughResolver struct{}

func (passthroughResolver) FromSubject(ctx context.Context, subjectTenant model.TenantID) (model.TenantID, error) {
	return subjectTenant, nil
}

func (passthroughResolver) FromDevDefault(ctx context.Context) (model.TenantID, error) {
	return "", nil
}

// TestServerIngest_EmptyResolvedTenantIsRejected is the regression test for
// the w17 tenant Blocker. Ingest must not depend on which tenant.Resolver it
// was handed to stay tenant-isolated: a resolver that returns ("", nil) —
// whether through a bug, a stub, or a test double — must not be able to open
// a tenant-less ingest path that stamps spans with TenantID("") and merges
// unrelated senders into one shared bucket.
func TestServerIngest_EmptyResolvedTenantIsRejected(t *testing.T) {
	sink := newFakeSink()
	s := newTestServer(t, passthroughResolver{}, sink)

	req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op"})

	// The FromSubject branch: an unauthenticated caller yields an empty
	// subject tenant, which the pass-through resolver returns verbatim.
	_, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "", false)
	if err == nil {
		t.Fatal("Ingest accepted a batch with an empty resolved tenant; want UNAUTHENTICATED")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonUnauthenticated {
		t.Fatalf("want IngestError{Reason: unauthenticated}, got %v", err)
	}
	if !errors.Is(err, ErrNoTenant) {
		t.Errorf("want the error to wrap ErrNoTenant, got %v", err)
	}
	if code := status.Convert(ie.GRPCStatus().Err()).Code(); code != codes.Unauthenticated {
		t.Errorf("gRPC code = %v, want Unauthenticated", code)
	}

	// The FromDevDefault branch: an unset tenancy.default_tenant must not
	// become an empty-tenant dev bucket either.
	if _, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "", true); err == nil {
		t.Fatal("Ingest accepted a dev-default batch with an empty resolved tenant; want UNAUTHENTICATED")
	}

	// Nothing may have reached the sink.
	if consumed, _, _ := sink.snapshot(); len(consumed) != 0 {
		t.Fatalf("tenant-less spans reached the sink: %+v", consumed)
	}
}
