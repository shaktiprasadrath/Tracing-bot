// Package sqlite — see doc.go for the binding source and scope.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO (FR-F03-8)

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/topology"
)

// IndexedAttrKeys is the closed 8-key allowlist (DR-6 §6.2's
// store.hot.indexed_attribute_keys, exact order — key_id is the list index).
var IndexedAttrKeys = []string{
	"http.response.status_code", // 0
	"http.route",                // 1
	"error.type",                // 2
	"rpc.method",                // 3
	"db.system.name",            // 4
	"peer.service",              // 5
	"k8s.pod.name",              // 6
	"service.version",           // 7
}

// KeyID returns the allowlist index for key, or -1 if key is outside the
// 8-key allowlist (FR-F03-3: such a query must be answered from the cold
// tier, never silently from the hot index).
func KeyID(key string) int {
	for i, k := range IndexedAttrKeys {
		if k == key {
			return i
		}
	}
	return -1
}

// hashValue is the hot index's value-hash function for attr_index/attr_dict.
// DR-6 §6.2 names it "xxh3(normalized value)"; this driver uses FNV-1a
// instead (no xxh3 dependency in go.mod for this pass) — functionally
// equivalent for the closed-allowlist index's own read/write consistency,
// flagged as a deviation in docs/reports/w9-store.md.
func hashValue(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64())
}

// writeJob is one unit of work handed to the single telemetry writer
// goroutine (DR-6 §6.2's "1 telemetry writer").
type writeJob struct {
	fn   func(*sql.Tx) error
	done chan error
}

// Store is the store/sqlite driver: modernc.org/sqlite against
// ${data_dir}/hot/traceiq.db, WAL mode, one dedicated writer goroutine
// (DR-6 §6.2). It implements store.HotIndex and topology.EdgeSink/EdgeSource.
type Store struct {
	db   *sql.DB
	path string

	writeCh chan writeJob
	closed  chan struct{}
	wg      sync.WaitGroup

	closeOnce sync.Once
}

var _ store.HotIndex = (*Store)(nil)
var _ topology.EdgeSink = (*Store)(nil)
var _ topology.EdgeSource = (*Store)(nil)

// Open opens (creating if absent) the traceiq.db file at path, applies the
// DR-6 §6.2 PRAGMAs and DDL, and starts the single telemetry writer
// goroutine.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	// A single connection keeps every statement serialized through the one
	// writer goroutine below without fighting SQLite's single-writer model;
	// correctness over read concurrency for this pass (see w9-store.md).
	db.SetMaxOpenConns(1)

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite: pragma %q: %w", p, err)
		}
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite: schema: %w\n%s", err, stmt)
		}
	}

	s := &Store{
		db:      db,
		path:    path,
		writeCh: make(chan writeJob, 256),
		closed:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.writerLoop()
	return s, nil
}

func (s *Store) writerLoop() {
	defer s.wg.Done()
	for job := range s.writeCh {
		job.done <- s.runTx(job.fn)
	}
}

func (s *Store) runTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// write submits fn to the single writer goroutine and blocks for the result
// (DR-6 §6.1: "exactly one batch call per flush, never per row" — callers
// are expected to batch before calling write, not call it per row).
func (s *Store) write(fn func(*sql.Tx) error) error {
	done := make(chan error, 1)
	select {
	case s.writeCh <- writeJob{fn, done}:
	case <-s.closed:
		return errors.New("sqlite: store closed")
	}
	select {
	case err := <-done:
		return err
	case <-s.closed:
		return errors.New("sqlite: store closed")
	}
}

// Close stops the writer goroutine and closes the database.
func (s *Store) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.writeCh)
		s.wg.Wait()
		err = s.db.Close()
	})
	return err
}

func (s *Store) Capabilities() store.HotCapabilities {
	return store.HotCapabilities{
		ReadYourWrites:     true,
		RowLevelDelete:     true,
		FullTextSearch:     false, // LIKE-based search, not FTS5 in this pass (see hashValue's sibling note)
		MaxKeptSpansPerSec: 1200,
	}
}

func (s *Store) Health(ctx context.Context) store.HealthReport {
	now := time.Now()
	if err := s.db.PingContext(ctx); err != nil {
		return store.HealthReport{Healthy: false, Message: err.Error(), CheckedAt: now}
	}
	return store.HealthReport{Healthy: true, Message: "ok", CheckedAt: now}
}

// --- id encoding helpers ---

func traceIDBytes(id model.TraceID) []byte { b := id; return b[:] }
func spanIDBytes(id model.SpanID) []byte   { b := id; return b[:] }

func toTraceID(b []byte) model.TraceID {
	var id model.TraceID
	copy(id[:], b)
	return id
}

func toSpanID(b []byte) model.SpanID {
	var id model.SpanID
	copy(id[:], b)
	return id
}

func encodeHist(h model.LatencyHist) []byte {
	buf := make([]byte, 16*4)
	for i, v := range h {
		binary.BigEndian.PutUint32(buf[i*4:], v)
	}
	return buf
}

func decodeHist(b []byte) model.LatencyHist {
	var h model.LatencyHist
	for i := range h {
		if (i+1)*4 <= len(b) {
			h[i] = binary.BigEndian.Uint32(b[i*4:])
		}
	}
	return h
}

func encodeTraceIDs(ids []model.TraceID) []byte {
	buf := make([]byte, 0, len(ids)*16)
	for _, id := range ids {
		buf = append(buf, id[:]...)
	}
	return buf
}

func decodeTraceIDs(b []byte) []model.TraceID {
	n := len(b) / 16
	ids := make([]model.TraceID, 0, n)
	for i := 0; i < n; i++ {
		var id model.TraceID
		copy(id[:], b[i*16:(i+1)*16])
		ids = append(ids, id)
	}
	return ids
}

func redTable(res model.Resolution) (string, error) {
	switch res {
	case model.Res10s:
		return "red_rollup", nil
	case model.Res5m:
		return "red_rollup_5m", nil
	case model.Res1h:
		return "red_rollup_1h", nil
	default:
		return "", fmt.Errorf("sqlite: unknown resolution %d", res)
	}
}

// redTableForWindow picks the resolution table for QueryRED per DR-39 —
// the register doesn't publish an exact window->resolution boundary rule,
// so this driver uses the coarsest table whose bucket width still resolves
// the query window meaningfully: <=1h -> 10s, <=48h -> 5m, else 1h.
func redTableForWindow(w model.Window) string {
	d := w.End.Sub(w.Start)
	switch {
	case d <= time.Hour:
		return "red_rollup"
	case d <= 48*time.Hour:
		return "red_rollup_5m"
	default:
		return "red_rollup_1h"
	}
}

func removeFile(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
