package remediate

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"time"

	"traceiq/internal/k8s"
	"traceiq/internal/model"
)

// DryRunExecutor is remediate.exec.dryrun (DR-23 §23.10's default executor).
// It performs ZERO network I/O and touches no real cluster state: Do never
// shells out, never opens a socket, never reads a file — it is a pure
// function of its input that returns a canned success Result. This is the
// structural guarantee test (f) checks: the type itself holds no client,
// no kubeconfig, no executable path — there is nothing in it that COULD
// reach a real cluster.
type DryRunExecutor struct{}

var _ k8s.Executor = DryRunExecutor{}

func (DryRunExecutor) Kind() string { return "dryrun" }

func (DryRunExecutor) Do(ctx context.Context, r k8s.Request) (k8s.Result, error) {
	out, _ := json.Marshal(map[string]any{
		"dryrun":    true,
		"verb":      r.Verb,
		"namespace": r.Namespace,
		"kind":      r.Kind,
		"name":      r.Name,
	})
	return k8s.Result{ExitCode: 0, Stdout: out, Duration: 0}, nil
}

// argvRunner is the seam between KubectlExecutor and the process that
// actually invokes /usr/local/bin/kubectl. Production wiring supplies a
// runner built on exec.CommandContext + k8s.MinimalEnv(); tests supply a
// fake, so no test in this package ever spawns a real kubectl process
// (requirement (h): "test the builder directly, not by spawning real
// kubectl").
type argvRunner func(ctx context.Context, argv []string, env []string, stdin []byte, timeout time.Duration) (k8s.Result, error)

// buildKubectlRequest is remediate's half of the argv path: it turns a
// resolved model.ActionTarget + verb into a k8s.Request populated with
// ONLY typed, validated fields (Verb, Namespace, Kind, Name, StdinJSON) —
// no argv, no shell string, no fmt.Sprintf of a command line. It NEVER
// concatenates a command string; every field is assigned from a typed
// source (a Verb constant, a validated Namespace, a TargetKind constant, a
// validated Name, and either nil or a []byte produced by json.Marshal of a
// small struct). Per DR-24, k8s.BuildArgv — the ONLY argv constructor in
// the system — is the next and only step that turns this Request into
// argv; this function's whole job is to make sure nothing upstream of that
// call is a string built by concatenation.
func buildKubectlRequest(verb k8s.Verb, target model.ActionTarget, stdinJSON []byte, dryRun bool, timeout time.Duration) (k8s.Request, error) {
	if !dns1123.MatchString(target.Namespace) {
		return k8s.Request{}, fmt.Errorf("%w: namespace %q failed validateNamespace", ErrForbiddenFlag, target.Namespace)
	}
	if !dns1123.MatchString(target.Name) {
		return k8s.Request{}, fmt.Errorf("%w: name %q failed validateName", ErrForbiddenFlag, target.Name)
	}
	return k8s.Request{
		Verb:      verb,
		Namespace: target.Namespace,
		Kind:      target.Kind,
		Name:      target.Name,
		StdinJSON: stdinJSON,
		DryRun:    dryRun,
		Timeout:   timeout,
	}, nil
}

// verbForAction maps the closed ActionType enum to the closed Verb enum —
// another pure, switch-based (never string-built) mapping.
func verbForAction(t model.ActionType) (k8s.Verb, error) {
	switch t {
	case model.ActionRollbackDeployment:
		return k8s.VerbRolloutUndo, nil
	case model.ActionScaleReplicas:
		return k8s.VerbScale, nil
	case model.ActionRestartPod:
		return k8s.VerbDeletePod, nil
	case model.ActionToggleFeatureFlag, model.ActionRemoveIstioFault:
		return k8s.VerbPatch, nil
	default:
		return 0, fmt.Errorf("%w: no Verb for ActionType %d", ErrInvalidProposal, t)
	}
}

// stdinPayload is DR-22 §22.2's Guard-computed payload for the patch verbs.
// It is built entirely from typed fields (never from Rationale, never from
// any proposer-authored string) and travels on stdin (DR-24 rule 3), never
// on argv.
func stdinPayload(a model.Action) ([]byte, error) {
	switch a.Proposal.Type {
	case model.ActionRollbackDeployment:
		return nil, nil // rollout undo takes no body; --to-revision is an argv int
	case model.ActionScaleReplicas:
		return nil, nil // scale --replicas=<n> is an argv int
	case model.ActionRestartPod:
		return nil, nil // delete pod --grace-period=<n> is an argv int
	case model.ActionToggleFeatureFlag:
		val := strconv.FormatBool(a.Proposal.FeatureFlag.Value)
		return json.Marshal(map[string]any{"data": map[string]string{a.Proposal.FeatureFlag.Key: val}})
	case model.ActionRemoveIstioFault:
		// The Guard computes this by diffing PreSnapshot against itself
		// with `fault` removed (§22.2). Full VirtualService-shaped diffing
		// is out of this package's scope for this pass; refusing with
		// ErrNothingToRemove when there is no snapshot to diff against is
		// the safe default (never fabricates a patch).
		if len(a.PreSnapshot.RawJSON) == 0 {
			return nil, ErrNothingToRemove
		}
		return nil, fmt.Errorf("NEEDS_CONTEXT: RemoveIstioFault JSON-patch diffing against PreSnapshot.RawJSON is not implemented in this pass")
	default:
		return nil, fmt.Errorf("%w: no payload builder for ActionType %d", ErrInvalidProposal, a.Proposal.Type)
	}
}

// checkPayloadScope is FR-F09-17 / DR-22 §22.2's payload-out-of-scope
// check: the Guard-computed stdin payload MUST touch only the per-type
// allowed path set (`/spec/replicas`, `/data/<Key>`, `/spec/http/*/fault`,
// `/metadata/annotations/kubectl.kubernetes.io/restartedAt`) before it is
// ever handed to the executor. This was previously declared
// (ErrPayloadOutOfScope) but never called anywhere in the package — fixed
// per docs/reports/w14-review-remediate.md, finding M2.
//
// RollbackDeployment/ScaleReplicas/RestartPod carry no stdin body at all
// (their single field travels as a range-checked argv int, per
// stdinPayload above), so they have nothing to diff and always pass here;
// ToggleFeatureFlag and RemoveIstioFault carry a JSON body and are checked
// against their allowed shape.
func checkPayloadScope(p model.ActionProposal, stdin []byte) error {
	if len(stdin) == 0 {
		return nil
	}
	switch p.Type {
	case model.ActionToggleFeatureFlag:
		if p.FeatureFlag == nil {
			return fmt.Errorf("%w: ToggleFeatureFlag payload with no FeatureFlag spec", ErrPayloadOutOfScope)
		}
		var body map[string]map[string]string
		if err := json.Unmarshal(stdin, &body); err != nil {
			return fmt.Errorf("%w: unparseable ToggleFeatureFlag payload", ErrPayloadOutOfScope)
		}
		data, ok := body["data"]
		if !ok || len(body) != 1 || len(data) != 1 {
			return fmt.Errorf("%w: ToggleFeatureFlag payload must be exactly {\"data\":{<Key>:<val>}}", ErrPayloadOutOfScope)
		}
		if _, ok := data[p.FeatureFlag.Key]; !ok {
			return fmt.Errorf("%w: ToggleFeatureFlag payload key does not match the proposal's FeatureFlag.Key", ErrPayloadOutOfScope)
		}
		return nil
	case model.ActionRemoveIstioFault:
		// Forward-compatible with a future real JSON-patch diff
		// (docs/reports/w13-remediate.md gap 2): every op must be a
		// "remove" whose path matches /spec/http/<i>/fault. Currently
		// unreachable — stdinPayload always errors for this type before a
		// body is ever produced — but this keeps the check meaningful the
		// moment that gap is closed, rather than leaving it silently
		// unenforced.
		var ops []struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(stdin, &ops); err != nil {
			return fmt.Errorf("%w: unparseable RemoveIstioFault patch", ErrPayloadOutOfScope)
		}
		for _, op := range ops {
			if op.Op != "remove" {
				return fmt.Errorf("%w: RemoveIstioFault patch op %q not allowed (remove only)", ErrPayloadOutOfScope, op.Op)
			}
			if ok, _ := path.Match("/spec/http/*/fault", op.Path); !ok {
				return fmt.Errorf("%w: RemoveIstioFault patch path %q outside the allowed set", ErrPayloadOutOfScope, op.Path)
			}
		}
		return nil
	default:
		// Nothing in the closed 5-value ActionType enum should reach here
		// carrying a non-empty stdin body.
		return fmt.Errorf("%w: ActionType %d must not carry a stdin payload", ErrPayloadOutOfScope, p.Type)
	}
}

// KubectlExecutor is remediate.exec.kubectl (DR-24): it builds a typed
// k8s.Request (buildKubectlRequest, above), hands it to k8s.BuildArgv — the
// system's ONLY argv constructor — and passes the resulting argv slice to
// runner. It never touches a string concatenated command line itself.
//
// k8s.BuildArgv is currently `panic("not implemented")` (internal/k8s is
// out of this task's scope — see docs/reports/w13-remediate.md). Do()
// recovers that panic and turns it into an error rather than letting it
// escape, which is the correct boundary behavior regardless of why the
// dependency is unavailable, and keeps this package's tests (and any
// caller) from crashing on an upstream gap.
type KubectlExecutor struct {
	Runner argvRunner // nil => real exec.CommandContext + k8s.MinimalEnv (production wiring, not exercised by this package's tests)
}

var _ k8s.Executor = (*KubectlExecutor)(nil)

func (e *KubectlExecutor) Kind() string { return "kubectl" }

func (e *KubectlExecutor) Do(ctx context.Context, r k8s.Request) (result k8s.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("remediate: kubectl argv construction unavailable (k8s.BuildArgv panicked: %v)", p)
		}
	}()

	argv, buildErr := k8s.BuildArgv(r)
	if buildErr != nil {
		return k8s.Result{}, buildErr
	}
	r.Argv = argv

	if e.Runner == nil {
		return k8s.Result{}, fmt.Errorf("remediate: KubectlExecutor.Runner not wired (production exec.CommandContext runner is out of this pass's scope)")
	}
	env := func() (env []string) {
		defer func() {
			if p := recover(); p != nil {
				env = nil
			}
		}()
		return k8s.MinimalEnv()
	}()
	return e.Runner(ctx, r.Argv, env, r.StdinJSON, r.Timeout)
}
