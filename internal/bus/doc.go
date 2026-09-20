// Package bus abstracts the span transport between TraceIQ processes when
// server.mode splits gateway/sampler/brain across a fleet (DR-32 §32.2).
// DR-32 §32.3 fixes the default driver to "none" (the in-process ring) in
// every mode, including Kubernetes — Kafka/Redpanda is enabled only when
// one of 05 D-5's three triggers holds (DR-38 §38.2, CC-33(1)). DR-8
// requires that whichever driver is active, partitioning is computed by
// sampler.ShardFor — "a custom partitioner computes partition =
// ShardFor(ring, traceID) mod partitions" — so this package carries batches
// and partition keys but never re-derives routing itself.
//
// The register never prints an explicit `package bus` Go block; the
// interface below is reconstructed from DR-8's partitioner note and DR-32's
// driver enumeration. See docs/reports/scaffold-report.md.
//
// Binding source: docs/architecture/06-decision-register.md, DR-2, DR-8
// §"Kafka [P2]", DR-32 §32.2/§32.3.
//
// Feature IDs: cross-cutting (F02 sharding, X-OPS topology).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config.
package bus
