package sampler

import (
	"context"
	"testing"
	"time"

	"traceiq/internal/model"
)

// TestConsume_SameTraceIDAcrossTenants_StaysIsolated is the regression test
// for the w17 final review's tenant-isolation Blocker.
//
// TraceIDs are minted by the instrumented client and arrive verbatim over
// OTLP, so nothing stops two tenants from presenting the same one — by
// accident or deliberately. shardBuf.traces used to be keyed by
// model.TraceID alone, so the second tenant's spans were appended to the
// first tenant's open Trace, and finalize() then emitted ONE Decision under
// whichever tenant happened to open the buffer, carrying both tenants'
// spans into that tenant's store.
//
// This test fails loudly on that keying: it drives one identical TraceID
// from two tenants and requires two separate decisions, one per tenant,
// each with only its own span.
func TestConsume_SameTraceIDAcrossTenants_StaysIsolated(t *testing.T) {
	clock := &fakeClock{now: time.Unix(4000, 0)}
	acfg := AssemblyConfig{IdleTimeout: 5 * time.Second, HardTimeout: 30 * time.Second, WheelTick: 250 * time.Millisecond}
	mgr := newTestManager(clock, acfg)

	shared := mkTraceID(99) // the SAME trace id, presented by both tenants
	tenantA := model.TenantID("tenant-a")
	tenantB := model.TenantID("tenant-b")

	if err := mgr.Consume(context.Background(), tenantA,
		[]model.Span{mkSpan(shared, mkSpanID(1), model.SpanID{}, "svc-a", "op-a", 0, uint64(time.Millisecond), false)}); err != nil {
		t.Fatalf("Consume(tenantA): %v", err)
	}
	if err := mgr.Consume(context.Background(), tenantB,
		[]model.Span{mkSpan(shared, mkSpanID(2), model.SpanID{}, "svc-b", "op-b", 0, uint64(time.Millisecond), false)}); err != nil {
		t.Fatalf("Consume(tenantB): %v", err)
	}

	if st := mgr.Stats(); st.OpenTraces != 2 {
		t.Fatalf("two tenants sharing a TraceID must open two independent assembly buffers; OpenTraces=%d, want 2", st.OpenTraces)
	}

	clock.now = clock.now.Add(10 * time.Second)
	mgr.Tick(context.Background())

	seen := map[model.TenantID]int{}
	for i := 0; i < 2; i++ {
		select {
		case d := <-mgr.Decisions():
			if d.TraceID != shared {
				t.Fatalf("decision %d carries the wrong trace id: %x", i, d.TraceID)
			}
			if d.Tenant == "" {
				t.Fatalf("decision %d carries an empty tenant: %+v", i, d)
			}
			seen[d.Tenant]++
		default:
			t.Fatalf("expected 2 decisions (one per tenant), got %d", i)
		}
	}
	if seen[tenantA] != 1 || seen[tenantB] != 1 {
		t.Fatalf("expected exactly one decision per tenant, got %v", seen)
	}
}

// TestConsume_SpanTenantIsAlwaysOverwrittenByBatchTenant covers the second
// half of the same fix: Consume used to only FILL a blank span.Tenant
// (`if s.Tenant == "" { s.Tenant = tid }`), leaving a pre-stamped foreign
// tenant in place on a span it was nonetheless buffering under tid. The
// batch's resolved tenant is the single source of truth.
func TestConsume_SpanTenantIsAlwaysOverwrittenByBatchTenant(t *testing.T) {
	clock := &fakeClock{now: time.Unix(4100, 0)}
	acfg := AssemblyConfig{IdleTimeout: 5 * time.Second, HardTimeout: 30 * time.Second, WheelTick: 250 * time.Millisecond}
	mgr := newTestManager(clock, acfg)

	span := mkSpan(mkTraceID(7), mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)
	span.Tenant = "attacker-supplied-tenant"

	batchTenant := model.TenantID("real-tenant")
	spans := []model.Span{span}
	if err := mgr.Consume(context.Background(), batchTenant, spans); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if spans[0].Tenant != batchTenant {
		t.Fatalf("span.Tenant must be overwritten with the batch tenant; got %q, want %q", spans[0].Tenant, batchTenant)
	}
}

// TestReplayWAL_PreservesTenant covers the third half of the fix: replayed
// traces were reconstructed as &Trace{TraceID: tid, ...} with Tenant left at
// its zero value, so every trace recovered from the WAL finalized under
// TenantID("").
func TestReplayWAL_PreservesTenant(t *testing.T) {
	clock := &fakeClock{now: time.Unix(4200, 0)}
	acfg := AssemblyConfig{IdleTimeout: 5 * time.Second, HardTimeout: 30 * time.Second, WheelTick: 250 * time.Millisecond}

	wal := NewMemWAL()
	policy := NewDefaultPolicy(DefaultPolicyConfig(), clock)
	preds := NewPredicateSet()

	tid := model.TenantID("tenant-w")
	traceID := mkTraceID(11)
	key := TraceKey{Tenant: tid, TraceID: traceID}
	if err := wal.Append(0, key, mkSpan(traceID, mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)); err != nil {
		t.Fatalf("wal.Append: %v", err)
	}

	mgr := NewManager(acfg, clock, wal, policy, policy, preds, 1)
	if _, err := mgr.ReplayWAL(context.Background()); err != nil {
		t.Fatalf("ReplayWAL: %v", err)
	}

	clock.now = clock.now.Add(10 * time.Second)
	mgr.Tick(context.Background())

	select {
	case d := <-mgr.Decisions():
		if d.Tenant != tid {
			t.Fatalf("a WAL-replayed trace must finalize under the tenant it was journalled for; got %q, want %q", d.Tenant, tid)
		}
	default:
		t.Fatal("expected a Decision for the replayed trace")
	}
}

// TestWALTruncate_IsTenantScoped covers the WAL half: Truncate used to key
// on TraceID alone, so tenant A finalizing its trace deleted tenant B's
// journalled spans for the same id.
func TestWALTruncate_IsTenantScoped(t *testing.T) {
	wal := NewMemWAL()
	traceID := mkTraceID(21)
	a := TraceKey{Tenant: "tenant-a", TraceID: traceID}
	b := TraceKey{Tenant: "tenant-b", TraceID: traceID}

	span := mkSpan(traceID, mkSpanID(1), model.SpanID{}, "svc", "op", 0, uint64(time.Millisecond), false)
	if err := wal.Append(0, a, span); err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	if err := wal.Append(0, b, span); err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	if err := wal.Truncate(0, a); err != nil {
		t.Fatalf("Truncate(a): %v", err)
	}

	got, err := wal.Replay(0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, stillThere := got[a]; stillThere {
		t.Error("Truncate did not remove tenant-a's own entry")
	}
	if _, survived := got[b]; !survived {
		t.Error("Truncate(tenant-a) also deleted tenant-b's journalled spans for the same TraceID")
	}
}
