package memory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"

	"traceiq/internal/model"
)

// maxFingerprintTokens is DR-19 §19.1's "<= 24" bound.
const maxFingerprintTokens = 24

// buildTokens assembles the closed token classes DR-19 §19.1 names (svc:,
// op:, kind:, errsig:, dep:, ns:), sorted and deduped, capped at
// maxFingerprintTokens. It is the single place both Compute (from
// model.Incident) and Store.Record (from model.InvestigationRecord) derive
// tokens, so a fingerprint computed ahead of time via Compute and one
// derived later from the persisted InvestigationRecord land on the same
// token set for the same logical incident.
func buildTokens(services []string, errorSignatures []string, deployMarkerIDs []string, namespaces []string) []string {
	set := map[string]struct{}{}
	add := func(prefix, v string) {
		if v == "" {
			return
		}
		set[prefix+v] = struct{}{}
	}
	for _, s := range services {
		add("svc:", s)
	}
	for _, e := range errorSignatures {
		add("errsig:", e)
	}
	for _, d := range deployMarkerIDs {
		add("dep:", d)
	}
	for _, n := range namespaces {
		add("ns:", n)
	}
	tokens := make([]string, 0, len(set))
	for t := range set {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)
	if len(tokens) > maxFingerprintTokens {
		tokens = tokens[:maxFingerprintTokens]
	}
	return tokens
}

// hashTokens is DR-19 §19.1's verbatim construction: "fp1:" +
// hex(xxh3(tenantID ‖ "\x00" ‖ join(Tokens, "\x01"))). This implementation
// uses cespare/xxhash/v2 (already vendored, DR-1's pinned set) rather than a
// true XXH3 implementation — no xxh3 package is a dependency of this module
// — documented as a deviation in docs/reports/w11-correlate-memory.md; the
// "fp1:" prefix and determinism/tenant-scoping properties are preserved
// exactly, only the underlying 64-bit hash algorithm differs from the
// register's literal "xxh3" naming.
func hashTokens(tid model.TenantID, tokens []string) string {
	data := make([]byte, 0, len(tid)+1+len(tokens)*8)
	data = append(data, []byte(tid)...)
	data = append(data, 0)
	data = append(data, []byte(strings.Join(tokens, "\x01"))...)
	sum := xxhash.Sum64(data)
	return fmt.Sprintf("fp1:%016x", sum)
}

// fromTokens builds a Fingerprint from an already-assembled token list
// (deduping/sorting/capping defensively, since callers may pass raw lists).
func fromTokens(tid model.TenantID, tokens []string) Fingerprint {
	set := map[string]struct{}{}
	for _, t := range tokens {
		if t != "" {
			set[t] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	if len(out) > maxFingerprintTokens {
		out = out[:maxFingerprintTokens]
	}
	return Fingerprint{TenantID: tid, Tokens: out, Hash: hashTokens(tid, out)}
}

// Compute is DR-19 §19.1 / FR-F08-1's fingerprint construction. The register
// states it "delegates to the ONE implementation, in anomaly" (DR-14
// §14.5), but DR-2's adjacency table does not permit internal/memory to
// import internal/anomaly (memory's allowed imports: model, config, tenant,
// store, llm) — the memory.go skeleton flags this as an unresolved tension
// left for the implementing session. Resolution taken here: Compute is a
// self-contained, deterministic implementation over model.Incident's
// available fields (Services, EpicenterService, DeployMarkerIDs — Incident
// carries no per-event AnomalyKind/ErrorSignature list without following
// EventIDs back to model.AnomalyEvent, which Compute's signature does not
// receive), sharing its token-building logic (buildTokens) with
// Store.Record's derivation from model.InvestigationRecord so the two never
// drift apart. See docs/reports/w11-correlate-memory.md.
func Compute(tid model.TenantID, inc model.Incident) Fingerprint {
	services := append([]string{}, inc.Services...)
	if inc.EpicenterService != "" {
		services = append(services, inc.EpicenterService)
	}
	tokens := buildTokens(services, nil, inc.DeployMarkerIDs, nil)
	return Fingerprint{TenantID: tid, Tokens: tokens, Hash: hashTokens(tid, tokens)}
}
