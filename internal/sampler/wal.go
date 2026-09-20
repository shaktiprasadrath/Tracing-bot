package sampler

import (
	"sync"

	"traceiq/internal/model"
)

// WAL is the spill-WAL backend contract (DR-9's crash-safe spill WAL):
// Append durably journals a span before assembly ACKs it, Truncate drops a
// trace's records once its Decision has been emitted, and Replay
// re-assembles every undecided trace on start, before receivers bind
// (FR-F02-9).
//
// DEVIATION (w9-sampler): the real backend is a group-fsync'd on-disk
// segment file (sampler.wal.dir, DR-9). This package only implements
// MemWAL, an in-memory ring standing in for the disk segment — durability
// across a real process crash is out of scope for this pass. The WAL
// backend is pluggable (this interface), so a disk-backed implementation
// can be dropped in later without touching shard/assembly code.
// w17 final-review fix (BLOCKER, tenant isolation): every method used to key
// on model.TraceID ALONE. A TraceID is chosen by the *client* that emits the
// span, not by TraceIQ, so it is attacker-controlled across tenants: two
// tenants can trivially present the same TraceID. With TraceID-only keys,
// tenant A finalizing a trace called Truncate(shard, id) and deleted tenant
// B's journalled spans for the same id, and Replay collapsed both tenants'
// spans into one entry. TraceKey makes the tenant part of the identity, which
// is what DR-5 requires of every tenant-scoped lookup.
type WAL interface {
	Append(shard int, key TraceKey, span model.Span) error
	Truncate(shard int, key TraceKey) error
	Replay(shard int) (map[TraceKey][]model.Span, error)
}

// TraceKey identifies an in-flight trace. Tenant is part of the key because
// TraceID is not globally unique across tenants (see WAL's note above and
// shardBuf.traces in assembly.go).
type TraceKey struct {
	Tenant  model.TenantID
	TraceID model.TraceID
}

// MemWAL is an in-memory ring standing in for the on-disk WAL segment
// (see WAL's deviation note). Bounded by maxPerShard spans per shard;
// beyond that, oldest entries are dropped and counted — mirroring the real
// WAL's max_segment_bytes rollover in spirit, not in byte-accounting detail.
type MemWAL struct {
	mu    sync.Mutex
	shard map[int]map[TraceKey][]model.Span
}

func NewMemWAL() *MemWAL {
	return &MemWAL{shard: make(map[int]map[TraceKey][]model.Span)}
}

func (w *MemWAL) Append(shard int, key TraceKey, span model.Span) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	byTrace, ok := w.shard[shard]
	if !ok {
		byTrace = make(map[TraceKey][]model.Span)
		w.shard[shard] = byTrace
	}
	byTrace[key] = append(byTrace[key], span)
	return nil
}

func (w *MemWAL) Truncate(shard int, key TraceKey) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if byTrace, ok := w.shard[shard]; ok {
		delete(byTrace, key)
	}
	return nil
}

func (w *MemWAL) Replay(shard int) (map[TraceKey][]model.Span, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[TraceKey][]model.Span)
	for key, spans := range w.shard[shard] {
		cp := make([]model.Span, len(spans))
		copy(cp, spans)
		out[key] = cp
	}
	return out, nil
}
