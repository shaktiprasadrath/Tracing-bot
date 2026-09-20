package rca

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"traceiq/internal/model"
)

// sanitizer is DR-37 §37.1(2)/(3)'s Wrap/canary-echo mechanism: rca.Sanitizer
// (DR-15) implementation. One instance is scoped to a single investigation
// (its canary is derived from the investigation ID, not shared across
// investigations), which is what makes a canary leak attributable to the
// investigation that produced it.
type sanitizer struct {
	canary string // 16 random-looking hex bytes (32 hex chars), DR-37 §37.1(2)
}

// newSanitizer derives a per-investigation canary deterministically from
// invID rather than drawing fresh crypto/rand bytes on every call. DR-37
// §37.1(2) asks for "16 random hex bytes per investigation" — deterministic
// derivation from invID satisfies the *per-investigation* part (two calls
// within the same investigation's NextStep loop must wrap with the SAME
// canary so CheckEcho and the untrusted-region markers stay consistent
// across steps) while LLMReasoner itself stays stateless between calls (no
// mutable per-investigation map to synchronize), matching the rules
// reasoner's "no mutable state across calls" property (rules.go's doc
// comment). A real production canary MAY additionally salt this with a
// process-local secret; not required for this wave's testable contract.
func newSanitizer(invID string) *sanitizer {
	sum := sha256.Sum256([]byte("rca:canary:" + invID))
	return &sanitizer{canary: hex.EncodeToString(sum[:16])}
}

func (s *sanitizer) Canary() string { return s.canary }

// untrustedKindLabel is DR-37 §37.1(2)'s `{class}` in
// `<untrusted k="{class}" c="{canary}">`.
func untrustedKindLabel(k model.UntrustedKind) string {
	switch k {
	case model.UntrustedTelemetry:
		return "telemetry"
	case model.UntrustedLog:
		return "log"
	case model.UntrustedMemory:
		return "memory"
	case model.UntrustedUserQuestion:
		return "user_question"
	case model.UntrustedRunbook:
		return "runbook"
	case model.UntrustedDeployMetadata:
		return "deploy_metadata"
	default:
		return "unknown"
	}
}

// Wrap is DR-37 §37.1(2), verbatim mechanism: every string not authored by
// TraceIQ's own code is delimited as
// `<untrusted k="{class}" c="{canary}"> … escaped … </untrusted k="{class}" c="{canary}">`,
// with `<`, `>` and any literal occurrence of the canary inside the payload
// escaped so the payload cannot forge a closing delimiter or plant a second,
// spoofed canary that would defeat CheckEcho.
func (s *sanitizer) Wrap(k model.UntrustedKind, str string) string {
	escaped := escapeUntrusted(str, s.canary)
	class := untrustedKindLabel(k)
	return fmt.Sprintf("<untrusted k=%q c=%q>%s</untrusted k=%q c=%q>", class, s.canary, escaped, class, s.canary)
}

// escapeUntrusted neutralises `<`, `>` and any literal occurrence of the
// canary inside payload — DR-37 §37.1(2)'s exact escaping requirement.
func escapeUntrusted(payload, canary string) string {
	r := strings.NewReplacer(
		"<", "&lt;",
		">", "&gt;",
	)
	out := r.Replace(payload)
	if canary != "" {
		out = strings.ReplaceAll(out, canary, "["+"REDACTED-CANARY"+"]")
	}
	return out
}

// CheckEcho is DR-37 §37.1(3)'s canary-echo check: "a canary appearing in
// model output outside a legal position ⇒ Verdict = schema_error". This
// reasoner never emits a canary in any legal position of its own free-text
// output (rationale/statement fields are plain narrative, never asked to
// echo the canary back), so any occurrence at all is illegitimate — the
// signature of an injected payload instructing the model to exfiltrate the
// delimiter and defeat the wrapper.
func (s *sanitizer) CheckEcho(modelOutput string) error {
	if s.canary == "" {
		return nil
	}
	if strings.Contains(modelOutput, s.canary) {
		return fmt.Errorf("%w: canary %q present in model output", ErrLLMCanaryViolation, s.canary)
	}
	return nil
}

var _ Sanitizer = (*sanitizer)(nil)
