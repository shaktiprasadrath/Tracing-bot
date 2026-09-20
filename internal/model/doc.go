// Package model holds every shared Go type in TraceIQ: the one definition of
// each DTO that crosses a package boundary. It is the leaf of the package
// graph (DR-2's adjacency table: "internal/model | — (leaf; stdlib only)")
// and DR-0 assigns it sole ownership of every shared Go type ("Shared Go
// types (model.*) | 01 §4 | cite 01 §4.x; may not re-declare").
//
// Binding source: docs/architecture/06-decision-register.md, principally
// DR-4 (the base declaration and the deletions table), plus every later DR
// that adds or amends a model.* type: DR-6 (store types reference these),
// DR-13 (LatencyHist), DR-14 (AnomalyEvent/Incident/Severity — promoted out
// of "anomaly" so packages that need an incident/event value never have to
// import the anomaly package), DR-15/DR-18 (Investigation/Step/Evidence and
// the RCA vocabulary — promoted out of "rca" for the same reason), DR-19
// (Record/Provenance — promoted out of "memory"), DR-21 (CriticalSLOBreach),
// DR-22 (the closed ActionProposal/ActionTarget data model), DR-31 (Clock),
// DR-37 (UntrustedKind), DR-39 (the single REDSample/Quantiles/Resolution).
//
// Feature IDs: cross-cutting — every F0x feature and X-SEC/X-OPS depend on
// this package.
//
// Allowed imports: none from internal/*. Stdlib only (DR-1's toolchain rule
// and DR-4's leaf placement). internal/archtest fails the build if this
// package imports anything non-stdlib.
package model
