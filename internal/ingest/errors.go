package ingest

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RejectReason labels a rejected batch/span for the ingest_spans_rejected_total
// metric (FR-F01-9) and for choosing the gRPC/HTTP status code (§4.4).
type RejectReason string

const (
	ReasonMalformed          RejectReason = "malformed"
	ReasonOversize           RejectReason = "oversize"
	ReasonUnauthenticated    RejectReason = "unauthenticated"
	ReasonQueueFull          RejectReason = "queue_full"
	ReasonQueueFullAfterWait RejectReason = "queue_full_after_wait"
)

// ErrNoTenant is returned when tenant resolution produced an empty
// model.TenantID. DR-3/DR-5 make a non-empty tenant a precondition of every
// tenant-scoped operation, so an empty one is UNAUTHENTICATED, never a
// usable "default" bucket. See Server.Ingest for why this is enforced here
// and not only in the injected tenant.Resolver.
var ErrNoTenant = errors.New("ingest: tenant resolution produced an empty TenantID")

// IngestError is the single error type returned by the pipeline (§4.4's
// HandleInbound). It carries enough to pick a gRPC status code (via
// GRPCStatus, recognized by google.golang.org/grpc/status.FromError) and an
// HTTP status code (via HTTPStatus, for a future otlphttp/jaeger/zipkin
// handler), without every caller re-deriving the mapping.
type IngestError struct {
	Reason   RejectReason
	Protocol Protocol
	Err      error
}

func (e *IngestError) Error() string {
	if e.Protocol != "" {
		return fmt.Sprintf("ingest: %s rejected (%s): %v", e.Protocol, e.Reason, e.Err)
	}
	return fmt.Sprintf("ingest: rejected (%s): %v", e.Reason, e.Err)
}

func (e *IngestError) Unwrap() error { return e.Err }

// GRPCStatus implements the interface google.golang.org/grpc/status.FromError
// recognizes, so an otlpgrpc handler can `return nil, err` directly and get
// the right code (FR-F01-8: INVALID_ARGUMENT; FR-F01-10: UNAUTHENTICATED;
// FR-F01-6: RESOURCE_EXHAUSTED).
func (e *IngestError) GRPCStatus() *status.Status {
	var code codes.Code
	switch e.Reason {
	case ReasonMalformed, ReasonOversize:
		code = codes.InvalidArgument
	case ReasonUnauthenticated:
		code = codes.Unauthenticated
	case ReasonQueueFull, ReasonQueueFullAfterWait:
		code = codes.ResourceExhausted
	default:
		code = codes.Unknown
	}
	return status.New(code, e.Error())
}

// HTTPStatus mirrors GRPCStatus for a future otlphttp/jaeger/zipkin HTTP
// handler (§4.4: 400 / 401 / 429).
func (e *IngestError) HTTPStatus() int {
	switch e.Reason {
	case ReasonMalformed, ReasonOversize:
		return 400
	case ReasonUnauthenticated:
		return 401
	case ReasonQueueFull, ReasonQueueFullAfterWait:
		return 429
	default:
		return 500
	}
}
