package nl

import (
	"testing"
	"time"

	"traceiq/internal/model"
)

func TestConversationContext_AddTurnCapsRing(t *testing.T) {
	cc := ConversationContext{TenantID: model.TenantID("t1")}
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		cc = AddTurn(cc, Turn{AskedAt: base.Add(time.Duration(i) * time.Minute)}, base.Add(time.Duration(i)*time.Minute))
	}
	if len(cc.Turns) != DefaultContextTurns {
		t.Fatalf("len(Turns) = %d, want %d", len(cc.Turns), DefaultContextTurns)
	}
	// oldest turns should have been evicted; the last turn kept must be the
	// most recently added one (index 7).
	last := cc.Turns[len(cc.Turns)-1]
	if !last.AskedAt.Equal(base.Add(7 * time.Minute)) {
		t.Fatalf("last turn AskedAt = %v, want %v", last.AskedAt, base.Add(7*time.Minute))
	}
}

func TestConversationContext_TTLExpiry(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	cc := ConversationContext{TenantID: model.TenantID("t1"), UpdatedAt: base}

	if IsExpired(cc, base.Add(29*time.Minute)) {
		t.Fatal("context should not be expired at 29 minutes idle")
	}
	if !IsExpired(cc, base.Add(31*time.Minute)) {
		t.Fatal("context should be expired at 31 minutes idle")
	}
}

func TestConversationContext_KeyIsPerThreadNotChannel(t *testing.T) {
	k1 := ConversationKey{Tenant: "t1", Platform: "slack", WorkspaceID: "w1", ChannelID: "c1", ThreadID: "th1"}
	k2 := ConversationKey{Tenant: "t1", Platform: "slack", WorkspaceID: "w1", ChannelID: "c1", ThreadID: "th2"}
	if k1 == k2 {
		t.Fatal("two different threads in the same channel must not produce equal ConversationKeys")
	}
}
