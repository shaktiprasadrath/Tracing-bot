// Package memory holds tenant-bound investigation-memory: fingerprints, a
// bounded scan, and one correction semantics (DR-19). memory.Store.Record
// takes a model.InvestigationRecord directly, so memory never imports rca
// (DR-2's cycle-break table); the embedding LLM call goes through
// internal/llm.Client, so memory never imports rca's Anthropic client
// either.
//
// Binding source: docs/architecture/06-decision-register.md, DR-19
// (verbatim Go block).
//
// Feature IDs: F08 (investigation memory).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/store, internal/llm.
package memory
