package rca

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"traceiq/internal/model"
)

// ObjectPutter is rca's consumer-declared interface onto store.ObjectStore
// (DR-18 §18.1: "Bodies live in store.ObjectStore under evidence/ and
// reasoner/ ... never inline in SQLite"). Declared locally per DR-2 rather
// than importing internal/store's concrete type for this wave.
type ObjectPutter interface {
	Put(ctx context.Context, tid model.TenantID, key string, data []byte) error
	Get(ctx context.Context, tid model.TenantID, key string) ([]byte, error)
}

// MemObjectStore is an in-memory ObjectPutter used as this wave's dev/test
// default (DR-18 §18.1's "dev: ${data_dir}/evidence" note; a real
// store.ObjectStore lands when store is wired in). Safe for concurrent use.
type MemObjectStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemObjectStore() *MemObjectStore {
	return &MemObjectStore{data: make(map[string][]byte)}
}

func (m *MemObjectStore) Put(_ context.Context, tid model.TenantID, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[string(tid)+"/"+key] = cp
	return nil
}

func (m *MemObjectStore) Get(_ context.Context, tid model.TenantID, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[string(tid)+"/"+key]
	if !ok {
		return nil, fmt.Errorf("rca: object %q not found", key)
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

// canonicalJSON is this wave's stand-in for DR-18's "canonical (RFC 8785)
// JSON" requirement. encoding/json already sorts map[string]* keys, which is
// the only source of non-determinism in the closed ToolArgs/ToolResult
// shapes used here (MetricQueryArgs.Params is the sole map field on the
// request side); a full RFC 8785 encoder (float/escaping edge cases) is
// deferred — see docs/reports/w11-rca-rules.md.
func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MemJournal is an in-memory rca.Journal (DR-15, "02's name for what F06
// called StepStore"). It fsyncs nothing (there is no disk), but preserves
// the contract's ordering guarantee: AppendStep returns only after the step
// is durably visible to a subsequent Steps() call, which is what "fsynced
// before the loop may advance" (FR-F06-2) actually buys the caller in a
// test double. A real control.db-backed Journal is a later wave's concern.
type MemJournal struct {
	mu    sync.Mutex
	invs  map[string]model.Investigation // by ID; Engine is still source of truth for the live copy
	steps map[string][]model.Step        // by investigation ID, Seq order
	ev    map[string][]model.Evidence
}

func NewMemJournal() *MemJournal {
	return &MemJournal{
		invs:  make(map[string]model.Investigation),
		steps: make(map[string][]model.Step),
		ev:    make(map[string][]model.Evidence),
	}
}

func (j *MemJournal) Create(_ context.Context, tid model.TenantID, inv model.Investigation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if inv.Tenant != tid {
		return fmt.Errorf("rca: tenant mismatch on Create")
	}
	j.invs[inv.ID] = inv
	return nil
}

func (j *MemJournal) AppendStep(_ context.Context, tid model.TenantID, invID string, st model.Step) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	inv, ok := j.invs[invID]
	if !ok || inv.Tenant != tid {
		return fmt.Errorf("rca: unknown investigation %q", invID)
	}
	j.steps[invID] = append(j.steps[invID], st)
	return nil
}

func (j *MemJournal) AppendEvidence(_ context.Context, tid model.TenantID, invID string, evs []model.Evidence) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	inv, ok := j.invs[invID]
	if !ok || inv.Tenant != tid {
		return fmt.Errorf("rca: unknown investigation %q", invID)
	}
	j.ev[invID] = append(j.ev[invID], evs...)
	return nil
}

func (j *MemJournal) Steps(_ context.Context, tid model.TenantID, invID string) ([]model.Step, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	inv, ok := j.invs[invID]
	if !ok || inv.Tenant != tid {
		return nil, fmt.Errorf("rca: unknown investigation %q", invID)
	}
	out := make([]model.Step, len(j.steps[invID]))
	copy(out, j.steps[invID])
	return out, nil
}

func (j *MemJournal) SetStatus(_ context.Context, tid model.TenantID, invID string, st model.InvestigationStatus, at time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	inv, ok := j.invs[invID]
	if !ok || inv.Tenant != tid {
		return fmt.Errorf("rca: unknown investigation %q", invID)
	}
	inv.Status = st
	inv.EndedAt = at
	j.invs[invID] = inv
	return nil
}

func (j *MemJournal) ListRunning(_ context.Context) ([]model.Investigation, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []model.Investigation
	for _, inv := range j.invs {
		if inv.Status == model.InvestigationRunning {
			out = append(out, inv)
		}
	}
	return out, nil
}

var _ Journal = (*MemJournal)(nil)
