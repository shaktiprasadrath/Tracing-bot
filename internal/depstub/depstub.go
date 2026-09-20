// Package depstub pins the Set B dependency graph (DR-1) until real packages import them.
// Delete once every module below is imported by production code.
package depstub

import (
	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otlpcol "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/crypto/argon2"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var (
	_ = cmp.Diff
	_ = zstd.NewWriter
	_ = parquet.SchemaOf
	_ = prometheus.NewRegistry
	_ = otel.Tracer
	_ = sdktrace.NewTracerProvider
	_ = (*otlpcol.ExportTraceServiceRequest)(nil)
	_ = argon2.IDKey
	_ = (*errgroup.Group)(nil)
	_ = rate.NewLimiter
	_ = grpc.NewServer
	_ = proto.Marshal
)
