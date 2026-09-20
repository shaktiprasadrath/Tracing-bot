package api

import (
	"context"
	"net/http"

	"traceiq/internal/auth"
)

// --- DR-25 capability strings this package's handlers gate on. ---
//
// internal/auth ships only interfaces in this wave (no concrete
// Authenticator/Authorizer implementation exists yet — see
// internal/auth/auth.go's doc comments). Per the task brief this package
// uses the same injectable-seam pattern internal/nl already established
// (nl.Authorizer / nl.RulesAnswerer.AuthZ): Deps.Auth/Deps.Authorizer are
// held as the real auth.Authenticator/auth.Authorizer interfaces, and a
// caller (production wiring or a test) supplies a concrete implementation.
// A nil Auth/Authorizer FAILS CLOSED (401/403), never silently allows.
const (
	CapTelemetryRead        = auth.Capability("telemetry:read")
	CapIncidentInvestigate  = auth.Capability("incident:investigate") // matches nl.go's literal
	CapRemediatePropose     = auth.Capability("remediation_action:propose")
	CapRemediateApprove     = auth.Capability("remediation_action:approve")
	CapAdmin                = auth.Capability("admin:manage")
	CapAuditRead            = auth.Capability("audit:read")
	CapInvestigationReplay  = auth.Capability("investigation:replay")
	CapInvestigationCorrect = auth.Capability("investigation:correct")
)

type ctxKey int

const subjectCtxKey ctxKey = 1

func withSubject(ctx context.Context, s auth.Subject) context.Context {
	return context.WithValue(ctx, subjectCtxKey, s)
}

// subjectFrom returns the authenticated caller stashed by authenticate. The
// bool is false only if authenticate never ran (a wiring bug in this
// package, not a caller condition) — every registered route goes through
// authenticate first.
func subjectFrom(ctx context.Context) (auth.Subject, bool) {
	s, ok := ctx.Value(subjectCtxKey).(auth.Subject)
	return s, ok
}

// authenticate resolves the caller via Deps.Auth and stores the Subject on
// the request context. FAIL CLOSED: a nil Authenticator or an Authenticate
// error is 401, never an anonymous pass-through.
func (s *HTTPServer) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Deps.Auth == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication is not configured")
			return
		}
		subject, err := s.Deps.Auth.Authenticate(r.Context(), r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication failed: "+err.Error())
			return
		}
		// w17 final-review fix: every handler below reads subject.Tenant and
		// passes it straight to a tenant-scoped store/graph/detector call
		// (DR-5's ctx+TenantID first-param rule). A Subject that authenticated
		// but carries no tenant — a token with no tenant claim, or an
		// Authenticator that returns a partially-populated Subject — would
		// therefore query and write under TenantID(""), a shared bucket no real
		// tenant owns. DR-3 makes a non-empty tenant a property of the
		// principal, so a tenant-less principal is not authenticated at all.
		if subject.Tenant == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authenticated subject carries no tenant")
			return
		}
		next(w, r.WithContext(withSubject(r.Context(), subject)))
	}
}

// requireCapability gates a handler on a DR-25 capability, checked through
// Deps.Authorizer.Can (the capability matrix — DR-25 §25.2 — is the sole
// source of truth, never a role-name hierarchy). FAIL CLOSED: a nil
// Authorizer or a Can error is 403.
func (s *HTTPServer) requireCapability(cap auth.Capability, next http.HandlerFunc) http.HandlerFunc {
	return s.authenticate(func(w http.ResponseWriter, r *http.Request) {
		subject, ok := subjectFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "no authenticated subject")
			return
		}
		if s.Deps.Authorizer == nil {
			writeError(w, http.StatusForbidden, "forbidden", "authorization is not configured")
			return
		}
		res := auth.ResourceRef{Tenant: subject.Tenant}
		if err := s.Deps.Authorizer.Can(r.Context(), subject, cap, res); err != nil {
			writeError(w, http.StatusForbidden, "forbidden", "capability "+string(cap)+" denied: "+err.Error())
			return
		}
		next(w, r)
	})
}

// unauthenticated wraps a handler that deliberately skips auth (healthz/
// readyz, per F12 §4.3's endpoint table: "unauthenticated").
func unauthenticated(next http.HandlerFunc) http.HandlerFunc { return next }
