package sampler

import (
	"bytes"
	"sort"

	"github.com/cespare/xxhash/v2"

	"traceiq/internal/model"
)

// spanIsError, spanDurationNanos and spanService duplicate what would
// otherwise be model.Span.IsError/DurationNanos/Service — but those methods
// are still `panic("not implemented")` stubs in internal/model/telemetry.go
// and this package may not edit internal/model (scope lock). Local
// equivalents live here instead.
func spanIsError(s *model.Span) bool {
	if s.Status.Code == model.StatusError {
		return true
	}
	if v, ok := s.Attrs["error.type"]; ok {
		if v.Kind == model.AttrStr && v.Str != "" {
			return true
		}
	}
	return false
}

func spanDurationNanos(s *model.Span) uint64 {
	if s.EndUnixNano <= s.StartUnixNano {
		return 0
	}
	return s.EndUnixNano - s.StartUnixNano
}

func spanService(s *model.Span) string {
	if s.Resource != nil {
		return s.Resource.ServiceName
	}
	return ""
}

func spanIsRoot(s *model.Span) bool {
	return s.ParentSpanID == (model.SpanID{})
}

// traceHasError reports whether any span in the trace carries an error
// status (F02 §4.4 class 1 — Error, never shed).
func traceHasError(spans []model.Span) bool {
	for i := range spans {
		if spanIsError(&spans[i]) {
			return true
		}
	}
	return false
}

// traceMaxDurationNanos returns the maximum span duration in the trace.
//
// DEVIATION (w9-sampler): DR-10's canonical PolicyEvaluator.Evaluate
// signature is `Evaluate(ctx, tid, t, b KeyBaseline, m InterestMatch, g
// Governor) Decision` — a single KeyBaseline, not a per-(service,operation)
// map. But §4.4's algorithm pseudocode compares baseline.P99(k) against
// `distinctMaxDuration(t.Spans)` for every distinct key k the trace touches.
// Those two are inconsistent (singular struct vs. multi-key lookup) and no
// resolving text exists for it. This file resolves it minimally: the
// caller-supplied KeyBaseline is treated as the trace's root
// (service,operation) baseline, and the Slow check here compares against
// the trace's single overall max span duration (a superset-safe
// approximation of distinctMaxDuration for the root key). Flagged
// NEEDS_CONTEXT in docs/reports/w9-sampler.md.
func traceMaxDurationNanos(spans []model.Span) uint64 {
	var max uint64
	for i := range spans {
		d := spanDurationNanos(&spans[i])
		if d > max {
			max = d
		}
	}
	return max
}

func traceRootService(spans []model.Span) string {
	for i := range spans {
		if spanIsRoot(&spans[i]) {
			return spanService(&spans[i])
		}
	}
	if len(spans) > 0 {
		return spanService(&spans[0])
	}
	return ""
}

// hashTraceID implements FR-F02-4's deterministic probabilistic selection:
// hash(trace_id) mod 10000 < floor*10000, stable across shard restarts and
// replay (pure function of the 16-byte ID).
func hashTraceID(id model.TraceID) uint64 {
	return xxhash.Sum64(id[:])
}

// computePathSignature implements DR-10 §4.2's ordered edge list:
// xxh3(concat over edges of (caller_service, caller_operation, callee_service,
// callee_operation)), edges enumerated by pre-order DFS from the root,
// children sorted by (StartUnixNano, SpanID); orphan subtrees (dangling
// parent, i.e. ParentSpanID not present in this span set) are appended after
// the rooted tree, ordered by their own root's (StartUnixNano, SpanID).
//
// Uses XXH64 (see ShardFor's deviation note in sampler.go) rather than xxh3.
func computePathSignature(spans []model.Span) uint64 {
	bySpanID := make(map[model.SpanID]*model.Span, len(spans))
	for i := range spans {
		bySpanID[spans[i].SpanID] = &spans[i]
	}
	children := make(map[model.SpanID][]*model.Span)
	var roots []*model.Span
	for i := range spans {
		s := &spans[i]
		if spanIsRoot(s) {
			roots = append(roots, s)
			continue
		}
		if _, ok := bySpanID[s.ParentSpanID]; ok {
			children[s.ParentSpanID] = append(children[s.ParentSpanID], s)
		} else {
			roots = append(roots, s) // dangling parent -> orphan subtree root
		}
	}
	byStartThenID := func(list []*model.Span) {
		sort.Slice(list, func(i, j int) bool {
			if list[i].StartUnixNano != list[j].StartUnixNano {
				return list[i].StartUnixNano < list[j].StartUnixNano
			}
			return bytes.Compare(list[i].SpanID[:], list[j].SpanID[:]) < 0
		})
	}
	byStartThenID(roots)

	h := xxhash.New()
	var visit func(parent, s *model.Span)
	visit = func(parent, s *model.Span) {
		if parent != nil {
			h.WriteString(spanService(parent))
			h.Write([]byte{0})
			h.WriteString(parent.Name)
			h.Write([]byte{0})
			h.WriteString(spanService(s))
			h.Write([]byte{0})
			h.WriteString(s.Name)
			h.Write([]byte{0})
		}
		kids := children[s.SpanID]
		byStartThenID(kids)
		for _, k := range kids {
			visit(s, k)
		}
	}
	for _, r := range roots {
		visit(nil, r)
	}
	return h.Sum64()
}
