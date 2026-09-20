package llm

import (
	"context"
	"net/http"

	"traceiq/internal/config"
	"traceiq/internal/model"
)

// StopReason is the closed set the client maps from the API's stop_reason
// (DR-34 §34.1: "stop_reason == 'refusal' checked and mapped to
// StepVerdict = refused").
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopRefusal   StopReason = "refusal"
	StopMaxTokens StopReason = "max_tokens"
)

// Message is one turn of the request's message list.
type Message struct {
	Role    string // "user" | "assistant" — no assistant prefill (DR-34 §34.1)
	Content string
}

// ToolSchema is one of the five rca tool schemas rendered into the
// request's `tools` array (DR-15's rca.Tool.Schema(), DR-34 §34.1).
type ToolSchema struct {
	Name        string
	Description string
	InputSchema []byte // JSON schema
}

// Request is the binding request shape (DR-34 §34.1): thinking: adaptive,
// output_config.effort, tool schemas sent as `tools`, no temperature
// (incompatible with adaptive thinking).
type Request struct {
	Model           string
	System          string
	Messages        []Message
	Tools           []ToolSchema
	Effort          string // low | medium | high
	Thinking        string // "adaptive"
	MaxOutputTokens int
	PromptCache     bool
}

// Usage is the parsed usage block that feeds the rca budget governor
// (DR-34 §34.1, DR-17 §17.1's cached-read dimension).
type Usage struct {
	InputTokens      int64
	CachedReadTokens int64
	CacheWriteTokens int64
	OutputTokens     int64
}

// Response is the parsed API response: strict JSON output validated by
// rca.SchemaValidator (DR-15) happens one layer up, in rca — Content here
// is the raw content-block bytes.
type Response struct {
	StopReason StopReason
	Content    []byte
	Usage      Usage
}

// Client is the one type internal/rca and internal/memory both hold
// (DR-2, DR-34 §34.1).
type Client interface {
	Invoke(ctx context.Context, tid model.TenantID, req Request) (Response, error)
	Close() error
}

// NewClient constructs the production Client from rca.llm.* configuration
// and an *http.Client obtained from auth.EgressDialer.HTTPClient(timeout)
// (DR-20 §20.3: "the ONLY http.Client factory"). The dialer itself is not a
// parameter here — internal/llm may import only model and config (DR-2) —
// so cmd/traceiq builds the *http.Client via auth.EgressDialer and passes
// the stdlib value in, keeping this package's import set at DR-2's ceiling
// while still routing every outbound call through DR-20/DR-25's egress
// controls.
func NewClient(cfg config.RCALLMConfig, httpClient *http.Client) (Client, error) {
	panic("not implemented")
}
