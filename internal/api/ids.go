package api

import (
	"encoding/hex"
	"fmt"

	"traceiq/internal/model"
)

// traceIDHex/parseTraceID convert between model.TraceID's fixed [16]byte
// wire shape and the 32-char lowercase hex string every REST/MCP caller
// sends and receives (OTLP's own convention). Neither model nor store
// prints a canonical helper for this, so it is reconstructed here, scoped
// to this package's HTTP boundary only.
func traceIDHex(id model.TraceID) string {
	return hex.EncodeToString(id[:])
}

func parseTraceID(s string) (model.TraceID, error) {
	var id model.TraceID
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, fmt.Errorf("invalid trace id: %w", err)
	}
	if len(b) != len(id) {
		return id, fmt.Errorf("invalid trace id: want %d bytes, got %d", len(id), len(b))
	}
	copy(id[:], b)
	return id, nil
}
