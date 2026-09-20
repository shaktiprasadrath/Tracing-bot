package rca

import (
	"context"
	"sync"
	"time"

	"traceiq/internal/model"
)

// DR-17 §17.5's default budget values (rca.budget config block).
const (
	defaultWallClock          = 5 * time.Minute
	defaultMaxStepWallClock   = 45 * time.Second
	defaultMaxSteps           = 24
	defaultMaxToolCalls       = 40
	defaultMaxTokensIn        = 120000
	defaultMaxCachedTokensIn  = 600000
	defaultMaxTokensOut       = 64000
	defaultMaxCostMicroUSD    = 500000
	defaultMaxRowsPerToolCall = 500
	defaultMaxEvidenceBytes   = 8192

	toolCallTimeout = 15 * time.Second // "per-tool timeout" (DR-17 §17.2, retained)
)

// DefaultBudget returns DR-17 §17.5's configured defaults.
func DefaultBudget() model.Budget {
	return model.Budget{
		WallClock:          defaultWallClock,
		MaxStepWallClock:   defaultMaxStepWallClock,
		MaxSteps:           defaultMaxSteps,
		MaxToolCalls:       defaultMaxToolCalls,
		MaxTokensIn:        defaultMaxTokensIn,
		MaxCachedTokensIn:  defaultMaxCachedTokensIn,
		MaxTokensOut:       defaultMaxTokensOut,
		MaxCostMicroUSD:    defaultMaxCostMicroUSD,
		MaxRowsPerToolCall: defaultMaxRowsPerToolCall,
		MaxEvidenceBytes:   defaultMaxEvidenceBytes,
	}
}

// MemBudget is an in-memory rca.Budget (DR-15). It accumulates Charge deltas
// per investigation and answers Remaining/Terminated queries against the
// model.Budget snapshot the Engine registered at Reserve time. Engine.
// Investigate is still the place that actually breaks the loop (F06 §4.4's
// pseudocode checks Spend against Budget directly); MemBudget exists so
// that check has somewhere real to read Spend from, and so Budget.
// Terminated is queryable by callers other than the loop itself (e.g. a
// future status endpoint).
type MemBudget struct {
	mu     sync.Mutex
	budget map[string]model.Budget
	spend  map[string]model.Spend
	term   map[string]model.TerminationReason
}

func NewMemBudget() *MemBudget {
	return &MemBudget{
		budget: make(map[string]model.Budget),
		spend:  make(map[string]model.Spend),
		term:   make(map[string]model.TerminationReason),
	}
}

// Reserve registers invID's budget snapshot. Not part of the rca.Budget
// interface (DR-15 fixes that interface's method set); it is this
// implementation's own setup hook, called once by Engine.Investigate.
func (b *MemBudget) Reserve(invID string, budget model.Budget) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.budget[invID] = budget
	b.spend[invID] = model.Spend{}
}

func (b *MemBudget) Charge(_ context.Context, _ model.TenantID, invID string, d Charge) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.spend[invID]
	s.TokensIn += d.TokensIn
	s.CachedTokensIn += d.CachedTokensIn
	s.TokensOut += d.TokensOut
	s.CostMicroUSD += d.CostMicroUSD
	b.spend[invID] = s
	return nil
}

func (b *MemBudget) Remaining(invID string) model.Spend {
	b.mu.Lock()
	defer b.mu.Unlock()
	bud := b.budget[invID]
	sp := b.spend[invID]
	return model.Spend{
		TokensIn:       int64(bud.MaxTokensIn) - sp.TokensIn,
		CachedTokensIn: int64(bud.MaxCachedTokensIn) - sp.CachedTokensIn,
		TokensOut:      int64(bud.MaxTokensOut) - sp.TokensOut,
		CostMicroUSD:   bud.MaxCostMicroUSD - sp.CostMicroUSD,
		ToolCalls:      bud.MaxToolCalls - sp.ToolCalls,
		Steps:          bud.MaxSteps - sp.Steps,
	}
}

func (b *MemBudget) Terminated(invID string) (model.TerminationReason, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.term[invID]
	return r, ok
}

// SetTerminated records the loop's termination reason. Engine-only hook,
// same rationale as Reserve.
func (b *MemBudget) SetTerminated(invID string, r model.TerminationReason) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.term[invID] = r
}

var _ Budget = (*MemBudget)(nil)
