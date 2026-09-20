package model

// UntrustedKind classes every string not authored by TraceIQ's own code
// (DR-37 §37.1(2)), wrapped by rca.Sanitizer.Wrap(k UntrustedKind, s string)
// before it can reach a prompt.
type UntrustedKind uint8

const (
	UntrustedTelemetry      UntrustedKind = 1
	UntrustedLog            UntrustedKind = 2
	UntrustedMemory         UntrustedKind = 3
	UntrustedUserQuestion   UntrustedKind = 4
	UntrustedRunbook        UntrustedKind = 5
	UntrustedDeployMetadata UntrustedKind = 6
)
