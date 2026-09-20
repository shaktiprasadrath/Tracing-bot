package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_NoFile_DevDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load with missing file: %v", err)
	}
	if cfg.Server.Mode != "single" || cfg.Server.Profile != "dev" {
		t.Fatalf("unexpected server defaults: %+v", cfg.Server)
	}
	if cfg.Store.Hot.Driver != "sqlite" || cfg.Store.Cold.Driver != "parquet_local" {
		t.Fatalf("unexpected store driver defaults: %+v", cfg.Store.Hot.Driver)
	}
	if cfg.Cluster.Bus.Driver != "none" {
		t.Fatalf("cluster.bus.driver default should be none, got %q", cfg.Cluster.Bus.Driver)
	}
	// ${data_dir} interpolation.
	want := filepath.ToSlash(cfg.Server.DataDir) + "/hot/traceiq.db"
	if filepath.ToSlash(cfg.Store.Hot.SQLite.Path) != want {
		t.Fatalf("data_dir interpolation: got %q want %q", cfg.Store.Hot.SQLite.Path, want)
	}
	if cfg.Sampler.Assembly.IdleTimeout != 8*time.Second {
		t.Fatalf("duration default not applied: %v", cfg.Sampler.Assembly.IdleTimeout)
	}
}

func TestLoad_YAMLOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "traceiq.yaml")
	yamlBody := `
server:
  data_dir: ` + filepath.ToSlash(dir) + `
  log_level: debug
sampler:
  policy:
    healthy_sample_rate: 0.5
store:
  hot:
    sqlite:
      busy_timeout: 9s
`
	if err := os.WriteFile(p, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.LogLevel != "debug" {
		t.Fatalf("log_level override not applied: %q", cfg.Server.LogLevel)
	}
	if cfg.Sampler.Policy.HealthySampleRate != 0.5 {
		t.Fatalf("nested float override not applied: %v", cfg.Sampler.Policy.HealthySampleRate)
	}
	if cfg.Store.Hot.SQLite.BusyTimeout != 9*time.Second {
		t.Fatalf("nested duration override not applied: %v", cfg.Store.Hot.SQLite.BusyTimeout)
	}
	// Untouched defaults survive the overlay.
	if cfg.Server.Mode != "single" {
		t.Fatalf("untouched default clobbered: %q", cfg.Server.Mode)
	}
}

func TestLoad_UnknownKeyIsStartupError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "traceiq.yaml")
	if err := os.WriteFile(p, []byte("server:\n  not_a_real_key: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected an error for an unknown config key")
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("TRACEIQ_SAMPLER__POLICY__HEALTHY_SAMPLE_RATE", "0.42")
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sampler.Policy.HealthySampleRate != 0.42 {
		t.Fatalf("env override not applied: %v", cfg.Sampler.Policy.HealthySampleRate)
	}
}

func TestValidate_AlertingGateImmediateRejected(t *testing.T) {
	cfg := Default()
	cfg.Alerting.Gate = "immediate"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected alerting.gate=immediate to be rejected")
	}
}

func TestValidate_RemediateExecuteRequiresProdAndApproval(t *testing.T) {
	cfg := Default()
	cfg.Remediate.Mode = "execute"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected remediate.mode=execute with dev profile/no allowlist to be rejected")
	}
}

func TestValidate_DevAuthNoneRequiresLoopback(t *testing.T) {
	cfg := Default()
	cfg.Auth.Mode = "none"
	cfg.API.Endpoint = "0.0.0.0:8443"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected auth.mode=none on a non-loopback endpoint to be rejected")
	}
}

func TestYamlKeyFor_KnownTrickyFields(t *testing.T) {
	cases := map[string]string{
		"OTLPGRPC":                "otlp_grpc",
		"OTLPHTTP":                "otlp_http",
		"ClickHouse":              "clickhouse",
		"SQLite":                  "sqlite",
		"TDigestCompression":      "tdigest_compression",
		"MaxTraceIDsPerPredicate": "max_trace_ids_per_predicate",
		"PagerDuty":               "pagerduty",
		"JaegerGRPC":              "jaeger_grpc",
		"ZipkinHTTP":              "zipkin_http",
		"Red10s":                  "red_10s",
		"Topology1h":              "topology_1h",
		"EWMAAlpha":               "ewma_alpha",
		"BaseURL":                 "base_url",
		"TopK":                    "top_k",
	}
	for field, want := range cases {
		if got := yamlKeyFor(field); got != want {
			t.Errorf("yamlKeyFor(%q) = %q, want %q", field, got, want)
		}
	}
}
