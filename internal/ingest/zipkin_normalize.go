package ingest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"traceiq/internal/model"
)

// zipkinSpan mirrors the Zipkin v2 JSON span schema (FR-F01-5, migration-only
// protocol per D-Z1). Field-mapping is a static table (§4.4: "no runtime
// branching beyond table lookup").
type zipkinSpan struct {
	TraceID       string            `json:"traceId"`
	ID            string            `json:"id"`
	ParentID      string            `json:"parentId,omitempty"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind,omitempty"`      // CLIENT|SERVER|PRODUCER|CONSUMER
	Timestamp     uint64            `json:"timestamp,omitempty"` // micros since epoch
	Duration      uint64            `json:"duration,omitempty"`  // micros
	LocalEndpoint *zipkinEndpoint   `json:"localEndpoint,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
	Debug         bool              `json:"debug,omitempty"`
}

type zipkinEndpoint struct {
	ServiceName string `json:"serviceName,omitempty"`
}

// zipkinSpanKind maps the Zipkin v2 `kind` string onto model.SpanKind.
// Zipkin has no INTERNAL kind; an empty/unknown kind maps to Unspecified.
var zipkinSpanKind = map[string]model.SpanKind{
	"CLIENT":   model.SpanKindClient,
	"SERVER":   model.SpanKindServer,
	"PRODUCER": model.SpanKindProducer,
	"CONSUMER": model.SpanKindConsumer,
}

// ZipkinNormalizer implements Normalizer for the Zipkin v2 JSON receiver
// (FR-F01-5). Normalize accepts raw JSON bytes -- `POST /api/v2/spans`'s
// body is either a single span object or a JSON array of spans; both are
// accepted, matching the Zipkin v2 spec.
type ZipkinNormalizer struct{}

func (n *ZipkinNormalizer) Normalize(raw any) (model.Batch, error) {
	body, ok := raw.([]byte)
	if !ok {
		return model.Batch{}, fmt.Errorf("ingest: ZipkinNormalizer.Normalize: expected []byte JSON body, got %T", raw)
	}

	spans, err := parseZipkinJSON(body)
	if err != nil {
		return model.Batch{}, fmt.Errorf("ingest: ZipkinNormalizer.Normalize: %w", err)
	}

	out := make([]model.Span, 0, len(spans))
	for _, zs := range spans {
		sp, err := convertZipkinSpan(zs)
		if err != nil {
			return model.Batch{}, fmt.Errorf("ingest: ZipkinNormalizer.Normalize: span %q: %w", zs.ID, err)
		}
		out = append(out, sp)
	}

	return model.Batch{
		Spans:        out,
		SourceFormat: model.SourceZipkinV2,
		SizeBytes:    uint32(len(body)),
	}, nil
}

// parseZipkinJSON accepts either a JSON array of spans (the common case) or
// a single span object.
func parseZipkinJSON(body []byte) ([]zipkinSpan, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty body")
	}
	var spans []zipkinSpan
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &spans); err != nil {
			return nil, fmt.Errorf("decode zipkin span array: %w", err)
		}
		return spans, nil
	}
	var one zipkinSpan
	if err := json.Unmarshal(body, &one); err != nil {
		return nil, fmt.Errorf("decode zipkin span: %w", err)
	}
	return []zipkinSpan{one}, nil
}

func convertZipkinSpan(zs zipkinSpan) (model.Span, error) {
	traceID, err := zipkinHexToTraceID(zs.TraceID)
	if err != nil {
		return model.Span{}, fmt.Errorf("traceId: %w", err)
	}
	spanID, err := zipkinHexToSpanID(zs.ID)
	if err != nil {
		return model.Span{}, fmt.Errorf("id: %w", err)
	}
	var parentID model.SpanID
	if zs.ParentID != "" {
		parentID, err = zipkinHexToSpanID(zs.ParentID)
		if err != nil {
			return model.Span{}, fmt.Errorf("parentId: %w", err)
		}
	}

	var attrs model.AttrMap
	if len(zs.Tags) > 0 {
		attrs = make(model.AttrMap, len(zs.Tags))
		for k, v := range zs.Tags {
			// Zipkin tags are always string-valued; preserved verbatim as
			// AttrStr, not coerced (mirrors FR-F01-7's intent for the one
			// non-OTLP first-class conversion path).
			attrs[k] = model.AttrValue{Kind: model.AttrStr, Str: v}
		}
	}

	var res *model.Resource
	if zs.LocalEndpoint != nil && zs.LocalEndpoint.ServiceName != "" {
		res = &model.Resource{ServiceName: zs.LocalEndpoint.ServiceName}
	}

	startNanos := zs.Timestamp * 1000
	endNanos := startNanos + zs.Duration*1000

	// W3C trace-flags: Zipkin's `debug` maps to the sampled bit (bit 0).
	var flags uint32
	if zs.Debug {
		flags = 1
	}

	return model.Span{
		TraceID:       traceID,
		SpanID:        spanID,
		ParentSpanID:  parentID,
		Flags:         flags,
		Name:          zs.Name,
		Kind:          zipkinSpanKind[zs.Kind],
		StartUnixNano: startNanos,
		EndUnixNano:   endNanos,
		Attrs:         attrs,
		Resource:      res,
		SourceFormat:  model.SourceZipkinV2,
	}, nil
}

func zipkinHexToTraceID(s string) (model.TraceID, error) {
	var id model.TraceID
	// Zipkin v2 traceId is 16 or 32 hex chars (64- or 128-bit); left-pad the
	// 64-bit form into the low 8 bytes, matching the W3C/OTLP 128-bit shape.
	switch len(s) {
	case 32:
		b, err := hex.DecodeString(s)
		if err != nil {
			return id, err
		}
		copy(id[:], b)
	case 16:
		b, err := hex.DecodeString(s)
		if err != nil {
			return id, err
		}
		copy(id[8:], b)
	default:
		return id, fmt.Errorf("want 16 or 32 hex chars, got %d", len(s))
	}
	if id == (model.TraceID{}) {
		return id, fmt.Errorf("all-zero")
	}
	return id, nil
}

func zipkinHexToSpanID(s string) (model.SpanID, error) {
	var id model.SpanID
	if len(s) != 16 {
		return id, fmt.Errorf("want 16 hex chars, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, err
	}
	copy(id[:], b)
	if id == (model.SpanID{}) {
		return id, fmt.Errorf("all-zero")
	}
	return id, nil
}

// B3HeadersToTraceParent implements FR-F01-5's "converting B3 single/multi-header
// propagation into the equivalent W3C traceparent fields on model.Span". It
// is a standalone conversion utility (not yet wired to an HTTP handler --
// see docs/reports/w9-ingest-cont.md) accepting either the single "b3"
// header or the four X-B3-* headers, and returning a W3C
// `traceparent`-formatted string suitable for model.Span.TraceState.
//
// Single-header format: {TraceId}-{SpanId}-{SamplingState}-{ParentSpanId}
// (ParentSpanId is optional). Multi-header: X-B3-TraceId, X-B3-SpanId,
// X-B3-ParentSpanId, X-B3-Sampled.
func B3HeadersToTraceParent(headers map[string]string) (string, error) {
	var traceID, spanID, sampled string

	if b3 := headers["b3"]; b3 != "" {
		parts := strings.Split(b3, "-")
		if len(parts) < 2 {
			return "", fmt.Errorf("ingest: malformed b3 single header %q", b3)
		}
		traceID, spanID = parts[0], parts[1]
		if len(parts) >= 3 {
			sampled = parts[2]
		}
	} else {
		traceID = headers["X-B3-TraceId"]
		spanID = headers["X-B3-SpanId"]
		sampled = headers["X-B3-Sampled"]
	}

	if traceID == "" || spanID == "" {
		return "", fmt.Errorf("ingest: missing B3 trace/span id")
	}
	if len(traceID) == 16 {
		traceID = strings.Repeat("0", 16) + traceID
	}
	if len(traceID) != 32 || len(spanID) != 16 {
		return "", fmt.Errorf("ingest: malformed B3 trace/span id (traceId=%dB spanId=%dB)", len(traceID), len(spanID))
	}

	flags := "00"
	if sampledBool, err := strconv.ParseBool(sampled); err == nil && sampledBool {
		flags = "01"
	} else if sampled == "1" {
		flags = "01"
	}

	return fmt.Sprintf("00-%s-%s-%s", strings.ToLower(traceID), strings.ToLower(spanID), flags), nil
}
