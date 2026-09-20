// Package auth is the single identity, RBAC, audit, rate-limiting, secret
// and egress-dialing surface (DR-25): "one identity model, one RBAC
// matrix, one audit sink, one rate limiter." X-SEC's linear role hierarchy
// (admin ⊇ approver ⊇ operator ⊇ viewer) is deleted in favor of the
// capability matrix in DR-25 §25.2, which keeps separation of duty
// structural (an approver cannot propose). The opaque
// tiq_<tokenID>_<secret> token model (01 §8) is canonical; the short-TTL
// JWT model is deleted (DR-25 §25.3).
//
// Binding source: docs/architecture/06-decision-register.md, DR-25
// (verbatim Go block, §25.1).
//
// Feature IDs: X-SEC (identity, RBAC, audit, rate limiting), DR-20/DR-25
// (EgressDialer — the only http.Client factory), F09 (audit of remediation
// actions), F10/F12 (chat identity binding, DR-25 §25.4).
//
// Allowed imports (DR-2's adjacency table): internal/model, internal/config,
// internal/tenant.
package auth
