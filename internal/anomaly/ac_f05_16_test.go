package anomaly

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertSourceFree scans every non-test .go file in this package's own
// directory for any of the given substrings and fails the test if found.
// Used by AC-F05-16's scope-limited proxy check (grouper_test.go): DR-21
// §21.1 requires no code path from model.ServiceMeta.Tier to the paging
// decision; the full AST/route test spans api.AlertRouter, which is outside
// internal/anomaly's scope lock, so this asserts the necessary local
// precondition -- this package never touches ServiceMeta/.Tier at all.
func assertSourceFree(t *testing.T, needles []string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		content := string(data)
		for _, needle := range needles {
			if strings.Contains(content, needle) {
				t.Errorf("%s contains forbidden reference %q (DR-21 §21.1: no code path from model.ServiceMeta.Tier to paging)", name, needle)
			}
		}
	}
}
