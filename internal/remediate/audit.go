package remediate

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"

	"traceiq/internal/auth"
	"traceiq/internal/model"
)

// InMemoryAuditLog is an append-only, hash-chained implementation of
// auth.AuditSink (X-SEC / DR-27), scoped here because internal/auth ships
// only the interface (no concrete AuditSink yet exists anywhere in the
// tree). Each entry's hash commits to the previous entry's hash plus its
// own fields, so mutating any stored field breaks VerifyChain from that
// point forward — the property AC-F09-*'s audit requirement and this
// package's own test (g) exercise directly.
//
// Per-tenant chains are independent (each tenant starts its own chain at
// seq 1), matching AuditSink.VerifyChain/Query's per-tenant scoping.
type InMemoryAuditLog struct {
	mu      sync.Mutex
	entries []storedEvent
	seqs    map[model.TenantID]int64
}

type storedEvent struct {
	seq      int64
	tenant   model.TenantID
	event    auth.Event
	prevHash []byte
	hash     []byte
}

var _ auth.AuditSink = (*InMemoryAuditLog)(nil)

func NewInMemoryAuditLog() *InMemoryAuditLog {
	return &InMemoryAuditLog{seqs: map[model.TenantID]int64{}}
}

func chainHash(prev []byte, seq int64, e auth.Event) []byte {
	h := sha256.New()
	h.Write(prev)
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], uint64(seq))
	h.Write(seqBuf[:])
	h.Write([]byte(e.Tenant))
	h.Write([]byte(e.Actor))
	h.Write([]byte(e.Action))
	h.Write(e.Payload)
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(e.Timestamp.UnixNano()))
	h.Write(tsBuf[:])
	return h.Sum(nil)
}

// Append is FAIL-CLOSED (auth.AuditSink's contract): it never returns a
// partially-written state — either the entry is chained and stored, or an
// error is returned and nothing changes.
func (l *InMemoryAuditLog) Append(ctx context.Context, e auth.Event) (auth.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seqs[e.Tenant]++
	seq := l.seqs[e.Tenant]

	var prev []byte
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].tenant == e.Tenant {
			prev = l.entries[i].hash
			break
		}
	}
	hash := chainHash(prev, seq, e)
	l.entries = append(l.entries, storedEvent{seq: seq, tenant: e.Tenant, event: e, prevHash: prev, hash: hash})
	return auth.Receipt{Seq: seq, Hash: hash}, nil
}

func (l *InMemoryAuditLog) Query(ctx context.Context, tid model.TenantID, f auth.Filter) ([]auth.Event, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []auth.Event
	for _, se := range l.entries {
		if se.tenant != tid {
			continue
		}
		if f.Actor != "" && se.event.Actor != f.Actor {
			continue
		}
		if f.Action != "" && se.event.Action != f.Action {
			continue
		}
		if !f.From.IsZero() && se.event.Timestamp.Before(f.From) {
			continue
		}
		if !f.To.IsZero() && se.event.Timestamp.After(f.To) {
			continue
		}
		out = append(out, se.event)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, "", nil
}

// VerifyChain recomputes the hash chain over [from, to) (seq numbers,
// per-tenant) and reports the first seq where the recomputed hash diverges
// from what is stored — which is exactly what happens if any entry's
// fields are mutated in place after Append.
func (l *InMemoryAuditLog) VerifyChain(ctx context.Context, tid model.TenantID, from, to int64) (auth.VerifyReport, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var prev []byte
	// Establish prev as the hash of the entry immediately before `from`,
	// if any, using the stored (possibly-tampered) chain up to that point.
	// We only need forward verification from `from` onward for this
	// package's test, so if from > 1 we seed prev from the entry at
	// seq==from-1 as currently stored (already-verified region is assumed
	// intact by the caller supplying a sane `from`).
	for _, se := range l.entries {
		if se.tenant != tid {
			continue
		}
		if se.seq == from-1 {
			prev = se.hash
		}
	}

	for _, se := range l.entries {
		if se.tenant != tid || se.seq < from || (to > 0 && se.seq >= to) {
			continue
		}
		want := chainHash(prev, se.seq, se.event)
		if fmt.Sprintf("%x", want) != fmt.Sprintf("%x", se.hash) {
			return auth.VerifyReport{OK: false, BrokenAtSeq: se.seq}, nil
		}
		prev = se.hash
	}
	return auth.VerifyReport{OK: true}, nil
}

func (l *InMemoryAuditLog) Anchor(ctx context.Context, tid model.TenantID) (auth.Anchor, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var last storedEvent
	found := false
	for _, se := range l.entries {
		if se.tenant == tid {
			last = se
			found = true
		}
	}
	if !found {
		return auth.Anchor{}, ErrNotFound
	}
	return auth.Anchor{Seq: last.seq, Hash: last.hash, AnchoredAt: last.event.Timestamp}, nil
}

// tamperForTest mutates a stored entry's payload in place without
// recomputing its hash, purely so remediate's own tests can prove
// VerifyChain detects tampering. It is exported only within the package's
// test binary's reach (lowercase, same package) — never a production path.
func (l *InMemoryAuditLog) tamperForTest(tid model.TenantID, seq int64, newPayload []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.entries {
		if l.entries[i].tenant == tid && l.entries[i].seq == seq {
			l.entries[i].event.Payload = newPayload
		}
	}
}
