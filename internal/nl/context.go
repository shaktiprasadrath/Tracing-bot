package nl

import "time"

// DefaultContextTurns is nl.context_turns's default (F10 §4.3, FR-F10-8).
const DefaultContextTurns = 5

// DefaultContextTTL is FR-F10-8's idle-eviction window: "context expires
// after 30 minutes idle".
const DefaultContextTTL = 30 * time.Minute

// AddTurn appends turn to cc's per-thread ring buffer (ConversationKey —
// tenant/platform/workspace/channel/thread, never per channel alone,
// FR-F10-8), evicting the oldest entry once len(Turns) exceeds
// DefaultContextTurns, and stamps UpdatedAt with now (model.Clock-driven,
// never time.Now directly — the caller supplies now per DR-31).
func AddTurn(cc ConversationContext, t Turn, now time.Time) ConversationContext {
	turns := append(cc.Turns, t)
	if len(turns) > DefaultContextTurns {
		turns = turns[len(turns)-DefaultContextTurns:]
	}
	cc.Turns = turns
	cc.UpdatedAt = now
	return cc
}

// IsExpired implements FR-F10-8's TTL eviction: a per-thread
// ConversationContext idle for more than DefaultContextTTL is treated as
// stale — the next question starts a fresh context rather than resolving a
// follow-up against a conversation that has moved on.
func IsExpired(cc ConversationContext, now time.Time) bool {
	if cc.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(cc.UpdatedAt) > DefaultContextTTL
}
