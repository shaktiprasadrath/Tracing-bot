package main

import (
	"context"
	"errors"

	"traceiq/internal/config"
	"traceiq/internal/model"
	"traceiq/internal/tenant"
)

// ErrDevDefaultNotPermitted mirrors X-OPS §4.4's FromDevDefault gate.
var ErrDevDefaultNotPermitted = errors.New("tenant: FromDevDefault is only permitted at server.profile=dev with a loopback listener and auth.mode=none")

// simpleResolver is the composition root's tenant.Resolver (DR-3):
// internal/tenant declares the interface only (FromSubject/FromDevDefault
// both panic("not implemented") in the scaffold, see internal/tenant/
// tenant.go) — this pass supplies the concrete implementation here, since
// internal/tenant is out of this wave's edit scope. FromSubject is the sole
// production path (DR-5): the tenant is exactly the subject's tenant, never
// re-derived from a header/body/query field. FromDevDefault is gated on the
// same three conditions X-OPS §4.4 states.
type simpleResolver struct {
	cfg config.Config
}

func newTenantResolver(cfg config.Config) tenant.Resolver {
	return &simpleResolver{cfg: cfg}
}

// ErrNoSubjectTenant is FromSubject's fail-closed refusal of a principal that
// carries no tenant.
var ErrNoSubjectTenant = errors.New("tenant: authenticated subject carries no TenantID")

// ErrNoDefaultTenant is FromDevDefault's refusal when tenancy.default_tenant
// is itself unset — "" is not a tenant, so there is nothing to default to.
var ErrNoDefaultTenant = errors.New("tenant: tenancy.default_tenant is empty; FromDevDefault has no tenant to return")

// FromSubject is DR-3's sole production resolution path.
//
// w17 final-review fix (BLOCKER): this was `return subjectTenant, nil` — an
// unconditional pass-through. internal/ingest's otlpgrpc receiver hands an
// EMPTY subject tenant to this method for every auth mode it has not
// implemented, with an explicit comment that FromSubject "is expected to
// reject [it] as UNAUTHENTICATED -- fail-closed, not fail-open". The
// pass-through instead returned ("", nil), so those spans were accepted and
// written under TenantID(""). Rejecting an empty subject tenant here restores
// the contract the ingest side was written against; internal/ingest now
// re-checks the resolved value as well, since Resolver is an injected seam.
func (r *simpleResolver) FromSubject(ctx context.Context, subjectTenant model.TenantID) (model.TenantID, error) {
	if subjectTenant == "" {
		return "", ErrNoSubjectTenant
	}
	return subjectTenant, nil
}

func (r *simpleResolver) FromDevDefault(ctx context.Context) (model.TenantID, error) {
	if r.cfg.Server.Profile != "dev" {
		return "", ErrDevDefaultNotPermitted
	}
	if r.cfg.Auth.Mode != "none" {
		return "", ErrDevDefaultNotPermitted
	}
	if !isLoopbackHost(r.cfg.API.Endpoint) {
		return "", ErrDevDefaultNotPermitted
	}
	// Same fail-closed posture as FromSubject: an operator who blanks
	// tenancy.default_tenant gets an error, not an empty-tenant dev bucket.
	if r.cfg.Tenancy.DefaultTenant == "" {
		return "", ErrNoDefaultTenant
	}
	return model.TenantID(r.cfg.Tenancy.DefaultTenant), nil
}

func isLoopbackHost(endpoint string) bool {
	host := endpoint
	for i := len(endpoint) - 1; i >= 0; i-- {
		if endpoint[i] == ':' {
			host = endpoint[:i]
			break
		}
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || host == ""
}
