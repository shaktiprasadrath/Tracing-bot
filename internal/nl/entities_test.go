package nl

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/model"
)

// fakeDeployIndex is a minimal in-memory anomaly.DeployIndex test double.
// Only ListWindow is exercised by resolveDeployTimeRange; the rest exist
// solely to satisfy the interface.
type fakeDeployIndex struct {
	markers []model.DeployMarker
}

func (f *fakeDeployIndex) Near(ctx context.Context, tid model.TenantID, service string, at time.Time, window time.Duration) ([]model.DeployMarker, error) {
	return nil, nil
}

func (f *fakeDeployIndex) PrePostSplit(ctx context.Context, tid model.TenantID, m model.DeployMarker) (model.Window, model.Window, error) {
	return model.Window{}, model.Window{}, nil
}

func (f *fakeDeployIndex) Record(ctx context.Context, tid model.TenantID, m model.DeployMarker) error {
	f.markers = append(f.markers, m)
	return nil
}

func (f *fakeDeployIndex) ListWindow(ctx context.Context, tid model.TenantID, w model.Window) ([]model.DeployMarker, error) {
	var out []model.DeployMarker
	for _, m := range f.markers {
		if !m.At.Before(w.Start) && !m.At.After(w.End) {
			out = append(out, m)
		}
	}
	return out, nil
}

var _ anomaly.DeployIndex = (*fakeDeployIndex)(nil)

func TestMatchService(t *testing.T) {
	catalog := []string{"checkout-svc", "payment-svc", "cart-svc"}
	cases := []struct {
		name     string
		text     string
		want     string
		wantDist int
		ok       bool
	}{
		{"exact", "how is checkout-svc doing", "checkout-svc", 0, true},
		{"case-insensitive", "status of CHECKOUT-SVC", "checkout-svc", 0, true},
		{"distance1", "is checkot-svc healthy", "checkout-svc", 1, true}, // 1 char dropped
		{"distance2", "is chekot-svc healthy", "checkout-svc", 2, true},  // 2 edits
		{"distance3-fails", "is chkzot-svc healthy", "", 0, false},       // >2 edits, must fail
		{"no-service-mentioned", "what incidents are active", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dist, ok := matchService(tc.text, catalog)
			if ok != tc.ok {
				t.Fatalf("matchService(%q) ok = %v, want %v (got %q)", tc.text, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("matchService(%q) = %q, want %q", tc.text, got, tc.want)
			}
			if ok && dist != tc.wantDist {
				t.Fatalf("matchService(%q) distance = %d, want %d", tc.text, dist, tc.wantDist)
			}
		})
	}
}

// TestMatchService_TieBreaksToFirstCatalogEntry documents and locks in the
// deterministic tie-break decision (W14 review #6, finding (a)): when two
// catalog entries land at the same best distance, the FIRST one encountered
// in catalog iteration order wins, not an arbitrary or last-write choice.
func TestMatchService_TieBreaksToFirstCatalogEntry(t *testing.T) {
	catalog := []string{"aaa-svc", "abb-svc"} // both distance 1 from "aab-svc"
	text := "how is aab-svc doing"
	if d1, d2 := levenshtein("aab-svc", "aaa-svc"), levenshtein("aab-svc", "abb-svc"); d1 != 1 || d2 != 1 {
		t.Fatalf("test setup invalid: distances are %d and %d, want 1 and 1", d1, d2)
	}
	got, dist, ok := matchService(text, catalog)
	if !ok {
		t.Fatalf("matchService(%q) ok = false, want true", text)
	}
	if dist != 1 {
		t.Fatalf("matchService(%q) distance = %d, want 1", text, dist)
	}
	if got != "aaa-svc" {
		t.Fatalf("matchService(%q) = %q, want %q (first catalog entry on a tie)", text, got, "aaa-svc")
	}

	// Reversing catalog order flips the winner too — proves the tie-break is
	// genuinely "first encountered", not some property of the string itself.
	reversed := []string{"abb-svc", "aaa-svc"}
	got2, _, ok2 := matchService(text, reversed)
	if !ok2 || got2 != "abb-svc" {
		t.Fatalf("matchService(%q) with reversed catalog = %q, ok=%v, want %q, true", text, got2, ok2, "abb-svc")
	}
}

func TestExtractTimeRange(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		text string
		ok   bool
	}{
		{"last-n-minutes", "show errors in the last 30 minutes", true},
		{"last-n-hours", "compare the last 2 hours", true},
		{"since-iso", "since 2026-09-19T10:00:00Z what changed", true},
		{"between", "between 2026-09-19T09:00:00Z and 2026-09-19T10:00:00Z", true},
		{"yesterday", "what happened yesterday", true},
		{"today", "any incidents today", true},
		{"iso-standalone", "traces around 2026-09-19T11:30:00Z", true},
		{"unmatched", "traces for checkout-svc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := extractTimeRange(tc.text, now)
			if ok != tc.ok {
				t.Fatalf("extractTimeRange(%q) ok = %v, want %v", tc.text, ok, tc.ok)
			}
			if ok && !start.Before(end) {
				t.Fatalf("extractTimeRange(%q) start %v not before end %v", tc.text, start, end)
			}
		})
	}
}

func TestExtractTimeRange_LastNMinutesWindow(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	start, end, ok := extractTimeRange("errors in the last 30 minutes", now)
	if !ok {
		t.Fatal("expected match")
	}
	if !end.Equal(now) {
		t.Fatalf("end = %v, want %v", end, now)
	}
	if want := now.Add(-30 * time.Minute); !start.Equal(want) {
		t.Fatalf("start = %v, want %v", start, want)
	}
}

func TestExtractTraceID(t *testing.T) {
	valid := "4bf92f3577b34da6a3ce929d0e0e4736"
	cases := []struct {
		name string
		text string
		ok   bool
	}{
		{"valid-32-hex", "explain trace " + valid, true},
		{"too-short", "explain trace 4bf92f3577b34da6", false},
		{"non-hex", "explain trace zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"absent", "explain that trace from earlier", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := extractTraceID(tc.text)
			if ok != tc.ok {
				t.Fatalf("extractTraceID(%q) ok = %v, want %v", tc.text, ok, tc.ok)
			}
			if ok && traceIDHex(id) != valid {
				t.Fatalf("extractTraceID(%q) = %x, want %s", tc.text, id, valid)
			}
		})
	}
}

// TestResolveDeployTimeRange covers FR-F10-3/W14 review #5's fix: "since the
// <service> deploy" resolves to a real [deploy.At, now) window via
// anomaly.DeployIndex rather than silently defaulting, but only ever
// resolves — never guesses — when the phrase, service, and deploy history
// all genuinely line up.
func TestResolveDeployTimeRange(t *testing.T) {
	catalog := []string{"checkout-svc", "payment-svc"}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	tid := model.TenantID("tenant-a")

	olderDeploy := now.Add(-3 * time.Hour)
	latestDeploy := now.Add(-45 * time.Minute)

	t.Run("resolves to the most recent matching deploy", func(t *testing.T) {
		idx := &fakeDeployIndex{markers: []model.DeployMarker{
			{Service: "checkout-svc", At: olderDeploy},
			{Service: "checkout-svc", At: latestDeploy},
			{Service: "payment-svc", At: now.Add(-1 * time.Minute)}, // different service, must be ignored
		}}
		start, end, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the checkout-svc deploy", catalog, idx, now)
		if !ok {
			t.Fatal("expected resolution, got ok=false")
		}
		if !start.Equal(latestDeploy) {
			t.Fatalf("start = %v, want %v (the most recent checkout-svc deploy)", start, latestDeploy)
		}
		if !end.Equal(now) {
			t.Fatalf("end = %v, want %v", end, now)
		}
	})

	t.Run("fuzzy-matches the service token in the phrase", func(t *testing.T) {
		idx := &fakeDeployIndex{markers: []model.DeployMarker{{Service: "checkout-svc", At: latestDeploy}}}
		// "chekot-svc" is distance 2 from "checkout-svc" (within matchService's threshold).
		start, _, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the chekot-svc deploy", catalog, idx, now)
		if !ok || !start.Equal(latestDeploy) {
			t.Fatalf("resolveDeployTimeRange fuzzy match: ok=%v start=%v, want ok=true start=%v", ok, start, latestDeploy)
		}
	})

	t.Run("nil DeployIndex never resolves", func(t *testing.T) {
		if _, _, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the checkout-svc deploy", catalog, nil, now); ok {
			t.Fatal("expected ok=false with a nil DeployIndex")
		}
	})

	t.Run("phrase shape must match: since <time>, not since the <service> deploy", func(t *testing.T) {
		idx := &fakeDeployIndex{markers: []model.DeployMarker{{Service: "checkout-svc", At: latestDeploy}}}
		if _, _, ok := resolveDeployTimeRange(context.Background(), tid, "since yesterday what changed", catalog, idx, now); ok {
			t.Fatal("expected ok=false: phrase doesn't match the deploy-marker shape")
		}
	})

	t.Run("service token that doesn't resolve in the catalog never resolves", func(t *testing.T) {
		idx := &fakeDeployIndex{markers: []model.DeployMarker{{Service: "checkout-svc", At: latestDeploy}}}
		if _, _, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the totally-unknown-svc deploy", catalog, idx, now); ok {
			t.Fatal("expected ok=false: service token doesn't fuzzy-match the catalog")
		}
	})

	t.Run("no recorded deploy for the service never resolves", func(t *testing.T) {
		idx := &fakeDeployIndex{} // empty: no deploys on record at all
		if _, _, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the checkout-svc deploy", catalog, idx, now); ok {
			t.Fatal("expected ok=false: no deploy marker on record")
		}
	})

	t.Run("future-dated deploy marker is ignored, not projected as a negative window", func(t *testing.T) {
		idx := &fakeDeployIndex{markers: []model.DeployMarker{{Service: "checkout-svc", At: now.Add(1 * time.Hour)}}}
		if _, _, ok := resolveDeployTimeRange(context.Background(), tid, "what changed since the checkout-svc deploy", catalog, idx, now); ok {
			t.Fatal("expected ok=false: only a future deploy marker is on record")
		}
	})
}

// TestExtractToolArgs_DeployMarkerResolvesRealWindow is the end-to-end proof
// for W14 review #5: with a wired anomaly.DeployIndex, RulesInterpreter no
// longer silently defaults "since the checkout-svc deploy" to a trailing
// 30-minute window — it resolves the actual deploy time.
func TestExtractToolArgs_DeployMarkerResolvesRealWindow(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	deployAt := now.Add(-90 * time.Minute)
	idx := &fakeDeployIndex{markers: []model.DeployMarker{{Service: "checkout-svc", At: deployAt}}}

	ri := newTestInterpreter()
	ri.Deploys = idx
	tid := model.TenantID("tenant-a")

	got, err := ri.Interpret(context.Background(), tid, Question{Text: "what changed since the checkout-svc deploy", AskedAt: now}, ConversationContext{TenantID: tid})
	if err != nil {
		t.Fatalf("Interpret error: %v", err)
	}
	if got.Kind != IntentCompareWindows {
		t.Fatalf("Kind = %v, want IntentCompareWindows", got.Kind)
	}
	if got.Args.Metric == nil || got.Args.Metric.Start.IsZero() {
		t.Fatal("expected Metric window to be populated")
	}
	if !got.Args.Metric.Start.Equal(deployAt) {
		t.Fatalf("Metric.Start = %v, want %v (the real deploy time, not a 30-minute default)", got.Args.Metric.Start, deployAt)
	}
	if wrongDefault := now.Add(-30 * time.Minute); got.Args.Metric.Start.Equal(wrongDefault) {
		t.Fatal("Metric.Start silently fell back to the 30-minute default despite a resolvable deploy marker")
	}
}

func TestExtractErrorString(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{"quoted", `find traces with error "connection refused"`, "connection refused", true},
		{"keyword-colon", "traces failing with error: timeout waiting for upstream", "timeout waiting for upstream", true},
		{"no-error", "how is checkout-svc doing", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractErrorString(tc.text)
			if ok != tc.ok {
				t.Fatalf("extractErrorString(%q) ok = %v, want %v (got %q)", tc.text, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("extractErrorString(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
