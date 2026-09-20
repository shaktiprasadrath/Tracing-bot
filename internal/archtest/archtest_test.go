// Package archtest enforces DR-2's package adjacency table: "internal/archtest
// asserts it exactly; any edge not listed fails CI." It parses every
// non-test Go file's import block with go/parser (ParseComments off,
// ImportsOnly) — no new dependencies — and fails the build if any
// traceiq-internal import is not permitted by the table below, or if
// internal/model imports anything beyond the standard library.
//
// Binding source: docs/architecture/06-decision-register.md, DR-2 (the
// adjacency table and its verbatim rule).
//
// Allowed imports (DR-2's adjacency table): internal/model only (for the
// stdlib bits of this file itself; archtest is a test-only tool, not a
// graph node, so DR-2's table does not list it).
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "traceiq"

// adjacency is DR-2's adjacency table, verbatim, keyed by the package's
// path relative to internal/ (e.g. "store/sqlite"). The value is the set
// of internal packages (same key form) that package may import.
//
// internal/store's row explicitly covers "+ store/sqlite, store/parquet,
// store/tiered, store/blob, store/clickhouse" with the same allowed set
// as store itself (model, config, tenant, topology). Those driver
// subpackages exist to implement store's own exported types (e.g.
// store.Trace), which is unreachable without importing store — DR-2's
// "declared in consumer, references only model/tenant/stdlib" rule
// applies to *interfaces*, not to the concrete types those drivers must
// return. This scaffold therefore also allows each store/* subpackage to
// import its parent "store" package; this is a register ambiguity, noted
// in docs/reports/w1-scaffold.md rather than silently resolved.
var adjacency = map[string][]string{
	"model":            {},
	"config":           {"model"},
	"tenant":           {"model"},
	"selfobs":          {"model"},
	"bus":              {"model", "config"},
	"cluster":          {"config"},
	"llm":              {"model", "config"},
	"k8s":              {"model"},
	"auth":             {"model", "config", "tenant"},
	"topology":         {"model", "config", "tenant"},
	"store":            {"model", "config", "tenant", "topology"},
	"store/sqlite":     {"model", "config", "tenant", "topology", "store"},
	"store/parquet":    {"model", "config", "tenant", "topology", "store"},
	"store/tiered":     {"model", "config", "tenant", "topology", "store"},
	"store/blob":       {"model", "config", "tenant", "topology", "store"},
	"store/clickhouse": {"model", "config", "tenant", "topology", "store"},
	"ingest":           {"model", "config", "tenant", "selfobs"},
	"sampler":          {"model", "config", "tenant"},
	"anomaly":          {"model", "config", "tenant", "topology", "store"},
	"correlate":        {"model", "config", "tenant", "auth"},
	"memory":           {"model", "config", "tenant", "store", "llm"},
	"remediate":        {"model", "config", "tenant", "store", "topology", "auth", "k8s"},
	"rca": {
		"model", "config", "tenant", "store", "topology", "anomaly",
		"correlate", "memory", "sampler", "auth", "llm",
	},
	// rca/rules is F06 §4.4's deterministic rules Reasoner, a subpackage of
	// rca added in the w11 wave (same store/sqlite-under-store shape as the
	// comment atop this table describes): it implements rca.Reasoner and so
	// must import its parent "rca" for the interface/types it satisfies,
	// plus "model" for the shared DTOs. This entry was missing from the
	// table until this wave's TDD pass caught the gap via
	// TestPackageAdjacency's own "no entry for this package" failure mode —
	// see docs/reports/w11-rca-cont.md.
	"rca/rules": {"model", "rca"},
	"nl": {
		"model", "config", "tenant", "store", "topology", "anomaly",
		"rca", "memory", "auth",
	},
	"eval": {"model", "config", "tenant", "ingest", "store", "anomaly", "rca"},
	"api": {
		"model", "config", "tenant", "selfobs", "auth",
		"topology", "store", "ingest", "sampler", "anomaly", "correlate",
		"memory", "remediate", "rca", "nl", "eval", "llm", "k8s", "bus", "cluster",
	},
}

// webImporters is DR-2's `web` edge, which lives OUTSIDE internal/ and was
// therefore invisible to this test until the w17 final review: the adjacency
// walk skipped every import that did not start with "traceiq/internal/", so
// `traceiq/web` was silently permitted from any package. DR-2's table grants
// `web` to internal/api alone ("api | all feature packages, auth, tenant,
// config, selfobs, web"); no other row mentions it. TestWebPackageEdge below
// asserts both halves of that edge — who may import web, and that web itself
// (a leaf that embeds the SPA) imports nothing from the module.
var webImporters = map[string]bool{"api": true}

// depstub and archtest itself are tooling, not graph nodes (DR-2 lists
// neither), so they are exempt from the adjacency check below.
var exempt = map[string]bool{
	"depstub":  true,
	"archtest": true,
}

// cmdPackages lists cmd/* composition roots and the internal packages they
// may import. DR-2: "api, cluster, bus, selfobs, config, tenant, auth, all
// feature constructors" — i.e. every internal package.
var cmdPackages = map[string]bool{
	"cmd/traceiq": true,
}

func TestPackageAdjacency(t *testing.T) {
	repoRoot := repoRoot(t)
	internalDir := filepath.Join(repoRoot, "internal")

	fset := token.NewFileSet()

	// checked records every package key this test actually visited and
	// parsed (or exempted), keyed the same way as adjacency, with a count
	// of non-test .go files found there. It backs the "ScopeCoverage"
	// subtest below, whose whole job is to make sure a silently-empty or
	// silently-partial walk (e.g. a resurgence of the path-separator bug
	// this test now guards against — filepath.Dir on Windows re-emits `\`
	// even when fed an already-ToSlash'd input, which used to desync
	// path2pkg's keys from the adjacency table for any two-level package
	// such as "store/sqlite") cannot pass by simply checking nothing.
	checked := map[string]int{}

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(internalDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pkgKey := path2pkg(rel)
		checked[pkgKey]++

		if exempt[pkgKey] {
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		allowed, known := adjacency[pkgKey]
		if !known {
			t.Errorf("%s: package %q has no entry in archtest's DR-2 adjacency table; add one", path, pkgKey)
			return nil
		}
		allowedSet := map[string]bool{pkgKey: true} // a package may always "import" itself (no-op, just documentation)
		for _, a := range allowed {
			allowedSet[a] = true
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			if pkgKey == "model" && !isStdlib(importPath) {
				t.Errorf("%s: internal/model may import stdlib only (DR-2), found %q", path, importPath)
				continue
			}

			if !strings.HasPrefix(importPath, modulePath+"/internal/") {
				continue // stdlib or third-party: not part of the internal graph
			}

			target := strings.TrimPrefix(importPath, modulePath+"/internal/")
			if !allowedSet[target] {
				t.Errorf("%s: package %q imports %q — edge %s -> %s is not in DR-2's adjacency table",
					path, pkgKey, importPath, pkgKey, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ScopeCoverage", func(t *testing.T) {
		if len(checked) == 0 {
			t.Fatal("TestPackageAdjacency's walk visited zero internal packages — a silently-empty scan must not be able to pass")
		}

		keys := make([]string, 0, len(checked))
		total := 0
		for k, n := range checked {
			keys = append(keys, k)
			total += n
		}
		sort.Strings(keys)
		t.Logf("TestPackageAdjacency checked %d internal package(s), %d non-test .go file(s) total: %v", len(keys), total, keys)

		// Independently re-discover which directories under internal/ hold
		// non-test .go files, using plain "/"-splitting rather than
		// path2pkg/filepath.Dir, so a regression in the normalization this
		// test exists to verify can't also blind this cross-check.
		found := map[string]bool{}
		walkErr := filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(internalDir, path)
			if err != nil {
				return err
			}
			slash := strings.ReplaceAll(rel, `\`, "/")
			if idx := strings.LastIndex(slash, "/"); idx >= 0 {
				found[slash[:idx]] = true
			} else {
				found[""] = true
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("independent package discovery failed: %v", walkErr)
		}

		for pkg := range found {
			if _, ok := checked[pkg]; !ok {
				t.Errorf("package %q has non-test .go files on disk but TestPackageAdjacency's walk never visited it — coverage gap", pkg)
			}
		}
		for pkg := range checked {
			if !found[pkg] {
				t.Errorf("TestPackageAdjacency's walk visited %q but independent discovery found no .go files there — inconsistent walk state", pkg)
			}
		}
	})
}

// TestWebPackageEdge closes the hole described on webImporters: it walks
// internal/ again looking specifically for the module-level `traceiq/web`
// import that TestPackageAdjacency's `strings.HasPrefix(importPath,
// "traceiq/internal/")` filter skips, and separately checks that web/ is the
// leaf DR-2 makes it (cmd/* is exempt for the same reason it is exempt in
// TestCmdImports: DR-2 grants the composition root everything).
func TestWebPackageEdge(t *testing.T) {
	repoRoot := repoRoot(t)
	fset := token.NewFileSet()

	const webPkg = modulePath + "/web"

	err := filepath.Walk(filepath.Join(repoRoot, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(repoRoot, "internal"), path)
		if err != nil {
			return err
		}
		pkgKey := path2pkg(filepath.ToSlash(rel))
		if exempt[pkgKey] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != webPkg {
				continue
			}
			if !webImporters[pkgKey] {
				t.Errorf("%s: package %q imports %q — DR-2's adjacency table grants the `web` edge to internal/api only",
					path, pkgKey, webPkg)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// web is a leaf: it embeds static assets and must not import back into
	// the graph it is served from (that would be a cycle through api).
	webDir := filepath.Join(repoRoot, "web")
	if _, statErr := os.Stat(webDir); statErr != nil {
		t.Fatalf("web/ is named in DR-2's adjacency table but is not present at %s: %v", webDir, statErr)
	}
	sawWebFile := false
	err = filepath.Walk(webDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		sawWebFile = true
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(importPath, modulePath+"/") {
				t.Errorf("%s: package web imports %q — web is a leaf in DR-2's adjacency table (stdlib only)", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawWebFile {
		t.Error("TestWebPackageEdge found no non-test .go files under web/ — a silently-empty scan must not be able to pass")
	}
}

func TestCmdImports(t *testing.T) {
	repoRoot := repoRoot(t)
	cmdDir := filepath.Join(repoRoot, "cmd")
	if _, err := os.Stat(cmdDir); os.IsNotExist(err) {
		return
	}

	fset := token.NewFileSet()
	err := filepath.Walk(cmdDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(filepath.Dir(cmdDir), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pkgDir := filepath.ToSlash(filepath.Dir(rel)) // e.g. "cmd/traceiq"

		if !cmdPackages[pkgDir] {
			t.Errorf("%s: %q has no entry in archtest's cmd allowlist; add one (DR-2)", path, pkgDir)
			return nil
		}

		// DR-2 grants cmd/traceiq every internal package ("all feature
		// constructors"), so there is nothing to deny here — parsing only
		// confirms the file's import block is well-formed.
		if _, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// path2pkg maps a file path relative to internal/ (e.g.
// "store/sqlite/engine.go") to its adjacency-table key (e.g.
// "store/sqlite").
func path2pkg(relPath string) string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "." {
		return ""
	}
	return dir
}

// isStdlib reports whether importPath looks like a standard-library
// import: its first path segment has no dot, and it is not this module's
// own path. This is a heuristic (no network access, no go/build resolution
// against GOROOT), sufficient because DR-1 pins the full third-party set
// and every one of those module paths contains a dot in its first segment.
func isStdlib(importPath string) bool {
	if strings.HasPrefix(importPath, modulePath+"/") {
		return false
	}
	first := strings.SplitN(importPath, "/", 2)[0]
	return !strings.Contains(first, ".")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// This file lives at <repoRoot>/internal/archtest/archtest_test.go.
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("could not locate repo root from %s: %v", wd, err)
	}
	return root
}
