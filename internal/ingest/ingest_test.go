package ingest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	otlpcollectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"traceiq/internal/config"
	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// ---- test doubles (mirrors internal/topology/livegraph_test.go's style) ----

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time                         { return f.now }
func (f *fakeClock) Since(t time.Time) time.Duration        { return f.now.Sub(t) }
func (f *fakeClock) NewTicker(d time.Duration) model.Ticker { return nil }
func (f *fakeClock) NewTimer(d time.Duration) model.Timer {
	return &fakeTimer{ch: make(chan time.Time, 1)}
}
func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error { return nil }

// fakeTimer fires immediately: NewTimer above pre-loads the channel, so any
// `block_up_to_timeout` wait resolves without a real sleep (deterministic,
// DR-31).
type fakeTimer struct{ ch chan time.Time }

func (t *fakeTimer) C() <-chan time.Time {
	select {
	case t.ch <- time.Now():
	default:
	}
	return t.ch
}
func (t *fakeTimer) Stop() bool               { return true }
func (t *fakeTimer) Reset(time.Duration) bool { return true }

type fakeResolver struct {
	subjectOK  map[model.TenantID]bool
	devDefault model.TenantID
	devErr     error
}

func (r *fakeResolver) FromSubject(ctx context.Context, subjectTenant model.TenantID) (model.TenantID, error) {
	if subjectTenant == "" || !r.subjectOK[subjectTenant] {
		return "", errors.New("no resolvable principal")
	}
	return subjectTenant, nil
}

func (r *fakeResolver) FromDevDefault(ctx context.Context) (model.TenantID, error) {
	if r.devErr != nil {
		return "", r.devErr
	}
	return r.devDefault, nil
}

// fakeSink is driven by sinkQueue's own drain goroutine (Server.Start),
// same as the real pipeline. done is signalled once per Consume call so
// tests can wait for the async drain instead of racing on consumed/tenant.
type fakeSink struct {
	mu       sync.Mutex
	consumed []model.Span
	tenant   model.TenantID
	err      error
	calls    int
	done     chan struct{}
}

func newFakeSink() *fakeSink { return &fakeSink{done: make(chan struct{}, 16)} }

func (s *fakeSink) Consume(ctx context.Context, tid model.TenantID, spans []model.Span) error {
	s.mu.Lock()
	s.calls++
	s.tenant = tid
	s.consumed = append(s.consumed, spans...)
	err := s.err
	s.mu.Unlock()
	s.done <- struct{}{}
	return err
}

func (s *fakeSink) waitConsumed(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sink.Consume")
	}
}

func (s *fakeSink) snapshot() (spans []model.Span, tid model.TenantID, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Span(nil), s.consumed...), s.tenant, s.calls
}

func strVal(s string) *otlpcommonv1.AnyValue {
	return &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: s}}
}

func mkTraceID(b byte) []byte {
	id := make([]byte, 16)
	id[15] = b
	return id
}

func mkSpanID(b byte) []byte {
	id := make([]byte, 8)
	id[7] = b
	return id
}

func mkOTLPRequest(spans ...*otlptracev1.Span) *otlpcollectortracev1.ExportTraceServiceRequest {
	return &otlpcollectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{
			{
				Resource: &otlpresourcev1.Resource{
					Attributes: []*otlpcommonv1.KeyValue{
						{Key: "service.name", Value: strVal("checkout")},
					},
				},
				ScopeSpans: []*otlptracev1.ScopeSpans{
					{Spans: spans},
				},
			},
		},
	}
}

// ---- FR-F01-7 / AC-F01-1/5: OTLP batch -> correct model.Span fields ----

func TestOTLPNormalizer_ConvertsFieldsAndPreservesAttributes(t *testing.T) {
	sp := &otlptracev1.Span{
		TraceId:           mkTraceID(1),
		SpanId:            mkSpanID(1),
		ParentSpanId:      mkSpanID(2),
		Name:              "GET /checkout",
		Kind:              otlptracev1.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: 1000,
		EndTimeUnixNano:   2000,
		Status:            &otlptracev1.Status{Code: otlptracev1.Status_STATUS_CODE_OK},
		Attributes: []*otlpcommonv1.KeyValue{
			{Key: "http.method", Value: strVal("GET")},
			{Key: "http.status_code", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: 200}}},
			{Key: "http.success", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}}},
			{Key: "http.ratio", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_DoubleValue{DoubleValue: 0.5}}},
			{Key: "http.raw", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: []byte{1, 2, 3}}}},
		},
	}
	req := mkOTLPRequest(sp)

	n := &OTLPNormalizer{}
	batch, err := n.Normalize(req)
	if err != nil {
		t.Fatalf("Normalize: unexpected error: %v", err)
	}
	if len(batch.Spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(batch.Spans))
	}
	got := batch.Spans[0]

	wantTraceID := model.TraceID{}
	copy(wantTraceID[:], mkTraceID(1))
	if got.TraceID != wantTraceID {
		t.Errorf("TraceID = %x, want %x", got.TraceID, wantTraceID)
	}
	wantSpanID := model.SpanID{}
	copy(wantSpanID[:], mkSpanID(1))
	if got.SpanID != wantSpanID {
		t.Errorf("SpanID = %x, want %x", got.SpanID, wantSpanID)
	}
	wantParentID := model.SpanID{}
	copy(wantParentID[:], mkSpanID(2))
	if got.ParentSpanID != wantParentID {
		t.Errorf("ParentSpanID = %x, want %x", got.ParentSpanID, wantParentID)
	}
	if got.Name != "GET /checkout" {
		t.Errorf("Name = %q, want %q", got.Name, "GET /checkout")
	}
	if got.Kind != model.SpanKindServer {
		t.Errorf("Kind = %v, want %v", got.Kind, model.SpanKindServer)
	}
	if got.StartUnixNano != 1000 || got.EndUnixNano != 2000 {
		t.Errorf("Start/End = %d/%d, want 1000/2000", got.StartUnixNano, got.EndUnixNano)
	}
	if got.Status.Code != model.StatusOk {
		t.Errorf("Status.Code = %v, want StatusOk", got.Status.Code)
	}
	if got.Resource == nil || got.Resource.ServiceName != "checkout" {
		t.Fatalf("Resource.ServiceName not preserved: %+v", got.Resource)
	}

	// FR-F01-7: attribute key, value AND type preserved verbatim.
	if v := got.Attrs["http.method"]; v.Kind != model.AttrStr || v.Str != "GET" {
		t.Errorf("http.method = %+v, want AttrStr GET", v)
	}
	if v := got.Attrs["http.status_code"]; v.Kind != model.AttrInt || v.Num != 200 {
		t.Errorf("http.status_code = %+v, want AttrInt 200", v)
	}
	if v := got.Attrs["http.success"]; v.Kind != model.AttrBool || v.Num != 1 {
		t.Errorf("http.success = %+v, want AttrBool true", v)
	}
	if v := got.Attrs["http.ratio"]; v.Kind != model.AttrFloat || v.Float != 0.5 {
		t.Errorf("http.ratio = %+v, want AttrFloat 0.5", v)
	}
	if v := got.Attrs["http.raw"]; v.Kind != model.AttrBytes || string(v.Bytes) != "\x01\x02\x03" {
		t.Errorf("http.raw = %+v, want AttrBytes [1 2 3]", v)
	}
}

func TestOTLPNormalizer_NestedArrayAndMapAttributesPreserved(t *testing.T) {
	sp := &otlptracev1.Span{
		TraceId: mkTraceID(1),
		SpanId:  mkSpanID(1),
		Name:    "op",
		Attributes: []*otlpcommonv1.KeyValue{
			{Key: "tags", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_ArrayValue{
				ArrayValue: &otlpcommonv1.ArrayValue{Values: []*otlpcommonv1.AnyValue{strVal("a"), strVal("b")}},
			}}},
			{Key: "meta", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_KvlistValue{
				KvlistValue: &otlpcommonv1.KeyValueList{Values: []*otlpcommonv1.KeyValue{{Key: "k", Value: strVal("v")}}},
			}}},
		},
	}
	req := mkOTLPRequest(sp)

	batch, err := (&OTLPNormalizer{}).Normalize(req)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	attrs := batch.Spans[0].Attrs
	tags := attrs["tags"]
	if tags.Kind != model.AttrSlice || len(tags.List) != 2 || tags.List[0].Str != "a" || tags.List[1].Str != "b" {
		t.Errorf("tags = %+v, want AttrSlice [a b]", tags)
	}
	meta := attrs["meta"]
	if meta.Kind != model.AttrMapK || meta.Map["k"].Str != "v" {
		t.Errorf("meta = %+v, want AttrMapK {k: v}", meta)
	}
}

// ---- FR-F01-8: malformed batch rejected, batch-scoped, right error ----

func TestOTLPNormalizer_RejectsMalformedIDs(t *testing.T) {
	tests := []struct {
		name string
		sp   *otlptracev1.Span
	}{
		{"all-zero trace id", &otlptracev1.Span{TraceId: make([]byte, 16), SpanId: mkSpanID(1)}},
		{"wrong-length trace id", &otlptracev1.Span{TraceId: []byte{1, 2, 3}, SpanId: mkSpanID(1)}},
		{"all-zero span id", &otlptracev1.Span{TraceId: mkTraceID(1), SpanId: make([]byte, 8)}},
		{"wrong-length span id", &otlptracev1.Span{TraceId: mkTraceID(1), SpanId: []byte{1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&OTLPNormalizer{}).Normalize(mkOTLPRequest(tt.sp))
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestOTLPNormalizer_RejectsWrongRawType(t *testing.T) {
	_, err := (&OTLPNormalizer{}).Normalize("not a request")
	if err == nil {
		t.Fatal("want error for wrong raw type, got nil")
	}
}

// ---- applyLimits: oversize batch / attribute truncation vs reject ----

func TestApplyLimits_OversizeBatchRejected(t *testing.T) {
	batch := model.Batch{Spans: make([]model.Span, 3)}
	lim := config.IngestLimitsConfig{MaxSpansPerBatch: 2}

	err := applyLimits(&batch, lim)
	if err == nil {
		t.Fatal("want error for batch exceeding max_spans_per_batch")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonOversize {
		t.Errorf("want IngestError{Reason: oversize}, got %v", err)
	}
}

func TestApplyLimits_TruncatesOversizeAttributeByDefault(t *testing.T) {
	batch := model.Batch{Spans: []model.Span{{
		Attrs: model.AttrMap{"big": {Kind: model.AttrStr, Str: "0123456789"}},
	}}}
	lim := config.IngestLimitsConfig{MaxAttributeValueBytes: 4, OversizeAttr: "truncate"}

	if err := applyLimits(&batch, lim); err != nil {
		t.Fatalf("applyLimits: unexpected error: %v", err)
	}
	sp := batch.Spans[0]
	if !sp.Truncated {
		t.Error("want Span.Truncated == true")
	}
	if got := sp.Attrs["big"].Str; got != "0123" {
		t.Errorf("truncated value = %q, want %q", got, "0123")
	}
}

func TestApplyLimits_RejectsOversizeAttributeWhenConfiguredStrict(t *testing.T) {
	batch := model.Batch{Spans: []model.Span{{
		Attrs: model.AttrMap{"big": {Kind: model.AttrStr, Str: "0123456789"}},
	}}}
	lim := config.IngestLimitsConfig{MaxAttributeValueBytes: 4, OversizeAttr: "reject"}

	err := applyLimits(&batch, lim)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonOversize {
		t.Errorf("want IngestError{Reason: oversize}, got %v", err)
	}
}

func TestApplyLimits_TruncatesExcessAttributeCount(t *testing.T) {
	batch := model.Batch{Spans: []model.Span{{
		Attrs: model.AttrMap{"a": {Kind: model.AttrStr, Str: "1"}, "b": {Kind: model.AttrStr, Str: "2"}, "c": {Kind: model.AttrStr, Str: "3"}},
	}}}
	lim := config.IngestLimitsConfig{MaxAttributesPerSpan: 2}

	if err := applyLimits(&batch, lim); err != nil {
		t.Fatalf("applyLimits: unexpected error: %v", err)
	}
	sp := batch.Spans[0]
	if len(sp.Attrs) != 2 {
		t.Errorf("len(Attrs) = %d, want 2", len(sp.Attrs))
	}
	if sp.DroppedAttrsCount != 1 {
		t.Errorf("DroppedAttrsCount = %d, want 1", sp.DroppedAttrsCount)
	}
	if !sp.Truncated {
		t.Error("want Span.Truncated == true")
	}
}

// FR-F01-8/AC-F01-6, §4.4's sizeOf(batch) > max_batch_bytes check (regression
// for the review-pass fix: MaxRequestBytes was populated on model.Batch but
// never actually enforced by applyLimits).
func TestApplyLimits_OversizeRequestBytesRejected(t *testing.T) {
	batch := model.Batch{Spans: []model.Span{{}}, SizeBytes: 1000}
	lim := config.IngestLimitsConfig{MaxRequestBytes: 500}

	err := applyLimits(&batch, lim)
	if err == nil {
		t.Fatal("want error for batch exceeding max_request_bytes")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonOversize {
		t.Errorf("want IngestError{Reason: oversize}, got %v", err)
	}
}

// ---- FR-F01-5: Zipkin v2 JSON conversion ----

func TestZipkinNormalizer_ConvertsFields(t *testing.T) {
	body := []byte(`[{
		"traceId": "0000000000000001",
		"id": "0000000000000002",
		"parentId": "0000000000000003",
		"name": "GET /checkout",
		"kind": "SERVER",
		"timestamp": 1700000000000000,
		"duration": 1500000,
		"localEndpoint": {"serviceName": "checkout"},
		"tags": {"http.method": "GET", "http.status_code": "200"}
	}]`)

	batch, err := (&ZipkinNormalizer{}).Normalize(body)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(batch.Spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(batch.Spans))
	}
	sp := batch.Spans[0]

	var wantTraceID model.TraceID
	wantTraceID[15] = 1
	if sp.TraceID != wantTraceID {
		t.Errorf("TraceID = %x, want %x", sp.TraceID, wantTraceID)
	}
	var wantSpanID model.SpanID
	wantSpanID[7] = 2
	if sp.SpanID != wantSpanID {
		t.Errorf("SpanID = %x, want %x", sp.SpanID, wantSpanID)
	}
	var wantParentID model.SpanID
	wantParentID[7] = 3
	if sp.ParentSpanID != wantParentID {
		t.Errorf("ParentSpanID = %x, want %x", sp.ParentSpanID, wantParentID)
	}
	if sp.Kind != model.SpanKindServer {
		t.Errorf("Kind = %v, want SpanKindServer", sp.Kind)
	}
	if sp.Resource == nil || sp.Resource.ServiceName != "checkout" {
		t.Fatalf("ServiceName not preserved: %+v", sp.Resource)
	}
	if sp.StartUnixNano != 1700000000000000000 {
		t.Errorf("StartUnixNano = %d, want micros*1000", sp.StartUnixNano)
	}
	if sp.EndUnixNano != sp.StartUnixNano+1500000000 {
		t.Errorf("EndUnixNano = %d, want start+duration*1000", sp.EndUnixNano)
	}
	if v := sp.Attrs["http.method"]; v.Kind != model.AttrStr || v.Str != "GET" {
		t.Errorf("http.method = %+v, want AttrStr GET", v)
	}
	if v := sp.Attrs["http.status_code"]; v.Kind != model.AttrStr || v.Str != "200" {
		t.Errorf("http.status_code = %+v, want AttrStr 200", v)
	}
}

func TestZipkinNormalizer_RejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `not json`},
		{"bad trace id length", `[{"traceId":"abc","id":"0000000000000001","name":"op"}]`},
		{"all-zero span id", `[{"traceId":"00000000000000000000000000000001","id":"0000000000000000","name":"op"}]`},
		{"empty body", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&ZipkinNormalizer{}).Normalize([]byte(tt.body))
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestZipkinNormalizer_RejectsWrongRawType(t *testing.T) {
	_, err := (&ZipkinNormalizer{}).Normalize(123)
	if err == nil {
		t.Fatal("want error for wrong raw type, got nil")
	}
}

// ---- FR-F01-5: B3 -> W3C traceparent ----

func TestB3HeadersToTraceParent(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "single header sampled",
			headers: map[string]string{"b3": "80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1"},
			want:    "00-80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-01",
		},
		{
			name:    "multi header not sampled",
			headers: map[string]string{"X-B3-TraceId": "80f198ee56343ba864fe8b2a57d3eff7", "X-B3-SpanId": "e457b5a2e4d86bd1", "X-B3-Sampled": "0"},
			want:    "00-80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-00",
		},
		{
			name:    "64-bit trace id left-padded",
			headers: map[string]string{"X-B3-TraceId": "64fe8b2a57d3eff7", "X-B3-SpanId": "e457b5a2e4d86bd1"},
			want:    "00-000000000000000064fe8b2a57d3eff7-e457b5a2e4d86bd1-00",
		},
		{
			name:    "missing span id",
			headers: map[string]string{"X-B3-TraceId": "80f198ee56343ba864fe8b2a57d3eff7"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := B3HeadersToTraceParent(tt.headers)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- Server.Ingest: pipeline wiring (tenant stamping, rejection codes) ----

func newTestServer(t *testing.T, resolver tenant.Resolver, sinks ...SpanSink) *Server {
	t.Helper()
	s, err := New(config.IngestConfig{
		Queue: config.IngestQueueConfig{Capacity: 4, MaxBytes: 1 << 20, EnqueueTimeout: time.Millisecond, OverflowPolicy: "shed"},
		Limits: config.IngestLimitsConfig{
			MaxSpansPerBatch:       1000,
			MaxAttributesPerSpan:   100,
			MaxAttributeValueBytes: 4096,
			MaxAttributeKeyBytes:   256,
			OversizeAttr:           "truncate",
		},
	}, &fakeClock{now: time.Unix(1000, 0)}, resolver, sinks)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(cancel)
	return s
}

func TestServerIngest_StampsResolvedTenant(t *testing.T) {
	sink := newFakeSink()
	resolver := &fakeResolver{subjectOK: map[model.TenantID]bool{"tenant-a": true}}
	s := newTestServer(t, resolver, sink)

	req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op"})
	batch, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "tenant-a", false)
	if err != nil {
		t.Fatalf("Ingest: unexpected error: %v", err)
	}
	if batch.Tenant != "tenant-a" {
		t.Errorf("batch.Tenant = %q, want tenant-a", batch.Tenant)
	}
	sink.waitConsumed(t)
	consumed, _, _ := sink.snapshot()
	if len(consumed) != 1 || consumed[0].Tenant != "tenant-a" {
		t.Fatalf("sink did not receive tenant-stamped span: %+v", consumed)
	}
	if got := s.metrics.Received(ProtocolOTLPGRPC, "tenant-a"); got != 1 {
		t.Errorf("metrics.Received = %d, want 1", got)
	}
}

func TestServerIngest_UnresolvableTenantRejectedUnauthenticated(t *testing.T) {
	sink := newFakeSink()
	resolver := &fakeResolver{} // no subjects allowed
	s := newTestServer(t, resolver, sink)

	req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op"})
	_, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "tenant-x", false)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonUnauthenticated {
		t.Fatalf("want IngestError{Reason: unauthenticated}, got %v", err)
	}
	if code := status.Convert(ie.GRPCStatus().Err()).Code(); code != codes.Unauthenticated {
		t.Errorf("gRPC code = %v, want Unauthenticated", code)
	}
	if sink.calls != 0 {
		t.Errorf("sink.calls = %d, want 0 (rejected before fan-out)", sink.calls)
	}
}

func TestServerIngest_MalformedBatchRejectedInvalidArgument(t *testing.T) {
	sink := newFakeSink()
	resolver := &fakeResolver{subjectOK: map[model.TenantID]bool{"tenant-a": true}}
	s := newTestServer(t, resolver, sink)

	_, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, "not a request", "tenant-a", false)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonMalformed {
		t.Fatalf("want IngestError{Reason: malformed}, got %v", err)
	}
	if code := status.Convert(ie.GRPCStatus().Err()).Code(); code != codes.InvalidArgument {
		t.Errorf("gRPC code = %v, want InvalidArgument", code)
	}
}

func TestServerIngest_OversizeBatchRejectedInvalidArgument(t *testing.T) {
	sink := newFakeSink()
	resolver := &fakeResolver{subjectOK: map[model.TenantID]bool{"tenant-a": true}}
	s, err := New(config.IngestConfig{
		Queue:  config.IngestQueueConfig{Capacity: 4, OverflowPolicy: "shed"},
		Limits: config.IngestLimitsConfig{MaxSpansPerBatch: 1},
	}, &fakeClock{now: time.Unix(1000, 0)}, resolver, []SpanSink{sink})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := mkOTLPRequest(
		&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op1"},
		&otlptracev1.Span{TraceId: mkTraceID(2), SpanId: mkSpanID(2), Name: "op2"},
	)
	_, err = s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "tenant-a", false)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonOversize {
		t.Fatalf("want IngestError{Reason: oversize}, got %v", err)
	}
	if code := status.Convert(ie.GRPCStatus().Err()).Code(); code != codes.InvalidArgument {
		t.Errorf("gRPC code = %v, want InvalidArgument", code)
	}
}

func TestServerIngest_DevDefaultTenant(t *testing.T) {
	sink := newFakeSink()
	resolver := &fakeResolver{devDefault: "default"}
	s := newTestServer(t, resolver, sink)

	req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op"})
	batch, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "", true)
	if err != nil {
		t.Fatalf("Ingest: unexpected error: %v", err)
	}
	if batch.Tenant != "default" {
		t.Errorf("batch.Tenant = %q, want default", batch.Tenant)
	}
}

// FR-F01-11: fan-out to every sink independently.
func TestServerIngest_FansOutToEverySink(t *testing.T) {
	sinkA, sinkB := newFakeSink(), newFakeSink()
	resolver := &fakeResolver{subjectOK: map[model.TenantID]bool{"tenant-a": true}}
	s := newTestServer(t, resolver, sinkA, sinkB)

	req := mkOTLPRequest(&otlptracev1.Span{TraceId: mkTraceID(1), SpanId: mkSpanID(1), Name: "op"})
	if _, err := s.Ingest(context.Background(), ProtocolOTLPGRPC, req, "tenant-a", false); err != nil {
		t.Fatalf("Ingest: unexpected error: %v", err)
	}
	sinkA.waitConsumed(t)
	sinkB.waitConsumed(t)
	_, _, callsA := sinkA.snapshot()
	_, _, callsB := sinkB.snapshot()
	if callsA != 1 || callsB != 1 {
		t.Errorf("sinkA.calls=%d sinkB.calls=%d, want 1/1", callsA, callsB)
	}
}

// FR-F01-6/AC-F01-4: overflow_policy "shed" rejects immediately once the
// queue is full, RESOURCE_EXHAUSTED.
func TestSinkQueue_ShedPolicyRejectsWhenFull(t *testing.T) {
	sink := newFakeSink()
	clock := &fakeClock{now: time.Unix(1000, 0)}
	q := newSinkQueue(sink, config.IngestQueueConfig{Capacity: 1, OverflowPolicy: "shed"}, clock)

	if err := q.push(context.Background(), "t", []model.Span{{}}, 0); err != nil {
		t.Fatalf("first push: unexpected error: %v", err)
	}
	err := q.push(context.Background(), "t", []model.Span{{}}, 0)
	if err == nil {
		t.Fatal("want error on second push into a full shed-policy queue, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonQueueFull {
		t.Fatalf("want IngestError{Reason: queue_full}, got %v", err)
	}
	if code := status.Convert(ie.GRPCStatus().Err()).Code(); code != codes.ResourceExhausted {
		t.Errorf("gRPC code = %v, want ResourceExhausted", code)
	}
}

// AC-F01-4: block_up_to_timeout waits at most enqueue_timeout, then sheds if
// still full.
func TestSinkQueue_BlockUpToTimeoutShedsAfterWait(t *testing.T) {
	sink := newFakeSink()
	clock := &fakeClock{now: time.Unix(1000, 0)}
	q := newSinkQueue(sink, config.IngestQueueConfig{Capacity: 1, OverflowPolicy: "block_up_to_timeout", EnqueueTimeout: time.Millisecond}, clock)

	if err := q.push(context.Background(), "t", []model.Span{{}}, 0); err != nil {
		t.Fatalf("first push: unexpected error: %v", err)
	}
	// Second push: queue is still full (nothing drains it), so it must wait
	// out the fake timer and then shed with queue_full_after_wait.
	err := q.push(context.Background(), "t", []model.Span{{}}, 0)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var ie *IngestError
	if !errors.As(err, &ie) || ie.Reason != ReasonQueueFullAfterWait {
		t.Fatalf("want IngestError{Reason: queue_full_after_wait}, got %v", err)
	}
}

func TestNew_RejectsMissingDependencies(t *testing.T) {
	resolver := &fakeResolver{}
	sink := newFakeSink()
	cfg := config.IngestConfig{}

	if _, err := New(cfg, nil, resolver, []SpanSink{sink}); err == nil {
		t.Error("want error for nil clock")
	}
	if _, err := New(cfg, &fakeClock{}, nil, []SpanSink{sink}); err == nil {
		t.Error("want error for nil resolver")
	}
	if _, err := New(cfg, &fakeClock{}, resolver, nil); err == nil {
		t.Error("want error for no sinks")
	}
}
