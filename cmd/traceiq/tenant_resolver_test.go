package main

import (
	"context"
	"errors"
	"testing"

	"traceiq/internal/config"
)

// TestSimpleResolver_FailsClosedOnEmptyTenant is the composition-root half of
// the w17 final review's tenant Blocker. internal/ingest's otlpgrpc receiver
// hands FromSubject an EMPTY subject tenant for every auth mode it has not
// implemented, with an explicit comment that FromSubject "is expected to
// reject [it] as UNAUTHENTICATED -- fail-closed, not fail-open". This
// resolver was `return subjectTenant, nil`, which returned ("", nil) and let
// those spans through to be stamped with TenantID("").
func TestSimpleResolver_FailsClosedOnEmptyTenant(t *testing.T) {
	r := newTenantResolver(config.Config{})

	if _, err := r.FromSubject(context.Background(), ""); !errors.Is(err, ErrNoSubjectTenant) {
		t.Fatalf("FromSubject(\"\") = err %v, want ErrNoSubjectTenant", err)
	}

	got, err := r.FromSubject(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("FromSubject(tenant-a): unexpected error %v", err)
	}
	if got != "tenant-a" {
		t.Fatalf("FromSubject(tenant-a) = %q, want tenant-a", got)
	}
}

// TestSimpleResolver_DevDefaultRefusesEmptyDefaultTenant covers the same
// posture on the dev-default branch: blanking tenancy.default_tenant must be
// an error, not an empty-tenant dev bucket.
func TestSimpleResolver_DevDefaultRefusesEmptyDefaultTenant(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.Profile = "dev"
	cfg.Auth.Mode = "none"
	cfg.API.Endpoint = "127.0.0.1:8080"
	cfg.Tenancy.DefaultTenant = "" // explicitly blanked by an operator

	r := newTenantResolver(cfg)
	if _, err := r.FromDevDefault(context.Background()); !errors.Is(err, ErrNoDefaultTenant) {
		t.Fatalf("FromDevDefault with an empty default_tenant = err %v, want ErrNoDefaultTenant", err)
	}

	cfg.Tenancy.DefaultTenant = "default"
	r = newTenantResolver(cfg)
	got, err := r.FromDevDefault(context.Background())
	if err != nil {
		t.Fatalf("FromDevDefault: unexpected error %v", err)
	}
	if got != "default" {
		t.Fatalf("FromDevDefault = %q, want default", got)
	}
}

// TestSimpleResolver_DevDefaultGates re-asserts X-OPS §4.4's three
// preconditions, so the fail-closed additions above cannot mask a regression
// that widens the dev-default path itself.
func TestSimpleResolver_DevDefaultGates(t *testing.T) {
	base := func() config.Config {
		var c config.Config
		c.Server.Profile = "dev"
		c.Auth.Mode = "none"
		c.API.Endpoint = "127.0.0.1:8080"
		c.Tenancy.DefaultTenant = "default"
		return c
	}

	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"non-dev profile", func(c *config.Config) { c.Server.Profile = "prod" }},
		{"auth enabled", func(c *config.Config) { c.Auth.Mode = "token" }},
		{"non-loopback listener", func(c *config.Config) { c.API.Endpoint = "0.0.0.0:8080" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if _, err := newTenantResolver(cfg).FromDevDefault(context.Background()); !errors.Is(err, ErrDevDefaultNotPermitted) {
				t.Fatalf("FromDevDefault = err %v, want ErrDevDefaultNotPermitted", err)
			}
		})
	}
}
