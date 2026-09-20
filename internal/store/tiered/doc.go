// Package tiered composes the store/sqlite hot-index driver and the
// store/parquet cold-store driver into store.TieredStore: the process
// that opens both traceiq.db and control.db (DR-6 §6.2), routes each
// table to the right file and writer goroutine, drives BindColdBlock
// after a cold seal (DR-7's ordering invariant), and emits the
// store.CostSignal stream a store.CostController consumes (DR-12).
// cmd/traceiq constructs and injects this driver; no other package
// depends on it.
//
// Binding source: docs/architecture/06-decision-register.md, DR-6
// (two-file routing), DR-7 (seal/bind ordering), DR-12 (CostSignal).
//
// Feature IDs: F01 (ingestion persistence), F03 (retention).
//
// Allowed imports (DR-2's adjacency table, plus internal/archtest's
// documented allowance for a store driver subpackage to import its own
// parent): internal/model, internal/config, internal/tenant,
// internal/topology, internal/store.
package tiered
