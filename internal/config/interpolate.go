package config

import (
	"os"
	"reflect"
	"strings"
)

// interpolate walks every string field (recursively, including string
// slice elements) reachable from v and expands 01 §7's two interpolations:
// "${data_dir}" (Config.Server.DataDir, already resolved) and
// "${env:VAR}" (os.Getenv("VAR"), empty if unset).
func interpolate(v reflect.Value, dataDir string) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			interpolate(v.Field(i), dataDir)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			interpolate(v.Index(i), dataDir)
		}
	case reflect.String:
		v.SetString(expand(v.String(), dataDir))
	}
}

func expand(s, dataDir string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	s = strings.ReplaceAll(s, "${data_dir}", dataDir)
	for {
		start := strings.Index(s, "${env:")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		end += start
		varName := s[start+len("${env:") : end]
		s = s[:start] + os.Getenv(varName) + s[end+1:]
	}
	return s
}
