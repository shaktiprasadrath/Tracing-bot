package sampler

import "testing"

// TestShardFor_Deterministic covers AC-F02-1's routing-agreement requirement
// at its root: ShardFor must return the same shard for the same (Ring, id)
// on every call, whether invoked repeatedly against the same Ring value or a
// copy of it (Ring is a plain, immutable-by-convention value type).
func TestShardFor_Deterministic(t *testing.T) {
	ring := Ring{Epoch: 1, Members: []MemberID{"a", "b", "c", "d"}, State: RingStable}
	id := mkTraceID(42)

	first := ShardFor(ring, id)
	for i := 0; i < 200; i++ {
		if got := ShardFor(ring, id); got != first {
			t.Fatalf("ShardFor not deterministic: run %d got shard %d, want %d", i, got, first)
		}
	}

	ringCopy := ring
	if got := ShardFor(ringCopy, id); got != first {
		t.Fatalf("ShardFor differs across an equal Ring value: got %d want %d", got, first)
	}

	// Every member owns at least one key out of a reasonably large sample —
	// a sharding function that always picked the same member would still be
	// "deterministic" but would not be sharding at all.
	owners := map[int]bool{}
	for i := uint64(0); i < 5000; i++ {
		owners[ShardFor(ring, mkTraceID(i))] = true
	}
	if len(owners) != len(ring.Members) {
		t.Fatalf("ShardFor only used %d of %d members across 5000 keys: %v", len(owners), len(ring.Members), owners)
	}
}

// TestShardFor_ResizeRemapsApproxOneOverNPlusOne covers DR-8's core rendezvous
// (HRW) hashing property, and is the thing that actually distinguishes it
// from the deleted `fnv64a(TraceID) mod N` scheme: growing the ring from N to
// N+1 members should remap only ~1/(N+1) of keys (to the new member), not a
// large fraction of all keys the way a mod-N reshuffle would.
func TestShardFor_ResizeRemapsApproxOneOverNPlusOne(t *testing.T) {
	const n = 8
	const numKeys = 20000

	members := make([]MemberID, n)
	for i := range members {
		members[i] = MemberID(itoa(i))
	}
	before := Ring{Epoch: 1, Members: members, State: RingStable}

	grown := make([]MemberID, n+1)
	copy(grown, members)
	grown[n] = MemberID("new-member")
	after := Ring{Epoch: 2, Members: grown, State: RingStable}

	remapped := 0
	remappedToNew := 0
	for i := uint64(0); i < numKeys; i++ {
		id := mkTraceID(i + 1)
		b := ShardFor(before, id)
		a := ShardFor(after, id)
		if before.Members[b] != after.Members[a] {
			remapped++
			if after.Members[a] == grown[n] {
				remappedToNew++
			}
		}
	}

	frac := float64(remapped) / float64(numKeys)
	want := 1.0 / float64(n+1) // ~0.1111

	// Statistical band around the expected 1/(N+1): at numKeys=20000 the
	// binomial std-dev is ~0.0022, so [0.7x, 1.4x] is generous headroom
	// against hash noise while still failing hard on a mod-N-style
	// regression (which would remap close to 100% of keys).
	if frac < want*0.7 || frac > want*1.4 {
		t.Fatalf("resize remapped %.4f of keys, want within [%.4f, %.4f] of expected ~1/(N+1)=%.4f",
			frac, want*0.7, want*1.4, want)
	}

	// Rendezvous hashing's other defining property: every remapped key moves
	// to the newly added member, never to a pre-existing one.
	if remappedToNew != remapped {
		t.Fatalf("%d of %d remapped keys moved to a pre-existing member, not the new one; rendezvous hashing should only ever move keys to the newly added member", remapped-remappedToNew, remapped)
	}
}
