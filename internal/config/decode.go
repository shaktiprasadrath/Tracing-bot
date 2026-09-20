package config

import (
	"fmt"
	"reflect"
	"time"
	"unicode"
)

// fieldKeyOverrides lists Go field names whose mechanical CamelCase ->
// snake_case conversion (yamlKeyFor) does not reproduce 01 §7's actual YAML
// key, because the field name fuses an acronym with an adjacent word in a
// way the generic algorithm cannot disambiguate (e.g. "ClickHouse" is one
// YAML word "clickhouse", not two). Verified by hand against every 01 §7
// key transcribed into defaults.go.
var fieldKeyOverrides = map[string]string{
	"OTLPGRPC":                "otlp_grpc",
	"OTLPHTTP":                "otlp_http",
	"ClickHouse":              "clickhouse",
	"SQLite":                  "sqlite",
	"TDigestCompression":      "tdigest_compression",
	"MaxTraceIDsPerPredicate": "max_trace_ids_per_predicate",
	"PagerDuty":               "pagerduty",
}

// yamlKeyFor converts a Go exported field name to its 01 §7 snake_case YAML
// key. This package deliberately carries no per-field `yaml:"..."` struct
// tags (config.go, ~250 fields, is generated/maintained elsewhere per its
// own doc.go); this mechanical converter plus fieldKeyOverrides's short
// exception list is this pass's substitute, so config.go itself needn't be
// touched to gain real YAML loading.
func yamlKeyFor(field string) string {
	if k, ok := fieldKeyOverrides[field]; ok {
		return k
	}
	var out []rune
	runes := []rune(field)
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			switch {
			case unicode.IsDigit(r) && unicode.IsLetter(prev):
				out = append(out, '_')
			case unicode.IsUpper(r) && unicode.IsLower(prev):
				out = append(out, '_')
			case unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(prev):
				out = append(out, '_')
			}
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

var durationType = reflect.TypeOf(time.Duration(0))

// decodeStruct overlays raw (a YAML mapping decoded generically into
// map[string]interface{}) onto v, an addressable struct value. Unmatched
// keys are a hard error (01 §7: "Unknown keys are a startup error, not a
// warning"), enforced at every nesting level.
func decodeStruct(raw map[string]interface{}, v reflect.Value, path string) error {
	t := v.Type()
	used := make(map[string]bool, len(raw))
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		key := yamlKeyFor(f.Name)
		val, ok := raw[key]
		if !ok {
			continue
		}
		used[key] = true
		childPath := path + key
		if err := decodeValue(val, v.Field(i), childPath); err != nil {
			return err
		}
	}
	for k := range raw {
		if !used[k] {
			return fmt.Errorf("config: unknown key %q", path+k)
		}
	}
	return nil
}

func decodeValue(raw interface{}, fv reflect.Value, path string) error {
	if raw == nil {
		return nil
	}
	if fv.Type() == durationType {
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("config: %s: expected a duration string, got %T", path, raw)
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
		fv.SetInt(int64(d))
		return nil
	}
	switch fv.Kind() {
	case reflect.Struct:
		m, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("config: %s: expected a mapping, got %T", path, raw)
		}
		return decodeStruct(m, fv, path+".")
	case reflect.Slice:
		items, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("config: %s: expected a sequence, got %T", path, raw)
		}
		elemType := fv.Type().Elem()
		out := reflect.MakeSlice(fv.Type(), 0, len(items))
		for i, it := range items {
			ev := reflect.New(elemType).Elem()
			if err := decodeValue(it, ev, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
			out = reflect.Append(out, ev)
		}
		fv.Set(out)
		return nil
	case reflect.String:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("config: %s: expected a string, got %T", path, raw)
		}
		fv.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := raw.(bool)
		if !ok {
			return fmt.Errorf("config: %s: expected a bool, got %T", path, raw)
		}
		fv.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := asInt64(raw)
		if err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
		fv.SetInt(n)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := asFloat64(raw)
		if err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
		fv.SetFloat(f)
		return nil
	default:
		return fmt.Errorf("config: %s: unsupported field type %s (not decoded by this loader)", path, fv.Type())
	}
}

func asInt64(raw interface{}) (int64, error) {
	switch n := raw.(type) {
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", raw)
	}
}

func asFloat64(raw interface{}) (float64, error) {
	switch n := raw.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", raw)
	}
}
