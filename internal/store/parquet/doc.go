// Package parquet is the Parquet driver for store.ColdStore: per-trace
// spans appended to a cold write-ahead journal and sealed into
// time-bucketed, per-`(tenant, tier, hour)` blocks
// (spans.parquet/traces.parquet/meta.json), with the crash matrix, block
// compaction and per-trace tombstone erasure DR-7 defines. There is no
// per-trace row group and no `ColdPointer`.
//
// Binding source: docs/architecture/06-decision-register.md, DR-7
// (verbatim `ColdStore` Go block, crash matrix, retention tiers).
//
// Feature IDs: F03 (retention / cold storage).
//
// Allowed imports (DR-2's adjacency table, plus internal/archtest's
// documented allowance for a store driver subpackage to import its own
// parent): internal/model, internal/config, internal/tenant,
// internal/topology, internal/store.
package parquet
