package k8s // imports model + stdlib only (DR-2)

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// Verb is DR-24, verbatim: the closed set of kubectl subcommands BuildArgv
// may emit.
type Verb uint8

const (
	VerbGet         Verb = 1
	VerbScale       Verb = 2
	VerbRolloutUndo Verb = 3
	VerbDeletePod   Verb = 4
	VerbPatch       Verb = 5
)

// Request is DR-24, verbatim: Argv is produced by BuildArgv ONLY, never
// assembled by a caller; patch bodies travel on StdinJSON, never on the
// command line (DR-24 rule 3).
type Request struct {
	Verb      Verb
	Namespace string
	Kind      model.TargetKind
	Name      string
	Argv      []string // produced by BuildArgv ONLY; never assembled by a caller
	StdinJSON []byte   // patch bodies travel on STDIN, never on the command line
	DryRun    bool
	Timeout   time.Duration
}

// Result is DR-24, verbatim.
type Result struct {
	ExitCode       int
	Stdout, Stderr []byte
	Duration       time.Duration
}

// Executor is DR-24, verbatim: the sole cluster-write path (also used
// unmodified by the eval harness, DR-24 rule 8 / DR-36 §36.7).
type Executor interface {
	Do(ctx context.Context, r Request) (Result, error)
	Kind() string // "kubectl" | "dryrun" | "fake"
}

// BuildArgv is DR-24, verbatim: the ONLY argv constructor in the system.
// Its output goes straight to exec.CommandContext. There is no shell, no
// "sh -c", no string join, no interpolation.
func BuildArgv(r Request) ([]string, error) {
	panic("not implemented")
}

// MinimalEnv is DR-24, verbatim: exactly KUBECONFIG, HOME,
// PATH=/usr/local/bin. The ambient environment is never inherited.
func MinimalEnv() []string {
	panic("not implemented")
}
