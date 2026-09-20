// Package store holds the hot index (span rows, an attribute index, two
// SQLite files, DR-6), the cold store (one write model, one crash matrix,
// tombstones for per-trace erasure, DR-7), sharding (DR-8), and assembly
// (DR-9). store.Trace carries model.StorageTier and model.KeepReason so
// store never needs sampler.Reason, which is deleted (DR-4, DR-2's
// cycle-break table). topology.EdgeSink / topology.EdgeSource are declared
// in topology and satisfied by store/sqlite and store/clickhouse, injected
// by cmd/traceiq — store depends on topology (for the topology.Edge value
// type) but topology never imports store.
//
// Binding source: docs/architecture/06-decision-register.md, DR-6 through
// DR-9 (verbatim Go blocks).
//
// Feature IDs: F01 (ingestion persistence), F03 (retention).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/topology. Driver subpackages (store/sqlite,
// store/parquet, store/tiered, store/blob, store/clickhouse) implement this
// package's storage-engine interfaces.
package store
