// Package web embeds the TraceIQ SPA (F12 §1, §3.1 FR-F12-1): vanilla ES
// modules + CSS, no build step, no Node toolchain, zero runtime network
// fetch of UI assets. internal/api imports this package (DR-2's adjacency
// table: api's allowed imports include "web") and serves FS() as the
// static root, with unmatched client-side routes falling back to
// static/index.html so the SPA's History-API router (FR-F12-2) owns
// navigation.
//
// This wave ships 2 of F12 §4.4's 9 screens (Overview, Traces) — functional
// and wired to real /v1 endpoints, not a placeholder. The remaining 7
// (Topology, Investigations, Remediation, Chat, Eval, Cost & Retention,
// Memory) and dark/light theming (FR-F12-9) are deferred; see
// docs/reports/w15-api-web.md.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// FS returns the embedded SPA root (static/index.html, static/css/*,
// static/js/*) rooted at "static" so callers serve it directly at "/".
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		// embed.FS is compiled in; "static" always exists at build time.
		panic("web: static assets missing from embed: " + err.Error())
	}
	return sub
}
