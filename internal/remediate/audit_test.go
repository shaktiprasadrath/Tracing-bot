package remediate

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/auth"
	"traceiq/internal/model"
)

// --- (g) audit log entries are hash-chained; tampering breaks the chain ---

func TestAuditLog_HashChainDetectsTampering(t *testing.T) {
	log := NewInMemoryAuditLog()
	ctx := context.Background()
	tid := model.TenantID("t1")

	for i := 0; i < 5; i++ {
		_, err := log.Append(ctx, auth.Event{
			Tenant: tid, Actor: "user:alice", Action: "action_proposed",
			Payload: []byte(`{"n":` + string(rune('0'+i)) + `}`), Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	report, err := log.VerifyChain(ctx, tid, 1, 0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.OK {
		t.Fatalf("expected an intact chain before tampering, got broken at seq %d", report.BrokenAtSeq)
	}

	// Tamper with entry 3's payload in place, WITHOUT recomputing its hash
	// (exactly what an attacker editing the underlying store would do).
	log.tamperForTest(tid, 3, []byte(`{"n":"TAMPERED"}`))

	report, err = log.VerifyChain(ctx, tid, 1, 0)
	if err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	if report.OK {
		t.Fatalf("expected VerifyChain to detect tampering, got OK=true")
	}
	if report.BrokenAtSeq != 3 {
		t.Fatalf("want BrokenAtSeq=3, got %d", report.BrokenAtSeq)
	}
}

func TestAuditLog_PerTenantChainsAreIndependent(t *testing.T) {
	log := NewInMemoryAuditLog()
	ctx := context.Background()

	r1, _ := log.Append(ctx, auth.Event{Tenant: "t1", Actor: "a", Action: "x", Timestamp: time.Now()})
	r2, _ := log.Append(ctx, auth.Event{Tenant: "t2", Actor: "a", Action: "x", Timestamp: time.Now()})
	if r1.Seq != 1 || r2.Seq != 1 {
		t.Fatalf("each tenant's chain must start its own seq at 1, got %d and %d", r1.Seq, r2.Seq)
	}

	rep1, _ := log.VerifyChain(ctx, "t1", 1, 0)
	rep2, _ := log.VerifyChain(ctx, "t2", 1, 0)
	if !rep1.OK || !rep2.OK {
		t.Fatalf("both independent chains must verify intact")
	}
}

func TestAuditLog_QueryFiltersByActorAndAction(t *testing.T) {
	log := NewInMemoryAuditLog()
	ctx := context.Background()
	tid := model.TenantID("t1")
	log.Append(ctx, auth.Event{Tenant: tid, Actor: "user:alice", Action: "action_proposed", Timestamp: time.Now()})
	log.Append(ctx, auth.Event{Tenant: tid, Actor: "user:bob", Action: "action_approved", Timestamp: time.Now()})

	events, _, err := log.Query(ctx, tid, auth.Filter{Actor: "user:bob"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(events) != 1 || events[0].Action != "action_approved" {
		t.Fatalf("expected exactly bob's action_approved event, got %+v", events)
	}
}
