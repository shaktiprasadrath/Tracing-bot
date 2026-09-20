package nl

import (
	"context"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/model"
)

// levenshtein returns the edit distance between a and b. Used by
// matchService for FR-F10-2's "case-insensitive, Levenshtein distance <= 2
// fuzzy match" requirement.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

var tokenRe = regexp.MustCompile(`[a-zA-Z0-9_-]+`)

// matchService is FR-F10-2's service-entity extraction: it matches tokens
// and bigrams of text against the live topology.Graph service catalog
// snapshot (case-insensitive, Levenshtein distance <= 2), never a static
// configured list. Distance 3+ must fail (AC per F10 §7's edge-case list).
//
// It also returns the winning distance (0 == exact) so callers can surface
// F10 §5's "did you mean X?" caveat for any non-exact match rather than
// presenting a fuzzy guess identically to an exact hit (W14 review #6).
// On a tie between catalog entries at the same best distance, the FIRST one
// encountered in catalog iteration order wins — deterministic given a fixed
// catalog snapshot, documented here rather than left as an accidental
// side-effect of "<" vs "<=" in the loop below.
func matchService(text string, catalog []string) (string, int, bool) {
	if len(catalog) == 0 {
		return "", 0, false
	}
	lower := strings.ToLower(text)
	words := tokenRe.FindAllString(lower, -1)
	candidates := make([]string, 0, len(words)*2)
	candidates = append(candidates, words...)
	for i := 0; i+1 < len(words); i++ {
		candidates = append(candidates, words[i]+"-"+words[i+1])
	}

	const maxDistance = 2
	best := ""
	bestDist := maxDistance + 1
	for _, svc := range catalog {
		ls := strings.ToLower(svc)
		for _, c := range candidates {
			d := levenshtein(c, ls)
			// strict "<" (not "<=") is what makes ties resolve to the first
			// catalog entry encountered, not the last.
			if d <= maxDistance && d < bestDist {
				bestDist = d
				best = svc
			}
		}
	}
	if best == "" {
		return "", 0, false
	}
	return best, bestDist, true
}

// --- time-range entity extraction: the closed grammar of FR-F10-3 ---

var (
	lastNUnitRe   = regexp.MustCompile(`(?i)\blast\s+(\d+)\s*(second|minute|hour|day|week)s?\b`)
	betweenRe     = regexp.MustCompile(`(?i)\bbetween\s+(\S+)\s+and\s+(\S+)\b`)
	sinceRe       = regexp.MustCompile(`(?i)\bsince\s+(\S+)\b`)
	sinceDeployRe = regexp.MustCompile(`(?i)\bsince\s+the\s+([a-zA-Z0-9_-]+)\s+deploy\b`)
	yesterdayRe   = regexp.MustCompile(`(?i)\byesterday\b`)
	todayRe       = regexp.MustCompile(`(?i)\btoday\b`)
	isoRe         = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})`)
)

// extractTimeRange implements FR-F10-3's closed time grammar: "last <n>
// <unit>", "since <time>", "between <t1> and <t2>", "yesterday", "today",
// ISO-8601. Deploy-marker references ("since the checkout-svc deploy") are
// intentionally NOT resolved here — see resolveDeployTimeRange, which wraps
// this case with an anomaly.DeployIndex lookup (allowed import per doc.go);
// when no such resolver is wired (or it can't resolve the phrase), it falls
// through to ok=false, which is FR-F10-3's own fallback ("becomes
// IntentUnknown ... never a silently wrong window").
func extractTimeRange(text string, now time.Time) (start, end time.Time, ok bool) {
	if m := lastNUnitRe.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			d := unitDuration(strings.ToLower(m[2]), n)
			return now.Add(-d), now, true
		}
	}
	if m := betweenRe.FindStringSubmatch(text); m != nil {
		t1, e1 := time.Parse(time.RFC3339, m[1])
		t2, e2 := time.Parse(time.RFC3339, m[2])
		if e1 == nil && e2 == nil {
			if t2.Before(t1) {
				t1, t2 = t2, t1
			}
			return t1, t2, true
		}
	}
	if m := sinceRe.FindStringSubmatch(text); m != nil {
		if t1, err := time.Parse(time.RFC3339, m[1]); err == nil {
			return t1, now, true
		}
	}
	if yesterdayRe.MatchString(text) {
		y := now.AddDate(0, 0, -1)
		s := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, y.Location())
		return s, s.Add(24 * time.Hour), true
	}
	if todayRe.MatchString(text) {
		s := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return s, now, true
	}
	if m := isoRe.FindString(text); m != "" {
		if t1, err := time.Parse(time.RFC3339, m); err == nil {
			return t1, now, true
		}
	}
	return time.Time{}, time.Time{}, false
}

// resolveDeployTimeRange implements FR-F10-3/F10 §4's deploy-marker time
// grammar: "since the <service> deploy" resolves to [mostRecentDeploy.At,
// now) via anomaly.DeployIndex (DR-14 §14.6) rather than failing to parse.
// It returns ok=false — never a guessed window — when deploys is nil, the
// phrase doesn't match this shape, the service token doesn't fuzzy-match
// the live catalog (matchService, FR-F10-2), or no deploy marker is on
// record for that service; callers then fall back to extractTimeRange's own
// ok=false handling (IntentUnknown, per FR-F10-3), never a silent default.
func resolveDeployTimeRange(ctx context.Context, tid model.TenantID, text string, catalog []string, deploys anomaly.DeployIndex, now time.Time) (start, end time.Time, ok bool) {
	if deploys == nil {
		return time.Time{}, time.Time{}, false
	}
	m := sinceDeployRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	service, _, found := matchService(m[1], catalog)
	if !found {
		return time.Time{}, time.Time{}, false
	}
	markers, err := deploys.ListWindow(ctx, tid, model.Window{Start: time.Time{}, End: now})
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	var latest model.DeployMarker
	haveLatest := false
	for _, mk := range markers {
		if mk.Service != service || mk.At.After(now) {
			continue
		}
		if !haveLatest || mk.At.After(latest.At) {
			latest = mk
			haveLatest = true
		}
	}
	if !haveLatest {
		return time.Time{}, time.Time{}, false
	}
	return latest.At, now, true
}

func unitDuration(unit string, n int) time.Duration {
	switch unit {
	case "second":
		return time.Duration(n) * time.Second
	case "minute":
		return time.Duration(n) * time.Minute
	case "hour":
		return time.Duration(n) * time.Hour
	case "day":
		return time.Duration(n) * 24 * time.Hour
	case "week":
		return time.Duration(n) * 7 * 24 * time.Hour
	default:
		return 0
	}
}

// --- trace-ID entity extraction ---

var traceIDRe = regexp.MustCompile(`\b([0-9a-fA-F]{32})\b`)

// extractTraceID matches the literal OTLP trace-ID shape (32 hex chars,
// model.TraceID's [16]byte). Anything shorter/longer or non-hex fails.
func extractTraceID(text string) (model.TraceID, bool) {
	m := traceIDRe.FindStringSubmatch(text)
	if m == nil {
		return model.TraceID{}, false
	}
	b, err := hex.DecodeString(m[1])
	if err != nil || len(b) != 16 {
		return model.TraceID{}, false
	}
	var id model.TraceID
	copy(id[:], b)
	return id, true
}

func traceIDHex(id model.TraceID) string {
	return hex.EncodeToString(id[:])
}

// --- error-string entity extraction ---

var (
	quotedErrRe  = regexp.MustCompile(`"([^"]{1,128})"`)
	keywordErrRe = regexp.MustCompile(`(?i)\berrors?\s*[:]\s*([A-Za-z0-9_./:\- ]{2,128})`)
)

// extractErrorString matches an explicit error string, either quoted
// literally or introduced by "error:"/"errors:". It is never interpolated
// into a query language — callers place it in the typed ErrorSigID/Contains
// field, matched as a literal (DR-16 §16.2).
func extractErrorString(text string) (string, bool) {
	if m := quotedErrRe.FindStringSubmatch(text); m != nil {
		s := strings.TrimSpace(m[1])
		if s != "" {
			return s, true
		}
	}
	if m := keywordErrRe.FindStringSubmatch(text); m != nil {
		s := strings.TrimSpace(m[1])
		if s != "" {
			return s, true
		}
	}
	return "", false
}
