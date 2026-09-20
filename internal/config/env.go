package config

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// envPrefix is 01 §7's environment-override prefix: "TRACEIQ_" followed by
// a "__"-separated path, e.g. TRACEIQ_SAMPLER__POLICY__HEALTHY_SAMPLE_RATE.
const envPrefix = "TRACEIQ_"

// applyEnvOverrides scans os.Environ() for TRACEIQ_-prefixed variables and,
// for each whose "__"-separated path resolves to a leaf field of v, sets
// that field from the variable's string value. Unlike YAML's unknown-key
// rule, an env var that doesn't fully resolve to a field is silently
// skipped rather than erroring: TRACEIQ_-prefixed process environment can
// carry values (e.g. TRACEIQ_ANTHROPIC_API_KEY, named by rca.llm.api_key_env)
// that are not themselves config paths.
func applyEnvOverrides(v reflect.Value) {
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, envPrefix) {
			continue
		}
		rest := strings.TrimPrefix(name, envPrefix)
		if rest == "" || !strings.Contains(rest, "__") {
			continue
		}
		segments := strings.Split(strings.ToLower(rest), "__")
		fv, ok := resolvePath(v, segments)
		if !ok || !fv.CanSet() {
			continue
		}
		setFromEnvString(fv, val)
	}
}

// setFromEnvString assigns val (always a raw string, unlike YAML's
// natively-typed scalars) onto fv, parsing it per fv's kind. Unparsable or
// unsupported values are skipped rather than failing startup, since a
// stray TRACEIQ_-prefixed env var carries lower stakes than a malformed
// traceiq.yaml key.
func setFromEnvString(fv reflect.Value, val string) {
	if fv.Type() == durationType {
		if d, err := time.ParseDuration(val); err == nil {
			fv.SetInt(int64(d))
		}
		return
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(val)
	case reflect.Bool:
		if b, err := strconv.ParseBool(val); err == nil {
			fv.SetBool(b)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			fv.SetInt(n)
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			fv.SetFloat(f)
		}
	}
}

// resolvePath walks segments (already-lowercased snake_case keys) down
// nested structs starting at v, returning the leaf field if every segment
// matches a struct field's yamlKeyFor name.
func resolvePath(v reflect.Value, segments []string) (reflect.Value, bool) {
	cur := v
	for _, seg := range segments {
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		t := cur.Type()
		found := false
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if yamlKeyFor(f.Name) == seg {
				cur = cur.Field(i)
				found = true
				break
			}
		}
		if !found {
			return reflect.Value{}, false
		}
	}
	return cur, true
}
