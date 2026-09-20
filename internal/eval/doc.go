// Package eval holds the real-pipeline evaluation harness: a virtual
// clock, isolation, and CI gates that actually fail (DR-36). The harness
// uses the same internal/k8s.Executor and internal/k8s.BuildArgv as
// production remediation — there is no second cluster-write path
// (DR-24 rule 8, DR-36 §36.7).
//
// Binding source: docs/architecture/06-decision-register.md, DR-36
// (verbatim Go block).
//
// Feature IDs: F11 (evaluation harness).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/ingest, internal/store, internal/anomaly,
// internal/rca.
package eval
