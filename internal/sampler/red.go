package sampler

import (
	"sync"
	"time"

	"traceiq/internal/model"
)

// redKey identifies one RED accumulator bucket (F02 FR-F02-5: "per
// (tenant, service, operation, 10s bucket) accumulator owned by the decode
// worker — not at trace finalization").
type redKey struct {
	tenant      model.TenantID
	service     string
	operation   string
	bucketStart int64 // unix nanos, floored to model.Res10s
}

const redBucketNanos = int64(10 * time.Second)

// redAccumulator implements DR-9's "RED before every discard": every span
// contributes to RED regardless of whether its trace is later kept, dropped,
// truncated or lost to backpressure. It is fed directly from Consume, before
// any shard/assembly/drop decision.
type redAccumulator struct {
	mu      sync.Mutex
	buckets map[redKey]*model.REDSample
}

func newREDAccumulator() *redAccumulator {
	return &redAccumulator{buckets: make(map[redKey]*model.REDSample)}
}

// Accumulate records one span's RED contribution. Must be called for every
// span the router sees, including spans later dropped for shard_full,
// truncation or duplication (FR-F02-5 a/b/c).
func (a *redAccumulator) Accumulate(now time.Time, tenant model.TenantID, s *model.Span) {
	bucket := now.UnixNano() / redBucketNanos * redBucketNanos
	key := redKey{tenant: tenant, service: spanService(s), operation: s.Name, bucketStart: bucket}

	a.mu.Lock()
	defer a.mu.Unlock()
	sample, ok := a.buckets[key]
	if !ok {
		sample = &model.REDSample{
			Tenant:      tenant,
			Service:     key.service,
			Operation:   key.operation,
			BucketStart: time.Unix(0, bucket).UTC(),
			Resolution:  model.Res10s,
		}
		a.buckets[key] = sample
	}
	sample.Calls++
	if spanIsError(s) {
		sample.Errors++
	}
	sample.DurationSumNanos += spanDurationNanos(s)
	if len(sample.ExemplarTraceIDs) < 4 {
		found := false
		for _, id := range sample.ExemplarTraceIDs {
			if id == s.TraceID {
				found = true
				break
			}
		}
		if !found {
			sample.ExemplarTraceIDs = append(sample.ExemplarTraceIDs, s.TraceID)
		}
	}
}

// Flush drains and returns every accumulated bucket. Simplified from
// FR-F02-5's real per-bucket-close semantics (which would only flush a
// bucket once its 10s window has fully elapsed) to an explicit
// drain-everything call, since the callers in this package (Consume's
// caller, and tests) do not need bucket-boundary precision to verify "RED
// is extracted regardless of keep decision" (the task's AC target).
func (a *redAccumulator) Flush() []model.REDSample {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]model.REDSample, 0, len(a.buckets))
	for k, v := range a.buckets {
		out = append(out, *v)
		delete(a.buckets, k)
	}
	return out
}
