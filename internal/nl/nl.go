package nl

import (
	"context"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/model"
	"traceiq/internal/rca"
)

// IntentKind is DR-35 §35.1's closed, eleven-value (0..10) taxonomy,
// verbatim. CLOSED. A new intent is a DR, not a config value.
type IntentKind uint8

const (
	IntentUnknown            IntentKind = 0
	IntentTraceSearch        IntentKind = 1
	IntentServiceHealth      IntentKind = 2
	IntentTopologyQuestion   IntentKind = 3
	IntentIncidentStatus     IntentKind = 4
	IntentInvestigationAsk   IntentKind = 5
	IntentMemoryLookup       IntentKind = 6
	IntentStartInvestigation IntentKind = 7
	IntentCompareWindows     IntentKind = 8
	IntentExplainTrace       IntentKind = 9
	IntentRemediate          IntentKind = 10
)

// Interpreter is DR-35 §35.2, verbatim.
type Interpreter interface {
	Kind() string // "rules" | "llm"
	Interpret(ctx context.Context, tid model.TenantID, q Question, cc ConversationContext) (Intent, error)
}

// Answerer is DR-35 §35.2, verbatim.
type Answerer interface {
	Answer(ctx context.Context, tid model.TenantID, in Intent, cc ConversationContext) (Answer, error)
}

// ChatContext is DR-35 §35.2's per-request identity block ("ChatContext (02)
// ... IS request identity"). The register asserts its existence and role but
// never prints its field list; reconstructed minimally from that sentence
// and DR-23 §23.7's chat-identity tuple.
// TODO(DR-35): confirm field list against F10 §4.3's chat adapter.
type ChatContext struct {
	Platform    string
	WorkspaceID string
	ChannelID   string
	ThreadID    string
	UserID      string
}

// Question is DR-35 §35.2, verbatim.
type Question struct {
	Text    string // <= nl.max_question_bytes (4096)
	Subject auth.Subject
	Chat    *ChatContext // per-request identity: platform, workspace, channel, thread, user
	AskedAt time.Time
}

// ConversationKey is DR-35 §35.2, verbatim: per-THREAD, per F10 §8's own
// correction.
type ConversationKey struct {
	Tenant      model.TenantID
	Platform    string
	WorkspaceID string
	ChannelID   string
	ThreadID    string
}

// Turn is one entry in ConversationContext.Turns. The register bounds the
// ring ("<= nl.context_turns (5)") but never prints Turn's fields;
// reconstructed minimally from Question/Answer's own shapes.
// TODO(DR-35): confirm field list.
type Turn struct {
	AskedAt time.Time
	Intent  Intent
	Answer  Answer
}

// Focus is ConversationContext.Focus: "last incident / investigation /
// service / trace referenced" per DR-35 §35.2's comment. Field list not
// printed by the register; reconstructed minimally.
// TODO(DR-35): confirm field list.
type Focus struct {
	IncidentID      string
	InvestigationID string
	Service         string
	TraceID         string
}

// ConversationContext is DR-35 §35.2, verbatim. ChatContext (request
// identity) and ConversationContext (multi-turn state) are deliberately
// distinct types per the register's own note.
type ConversationContext struct {
	TenantID  model.TenantID // ALWAYS present (DR-5)
	Key       ConversationKey
	Turns     []Turn // ring of <= nl.context_turns (5)
	Focus     Focus  // last incident / investigation / service / trace referenced
	UpdatedAt time.Time
}

// Intent is DR-35 §35.2, verbatim, plus Candidates (FR-F10-11): populated
// only when two rules match within nl.ambiguity_threshold of each other, in
// which case Kind is IntentUnknown and Answerer renders a clarifying
// question instead of guessing.
type Intent struct {
	Kind       IntentKind
	Args       rca.ToolArgs // the SAME typed args the RCA loop uses (DR-16, DR-35 §35.2)
	Raw        string       // the user's text, for the audit row only
	Confidence float64
	Candidates []IntentKind // FR-F10-11: top-2 near-tied candidates when ambiguous
	Source     string       // "rules" | "llm" — mirrors the Interpreter.Kind() that produced this Intent

	// FuzzyServiceMatch is F10 §5's "did you mean X?" signal (W14 review
	// #6): non-empty only when extractToolArgs's matchService call resolved
	// a non-exact (Levenshtein distance > 0) match against the live catalog
	// THIS turn, AND that matched service was actually incorporated into
	// Args — never set merely because some fuzzy candidate existed in text
	// that the matched IntentKind didn't end up using. Answerer appends a
	// caveat naming this service rather than silently presenting the guess
	// as if the user's exact wording were found.
	FuzzyServiceMatch string
}

// Answer is Answerer.Answer's return type. The register describes its
// content (evidence-limited, DR-35 §35.5 sanitized) but never prints a
// struct; reconstructed minimally.
// TODO(DR-35): confirm field list against F10's response schema.
type Answer struct {
	Text       string
	Evidence   []model.Evidence
	Citations  []string
	FollowUps  []string   // suggested follow-up questions
	IntentKind IntentKind // echoes the Intent this Answer resolved
	Clarifying bool       // true when a disambiguation question was rendered instead of a result (FR-F10-11)
}
