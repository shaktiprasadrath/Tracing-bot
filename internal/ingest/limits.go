package ingest

import (
	"fmt"
	"sort"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// applyLimits enforces 01 §8.4's single limits table (config.IngestLimitsConfig,
// DR-26 §26.4), cited by F01 §4.3/§6 and never restated here as a local
// constant. Two distinct outcomes per §6 "Input validation":
//
//   - MaxSpansPerBatch exceeded: the whole batch is structurally oversize
//     -> reject (FR-F01-8), reason "oversize".
//   - a single span's attribute count/key/value exceeds its cap: default
//     behavior is TRUNCATE, never silently (Span.Truncated + DroppedAttrsCount
//     set) -- reject is a per-tenant strict opt-in (lim.OversizeAttr == "reject").
func applyLimits(batch *model.Batch, lim config.IngestLimitsConfig) error {
	// FIXED (review pass): §4.4's HandleInbound pseudocode checks
	// `sizeOf(batch) > ingest.limits.max_batch_bytes` before the tenant
	// resolve step; that whole-request byte cap (config.MaxRequestBytes, 01
	// §8.4 / DR-26 §26.4) was never enforced here even though batch.SizeBytes
	// is already populated by every Normalizer (proto.Size/len(body)) — only
	// the span-COUNT cap (MaxSpansPerBatch) was checked. Without this, a
	// batch with few, huge (e.g. deeply nested AnyValue) spans could sail
	// through the count check while carrying an arbitrarily large payload,
	// which is exactly the DoS class §6 calls out as closed by structural
	// validation. FR-F01-8/AC-F01-6.
	if lim.MaxRequestBytes > 0 && int64(batch.SizeBytes) > lim.MaxRequestBytes {
		return &IngestError{Reason: ReasonOversize, Err: fmt.Errorf(
			"batch is %d bytes, exceeds ingest.limits.max_request_bytes=%d", batch.SizeBytes, lim.MaxRequestBytes)}
	}

	if lim.MaxSpansPerBatch > 0 && len(batch.Spans) > lim.MaxSpansPerBatch {
		return &IngestError{Reason: ReasonOversize, Err: fmt.Errorf(
			"batch has %d spans, exceeds ingest.limits.max_spans_per_batch=%d", len(batch.Spans), lim.MaxSpansPerBatch)}
	}

	for i := range batch.Spans {
		sp := &batch.Spans[i]
		if err := applySpanLimits(sp, lim); err != nil {
			return err
		}
	}
	return nil
}

func applySpanLimits(sp *model.Span, lim config.IngestLimitsConfig) error {
	if len(sp.Attrs) == 0 {
		return nil
	}

	// Cap attribute COUNT first (deterministic: sorted key order), dropping
	// the excess -- always truncate-not-reject for count, matching D-D3's
	// "flagged, never silent" evidence-fidelity claim (§6).
	if lim.MaxAttributesPerSpan > 0 && len(sp.Attrs) > lim.MaxAttributesPerSpan {
		keys := make([]string, 0, len(sp.Attrs))
		for k := range sp.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		drop := keys[lim.MaxAttributesPerSpan:]
		for _, k := range drop {
			delete(sp.Attrs, k)
		}
		sp.DroppedAttrsCount += uint32(len(drop))
		sp.Truncated = true
	}

	// Cap per-attribute key/value size (truncate by default; reject is a
	// per-tenant strict option, lim.OversizeAttr == "reject").
	for k, v := range sp.Attrs {
		oversizeKey := lim.MaxAttributeKeyBytes > 0 && int64(len(k)) > lim.MaxAttributeKeyBytes
		oversizeVal := lim.MaxAttributeValueBytes > 0 && attrValueApproxBytes(v) > lim.MaxAttributeValueBytes
		if !oversizeKey && !oversizeVal {
			continue
		}
		if lim.OversizeAttr == "reject" {
			return &IngestError{Reason: ReasonOversize, Err: fmt.Errorf(
				"attribute %q exceeds ingest.limits (key=%dB val=%dB)", k, len(k), attrValueApproxBytes(v))}
		}
		if oversizeKey {
			// An oversize key cannot be usefully truncated (it would collide
			// with other keys), so it is dropped entirely.
			delete(sp.Attrs, k)
			sp.DroppedAttrsCount++
		} else {
			sp.Attrs[k] = truncateAttrValue(v, lim.MaxAttributeValueBytes)
		}
		sp.Truncated = true
	}
	return nil
}

// attrValueApproxBytes is the size used for the MaxAttributeValueBytes cap.
// String/bytes values are measured exactly; scalar kinds (bool/int/float)
// are cheap and fixed-size and never exceed a byte cap in practice.
func attrValueApproxBytes(v model.AttrValue) int64 {
	switch v.Kind {
	case model.AttrStr:
		return int64(len(v.Str))
	case model.AttrBytes:
		return int64(len(v.Bytes))
	default:
		return 8
	}
}

// truncateAttrValue truncates a string/bytes value to max and sets
// Span.Truncated at the call site; other kinds are left as-is (they are
// already ≤ attrValueApproxBytes's fixed 8B estimate).
func truncateAttrValue(v model.AttrValue, max int64) model.AttrValue {
	if max < 0 {
		return v
	}
	switch v.Kind {
	case model.AttrStr:
		if int64(len(v.Str)) > max {
			v.Str = v.Str[:max]
		}
	case model.AttrBytes:
		if int64(len(v.Bytes)) > max {
			b := make([]byte, max)
			copy(b, v.Bytes)
			v.Bytes = b
		}
	}
	return v
}
