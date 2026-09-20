package rca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"traceiq/internal/model"
)

// engine is this wave's Engine implementation (DR-15's Engine interface,
// F06 §4.4's Investigate pseudocode), scoped to the rules reasoner only —
// the Reasoner it drives is injected at construction, and this wave always
// wires rules.New() (internal/rca/rules); LLM wiring is a later wave (see
// package doc.go).
//
// Deliberately out of scope / deferred (documented rather than silently
// missing — see docs/reports/w11-rca-rules.md):
//   - FR-F06-22 dedupe-before-dispatch (rca.Dispatcher / inflight map):
//     no Dispatcher type exists in the DR-15 interface set this wave reads
//     from; every Investigate call starts a fresh investigation.
//   - memory.Store.Record(tid, inv.Summary()) at conclusion: requires
//     memory wiring, out of this wave's scope.
//   - Daily/global cost-cap downgrade (DR-17 §17.4) and the LLM
//     fallback-swap machinery (DR-34 §34.4): both are properties of the LLM
//     reasoner path, which this wave does not implement.
//   - ReplayLiveDiff's per-step Drift annotation: DR-18's pseudocode
//     references a step.Drift field that does not exist on model.Step, and
//     this wave's scope lock permits ONLY additive ToolResult fields on
//     model — not a new Step field. ReplayLiveDiff therefore re-dispatches
//     and reports drift in aggregate (Investigation.ReportMarkdown) rather
//     than per step.
type engine struct {
	journal  Journal
	budget   *MemBudget
	registry ToolRegistry
	reasoner Reasoner
	objects  ObjectPutter
	interest InterestSink // nil-able; best-effort
	clock    model.Clock

	mu           sync.Mutex
	invs         map[string]model.Investigation
	corr         map[string][]model.Correction
	cancels      map[string]context.CancelFunc // registered only while Investigate's loop is in flight; Abort's hook
	abortReasons map[string]string

	// fallback is DR-34 §34.4's mid-loop llm -> rules swap target (w15). It
	// is read once at the top of each Investigate call, not mutated by it —
	// e.reasoner itself is never swapped (it is shared across every
	// concurrent investigation; DR-17 §17.3 allows up to
	// max_concurrent_investigations of them at once), so the swap only ever
	// changes a LOCAL variable inside that one call's Investigate loop. Nil
	// is valid: an investigation whose reasoner triggers a DR-34 §34.4
	// condition with no fallback wired simply fails with TermFailed, the
	// same behavior this wave found before SetFallbackReasoner existed.
	// Declared as its own field (not a NewEngine parameter) so this wave's
	// addition cannot change NewEngine's signature and break the two
	// existing out-of-scope call sites (internal/eval/driver.go and this
	// package's own pre-existing tests) that construct an engine today.
	fallback Reasoner
}

// SetFallbackReasoner wires DR-34 §34.4's rules-reasoner fallback target
// (typically rules.New(), constructed by the caller — rca cannot import
// internal/rca/rules itself without an import cycle, since rules already
// imports rca). Safe to call before Investigate is ever invoked; not
// intended to be changed mid-flight (concurrent Investigate calls read the
// field once at the top of their own loop, so a racing SetFallbackReasoner
// call is a data race — callers should wire this once at construction).
func (e *engine) SetFallbackReasoner(r Reasoner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fallback = r
}

// NewEngine constructs the rules-reasoner Engine. journal/registry/objects
// must be non-nil; budget/interest/clock fall back to sane in-memory/real
// defaults when nil so tests can omit what they don't exercise.
func NewEngine(journal Journal, registry ToolRegistry, reasoner Reasoner, objects ObjectPutter, interest InterestSink, clock model.Clock) *engine {
	if clock == nil {
		clock = realClock{}
	}
	return &engine{
		journal:      journal,
		budget:       NewMemBudget(),
		registry:     registry,
		reasoner:     reasoner,
		objects:      objects,
		interest:     interest,
		clock:        clock,
		invs:         make(map[string]model.Investigation),
		corr:         make(map[string][]model.Correction),
		cancels:      make(map[string]context.CancelFunc),
		abortReasons: make(map[string]string),
	}
}

// realClock is a minimal model.Clock so NewEngine has a usable default
// without depending on model.NewRealClock (still `panic("not implemented")`
// in this snapshot — see internal/model/clock.go).
type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (realClock) NewTicker(d time.Duration) model.Ticker {
	panic("realClock.NewTicker not implemented")
}
func (realClock) NewTimer(d time.Duration) model.Timer { panic("realClock.NewTimer not implemented") }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *engine) Investigate(ctx context.Context, tid model.TenantID, incident model.Incident) (model.Investigation, error) {
	// A cancellable derivative of ctx so a concurrent Abort() call (below)
	// can interrupt this loop rather than being silently clobbered by it:
	// Abort used to only mutate e.invs/the journal directly, which a
	// still-running Investigate would overwrite with its own eventual
	// (non-aborted) terminal status via the storeInv/SetStatus calls at the
	// bottom of this function — and Abort never removed the FR-F06-12a scope
	// predicate at all. Routing the abort through ctx cancellation means the
	// loop observes it at the next iteration boundary and finalizes status,
	// report, and interest-predicate removal itself, through the same single
	// terminal-status code path every other termination reason already uses.
	ctx, cancelInv := context.WithCancel(ctx)
	defer cancelInv()

	budget := DefaultBudget()
	inv := model.Investigation{
		ID:           genID(),
		Tenant:       tid,
		IncidentID:   incident.ID,
		IncidentIDs:  []string{incident.ID},
		Status:       model.InvestigationRunning,
		Phase:        model.PhaseContextualize,
		Trigger:      "auto",
		ReasonerKind: e.reasoner.Kind(),
		Budget:       budget,
		ReplaySeed:   cryptoRandInt64(),
		StartedAt:    e.clock.Now(),
	}
	if err := e.journal.Create(ctx, tid, inv); err != nil {
		return model.Investigation{}, fmt.Errorf("rca: journal create: %w", err)
	}
	e.budget.Reserve(inv.ID, budget)
	e.mu.Lock()
	e.cancels[inv.ID] = cancelInv
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancels, inv.ID)
		delete(e.abortReasons, inv.ID)
		e.mu.Unlock()
	}()
	e.storeInv(inv)

	// Contextualize: a pure-reasoning step recording what the investigation
	// starts with. No tool call, no reasoner involved yet (F06 §4.4:
	// "inv.Steps.append(contextualize(incident))").
	inv.Phase = model.PhaseHypothesize
	ctxStep := e.newPureStep(len(inv.Steps), model.PhaseContextualize, fmt.Sprintf("contextualize incident %s (epicenter=%s)", incident.ID, incident.EpicenterService))
	if err := e.journal.AppendStep(ctx, tid, inv.ID, ctxStep); err == nil {
		inv.Steps = append(inv.Steps, ctxStep)
		inv.Spend.Steps++
	}

	// FR-F06-12a: push the scope predicate before the first Hypothesize step.
	// The sink assigns and owns the predicate's real ID (InterestPredicate.ID
	// is unset by scopePredicate); that returned ID, not a reconstruction of
	// Source, is what RemoveInterestPredicate must be called with below — a
	// bug this wave's TDD pass caught: the removal call previously passed the
	// literal "rca:"+inv.ID string (the Source, not the ID), which only
	// happens to work if a sink's ID scheme is identical to its Source.
	var scopePredID string
	if e.interest != nil {
		p := scopePredicate(tid, inv, incident, e.clock.Now(), defaultScopeTTL)
		if id, err := e.interest.SetInterestPredicate(ctx, tid, p); err == nil {
			scopePredID = id
		}
	}

	var pendingArgs *ToolArgs
	var pendingResult *model.ToolResult
	var pendingHypID string

	// reasoner is a LOCAL, swappable handle on this one investigation's
	// active Reasoner (DR-34 §34.4's mid-loop llm -> rules fallback, w15):
	// e.reasoner itself is never mutated (see the engine struct's fallback
	// field doc comment). confidenceCap implements DR-34 §34.4 point 5
	// ("Confidence is capped at rca.confidence_threshold - 0.01 for the
	// remainder of the investigation") as a local rather than a
	// model.Investigation field, since DR-15's Investigation shape (as
	// scaffolded so far) has no ConfidenceCap field to persist it in and
	// this wave's scope lock does not permit adding one to internal/model.
	reasoner := e.reasoner
	e.mu.Lock()
	fallback := e.fallback
	e.mu.Unlock()
	var confidenceCap float64 // 0 == uncapped

	for {
		if ctx.Err() != nil {
			inv.TerminationReason = model.TermAborted
			break
		}
		if e.clock.Since(inv.StartedAt) > inv.Budget.WallClock {
			inv.TerminationReason = model.TermWallClock
			break
		}
		if inv.Spend.Steps >= inv.Budget.MaxSteps {
			inv.TerminationReason = model.TermSteps
			break
		}
		if inv.Spend.ToolCalls >= inv.Budget.MaxToolCalls {
			inv.TerminationReason = model.TermToolCalls
			break
		}
		if reasoner.Kind() == model.ReasonerLLM {
			// DR-17 §17.2's llm-only dimensions (F06 §4.4's main-loop
			// pseudocode: "if reasoner.Kind() == llm and ...").
			if inv.Spend.TokensIn >= int64(inv.Budget.MaxTokensIn) {
				inv.TerminationReason = model.TermTokens
				break
			}
			if inv.Spend.CachedTokensIn >= int64(inv.Budget.MaxCachedTokensIn) {
				inv.TerminationReason = model.TermCachedTokens
				break
			}
		}
		if inv.Spend.CostMicroUSD >= inv.Budget.MaxCostMicroUSD {
			inv.TerminationReason = model.TermCost
			break
		}

		state := State{
			Investigation:    inv,
			Incident:         incident,
			LastArgs:         pendingArgs,
			LastResult:       pendingResult,
			LastHypothesisID: pendingHypID,
		}
		pendingArgs, pendingResult, pendingHypID = nil, nil, ""

		stepCtx, cancel := context.WithTimeout(ctx, inv.Budget.MaxStepWallClock)
		proposal, err := reasoner.NextStep(stepCtx, tid, state)
		if err != nil {
			cancel()
			if errors.Is(err, ErrNoMoreRules) {
				inv.TerminationReason = model.TermConcluded
				break
			}
			if reasoner.Kind() == model.ReasonerLLM && fallback != nil && isFallbackTrigger(err) {
				// DR-34 §34.4: swap ONLY between steps (we are between
				// steps right here — no partial step has been recorded for
				// this failed NextStep call), never restart the
				// investigation, never swap back.
				inv.ReasonerSwaps = append(inv.ReasonerSwaps, model.ReasonerSwap{
					AtStep: len(inv.Steps),
					From:   reasoner.Kind(),
					To:     fallback.Kind(),
					Reason: err.Error(),
					At:     e.clock.Now(),
				})
				reasoner = fallback
				inv.ReasonerKind = reasoner.Kind()
				confidenceCap = confidenceThreshold - 0.01
				continue
			}
			inv.TerminationReason = model.TermFailed
			inv.Error = err.Error()
			break
		}

		mergeHypotheses(&inv, proposal.Hypotheses)

		// DR-17: fold the LLM call's real usage into Spend, whatever the
		// reasoner kind — the rules reasoner's Proposal.Usage is always the
		// zero Charge{}, so this is a no-op on that path.
		if proposal.Usage != (Charge{}) {
			inv.Spend.TokensIn += proposal.Usage.TokensIn
			inv.Spend.CachedTokensIn += proposal.Usage.CachedTokensIn
			inv.Spend.TokensOut += proposal.Usage.TokensOut
			inv.Spend.CostMicroUSD += proposal.Usage.CostMicroUSD
			_ = e.budget.Charge(ctx, tid, inv.ID, proposal.Usage)
		}

		if proposal.Done {
			cancel()
			inv.TerminationReason = model.TermConcluded
			break
		}

		if proposal.Call == nil {
			// Pure-reasoning step: typically the evaluate-fold-in step this
			// wave's rules.Reasoner emits after a dispatch (see rca.State's
			// doc comment). Counts against MaxSteps, not MaxToolCalls.
			step := e.newPureStep(len(inv.Steps), proposal.Phase, proposal.Rationale)
			step.ReasonerOutputRef, step.ReasonerOutputHash = e.putReasonerOutput(ctx, tid, inv.ID, len(inv.Steps), proposal)
			if err := e.journal.AppendStep(ctx, tid, inv.ID, step); err == nil {
				inv.Steps = append(inv.Steps, step)
			}
			inv.Spend.Steps++
			cancel()
			continue
		}

		call := *proposal.Call
		call.TenantID = tid
		seq := len(inv.Steps)
		startedAt := e.clock.Now()

		if verr := validateToolArgs(call); verr != nil {
			step := model.Step{
				ID:              genID(),
				InvestigationID: inv.ID,
				Tenant:          tid,
				Seq:             seq,
				Phase:           proposal.Phase,
				Tool:            call.Tool,
				Verdict:         model.VerdictInvalidArgs,
				StartedAt:       startedAt,
				EndedAt:         e.clock.Now(),
			}
			argsJSON, _ := canonicalJSON(call)
			step.ToolArgsJSON = string(argsJSON)
			step.ToolArgsHash = sha256Hex(argsJSON)
			_ = e.journal.AppendStep(ctx, tid, inv.ID, step)
			inv.Steps = append(inv.Steps, step)
			inv.Spend.Steps++
			inv.Spend.ToolCalls++ // "counts against max_tool_calls" (F06 §4.4)
			cancel()
			continue
		}

		result, dispatchErr := e.registry.Dispatch(stepCtx, tid, call)
		cancel()

		verdict := model.VerdictOK
		if dispatchErr != nil {
			if errors.Is(dispatchErr, ErrToolUnavailable) {
				verdict = model.VerdictUnavailable
			} else {
				verdict = model.VerdictInvalidArgs
			}
		}

		argsJSON, _ := canonicalJSON(call)
		resultBytes := result.Rows
		step := model.Step{
			ID:              genID(),
			InvestigationID: inv.ID,
			Tenant:          tid,
			Seq:             seq,
			Phase:           proposal.Phase,
			Tool:            call.Tool,
			ToolArgsJSON:    string(argsJSON),
			ToolArgsHash:    sha256Hex(argsJSON),
			Verdict:         verdict,
			StartedAt:       startedAt,
			EndedAt:         e.clock.Now(),
			Truncated:       result.Truncated,
			Clamped:         result.Clamped,
			FromCache:       result.FromCache,
			ToolResultBytes: int64(len(resultBytes)),
		}
		step.LatencyMillis = step.EndedAt.Sub(step.StartedAt).Milliseconds()

		if len(resultBytes) > 0 {
			step.ToolResultHash = sha256Hex(resultBytes)
			ref := fmt.Sprintf("evidence/%s/%s/%d.json", tid, inv.ID, seq)
			if e.objects != nil {
				if err := e.objects.Put(ctx, tid, ref, resultBytes); err == nil {
					step.ToolResultRef = ref
				}
			}
		}
		step.ReasonerOutputRef, step.ReasonerOutputHash = e.putReasonerOutput(ctx, tid, inv.ID, seq, proposal)

		if len(proposal.Hypotheses) > 0 {
			pendingHypID = proposal.Hypotheses[0].ID
		}
		pendingArgs = &call
		if dispatchErr == nil {
			rc := result
			pendingResult = &rc
		}

		_ = e.journal.AppendStep(ctx, tid, inv.ID, step)
		inv.Steps = append(inv.Steps, step)
		inv.Spend.Steps++
		inv.Spend.ToolCalls++
		_ = e.budget.Charge(ctx, tid, inv.ID, Charge{})
	}

	inv.EndedAt = e.clock.Now()
	if inv.TerminationReason == model.TermAborted {
		e.mu.Lock()
		if r := e.abortReasons[inv.ID]; r != "" {
			inv.Error = r
		}
		e.mu.Unlock()
	}

	// Finalization (report, journal status, interest-predicate teardown) must
	// still complete even when ctx was just cancelled by Abort() above — a
	// context-respecting Journal/InterestSink would otherwise fail every one
	// of these calls precisely when TermAborted needs them to succeed most.
	// WithoutCancel keeps any request-scoped values but drops the (already
	// fired) cancellation and deadline.
	finalCtx := context.WithoutCancel(ctx)

	finalState := State{Investigation: inv, Incident: incident}
	conclusion, _ := reasoner.Conclude(finalCtx, tid, finalState)
	inv.RootCause = conclusion.RootCause
	inv.Confidence = conclusion.Confidence
	if confidenceCap > 0 && inv.Confidence > confidenceCap {
		// DR-34 §34.4 point 5: a swapped investigation can never auto-
		// conclude at high confidence.
		inv.Confidence = confidenceCap
	}
	mergeHypotheses(&inv, conclusion.Hypotheses)
	inv.Phase = model.PhaseReport
	inv.ReportMarkdown = buildReport(inv)
	inv.Status = terminalStatus(inv.TerminationReason, inv.Confidence)
	e.budget.SetTerminated(inv.ID, inv.TerminationReason)
	_ = e.journal.SetStatus(finalCtx, tid, inv.ID, inv.Status, inv.EndedAt)

	if e.interest != nil {
		// Removed on any terminal status (DR-11's FR-F06-12a), best-effort.
		// Use the sink-assigned ID from Set above; fall back to the
		// Source-shaped string only if Set was never called or failed, so a
		// best-effort remove is still attempted.
		removeID := scopePredID
		if removeID == "" {
			removeID = "rca:" + inv.ID
		}
		_ = e.interest.RemoveInterestPredicate(finalCtx, tid, removeID)
		if inv.Status == model.InvestigationConcluded && inv.Confidence >= confidenceThreshold {
			errSigs, pathSigs := recurrenceSignatures(incident)
			p := recurrencePredicate(tid, inv, errSigs, pathSigs, e.clock.Now(), defaultRecurrenceTTL)
			_, _ = e.interest.SetInterestPredicate(finalCtx, tid, p)
		}
	}

	e.storeInv(inv)
	return inv, nil
}

func recurrenceSignatures(incident model.Incident) ([]string, []uint64) {
	var errSigs []string
	// Incident carries no direct ErrorSigID field; a real implementation
	// derives this from the concluding hypothesis's supporting Evidence.
	// Deferred for this wave.
	return errSigs, nil
}

func (e *engine) Get(_ context.Context, tid model.TenantID, id string) (model.Investigation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inv, ok := e.invs[id]
	if !ok || inv.Tenant != tid {
		return model.Investigation{}, fmt.Errorf("rca: investigation %q not found", id)
	}
	return inv, nil
}

func (e *engine) Replay(ctx context.Context, tid model.TenantID, investigationID string, mode ReplayMode) (model.Investigation, error) {
	original, err := e.Get(ctx, tid, investigationID)
	if err != nil {
		return model.Investigation{}, err
	}
	steps, err := e.journal.Steps(ctx, tid, investigationID)
	if err != nil {
		return model.Investigation{}, fmt.Errorf("rca: replay load steps: %w", err)
	}

	switch mode {
	case ReplayLiveDiff:
		return e.replayLiveDiff(ctx, tid, original, steps)
	default: // ReplayRecorded
		return e.replayRecorded(ctx, tid, original, steps)
	}
}

func (e *engine) replayRecorded(ctx context.Context, tid model.TenantID, original model.Investigation, steps []model.Step) (model.Investigation, error) {
	newSteps := make([]model.Step, len(steps))
	for i, st := range steps {
		if st.ToolResultRef != "" {
			body, err := e.objects.Get(ctx, tid, st.ToolResultRef)
			if err != nil {
				return model.Investigation{}, fmt.Errorf("%w: %v", ErrEvidenceCorrupt, err)
			}
			if sha256Hex(body) != st.ToolResultHash {
				return model.Investigation{}, ErrEvidenceCorrupt
			}
		}
		ns := st
		ns.ID = genID()
		ns.InvestigationID = "" // filled in below
		ns.StartedAt = time.Time{}
		ns.EndedAt = time.Time{}
		ns.LatencyMillis = 0
		newSteps[i] = ns
	}

	newInv := original
	newInv.ID = genID()
	newInv.ParentID = original.ID
	newInv.ReplayOf = original.ID
	newInv.ReplayMode = ReplayRecorded
	newInv.Spend = model.Spend{}
	newInv.StartedAt = time.Time{}
	newInv.EndedAt = time.Time{}
	for i := range newSteps {
		newSteps[i].InvestigationID = newInv.ID
	}
	newInv.Steps = newSteps

	_ = e.journal.Create(ctx, tid, newInv)
	for _, st := range newSteps {
		_ = e.journal.AppendStep(ctx, tid, newInv.ID, st)
	}
	_ = e.journal.SetStatus(ctx, tid, newInv.ID, newInv.Status, e.clock.Now())
	e.storeInv(newInv)
	return newInv, nil
}

// replayLiveDiff re-dispatches every tool step with its stored args and
// reports aggregate drift; see the engine doc comment for why per-step
// Drift is not modeled this wave.
func (e *engine) replayLiveDiff(ctx context.Context, tid model.TenantID, original model.Investigation, steps []model.Step) (model.Investigation, error) {
	drifted := 0
	for _, st := range steps {
		if st.Tool == "" || st.ToolArgsJSON == "" {
			continue
		}
		var args ToolArgs
		if err := decodeToolArgs(st.ToolArgsJSON, &args); err != nil {
			drifted++
			continue
		}
		args.TenantID = tid
		result, err := e.registry.Dispatch(ctx, tid, args)
		if err != nil {
			continue
		}
		if sha256Hex(result.Rows) != st.ToolResultHash {
			drifted++
		}
	}
	newInv := original
	newInv.ID = genID()
	newInv.ParentID = original.ID
	newInv.ReplayOf = original.ID
	newInv.ReplayMode = ReplayLiveDiff
	newInv.Spend = model.Spend{}
	newInv.ReportMarkdown = fmt.Sprintf("%s\n\nLiveDiff: %d/%d steps drifted.", original.ReportMarkdown, drifted, len(steps))
	e.storeInv(newInv)
	return newInv, nil
}

func (e *engine) Correct(_ context.Context, tid model.TenantID, investigationID, stepID string, c model.Correction) (model.Investigation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inv, ok := e.invs[investigationID]
	if !ok || inv.Tenant != tid {
		return model.Investigation{}, fmt.Errorf("rca: investigation %q not found", investigationID)
	}
	c.TargetID = stepID
	e.corr[investigationID] = append(e.corr[investigationID], c)
	inv.Version++
	e.invs[investigationID] = inv
	return inv, nil
}

// Abort marks investigationID as aborted. If Investigate's loop is currently
// running for it, Abort signals that loop's context instead of writing
// status directly: writing directly here would race the loop's own eventual
// (non-aborted) terminal-status write at the bottom of Investigate, which
// would silently clobber this call's effect, and it would skip removing the
// FR-F06-12a scope predicate entirely (that removal lives in Investigate's
// single terminal-status code path). The signaled loop observes ctx.Err() at
// its next iteration boundary, sets TerminationReason = TermAborted, and
// runs that same finalization path itself.
func (e *engine) Abort(ctx context.Context, tid model.TenantID, investigationID, reason string) error {
	e.mu.Lock()
	inv, ok := e.invs[investigationID]
	if !ok || inv.Tenant != tid {
		e.mu.Unlock()
		return fmt.Errorf("rca: investigation %q not found", investigationID)
	}
	cancelFn, running := e.cancels[investigationID]
	if running {
		e.abortReasons[investigationID] = reason
	}
	e.mu.Unlock()

	if running {
		cancelFn()
		return nil
	}

	// Not currently running (already terminal, or this call landed before
	// Investigate finished registering) — mark directly, best-effort, same
	// as before this fix.
	inv.Status = model.InvestigationAborted
	inv.TerminationReason = model.TermAborted
	inv.Error = reason
	inv.EndedAt = e.clock.Now()
	_ = e.journal.SetStatus(ctx, tid, investigationID, inv.Status, inv.EndedAt)
	e.storeInv(inv)
	return nil
}

func (e *engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	running := 0
	for _, inv := range e.invs {
		if inv.Status == model.InvestigationRunning {
			running++
		}
	}
	return Stats{RunningInvestigations: running}
}

func (e *engine) storeInv(inv model.Investigation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.invs[inv.ID] = inv
}

func (e *engine) newPureStep(seq int, phase model.Phase, note string) model.Step {
	now := e.clock.Now()
	return model.Step{
		ID:        genID(),
		Seq:       seq,
		Phase:     phase,
		StartedAt: now,
		EndedAt:   now,
		Verdict:   model.VerdictOK,
	}
}

func (e *engine) putReasonerOutput(ctx context.Context, tid model.TenantID, invID string, seq int, proposal Proposal) (ref, hash string) {
	if e.objects == nil {
		return "", ""
	}
	b, err := canonicalJSON(proposal)
	if err != nil {
		return "", ""
	}
	ref = fmt.Sprintf("reasoner/%s/%s/%d.json", tid, invID, seq)
	if err := e.objects.Put(ctx, tid, ref, b); err != nil {
		return "", ""
	}
	return ref, sha256Hex(b)
}

// mergeHypotheses applies Proposal.Hypotheses's "additions or score updates
// only" rule (DR-15): a Hypothesis.ID already present is updated in place;
// a new ID is appended.
func mergeHypotheses(inv *model.Investigation, updates []model.Hypothesis) {
	for _, u := range updates {
		found := false
		for i := range inv.Hypotheses {
			if inv.Hypotheses[i].ID == u.ID {
				inv.Hypotheses[i] = u
				found = true
				break
			}
		}
		if !found {
			inv.Hypotheses = append(inv.Hypotheses, u)
		}
	}
}

// decodeToolArgs is canonicalJSON's inverse: it unmarshals a Step's stored
// ToolArgsJSON (DR-18 §18.1: "canonical (RFC 8785) JSON of the typed
// ToolArgs, TenantID elided") back into a typed ToolArgs for ReplayLiveDiff's
// re-dispatch. This wave's canonicalJSON (journal.go) is a plain
// json.Marshal and does not actually elide TenantID from the encoded bytes
// (DR-18's elision is a stated property of the stored form this wave does
// not yet implement — see canonicalJSON's own doc comment); every call site
// that uses decodeToolArgs (replayLiveDiff) re-stamps args.TenantID = tid
// immediately after decode regardless, so a decoded TenantID is never
// trusted on its own.
func decodeToolArgs(raw string, args *ToolArgs) error {
	if raw == "" {
		return fmt.Errorf("rca: empty tool args JSON")
	}
	return json.Unmarshal([]byte(raw), args)
}

// validateToolArgs is this wave's minimal stand-in for the SchemaValidator
// component (DR-15 declares the interface; a full implementation is out of
// scope here). It enforces DR-16 §16.2's closed-shape rule: exactly one
// pointer field set, and it must match Tool.
func validateToolArgs(a ToolArgs) error {
	set := 0
	if a.Trace != nil {
		set++
	}
	if a.Log != nil {
		set++
	}
	if a.Metric != nil {
		set++
	}
	if a.Topology != nil {
		set++
	}
	if a.Memory != nil {
		set++
	}
	if set != 1 {
		return fmt.Errorf("rca: exactly one ToolArgs payload must be set, got %d", set)
	}
	switch a.Tool {
	case ToolTraceQuery:
		if a.Trace == nil {
			return fmt.Errorf("rca: tool trace_query requires Trace args")
		}
	case ToolLogQuery:
		if a.Log == nil {
			return fmt.Errorf("rca: tool log_query requires Log args")
		}
	case ToolMetricQuery:
		if a.Metric == nil {
			return fmt.Errorf("rca: tool metric_query requires Metric args")
		}
	case ToolTopologyQuery:
		if a.Topology == nil {
			return fmt.Errorf("rca: tool topology_query requires Topology args")
		}
	case ToolMemoryQuery:
		if a.Memory == nil {
			return fmt.Errorf("rca: tool memory_query requires Memory args")
		}
	default:
		return fmt.Errorf("rca: unknown tool %q", a.Tool)
	}
	return nil
}

func terminalStatus(reason model.TerminationReason, confidence float64) model.InvestigationStatus {
	switch reason {
	case model.TermConcluded:
		if confidence >= confidenceThreshold {
			return model.InvestigationConcluded
		}
		return model.InvestigationInconclusive
	case model.TermSteps, model.TermToolCalls, model.TermTokens, model.TermCachedTokens, model.TermCost, model.TermWallClock:
		return model.InvestigationBudgetExhausted
	case model.TermAborted:
		return model.InvestigationAborted
	case model.TermFailed:
		return model.InvestigationFailed
	default:
		return model.InvestigationInconclusive
	}
}

func buildReport(inv model.Investigation) string {
	if inv.RootCause == "" {
		return fmt.Sprintf("# RCA Report\n\nNo root cause confirmed within budget (%d steps, %d tool calls).\n", inv.Spend.Steps, inv.Spend.ToolCalls)
	}
	return fmt.Sprintf("# RCA Report\n\nRoot cause: %s\n\nConfidence: %.2f\n\nSteps: %d, tool calls: %d\n", inv.RootCause, inv.Confidence, inv.Spend.Steps, inv.Spend.ToolCalls)
}
