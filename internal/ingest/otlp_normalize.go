package ingest

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	otlpcollectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"traceiq/internal/model"
)

// OTLPNormalizer implements Normalizer for the OTLP gRPC and OTLP HTTP
// receivers (F01 §4.3: "otlpgrpc/otlphttp implementations are near-identity
// transforms"). Normalize preserves every resource/span attribute key,
// value and value type unchanged -- no rename, no drop, no type coercion
// (FR-F01-7, D-D3's evidence-fidelity fix).
type OTLPNormalizer struct{}

// Normalize accepts *otlpcollectortracev1.ExportTraceServiceRequest (raw is
// `any` per the Normalizer interface, matching F01 §4.3's protocol-agnostic
// signature; the OTLP receivers are the only callers that satisfy this
// concrete type).
func (n *OTLPNormalizer) Normalize(raw any) (model.Batch, error) {
	req, ok := raw.(*otlpcollectortracev1.ExportTraceServiceRequest)
	if !ok {
		return model.Batch{}, fmt.Errorf("ingest: OTLPNormalizer.Normalize: expected *v1.ExportTraceServiceRequest, got %T", raw)
	}

	var spans []model.Span
	for _, rs := range req.GetResourceSpans() {
		res := convertResource(rs.GetResource())
		for _, ss := range rs.GetScopeSpans() {
			scope := convertScope(ss.GetScope())
			for _, sp := range ss.GetSpans() {
				span, err := convertOTLPSpan(sp, res, scope)
				if err != nil {
					return model.Batch{}, err
				}
				spans = append(spans, span)
			}
		}
	}

	return model.Batch{
		Spans:        spans,
		SourceFormat: model.SourceOTLP,
		SizeBytes:    uint32(proto.Size(req)),
	}, nil
}

func convertOTLPSpan(sp *otlptracev1.Span, res *model.Resource, scope *model.Scope) (model.Span, error) {
	traceID, err := convertTraceID(sp.GetTraceId())
	if err != nil {
		return model.Span{}, fmt.Errorf("ingest: OTLPNormalizer.Normalize: span %q: %w", sp.GetName(), err)
	}
	spanID, err := convertSpanID(sp.GetSpanId())
	if err != nil {
		return model.Span{}, fmt.Errorf("ingest: OTLPNormalizer.Normalize: span %q: %w", sp.GetName(), err)
	}
	var parentID model.SpanID
	if len(sp.GetParentSpanId()) > 0 {
		parentID, err = convertSpanID(sp.GetParentSpanId())
		if err != nil {
			return model.Span{}, fmt.Errorf("ingest: OTLPNormalizer.Normalize: span %q: invalid parent_span_id: %w", sp.GetName(), err)
		}
	}

	return model.Span{
		TraceID:            traceID,
		SpanID:             spanID,
		ParentSpanID:       parentID,
		TraceState:         sp.GetTraceState(),
		Flags:              sp.GetFlags(),
		Name:               sp.GetName(),
		Kind:               model.SpanKind(sp.GetKind()),
		StartUnixNano:      sp.GetStartTimeUnixNano(),
		EndUnixNano:        sp.GetEndTimeUnixNano(),
		Status:             convertStatus(sp.GetStatus()),
		Attrs:              convertAttrs(sp.GetAttributes()),
		Resource:           res,
		Scope:              scope,
		Events:             convertEvents(sp.GetEvents()),
		Links:              convertLinks(sp.GetLinks()),
		DroppedAttrsCount:  sp.GetDroppedAttributesCount(),
		DroppedEventsCount: sp.GetDroppedEventsCount(),
		DroppedLinksCount:  sp.GetDroppedLinksCount(),
		SourceFormat:       model.SourceOTLP,
	}, nil
}

// convertTraceID/convertSpanID reject the all-zero and wrong-length IDs
// OTLP itself defines as invalid (trace.pb.go's doc comment on Span.TraceId/
// SpanId: "An ID with all zeroes OR of length other than 16/8 bytes is
// considered invalid"), which is FR-F01-8's "schema violation" case.
func convertTraceID(b []byte) (model.TraceID, error) {
	var id model.TraceID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid trace_id: want %d bytes, got %d", len(id), len(b))
	}
	copy(id[:], b)
	if id == (model.TraceID{}) {
		return id, fmt.Errorf("invalid trace_id: all-zero")
	}
	return id, nil
}

func convertSpanID(b []byte) (model.SpanID, error) {
	var id model.SpanID
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid span_id: want %d bytes, got %d", len(id), len(b))
	}
	copy(id[:], b)
	if id == (model.SpanID{}) {
		return id, fmt.Errorf("invalid span_id: all-zero")
	}
	return id, nil
}

func convertResource(r *otlpresourcev1.Resource) *model.Resource {
	if r == nil {
		return nil
	}
	res := &model.Resource{Attrs: convertAttrs(r.GetAttributes())}
	for _, kv := range r.GetAttributes() {
		switch kv.GetKey() {
		case "service.name":
			res.ServiceName = kv.GetValue().GetStringValue()
		case "service.version":
			res.ServiceVersion = kv.GetValue().GetStringValue()
		case "service.namespace":
			res.Namespace = kv.GetValue().GetStringValue()
		case "deployment.environment.name":
			res.Env = kv.GetValue().GetStringValue()
		}
	}
	return res
}

func convertScope(sc *otlpcommonv1.InstrumentationScope) *model.Scope {
	if sc == nil {
		return nil
	}
	return &model.Scope{
		Name:    sc.GetName(),
		Version: sc.GetVersion(),
		Attrs:   convertAttrs(sc.GetAttributes()),
	}
}

func convertStatus(s *otlptracev1.Status) model.Status {
	if s == nil {
		return model.Status{}
	}
	return model.Status{Code: model.StatusCode(s.GetCode()), Message: s.GetMessage()}
}

func convertEvents(evs []*otlptracev1.Span_Event) []model.SpanEvent {
	if len(evs) == 0 {
		return nil
	}
	out := make([]model.SpanEvent, len(evs))
	for i, e := range evs {
		out[i] = model.SpanEvent{
			Name:         e.GetName(),
			TimeUnixNano: e.GetTimeUnixNano(),
			Attrs:        convertAttrs(e.GetAttributes()),
		}
	}
	return out
}

func convertLinks(lnks []*otlptracev1.Span_Link) []model.SpanLink {
	if len(lnks) == 0 {
		return nil
	}
	out := make([]model.SpanLink, 0, len(lnks))
	for _, l := range lnks {
		var tid model.TraceID
		copy(tid[:], l.GetTraceId())
		var sid model.SpanID
		copy(sid[:], l.GetSpanId())
		out = append(out, model.SpanLink{
			TraceID: tid,
			SpanID:  sid,
			Attrs:   convertAttrs(l.GetAttributes()),
		})
	}
	return out
}

func convertAttrs(kvs []*otlpcommonv1.KeyValue) model.AttrMap {
	if len(kvs) == 0 {
		return nil
	}
	m := make(model.AttrMap, len(kvs))
	for _, kv := range kvs {
		m[kv.GetKey()] = convertAnyValue(kv.GetValue())
	}
	return m
}

// convertAnyValue is the FR-F01-7 hot spot: every OTel AnyValue kind maps
// 1:1 onto model.AttrValue with no coercion.
func convertAnyValue(v *otlpcommonv1.AnyValue) model.AttrValue {
	if v == nil {
		return model.AttrValue{}
	}
	switch val := v.GetValue().(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return model.AttrValue{Kind: model.AttrStr, Str: val.StringValue}
	case *otlpcommonv1.AnyValue_BoolValue:
		n := int64(0)
		if val.BoolValue {
			n = 1
		}
		return model.AttrValue{Kind: model.AttrBool, Num: n}
	case *otlpcommonv1.AnyValue_IntValue:
		return model.AttrValue{Kind: model.AttrInt, Num: val.IntValue}
	case *otlpcommonv1.AnyValue_DoubleValue:
		return model.AttrValue{Kind: model.AttrFloat, Float: val.DoubleValue}
	case *otlpcommonv1.AnyValue_BytesValue:
		return model.AttrValue{Kind: model.AttrBytes, Bytes: val.BytesValue}
	case *otlpcommonv1.AnyValue_ArrayValue:
		values := val.ArrayValue.GetValues()
		list := make([]model.AttrValue, len(values))
		for i, e := range values {
			list[i] = convertAnyValue(e)
		}
		return model.AttrValue{Kind: model.AttrSlice, List: list}
	case *otlpcommonv1.AnyValue_KvlistValue:
		values := val.KvlistValue.GetValues()
		m := make(map[string]model.AttrValue, len(values))
		for _, kv := range values {
			m[kv.GetKey()] = convertAnyValue(kv.GetValue())
		}
		return model.AttrValue{Kind: model.AttrMapK, Map: m}
	default:
		return model.AttrValue{}
	}
}
