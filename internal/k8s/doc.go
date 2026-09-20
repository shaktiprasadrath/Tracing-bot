// Package k8s is the sole Kubernetes access surface: a pinned kubectl
// binary invoked with a constructed argv, never client-go (DR-24). The
// board rejected k8s.io/client-go because it pulls roughly forty modules
// into a module graph the design deliberately holds small as a security
// control (DR-24, Appendix B) — the release image instead bakes in a
// digest-pinned kubectl next to a distroless, shell-less base.
//
// Binding source: docs/architecture/06-decision-register.md, DR-24
// (verbatim Go block).
//
// Feature IDs: F09 (remediation executor), F11 (eval live-mode faults, via
// the identical BuildArgv/Executor path — DR-36 §36.7).
//
// Allowed imports (DR-2's adjacency table): internal/model only.
package k8s
