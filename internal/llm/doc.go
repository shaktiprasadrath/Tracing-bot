// Package llm holds the Anthropic HTTP client (DR-34 §34.1): "internal/llm
// (DR-2) holds the client: stdlib net/http through auth.EgressDialer
// (DR-20/DR-25), streaming JSON decode, retry on 429/5xx honouring
// Retry-After, and a usage parser that feeds the budget governor."
// llm.Client is the one type internal/rca and internal/memory both hold,
// which is what kills the memory -> rca import cycle DR-2 lists
// ("Kills memory.AnthropicEmbedder{-client rca.AnthropicClient}").
//
// The register gives the binding request/response *shape* in prose
// (DR-34 §34.1: thinking: adaptive, output_config.effort, no assistant
// prefill, tool schemas as `tools`, stop_reason == "refusal" mapped to
// StepVerdict = refused, usage feeding the budget governor) but no
// explicit `package llm` Go block. The types below are reconstructed from
// that prose. See docs/reports/scaffold-report.md.
//
// Binding source: docs/architecture/06-decision-register.md, DR-2, DR-34.
//
// Feature IDs: F06 (RCA), F08 (memory embeddings via Embedder).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config.
package llm
