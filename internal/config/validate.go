package config

import (
	"fmt"
	"strings"
)

// Validate applies a subset of 01 §7's startup validation rules (DR-26
// §26.3's numbered list) and X-OPS's dev-mode contract. This is NOT the
// full ten-rule set — it covers the rules that gate dev-mode safety and the
// ones this wave's cmd/traceiq wiring actually depends on holding
// (documented in docs/reports/w15-cmd-wiring.md): rules 2/3/4/9 plus basic
// enum checks on server.mode/server.profile. Rules 1 (per-listener
// TLS+auth), 5 (correlate tenant_mode), 6 (byte-budget/tenancy), 7 (derived
// kept-span rate), 8 (rca.llm pricing) and 10 (anomaly max_keys) are not
// implemented in this pass and are flagged as remaining work.
func Validate(cfg *Config) error {
	switch cfg.Server.Mode {
	case "single", "gateway", "sampler", "brain", "api":
	default:
		return fmt.Errorf("config: server.mode %q is not one of single|gateway|sampler|brain|api", cfg.Server.Mode)
	}
	switch cfg.Server.Profile {
	case "dev", "prod":
	default:
		return fmt.Errorf("config: server.profile %q is not one of dev|prod", cfg.Server.Profile)
	}

	// Rule 2: server.profile: prod with any auth.mode: none => exit 2.
	if cfg.Server.Profile == "prod" && cfg.Auth.Mode == "none" {
		return fmt.Errorf("config: server.profile=prod requires auth.mode != none (DR-26 rule 2)")
	}

	// Rule 3: auth.mode: none requires server.profile: dev and a loopback
	// listener (api.endpoint here; ingest listeners are checked by the
	// caller at receiver-bind time in this pass).
	if cfg.Auth.Mode == "none" {
		if cfg.Server.Profile != "dev" {
			return fmt.Errorf("config: auth.mode=none requires server.profile=dev (DR-26 rule 3)")
		}
		if !isLoopback(cfg.API.Endpoint) {
			return fmt.Errorf("config: auth.mode=none requires a loopback api.endpoint, got %q (DR-26 rule 3)", cfg.API.Endpoint)
		}
	}

	// Rule 4: alerting.gate: immediate is deleted outright.
	if cfg.Alerting.Gate == "immediate" {
		return fmt.Errorf("config: alerting.gate=immediate is deleted (DR-21); use investigation")
	}

	// Rule 9: remediate.mode: execute requires require_approval, non-empty
	// allowlist/target_allowlist, auth.mode != none, and server.profile: prod.
	if cfg.Remediate.Mode == "execute" {
		var problems []string
		if !cfg.Remediate.RequireApproval {
			problems = append(problems, "remediate.require_approval must be true")
		}
		if len(cfg.Remediate.Allowlist) == 0 {
			problems = append(problems, "remediate.allowlist must be non-empty")
		}
		if len(cfg.Remediate.TargetAllowlist) == 0 {
			problems = append(problems, "remediate.target_allowlist must be non-empty")
		}
		if cfg.Auth.Mode == "none" {
			problems = append(problems, "auth.mode must not be none")
		}
		if cfg.Server.Profile != "prod" {
			problems = append(problems, "server.profile must be prod")
		}
		if len(problems) > 0 {
			return fmt.Errorf("config: remediate.mode=execute: %s (DR-26 rule 9)", strings.Join(problems, "; "))
		}
	}

	if cfg.RCA.Reasoner == "llm" {
		if cfg.RCA.LLM.APIKeyEnv == "" && cfg.RCA.LLM.APIKeyFile == "" {
			return fmt.Errorf("config: rca.reasoner=llm requires rca.llm.api_key_env or api_key_file (DR-26 rule 8)")
		}
	}

	return nil
}

func isLoopback(endpoint string) bool {
	host := endpoint
	if i := strings.LastIndex(endpoint, ":"); i >= 0 {
		host = endpoint[:i]
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || host == ""
}
