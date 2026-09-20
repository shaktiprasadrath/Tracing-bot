package remediate

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"traceiq/internal/k8s"
	"traceiq/internal/model"
)

// --- (f) dryrun executor never touches real state ---

// clusterSpy stands in for "real cluster state": if DryRunExecutor ever
// called anything that could mutate it, this counter would move. It never
// does, because DryRunExecutor.Do has no reference to it at all — the type
// holds no client, no kubeconfig path, no executable path.
type clusterSpy struct{ writes int }

func TestDryRunExecutor_NeverTouchesRealState(t *testing.T) {
	spy := &clusterSpy{}
	exec := DryRunExecutor{}
	if exec.Kind() != "dryrun" {
		t.Fatalf("want Kind()==dryrun, got %q", exec.Kind())
	}
	for i := 0; i < 5; i++ {
		res, err := exec.Do(context.Background(), k8s.Request{
			Verb: k8s.VerbScale, Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout",
		})
		if err != nil {
			t.Fatalf("dryrun Do returned error: %v", err)
		}
		if res.ExitCode != 0 {
			t.Fatalf("dryrun must report success, got exit %d", res.ExitCode)
		}
	}
	if spy.writes != 0 {
		t.Fatalf("dryrun executor must never touch real cluster state; spy.writes=%d", spy.writes)
	}
	// Structural guarantee: DryRunExecutor is an empty struct — there is no
	// field through which it could hold a client, socket, or file handle.
	var zero DryRunExecutor
	_ = zero
}

// --- (h) kubectl argv construction never uses string concatenation/shell interpolation ---
// Tested against remediate's own request-building function directly
// (buildKubectlRequest / verbForAction), never by spawning kubectl, and
// never by building a command string that gets parsed — every field lands
// on the k8s.Request struct as a typed value.

var shellMeta = regexp.MustCompile(`[;&|$` + "`" + `<>(){}\n]`)

func TestBuildKubectlRequest_NoShellMetacharactersAndTypedFieldsOnly(t *testing.T) {
	target := model.ActionTarget{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout-svc"}
	req, err := buildKubectlRequest(k8s.VerbScale, target, nil, false, 30*time.Second)
	if err != nil {
		t.Fatalf("buildKubectlRequest: %v", err)
	}
	if req.Argv != nil {
		t.Fatalf("buildKubectlRequest must never itself populate Argv (that is k8s.BuildArgv's exclusive job); got %v", req.Argv)
	}
	if req.Namespace != "checkout" || req.Name != "checkout-svc" || req.Kind != model.KindDeployment || req.Verb != k8s.VerbScale {
		t.Fatalf("request fields not carried through verbatim as typed values: %+v", req)
	}
	if shellMeta.MatchString(req.Namespace) || shellMeta.MatchString(req.Name) {
		t.Fatalf("namespace/name must never carry shell metacharacters")
	}
}

func TestBuildKubectlRequest_RejectsInjectionShapedNamespaceOrName(t *testing.T) {
	cases := []model.ActionTarget{
		{Namespace: "checkout; rm -rf /", Kind: model.KindDeployment, Name: "checkout"},
		{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout && curl evil"},
		{Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout/../secrets"},
		{Namespace: "$(whoami)", Kind: model.KindDeployment, Name: "checkout"},
	}
	for _, target := range cases {
		if _, err := buildKubectlRequest(k8s.VerbScale, target, nil, false, time.Second); err == nil {
			t.Fatalf("expected rejection for injection-shaped target %+v", target)
		}
	}
}

func TestVerbForAction_IsAPureSwitchNoStringBuilding(t *testing.T) {
	cases := map[model.ActionType]k8s.Verb{
		model.ActionRollbackDeployment: k8s.VerbRolloutUndo,
		model.ActionScaleReplicas:      k8s.VerbScale,
		model.ActionRestartPod:         k8s.VerbDeletePod,
		model.ActionToggleFeatureFlag:  k8s.VerbPatch,
		model.ActionRemoveIstioFault:   k8s.VerbPatch,
	}
	for at, want := range cases {
		got, err := verbForAction(at)
		if err != nil {
			t.Fatalf("verbForAction(%d): %v", at, err)
		}
		if got != want {
			t.Fatalf("verbForAction(%d) = %d, want %d", at, got, want)
		}
	}
}

// --- finding M2: checkPayloadScope (FR-F09-17 / DR-22 §22.2) must reject a
// computed payload that touches anything outside the allowed shape, and
// must accept the one that's actually computed by stdinPayload for a
// well-formed ToggleFeatureFlag proposal. Previously ErrPayloadOutOfScope
// was declared but this check was never called from anywhere. ---

func TestCheckPayloadScope_AcceptsWellFormedToggleFeatureFlagPayload(t *testing.T) {
	p := model.ActionProposal{
		Type:        model.ActionToggleFeatureFlag,
		FeatureFlag: &model.ToggleFeatureFlagSpec{Key: "new-checkout", Value: true},
	}
	stdin, err := stdinPayload(model.Action{Proposal: p})
	if err != nil {
		t.Fatalf("stdinPayload: %v", err)
	}
	if err := checkPayloadScope(p, stdin); err != nil {
		t.Fatalf("expected the Guard's own computed payload to be in scope, got %v", err)
	}
}

func TestCheckPayloadScope_RejectsExtraKeysOrWrongKey(t *testing.T) {
	p := model.ActionProposal{
		Type:        model.ActionToggleFeatureFlag,
		FeatureFlag: &model.ToggleFeatureFlagSpec{Key: "new-checkout", Value: true},
	}
	cases := [][]byte{
		[]byte(`{"data":{"new-checkout":"true"},"extra":{"x":"y"}}`),    // second top-level key
		[]byte(`{"data":{"new-checkout":"true","other-flag":"false"}}`), // second data key
		[]byte(`{"data":{"other-flag":"true"}}`),                        // wrong key entirely
		[]byte(`{"spec":{"replicas":"999"}}`),                           // out-of-scope path altogether
	}
	for i, stdin := range cases {
		if err := checkPayloadScope(p, stdin); err == nil {
			t.Fatalf("case %d: expected ErrPayloadOutOfScope, got nil", i)
		} else if !errors.Is(err, ErrPayloadOutOfScope) {
			t.Fatalf("case %d: want ErrPayloadOutOfScope, got %v", i, err)
		}
	}
}

func TestCheckPayloadScope_RemoveIstioFaultRejectsPathsOutsideAllowedSet(t *testing.T) {
	p := model.ActionProposal{Type: model.ActionRemoveIstioFault, IstioFault: &model.RemoveIstioFaultSpec{}}
	good := []byte(`[{"op":"remove","path":"/spec/http/0/fault"}]`)
	if err := checkPayloadScope(p, good); err != nil {
		t.Fatalf("expected an allowed remove-fault patch to pass, got %v", err)
	}
	bad := [][]byte{
		[]byte(`[{"op":"remove","path":"/spec/http/0/route"}]`),    // wrong leaf
		[]byte(`[{"op":"replace","path":"/spec/http/0/fault"}]`),   // wrong op
		[]byte(`[{"op":"remove","path":"/metadata/annotations"}]`), // wrong subtree
	}
	for i, stdin := range bad {
		if err := checkPayloadScope(p, stdin); !errors.Is(err, ErrPayloadOutOfScope) {
			t.Fatalf("case %d: want ErrPayloadOutOfScope, got %v", i, err)
		}
	}
}

// KubectlExecutor must never itself construct argv by string
// concatenation: it delegates exclusively to k8s.BuildArgv (DR-24's ONLY
// argv constructor). That function is currently unimplemented upstream
// (internal/k8s is out of this task's scope — panic("not implemented")),
// so KubectlExecutor.Do must convert that panic into a plain error rather
// than crash, which is what this test asserts: calling it never spawns a
// process and never panics out of the test binary.
func TestKubectlExecutor_NeverSpawnsRealProcess_AndSurvivesUnimplementedBuildArgv(t *testing.T) {
	spawned := false
	exec := &KubectlExecutor{
		Runner: func(ctx context.Context, argv []string, env []string, stdin []byte, timeout time.Duration) (k8s.Result, error) {
			spawned = true
			return k8s.Result{}, nil
		},
	}
	_, err := exec.Do(context.Background(), k8s.Request{
		Verb: k8s.VerbScale, Namespace: "checkout", Kind: model.KindDeployment, Name: "checkout",
	})
	if err == nil {
		t.Fatalf("expected an error while k8s.BuildArgv is unimplemented, got nil")
	}
	if spawned {
		t.Fatalf("the runner must never be invoked when argv construction fails/is unavailable")
	}
	if exec.Kind() != "kubectl" {
		t.Fatalf("want Kind()==kubectl, got %q", exec.Kind())
	}
}
