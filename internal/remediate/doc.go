// Package remediate holds the closed remediation-action enum with typed
// fields (DR-22) and the Guard interface's 9-state machine with a budget
// that bounds blast radius (DR-23). remediate.Verifier takes the locally
// declared remediate.RecoverySignal, so remediate never imports anomaly
// (DR-2's cycle-break table); cluster writes go exclusively through
// internal/k8s.Executor / k8s.BuildArgv (DR-24) — there is no second
// cluster-write path.
//
// Binding source: docs/architecture/06-decision-register.md, DR-22 and
// DR-23 (verbatim Go blocks).
//
// Feature IDs: F09 (remediation).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant, internal/store, internal/topology, internal/auth,
// internal/k8s.
package remediate
