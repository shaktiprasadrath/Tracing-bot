package bus

import (
	"context"

	"traceiq/internal/model"
)

// Message is one published unit: a partition key (DR-8's rendezvous-hashed
// TraceID) plus the encoded model.Batch payload.
type Message struct {
	Tenant    model.TenantID
	Partition int
	Key       []byte // xxh3 input for ShardFor; carried, never recomputed here
	Value     []byte // encoded model.Batch
}

// Bus is the transport seam driven by cluster.bus.driver (DR-32 §32.3):
// "none" (in-process ring, the default everywhere) or kafka/redpanda [P2].
// Partitioning is always computed upstream by sampler.ShardFor (DR-8); Bus
// only carries the already-assigned partition.
type Bus interface {
	Publish(ctx context.Context, topic string, m Message) error
	Subscribe(ctx context.Context, topic string) (<-chan Message, error)
	Kind() string // "none" | "kafka" | "redpanda"
	Close() error
}
