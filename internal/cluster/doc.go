// Package cluster provides leader election for the multi-replica case DR-27
// §27.2 describes: "multi-replica | the leader (cluster.leader_election).
// A non-leader api replica forwards the append over the internal control
// channel; if that is unavailable it refuses the operation (fail-closed)."
// It is the only package in DR-2's adjacency table permitted to import
// nothing but internal/config — leader election needs no shared telemetry
// types, only its own driver configuration (cluster.leader_election.*,
// 01 §7).
//
// The register never prints an explicit `package cluster` Go block; the
// interface below is reconstructed from DR-27 §27.2's prose. See
// docs/reports/scaffold-report.md.
//
// Binding source: docs/architecture/06-decision-register.md, DR-2, DR-27
// §27.2.
//
// Feature IDs: cross-cutting (X-OPS, X-SEC audit chain).
//
// Allowed imports (DR-2's adjacency table): internal/config only.
package cluster
