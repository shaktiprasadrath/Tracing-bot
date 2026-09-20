package llm

import (
	"context"
	"sync"

	"traceiq/internal/model"
)

// MockClient is a fully in-process, mockable llm.Client (this wave's brief:
// "no live Anthropic API key available in this environment ... llm.Client
// interface must be mockable ... do not make real network calls in tests").
// It makes zero network calls; every Invoke is answered by InvokeFunc (or,
// if that is nil, by walking Responses/Errors in call order), which lets a
// test script exactly the sequence of transport failures, refusals, and
// malformed/well-formed structured outputs an rca.LLMReasoner test needs.
// Safe for concurrent use (Invoke may be called from more than one
// goroutine across a test suite even though a single investigation's own
// NextStep calls are sequential).
type MockClient struct {
	mu sync.Mutex

	// InvokeFunc, if set, is called for every Invoke — the most flexible
	// seam (it sees the exact Request, so a test can assert on the
	// constructed prompt/system string/tools list).
	InvokeFunc func(ctx context.Context, tid model.TenantID, req Request) (Response, error)

	// Responses/Errors are consulted in order, one pair per call, when
	// InvokeFunc is nil. The last entry repeats once exhausted, so a test
	// doesn't have to size the slice exactly to the number of NextStep
	// calls it expects.
	Responses []Response
	Errors    []error

	// Requests records every Request Invoke was called with, in order, so a
	// test can assert on what was actually sent (e.g. that a rendered
	// prompt contains the untrusted-wrapper markers and not a raw dump).
	Requests []Request

	calls   int
	closed  bool
	closeFn func() error
}

// NewMockClient returns a MockClient ready to use with no configuration —
// every Invoke call fails until Responses/Errors/InvokeFunc is set, which is
// the safe default (a misconfigured test fails loudly rather than silently
// returning a zero Response).
func NewMockClient() *MockClient {
	return &MockClient{}
}

func (m *MockClient) Invoke(ctx context.Context, tid model.TenantID, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests = append(m.Requests, req)
	idx := m.calls
	m.calls++

	if m.InvokeFunc != nil {
		return m.InvokeFunc(ctx, tid, req)
	}

	var resp Response
	var err error
	if len(m.Responses) > 0 {
		i := idx
		if i >= len(m.Responses) {
			i = len(m.Responses) - 1
		}
		resp = m.Responses[i]
	}
	if len(m.Errors) > 0 {
		i := idx
		if i >= len(m.Errors) {
			i = len(m.Errors) - 1
		}
		err = m.Errors[i]
	}
	if len(m.Responses) == 0 && len(m.Errors) == 0 {
		err = errUnconfigured
	}
	return resp, err
}

func (m *MockClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// CallCount returns how many times Invoke has been called, for tests that
// need to assert an exact retry count.
func (m *MockClient) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

var errUnconfigured = mockError("llm: MockClient.Invoke called with no Responses/Errors/InvokeFunc configured")

type mockError string

func (e mockError) Error() string { return string(e) }

var _ Client = (*MockClient)(nil)
