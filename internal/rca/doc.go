// Package rca holds the canonical RCA interface set and the types the
// investigation loop persists (DR-15), the closed tool set (DR-16), real
// token/budget accounting (DR-17), and the replay contract (DR-18).
// rca.Investigation.SuggestedActions is []model.ActionProposal, so rca
// never imports remediate (DR-2's cycle-break table; api bridges to
// remediate.Guard.Propose); the Anthropic client lives in internal/llm —
// rca.LLMReasoner holds an llm.Client rather than rca declaring its own
// Anthropic client type.
//
// Binding source: docs/architecture/06-decision-register.md, DR-15
// through DR-18 (verbatim Go blocks).
//
// Feature IDs: F06 (root-cause investigation).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/store, internal/topology, internal/anomaly,
// internal/correlate, internal/memory, internal/sampler, internal/auth,
// internal/llm.
package rca
