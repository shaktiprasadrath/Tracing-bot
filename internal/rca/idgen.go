package rca

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
)

// genID returns a random 16-byte hex ID. A real implementation would use a
// time-sortable ULID (DR-15's AnomalyEvent.ID comment elsewhere in model
// documents that convention); a random hex string is sufficient for this
// wave's uniqueness and determinism-modulo-ID needs (DR-18 §18.2's replay
// byte-identity check explicitly erases IDs before comparing).
func genID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// cryptoRandInt64 is DR-18 §18.1's "ReplaySeed int64 (from crypto/rand at
// creation; seeds every tie-break in the rules reasoner and the memory
// scorer)". The rules reasoner in this wave has no tie-breaks to seed (its
// priority order is fixed), so ReplaySeed is stored but unused here.
func cryptoRandInt64() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := int64(binary.BigEndian.Uint64(b[:]))
	if v < 0 {
		v = -v
	}
	return v
}
