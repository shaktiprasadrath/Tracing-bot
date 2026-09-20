package rca

// LLMReasoner.NextStep tests driven by llm.MockClient (no network calls;
// see internal/llm/mock.go). Covers items (b)/(c)/(d)/(e)/(f) of this
// wave's brief at the single-reasoner-call level; engine_llm_test.go
// (package rca_test) covers the same triggers through the full
// engine.Investigate loop, including the mid-loop swap to a real
// rules.Reasoner.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"traceiq/internal/llm"
	"traceiq/internal/model"
)

func testState(invID string) State {
	return State{
		Investigation: testInv(invID),
		Incident:      model.Incident{ID: "inc-1", EpicenterService: "checkout"},
	}
}

const validNoCallJSON = `{"phase":"hypothesize","done":false,"rationale":"looking around","hypotheses":[],"tool_call":null}`

// --- (f) a successful mocked round-trip produces a valid proposal ---

func TestLLMReasoner_NextStep_HappyPath(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{{
		StopReason: llm.StopEndTurn,
		Content:    []byte(`{"phase":"test","done":false,"rationale":"testing pool exhaustion","hypotheses":[{"id":"h1","statement":"pool exhaustion","category":"resource_exhaustion","component":"checkout","post_score":0.3,"status":"testing"}],"tool_call":{"tool":"metric_query","metric":{"TemplateID":"pool_wait_and_exhaustion_logs","Params":{"service":"checkout"}}}}`),
		Usage:      llm.Usage{InputTokens: 3500, CachedReadTokens: 6700, OutputTokens: 220},
	}}
	r := &LLMReasoner{Client: mock, Pricing: LLMPricing{InputMicroUSDPerM: 1000, CachedReadMicroUSDPerM: 100, OutputMicroUSDPerM: 5000}}

	prop, err := r.NextStep(context.Background(), testTenant(), testState("inv-happy"))
	if err != nil {
		t.Fatalf("NextStep: %v", err)
	}
	if prop.Call == nil || prop.Call.Tool != ToolMetricQuery {
		t.Fatalf("Call = %+v, want a metric_query call", prop.Call)
	}
	if prop.Call.Metric == nil || prop.Call.Metric.TemplateID != "pool_wait_and_exhaustion_logs" {
		t.Fatalf("Call.Metric = %+v, want the parsed template id", prop.Call.Metric)
	}
	if len(prop.Hypotheses) != 1 || prop.Hypotheses[0].Source != model.HypSourceLLM {
		t.Fatalf("Hypotheses = %+v", prop.Hypotheses)
	}
	wantCost := (int64(3500)*1000 + int64(6700)*100 + int64(220)*5000) / 1_000_000
	if prop.Usage.TokensIn != 3500 || prop.Usage.CachedTokensIn != 6700 || prop.Usage.TokensOut != 220 || prop.Usage.CostMicroUSD != wantCost {
		t.Fatalf("Usage = %+v, want TokensIn=3500 CachedTokensIn=6700 TokensOut=220 CostMicroUSD=%d", prop.Usage, wantCost)
	}
	if mock.CallCount() != 1 {
		t.Fatalf("CallCount = %d, want exactly 1 for a well-formed first response", mock.CallCount())
	}
}

// --- (a) the CONSTRUCTED REQUEST wraps telemetry, proven at the Client boundary ---

func TestLLMReasoner_NextStep_RequestNeverCarriesRawUnwrappedToolResult(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{{StopReason: llm.StopEndTurn, Content: []byte(validNoCallJSON)}}
	r := &LLMReasoner{Client: mock}

	const marker = "UNWRAPPED_RAW_SPAN_MARKER"
	s := testState("inv-req")
	s.LastResult = &model.ToolResult{Rows: []byte(`{"x":"` + marker + `"}`), Tool: ToolTraceQuery}

	if _, err := r.NextStep(context.Background(), testTenant(), s); err != nil {
		t.Fatalf("NextStep: %v", err)
	}
	if len(mock.Requests) != 1 {
		t.Fatalf("expected exactly one recorded Request, got %d", len(mock.Requests))
	}
	sent := mock.Requests[0]
	if len(sent.Messages) != 1 {
		t.Fatalf("Messages = %+v, want exactly one", sent.Messages)
	}
	body := sent.Messages[0].Content
	if !strings.Contains(body, `<untrusted k="telemetry"`) {
		t.Fatalf("request body never wraps the tool result in the telemetry untrusted region: %s", body)
	}
	// The marker must appear ONLY inside the wrapper, never as a bare,
	// unwrapped substring before the opening tag.
	openIdx := strings.Index(body, `<untrusted k="telemetry"`)
	markerIdx := strings.Index(body, marker)
	if markerIdx < openIdx {
		t.Fatalf("marker appears before the untrusted wrapper opens (unwrapped): body=%s", body)
	}
}

// --- (b) malformed output is rejected, not silently accepted, then falls back ---

func TestLLMReasoner_NextStep_MalformedOutputRetriesThenSignalsFallback(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{
		{StopReason: llm.StopEndTurn, Content: []byte(`this is not json`)},
		{StopReason: llm.StopEndTurn, Content: []byte(`{"phase":"test","done":false,"rationale":"x","hypotheses":[],"tool_call":null,"unknown_field":1}`)},
		{StopReason: llm.StopEndTurn, Content: []byte(`still not valid`)},
	}
	r := &LLMReasoner{Client: mock, MaxRetries: 2}

	_, err := r.NextStep(context.Background(), testTenant(), testState("inv-malformed"))
	if err == nil {
		t.Fatal("expected an error for malformed output surviving every retry, got nil")
	}
	if !errors.Is(err, ErrLLMSchemaInvalid) {
		t.Fatalf("err = %v, want it to wrap ErrLLMSchemaInvalid (a fallback trigger)", err)
	}
	if !isFallbackTrigger(err) {
		t.Fatalf("err = %v must be recognized as a fallback trigger", err)
	}
	if mock.CallCount() != 3 {
		t.Fatalf("CallCount = %d, want exactly 3 (1 initial + 2 retries, MaxRetries=2)", mock.CallCount())
	}
}

func TestLLMReasoner_NextStep_MalformedThenValidSucceedsWithinRetryBudget(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{
		{StopReason: llm.StopEndTurn, Content: []byte(`garbage`)},
		{StopReason: llm.StopEndTurn, Content: []byte(validNoCallJSON)},
	}
	r := &LLMReasoner{Client: mock, MaxRetries: 2}
	prop, err := r.NextStep(context.Background(), testTenant(), testState("inv-recover"))
	if err != nil {
		t.Fatalf("NextStep: %v (expected the second attempt to succeed)", err)
	}
	if prop.Phase != model.PhaseHypothesize {
		t.Errorf("Phase = %v, want PhaseHypothesize", prop.Phase)
	}
	if mock.CallCount() != 2 {
		t.Fatalf("CallCount = %d, want exactly 2", mock.CallCount())
	}
}

// --- (c) an SSRF-shaped tool-call argument is rejected before dispatch ---

func TestLLMReasoner_NextStep_RejectsSSRFShapedToolArgBeforeDispatch(t *testing.T) {
	mock := llm.NewMockClient()
	ssrfContent := []byte(`{"phase":"test","done":false,"rationale":"x","hypotheses":[],"tool_call":{"tool":"trace_query","trace":{"Service":"http://169.254.169.254/latest/meta-data"}}}`)
	mock.Responses = []llm.Response{
		{StopReason: llm.StopEndTurn, Content: ssrfContent},
		{StopReason: llm.StopEndTurn, Content: ssrfContent},
		{StopReason: llm.StopEndTurn, Content: ssrfContent},
	}
	r := &LLMReasoner{Client: mock, MaxRetries: 2}

	prop, err := r.NextStep(context.Background(), testTenant(), testState("inv-ssrf"))
	if err == nil {
		t.Fatalf("expected an error rejecting the SSRF-shaped argument, got a proposal: %+v", prop)
	}
	if !errors.Is(err, ErrLLMSchemaInvalid) {
		t.Fatalf("err = %v, want it to wrap ErrLLMSchemaInvalid (SSRF-rejected args are treated as malformed output)", err)
	}
	if prop.Call != nil {
		t.Fatalf("a rejected proposal must never carry a Call a caller could accidentally dispatch: %+v", prop.Call)
	}
}

// --- (e) transport error triggers fallback, no retry burned on it ---

func TestLLMReasoner_NextStep_TransportErrorIsImmediateFallbackTrigger(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Errors = []error{errors.New("connection reset by peer")}
	r := &LLMReasoner{Client: mock, MaxRetries: 2}

	_, err := r.NextStep(context.Background(), testTenant(), testState("inv-transport"))
	if !errors.Is(err, ErrLLMTransport) {
		t.Fatalf("err = %v, want it to wrap ErrLLMTransport", err)
	}
	if !isFallbackTrigger(err) {
		t.Fatalf("err = %v must be a fallback trigger", err)
	}
	if mock.CallCount() != 1 {
		t.Fatalf("CallCount = %d, want exactly 1 (transport errors are not retried by the schema loop)", mock.CallCount())
	}
}

// --- (e) refusal triggers fallback ---

func TestLLMReasoner_NextStep_RefusalIsFallbackTrigger(t *testing.T) {
	mock := llm.NewMockClient()
	mock.Responses = []llm.Response{{StopReason: llm.StopRefusal, Content: []byte(`{}`)}}
	r := &LLMReasoner{Client: mock}

	_, err := r.NextStep(context.Background(), testTenant(), testState("inv-refused"))
	if !errors.Is(err, ErrLLMRefused) {
		t.Fatalf("err = %v, want it to wrap ErrLLMRefused", err)
	}
	if !isFallbackTrigger(err) {
		t.Fatalf("err = %v must be a fallback trigger", err)
	}
}

// --- canary echo triggers fallback ---

func TestLLMReasoner_NextStep_CanaryEchoIsFallbackTrigger(t *testing.T) {
	mock := llm.NewMockClient()
	mock.InvokeFunc = func(_ context.Context, _ model.TenantID, req llm.Request) (llm.Response, error) {
		// Extract the canary this call's system prompt embedded, and echo it
		// straight back — simulating a successful prompt-injection attempt
		// that convinced the model to leak the delimiter.
		const marker = `Never include the literal string "`
		idx := strings.Index(req.System, marker)
		if idx < 0 {
			t.Fatalf("system prompt does not contain the expected canary-warning marker: %s", req.System)
		}
		rest := req.System[idx+len(marker):]
		canary := rest[:strings.Index(rest, `"`)]
		return llm.Response{StopReason: llm.StopEndTurn, Content: []byte(`leaking canary ` + canary + ` right here`)}, nil
	}
	r := &LLMReasoner{Client: mock}

	_, err := r.NextStep(context.Background(), testTenant(), testState("inv-canary"))
	if !errors.Is(err, ErrLLMCanaryViolation) {
		t.Fatalf("err = %v, want it to wrap ErrLLMCanaryViolation", err)
	}
	if !isFallbackTrigger(err) {
		t.Fatalf("err = %v must be a fallback trigger", err)
	}
}

// --- (d) budget preflight: no LLM call is ever made once the budget is gone ---

func TestLLMReasoner_NextStep_BudgetExceededSkipsTheCallEntirely(t *testing.T) {
	mock := llm.NewMockClient() // unconfigured: any Invoke call fails the test via errUnconfigured
	r := &LLMReasoner{Client: mock}

	s := testState("inv-budget")
	s.Investigation.Spend.ToolCalls = s.Investigation.Budget.MaxToolCalls // already exhausted

	_, err := r.NextStep(context.Background(), testTenant(), s)
	if !errors.Is(err, ErrLLMBudgetExceeded) {
		t.Fatalf("err = %v, want it to wrap ErrLLMBudgetExceeded", err)
	}
	if !isFallbackTrigger(err) {
		t.Fatalf("err = %v must be a fallback trigger", err)
	}
	if mock.CallCount() != 0 {
		t.Fatalf("CallCount = %d, want 0: a budget-exhausted preflight must never place the LLM call", mock.CallCount())
	}
}

// --- nil Client is a transport-class error, not a panic ---

func TestLLMReasoner_NextStep_NilClientDoesNotPanic(t *testing.T) {
	r := &LLMReasoner{}
	_, err := r.NextStep(context.Background(), testTenant(), testState("inv-nilclient"))
	if !errors.Is(err, ErrLLMTransport) {
		t.Fatalf("err = %v, want ErrLLMTransport for a nil Client", err)
	}
}

// --- Conclude picks the best supported hypothesis, mirroring rules.Reasoner ---

func TestLLMReasoner_Conclude(t *testing.T) {
	r := &LLMReasoner{}
	s := State{Investigation: model.Investigation{Hypotheses: []model.Hypothesis{
		{ID: "h1", Statement: "low", Status: model.HypothesisSupported, PostScore: 0.6},
		{ID: "h2", Statement: "high", Status: model.HypothesisSupported, PostScore: 0.9},
		{ID: "h3", Statement: "refuted", Status: model.HypothesisRefuted, PostScore: 0.99},
	}}}
	c, err := r.Conclude(context.Background(), testTenant(), s)
	if err != nil {
		t.Fatalf("Conclude: %v", err)
	}
	if c.RootCause != "high" || c.Confidence != 0.9 {
		t.Fatalf("Conclusion = %+v, want the highest-scoring Supported hypothesis", c)
	}
}

func TestLLMReasoner_Conclude_NoneSupportedIsEmptyNotError(t *testing.T) {
	r := &LLMReasoner{}
	c, err := r.Conclude(context.Background(), testTenant(), State{})
	if err != nil {
		t.Fatalf("Conclude: %v", err)
	}
	if c.RootCause != "" {
		t.Errorf("RootCause = %q, want empty when nothing is Supported", c.RootCause)
	}
}
