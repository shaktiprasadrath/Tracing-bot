package config

import (
	"fmt"
	"os"
	"reflect"

	yaml "go.yaml.in/yaml/v3"
)

// Load reads and validates the configuration at path, applying 01 §7's
// precedence chain: defaults (defaults.go) -> traceiq.yaml -> TRACEIQ_
// environment -> (command-line flags are cmd/traceiq's own concern, applied
// by the caller after Load returns). A missing file is NOT an error
// (FR-XOPS-1: "traceiq run" with no config file starts on defaults alone);
// any other read error, or a YAML key that doesn't map onto a Config field,
// is.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: reading %q: %w", path, err)
			}
		} else {
			var raw map[string]interface{}
			if err := yaml.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("config: parsing %q: %w", path, err)
			}
			if raw != nil {
				if err := decodeStruct(raw, reflect.ValueOf(cfg).Elem(), ""); err != nil {
					return nil, err
				}
			}
		}
	}

	applyEnvOverrides(reflect.ValueOf(cfg).Elem())

	// ${data_dir} must resolve to the (possibly YAML/env-overridden)
	// server.data_dir before the rest of the tree is interpolated.
	interpolate(reflect.ValueOf(cfg).Elem(), cfg.Server.DataDir)

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
