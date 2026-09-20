package cluster

import "context"

// Elector is driven by cluster.leader_election.driver (01 §7: "none" |
// a Kubernetes Lease driver in-cluster). DR-27 §27.2 is the sole consumer
// named in the register: the control.db writer goroutine appends audit
// rows only on the leader; a non-leader forwards over an internal control
// channel and fails closed if that channel is unavailable.
type Elector interface {
	Campaign(ctx context.Context) error
	IsLeader() bool
	Resign(ctx context.Context) error
	// Notify delivers leadership transitions (true = became leader).
	Notify() <-chan bool
	Close() error
}
