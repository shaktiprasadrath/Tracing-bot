package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"traceiq/internal/model"
	"traceiq/internal/store"
	"traceiq/internal/topology"
)

// WriteBatch inserts one flush's worth of rows across every hot table in a
// single transaction on the telemetry writer goroutine (DR-6 §6.1).
func (s *Store) WriteBatch(ctx context.Context, tid model.TenantID, b store.HotBatch) (store.BatchReceipt, error) {
	start := time.Now()
	var rows int
	err := s.write(func(tx *sql.Tx) error {
		for _, t := range b.Traces {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO trace (tenant_id, trace_id, root_service, start_unix_nano, duration_nanos,
					error_count, path_signature, keep_reason, cold_state, block_id, row_group, row_offset, wal_segment)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, trace_id) DO UPDATE SET
					root_service=excluded.root_service, start_unix_nano=excluded.start_unix_nano,
					duration_nanos=excluded.duration_nanos, error_count=excluded.error_count,
					path_signature=excluded.path_signature, keep_reason=excluded.keep_reason,
					cold_state=excluded.cold_state, block_id=excluded.block_id,
					row_group=excluded.row_group, row_offset=excluded.row_offset, wal_segment=excluded.wal_segment`,
				string(t.Tenant), traceIDBytes(t.TraceID), t.RootService, t.StartUnixNano, t.DurationNanos,
				t.ErrorCount, int64(t.PathSignature), int(t.KeepReason), int(t.ColdState),
				nullString(t.BlockID), nullInt(int64(t.RowGroup), t.BlockID != ""), nullInt(t.RowOffset, t.BlockID != ""), nullString(t.WALSegment),
			); err != nil {
				return fmt.Errorf("insert trace: %w", err)
			}
			rows++
		}
		for _, sp := range b.Spans {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO span (tenant_id, trace_id, span_id, service, operation, start_unix_nano, duration_nanos, error_sig_id)
				VALUES (?,?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, trace_id, span_id) DO UPDATE SET
					service=excluded.service, operation=excluded.operation,
					start_unix_nano=excluded.start_unix_nano, duration_nanos=excluded.duration_nanos,
					error_sig_id=excluded.error_sig_id`,
				string(sp.Tenant), traceIDBytes(sp.TraceID), spanIDBytes(sp.SpanID), sp.Service, sp.Operation,
				sp.StartUnixNano, sp.DurationNanos, nullString(sp.ErrorSigID),
			); err != nil {
				return fmt.Errorf("insert span: %w", err)
			}
			rows++
		}
		for _, a := range b.AttrRows {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO attr_index (tenant_id, key_id, value_hash, bucket, trace_id, span_id)
				VALUES (?,?,?,?,?,?)`,
				string(a.Tenant), a.KeyID, a.ValueHash, a.Bucket, traceIDBytes(a.TraceID), spanIDBytes(a.SpanID),
			); err != nil {
				return fmt.Errorf("insert attr_index: %w", err)
			}
			rows++
		}
		for _, d := range b.AttrDict {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO attr_dict (tenant_id, key_id, value_hash, value, first_seen, last_seen)
				VALUES (?,?,?,?,?,?)
				ON CONFLICT(tenant_id, key_id, value_hash) DO UPDATE SET last_seen=excluded.last_seen`,
				string(d.Tenant), d.KeyID, d.ValueHash, d.Value, d.FirstSeen.UnixNano(), d.LastSeen.UnixNano(),
			); err != nil {
				return fmt.Errorf("insert attr_dict: %w", err)
			}
			rows++
		}
		for _, f := range b.FTSRows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO span_text_fts (tenant_id, trace_id, span_id, text, ts) VALUES (?,?,?,?,?)`,
				string(f.Tenant), traceIDBytes(f.TraceID), spanIDBytes(f.SpanID), f.Text, f.Timestamp.UnixNano(),
			); err != nil {
				return fmt.Errorf("insert span_text_fts: %w", err)
			}
			rows++
		}
		for _, r := range b.RED {
			tbl, err := redTable(r.Resolution)
			if err != nil {
				return err
			}
			exemplars := encodeTraceIDs(r.ExemplarTraceIDs)
			q := fmt.Sprintf(`
				INSERT INTO %s (tenant_id, service, operation, bucket_start, calls, errors, duration_sum_nanos, hist, exemplar_trace_ids, kept_count)
				VALUES (?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, service, operation, bucket_start) DO UPDATE SET
					calls=calls+excluded.calls, errors=errors+excluded.errors,
					duration_sum_nanos=duration_sum_nanos+excluded.duration_sum_nanos,
					kept_count=kept_count+excluded.kept_count`, tbl)
			if _, err := tx.ExecContext(ctx, q,
				string(r.Tenant), r.Service, r.Operation, r.BucketStart.UnixNano(), r.Calls, r.Errors,
				r.DurationSumNanos, encodeHist(r.Hist), exemplars, r.KeptCount,
			); err != nil {
				return fmt.Errorf("insert %s: %w", tbl, err)
			}
			rows++
		}
		for _, e := range b.Edges {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO topology_edge (tenant_id, edge_id, caller, callee, protocol, bucket_start, resolution,
					calls, errors, duration_sum_nanos, hist, first_seen, last_seen)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, edge_id, bucket_start) DO UPDATE SET
					calls=calls+excluded.calls, errors=errors+excluded.errors,
					duration_sum_nanos=duration_sum_nanos+excluded.duration_sum_nanos, last_seen=excluded.last_seen`,
				string(e.Tenant), e.ID, e.Caller, e.Callee, e.Protocol, e.BucketStart.UnixNano(), int(e.Resolution),
				e.Calls, e.Errors, e.DurationSumNanos, encodeHist(e.Hist), e.FirstSeen.UnixNano(), e.LastSeen.UnixNano(),
			); err != nil {
				return fmt.Errorf("insert topology_edge: %w", err)
			}
			rows++
		}
		for _, eo := range b.EdgeOps {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO topology_edge_op (tenant_id, edge_id, callee_operation, bucket_start, calls, errors, p99_nanos)
				VALUES (?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, edge_id, callee_operation, bucket_start) DO UPDATE SET
					calls=excluded.calls, errors=excluded.errors, p99_nanos=excluded.p99_nanos`,
				string(eo.Tenant), eo.EdgeID, eo.CalleeOperation, eo.BucketStart.UnixNano(), eo.Calls, eo.Errors, eo.P99Nanos,
			); err != nil {
				return fmt.Errorf("insert topology_edge_op: %w", err)
			}
			rows++
		}
		for _, es := range b.ErrorSigs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO error_signature (tenant_id, id, service, signature, first_seen, last_seen, samples)
				VALUES (?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, id) DO UPDATE SET last_seen=excluded.last_seen, samples=samples+excluded.samples`,
				string(es.Tenant), es.ID, es.Service, int64(es.Signature), es.FirstSeen.UnixNano(), es.LastSeen.UnixNano(), es.Samples,
			); err != nil {
				return fmt.Errorf("insert error_signature: %w", err)
			}
			rows++
		}
		for _, ps := range b.PathSigs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO path_signature (tenant_id, signature, first_seen, last_seen)
				VALUES (?,?,?,?)
				ON CONFLICT(tenant_id, signature) DO UPDATE SET last_seen=excluded.last_seen`,
				string(ps.Tenant), int64(ps.Signature), ps.FirstSeen.UnixNano(), ps.LastSeen.UnixNano(),
			); err != nil {
				return fmt.Errorf("insert path_signature: %w", err)
			}
			rows++
		}
		for _, ex := range b.Exemplars {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR REPLACE INTO exemplar (tenant_id, trace_id, span_id, service, operation, ts)
				VALUES (?,?,?,?,?,?)`,
				string(ex.Tenant), traceIDBytes(ex.TraceID), spanIDBytes(ex.SpanID), "", "", ex.Timestamp.UnixNano(),
			); err != nil {
				return fmt.Errorf("insert exemplar: %w", err)
			}
			rows++
		}
		for _, r := range b.Resources {
			attrsJSON, _ := json.Marshal(r.Attrs)
			if _, err := tx.ExecContext(ctx, `
				INSERT OR REPLACE INTO resource (tenant_id, id, attrs) VALUES (?,?,?)`,
				string(r.Tenant), string(r.Tenant), string(attrsJSON),
			); err != nil {
				return fmt.Errorf("insert resource: %w", err)
			}
			rows++
		}

		// approximate raw-ingested bytes for the cost controller's rate input
		// (DR-6 §6.4's 750B/kept-span figure), bucketed per minute.
		if len(b.Spans) > 0 {
			bucket := time.Now().Truncate(time.Minute).UnixNano()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ingest_bytes_bucket (tenant_id, bucket_start, bytes) VALUES (?,?,?)
				ON CONFLICT(tenant_id, bucket_start) DO UPDATE SET bytes=bytes+excluded.bytes`,
				string(tid), bucket, int64(len(b.Spans))*750,
			); err != nil {
				return fmt.Errorf("insert ingest_bytes_bucket: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return store.BatchReceipt{}, err
	}
	return store.BatchReceipt{Rows: rows, CommitMillis: time.Since(start).Milliseconds()}, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

// BindColdBlock runs the single ranged UPDATE DR-7's SealDue algorithm
// describes, served by trace_by_coldstate.
func (s *Store) BindColdBlock(ctx context.Context, tid model.TenantID, walSegment string, m store.BlockManifest) (int, error) {
	var n int64
	err := s.write(func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE trace SET block_id=?, row_group=?, row_offset=?, cold_state=1
			WHERE tenant_id=? AND cold_state=0 AND wal_segment=?`,
			m.BlockID, 0, 0, string(tid), walSegment,
		)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

func (s *Store) GetTrace(ctx context.Context, tid model.TenantID, id model.TraceID) (store.TraceIndex, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT root_service, start_unix_nano, duration_nanos, error_count, path_signature, keep_reason,
			cold_state, COALESCE(block_id,''), COALESCE(row_group,0), COALESCE(row_offset,0), COALESCE(wal_segment,'')
		FROM trace WHERE tenant_id=? AND trace_id=?`, string(tid), traceIDBytes(id))
	var ti store.TraceIndex
	var keepReason, coldState int
	var pathSig int64
	if err := row.Scan(&ti.RootService, &ti.StartUnixNano, &ti.DurationNanos, &ti.ErrorCount, &pathSig,
		&keepReason, &coldState, &ti.BlockID, &ti.RowGroup, &ti.RowOffset, &ti.WALSegment); err != nil {
		if err == sql.ErrNoRows {
			return store.TraceIndex{}, fmt.Errorf("sqlite: trace %x not found: %w", id, err)
		}
		return store.TraceIndex{}, err
	}
	ti.Tenant = tid
	ti.TraceID = id
	// path_signature is stored as a signed INTEGER (SQLite has no native
	// uint64), so a hash with the sign bit set round-trips as a negative
	// int64; scanning straight into a *uint64 makes database/sql's
	// strconv.ParseUint reject the "-...” literal ("invalid syntax"). Scan
	// into int64 and bit-reinterpret back to uint64 instead.
	ti.PathSignature = uint64(pathSig)
	ti.KeepReason = model.KeepReason(keepReason)
	ti.ColdState = uint8(coldState)
	return ti, nil
}

func (s *Store) SearchSpans(ctx context.Context, tid model.TenantID, q store.SpanQuery) (store.SpanPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	switch {
	case q.AttrKey != "":
		keyID := KeyID(q.AttrKey)
		if keyID < 0 {
			return store.SpanPage{}, fmt.Errorf("sqlite: attribute key %q outside hot allowlist, must be answered from cold tier (FR-F03-3)", q.AttrKey)
		}
		vh := hashValue(q.AttrValue)
		rows, err = s.db.QueryContext(ctx, `
			SELECT sp.tenant_id, sp.trace_id, sp.span_id, sp.service, sp.operation, sp.start_unix_nano, sp.duration_nanos, COALESCE(sp.error_sig_id,'')
			FROM attr_index ai JOIN span sp ON sp.tenant_id=ai.tenant_id AND sp.trace_id=ai.trace_id AND sp.span_id=ai.span_id
			WHERE ai.tenant_id=? AND ai.key_id=? AND ai.value_hash=?
			ORDER BY sp.start_unix_nano DESC LIMIT ?`, string(tid), keyID, vh, limit)
	case q.ErrorSigID != "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT tenant_id, trace_id, span_id, service, operation, start_unix_nano, duration_nanos, COALESCE(error_sig_id,'')
			FROM span WHERE tenant_id=? AND error_sig_id=? ORDER BY start_unix_nano DESC LIMIT ?`,
			string(tid), q.ErrorSigID, limit)
	case q.FullText != "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT sp.tenant_id, sp.trace_id, sp.span_id, sp.service, sp.operation, sp.start_unix_nano, sp.duration_nanos, COALESCE(sp.error_sig_id,'')
			FROM span_text_fts f JOIN span sp ON sp.tenant_id=f.tenant_id AND sp.trace_id=f.trace_id AND sp.span_id=f.span_id
			WHERE f.tenant_id=? AND f.text LIKE ? ORDER BY sp.start_unix_nano DESC LIMIT ?`,
			string(tid), "%"+q.FullText+"%", limit)
	case q.Service != "":
		// Operation and Window are optional narrowing filters, not mandatory
		// ones: an unset q.Operation must match every operation (not just
		// spans whose operation is literally ""), and an unset q.Window
		// (its zero value) must not silently exclude every span — both were
		// previously applied unconditionally, so a caller like
		// mcpSearchTraces that supplies only Service returned zero rows
		// regardless of what was actually in the store.
		conds := "tenant_id=? AND service=?"
		args := []any{string(tid), q.Service}
		if q.Operation != "" {
			conds += " AND operation=?"
			args = append(args, q.Operation)
		}
		if !q.Window.Start.IsZero() || !q.Window.End.IsZero() {
			conds += " AND start_unix_nano BETWEEN ? AND ?"
			args = append(args, q.Window.Start.UnixNano(), q.Window.End.UnixNano())
		}
		args = append(args, limit)
		rows, err = s.db.QueryContext(ctx, `
			SELECT tenant_id, trace_id, span_id, service, operation, start_unix_nano, duration_nanos, COALESCE(error_sig_id,'')
			FROM span WHERE `+conds+`
			ORDER BY start_unix_nano DESC LIMIT ?`, args...)
	default:
		rows, err = s.db.QueryContext(ctx, `
			SELECT tenant_id, trace_id, span_id, service, operation, start_unix_nano, duration_nanos, COALESCE(error_sig_id,'')
			FROM span WHERE tenant_id=? AND start_unix_nano BETWEEN ? AND ?
			ORDER BY start_unix_nano DESC LIMIT ?`,
			string(tid), q.Window.Start.UnixNano(), q.Window.End.UnixNano(), limit)
	}
	if err != nil {
		return store.SpanPage{}, err
	}
	defer rows.Close()

	var page store.SpanPage
	for rows.Next() {
		var sp store.SpanIndex
		var tenant string
		var traceID, spanID []byte
		if err := rows.Scan(&tenant, &traceID, &spanID, &sp.Service, &sp.Operation, &sp.StartUnixNano, &sp.DurationNanos, &sp.ErrorSigID); err != nil {
			return store.SpanPage{}, err
		}
		sp.Tenant = model.TenantID(tenant)
		sp.TraceID = toTraceID(traceID)
		sp.SpanID = toSpanID(spanID)
		page.Spans = append(page.Spans, sp)
	}
	return page, rows.Err()
}

func (s *Store) SearchTraces(ctx context.Context, tid model.TenantID, q store.TraceQuery) (store.TracePage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	switch {
	case q.PathSignature != 0:
		rows, err = s.db.QueryContext(ctx, `
			SELECT trace_id, root_service, start_unix_nano, duration_nanos, error_count, path_signature, keep_reason, cold_state
			FROM trace WHERE tenant_id=? AND path_signature=? ORDER BY start_unix_nano DESC LIMIT ?`,
			string(tid), int64(q.PathSignature), limit)
	case q.ErrorsOnly:
		rows, err = s.db.QueryContext(ctx, `
			SELECT trace_id, root_service, start_unix_nano, duration_nanos, error_count, path_signature, keep_reason, cold_state
			FROM trace WHERE tenant_id=? AND error_count > 0 AND start_unix_nano BETWEEN ? AND ?
			ORDER BY start_unix_nano DESC LIMIT ?`,
			string(tid), q.Window.Start.UnixNano(), q.Window.End.UnixNano(), limit)
	case q.Service != "":
		rows, err = s.db.QueryContext(ctx, `
			SELECT trace_id, root_service, start_unix_nano, duration_nanos, error_count, path_signature, keep_reason, cold_state
			FROM trace WHERE tenant_id=? AND root_service=? AND duration_nanos >= ?
			ORDER BY duration_nanos DESC LIMIT ?`,
			string(tid), q.Service, q.MinDuration.Nanoseconds(), limit)
	default:
		rows, err = s.db.QueryContext(ctx, `
			SELECT trace_id, root_service, start_unix_nano, duration_nanos, error_count, path_signature, keep_reason, cold_state
			FROM trace WHERE tenant_id=? AND start_unix_nano BETWEEN ? AND ?
			ORDER BY start_unix_nano DESC LIMIT ?`,
			string(tid), q.Window.Start.UnixNano(), q.Window.End.UnixNano(), limit)
	}
	if err != nil {
		return store.TracePage{}, err
	}
	defer rows.Close()

	var page store.TracePage
	for rows.Next() {
		var ti store.TraceIndex
		var traceID []byte
		var keepReason, coldState int
		var pathSig int64
		if err := rows.Scan(&traceID, &ti.RootService, &ti.StartUnixNano, &ti.DurationNanos, &ti.ErrorCount,
			&pathSig, &keepReason, &coldState); err != nil {
			return store.TracePage{}, err
		}
		ti.Tenant = tid
		ti.TraceID = toTraceID(traceID)
		// See GetTrace's comment: scan signed, bit-reinterpret to uint64.
		ti.PathSignature = uint64(pathSig)
		ti.KeepReason = model.KeepReason(keepReason)
		ti.ColdState = uint8(coldState)
		page.Traces = append(page.Traces, ti)
	}
	return page, rows.Err()
}

func (s *Store) QueryRED(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) (store.REDSeries, error) {
	tbl := redTableForWindow(w)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT bucket_start, calls, errors, duration_sum_nanos, hist, exemplar_trace_ids, kept_count
		FROM %s WHERE tenant_id=? AND service=? AND operation=? AND bucket_start BETWEEN ? AND ?
		ORDER BY bucket_start ASC`, tbl), string(tid), service, operation, w.Start.UnixNano(), w.End.UnixNano())
	if err != nil {
		return store.REDSeries{}, err
	}
	defer rows.Close()

	var series store.REDSeries
	for rows.Next() {
		var bucketStart int64
		var hist, exemplars []byte
		var sample model.REDSample
		if err := rows.Scan(&bucketStart, &sample.Calls, &sample.Errors, &sample.DurationSumNanos, &hist, &exemplars, &sample.KeptCount); err != nil {
			return store.REDSeries{}, err
		}
		sample.Tenant = tid
		sample.Service = service
		sample.Operation = operation
		sample.BucketStart = time.Unix(0, bucketStart).UTC()
		sample.Hist = decodeHist(hist)
		sample.ExemplarTraceIDs = decodeTraceIDs(exemplars)
		sample.Q = quantilesFromHist(sample.Hist)
		series.Samples = append(series.Samples, sample)
	}
	return series, rows.Err()
}

// quantilesFromHist interpolates p50/p95/p99/max from the fixed-boundary
// histogram at read time (DR-39 §39.1: "Q is interpolated from Hist at READ
// time"). Boundaries: 16 log-spaced buckets from 1ms to 32s.
func quantilesFromHist(h model.LatencyHist) model.Quantiles {
	var total uint64
	for _, c := range h {
		total += uint64(c)
	}
	if total == 0 {
		return model.Quantiles{}
	}
	bound := func(i int) uint64 {
		// boundary i (0-based) ~= 1ms * 2^i, capped at 32s (i=15)
		ms := uint64(1) << uint(i)
		if ms > 32000 {
			ms = 32000
		}
		return ms * uint64(time.Millisecond)
	}
	quantile := func(p float64) uint64 {
		target := uint64(p * float64(total))
		var cum uint64
		for i, c := range h {
			cum += uint64(c)
			if cum >= target {
				return bound(i)
			}
		}
		return bound(15)
	}
	return model.Quantiles{
		P50Nanos: quantile(0.50),
		P95Nanos: quantile(0.95),
		P99Nanos: quantile(0.99),
		MaxNanos: bound(15),
	}
}

func (s *Store) QueryEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT edge_id, caller, callee, protocol, bucket_start, resolution, calls, errors, duration_sum_nanos, hist,
			COALESCE(first_seen,0), COALESCE(last_seen,0)
		FROM topology_edge WHERE tenant_id=? AND bucket_start BETWEEN ? AND ? ORDER BY bucket_start DESC`,
		string(tid), w.Start.UnixNano(), w.End.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []topology.Edge
	for rows.Next() {
		var e topology.Edge
		var bucketStart int64
		var resolution int
		var hist []byte
		var firstSeen, lastSeen int64
		if err := rows.Scan(&e.ID, &e.Caller, &e.Callee, &e.Protocol, &bucketStart, &resolution,
			&e.Calls, &e.Errors, &e.DurationSumNanos, &hist, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		e.Tenant = tid
		e.BucketStart = time.Unix(0, bucketStart).UTC()
		e.Resolution = model.Resolution(resolution)
		e.Hist = decodeHist(hist)
		if firstSeen > 0 {
			e.FirstSeen = time.Unix(0, firstSeen).UTC()
		}
		if lastSeen > 0 {
			e.LastSeen = time.Unix(0, lastSeen).UTC()
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// WriteEdges implements topology.EdgeSink (DR-13).
func (s *Store) WriteEdges(ctx context.Context, tid model.TenantID, edges []topology.Edge) error {
	_, err := s.WriteBatch(ctx, tid, store.HotBatch{Edges: edges})
	return err
}

// LoadEdges implements topology.EdgeSource (DR-13).
func (s *Store) LoadEdges(ctx context.Context, tid model.TenantID, w model.Window) ([]topology.Edge, error) {
	return s.QueryEdges(ctx, tid, w)
}

func (s *Store) QueryErrorSignatures(ctx context.Context, tid model.TenantID, service string, w model.Window) ([]store.ErrorSignature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, signature, first_seen, last_seen, samples FROM error_signature
		WHERE tenant_id=? AND service=? AND last_seen BETWEEN ? AND ?`,
		string(tid), service, w.Start.UnixNano(), w.End.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ErrorSignature
	for rows.Next() {
		var es store.ErrorSignature
		var sig int64
		var first, last int64
		if err := rows.Scan(&es.ID, &sig, &first, &last, &es.Samples); err != nil {
			return nil, err
		}
		es.Tenant = tid
		es.Service = service
		es.Signature = uint64(sig)
		es.FirstSeen = time.Unix(0, first).UTC()
		es.LastSeen = time.Unix(0, last).UTC()
		out = append(out, es)
	}
	return out, rows.Err()
}

func (s *Store) ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window, n int) ([]store.Exemplar, error) {
	if n <= 0 {
		n = 4
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT trace_id, span_id, ts FROM exemplar
		WHERE tenant_id=? AND service=? AND operation=? AND ts BETWEEN ? AND ?
		ORDER BY ts DESC LIMIT ?`,
		string(tid), service, operation, w.Start.UnixNano(), w.End.UnixNano(), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Exemplar
	for rows.Next() {
		var traceID, spanID []byte
		var ts int64
		if err := rows.Scan(&traceID, &spanID, &ts); err != nil {
			return nil, err
		}
		out = append(out, store.Exemplar{Tenant: tid, TraceID: toTraceID(traceID), SpanID: toSpanID(spanID), Timestamp: time.Unix(0, ts).UTC()})
	}
	return out, rows.Err()
}

func (s *Store) PathSeen(ctx context.Context, tid model.TenantID, sig uint64, lookback time.Duration) (time.Time, bool, error) {
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT last_seen FROM path_signature WHERE tenant_id=? AND signature=?`,
		string(tid), int64(sig)).Scan(&last)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t := time.Unix(0, last).UTC()
	if lookback > 0 && time.Since(t) > lookback {
		return t, false, nil
	}
	return t, true, nil
}

func (s *Store) IngestedBytes(ctx context.Context, tid model.TenantID, w model.Window) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT SUM(bytes) FROM ingest_bytes_bucket WHERE tenant_id=? AND bucket_start BETWEEN ? AND ?`,
		string(tid), w.Start.UnixNano(), w.End.UnixNano()).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (s *Store) PendingColdRows(ctx context.Context, tid model.TenantID, olderThan time.Time) ([]store.PendingCold, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT trace_id, COALESCE(wal_segment,'') FROM trace
		WHERE tenant_id=? AND cold_state=0 AND start_unix_nano < ?`,
		string(tid), olderThan.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PendingCold
	for rows.Next() {
		var traceID []byte
		var seg string
		if err := rows.Scan(&traceID, &seg); err != nil {
			return nil, err
		}
		out = append(out, store.PendingCold{Tenant: tid, TraceID: toTraceID(traceID), WALSegment: seg})
	}
	return out, rows.Err()
}

// tableColumn maps a TableID to its table name and cutoff time column, for
// ExpireBefore's ranged deletes (DR-6 §6.3's retention-sweep row).
var tableColumn = map[store.TableID][2]string{
	store.TableTrace:       {"trace", "start_unix_nano"},
	store.TableSpan:        {"span", "start_unix_nano"},
	store.TableAttrDict:    {"attr_dict", "last_seen"},
	store.TableRedRollup:   {"red_rollup", "bucket_start"},
	store.TableTopologyEdg: {"topology_edge", "bucket_start"},
	store.TableSpanFTS:     {"span_text_fts", "ts"},
}

// attrIndexBucketSeconds is attr_index.bucket's width in seconds (DR-6
// §6.2: "start_unix_nano / 10e9, 10 s bucket").
const attrIndexBucketSeconds = 10

// ExpireBefore runs a per-table ranged DELETE in 5000-row batches (DR-6
// §6.3's retention-sweep row).
//
// w10 review fix: TableAttrIndex and TableSpanFTS previously no-op'd here,
// so DR-7's T0 tier ("Span rows + attr_index + FTS", 24h dev retention)
// only ever pruned `span` — attr_index and span_text_fts rows accumulated
// forever, silently breaking the published store_hot_index_ratio (DR-6
// §6.4) band over any run longer than the retention horizon. attr_index has
// no raw-nanosecond time column (only a 10s `bucket`), so it gets its own
// bucket-scale comparison instead of tableColumn's direct column compare.
func (s *Store) ExpireBefore(ctx context.Context, tid model.TenantID, tbl store.TableID, cutoff time.Time) (store.Expired, error) {
	if tbl == store.TableAttrIndex {
		return s.expireAttrIndexBefore(ctx, tid, cutoff)
	}
	tc, ok := tableColumn[tbl]
	if !ok {
		return store.Expired{}, fmt.Errorf("sqlite: unknown table %q for ExpireBefore", tbl)
	}
	table, col := tc[0], tc[1]
	var total int64
	for {
		var n int64
		err := s.write(func(tx *sql.Tx) error {
			q := fmt.Sprintf(`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE tenant_id=? AND %s < ? LIMIT 5000)`, table, table, col)
			res, err := tx.ExecContext(ctx, q, string(tid), cutoff.UnixNano())
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			// WITHOUT ROWID tables have no rowid; fall back to a plain ranged delete.
			var n2 int64
			err2 := s.write(func(tx *sql.Tx) error {
				q := fmt.Sprintf(`DELETE FROM %s WHERE tenant_id=? AND %s < ?`, table, col)
				res, err := tx.ExecContext(ctx, q, string(tid), cutoff.UnixNano())
				if err != nil {
					return err
				}
				n2, err = res.RowsAffected()
				return err
			})
			if err2 != nil {
				return store.Expired{Rows: total}, err2
			}
			total += n2
			break
		}
		total += n
		if n < 5000 {
			break
		}
	}
	return store.Expired{Rows: total}, nil
}

// expireAttrIndexBefore prunes attr_index (WITHOUT ROWID, so no rowid-batched
// delete is possible) using its 10s `bucket` column instead of a raw
// nanosecond timestamp, in 5000-row batches (w10 review fix — see
// ExpireBefore's doc comment).
func (s *Store) expireAttrIndexBefore(ctx context.Context, tid model.TenantID, cutoff time.Time) (store.Expired, error) {
	bucketCutoff := cutoff.UnixNano() / (attrIndexBucketSeconds * int64(time.Second))
	var total int64
	for {
		var n int64
		err := s.write(func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `
				DELETE FROM attr_index WHERE (tenant_id, key_id, value_hash, bucket, trace_id, span_id) IN (
					SELECT tenant_id, key_id, value_hash, bucket, trace_id, span_id FROM attr_index
					WHERE tenant_id=? AND bucket < ? LIMIT 5000)`,
				string(tid), bucketCutoff,
			)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return store.Expired{Rows: total}, err
		}
		total += n
		if n < 5000 {
			break
		}
	}
	return store.Expired{Rows: total}, nil
}

// CascadeRED aggregates rows from the `from` resolution's table into `to`'s
// table for buckets older than `before`, merging Hist by addition, then
// deletes the source rows (DR-6 §6.3's retention-sweep row; DR-39's
// mergeable-by-addition histogram).
func (s *Store) CascadeRED(ctx context.Context, tid model.TenantID, from, to model.Resolution, before time.Time) (int, error) {
	fromTbl, err := redTable(from)
	if err != nil {
		return 0, err
	}
	toTbl, err := redTable(to)
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT service, operation, bucket_start, calls, errors, duration_sum_nanos, hist, exemplar_trace_ids, kept_count
		FROM %s WHERE tenant_id=? AND bucket_start < ?`, fromTbl), string(tid), before.UnixNano())
	if err != nil {
		return 0, err
	}
	type srcRow struct {
		service, operation    string
		bucketStart           int64
		calls, errors, durSum uint64
		hist, exemplars       []byte
		keptCount             uint32
	}
	var src []srcRow
	for rows.Next() {
		var r srcRow
		if err := rows.Scan(&r.service, &r.operation, &r.bucketStart, &r.calls, &r.errors, &r.durSum, &r.hist, &r.exemplars, &r.keptCount); err != nil {
			rows.Close()
			return 0, err
		}
		src = append(src, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	err = s.write(func(tx *sql.Tx) error {
		for _, r := range src {
			h := decodeHist(r.hist)
			q := fmt.Sprintf(`
				INSERT INTO %s (tenant_id, service, operation, bucket_start, calls, errors, duration_sum_nanos, hist, exemplar_trace_ids, kept_count)
				VALUES (?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(tenant_id, service, operation, bucket_start) DO UPDATE SET
					calls=calls+excluded.calls, errors=errors+excluded.errors,
					duration_sum_nanos=duration_sum_nanos+excluded.duration_sum_nanos,
					kept_count=kept_count+excluded.kept_count`, toTbl)
			if _, err := tx.ExecContext(ctx, q, string(tid), r.service, r.operation, r.bucketStart, r.calls, r.errors,
				r.durSum, encodeHist(h), r.exemplars, r.keptCount); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id=? AND service=? AND operation=? AND bucket_start=?`, fromTbl),
				string(tid), r.service, r.operation, r.bucketStart); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func (s *Store) DiskUsage(ctx context.Context) (store.DiskReport, error) {
	fi, err := os.Stat(s.path)
	var hotBytes int64
	if err == nil {
		hotBytes = fi.Size()
	}
	return store.DiskReport{HotBytes: hotBytes}, nil
}
