package sqlite

// pragmas is the telemetry-writer PRAGMA set for traceiq.db (DR-6 §6.2's
// table row for `${data_dir}/hot/traceiq.db`, verbatim values).
var pragmas = []string{
	`PRAGMA journal_mode=WAL`,
	`PRAGMA synchronous=NORMAL`,
	`PRAGMA foreign_keys=ON`,
	`PRAGMA busy_timeout=5000`,
	`PRAGMA page_size=8192`,
	`PRAGMA cache_size=-262144`,
	`PRAGMA mmap_size=268435456`,
	`PRAGMA temp_store=MEMORY`,
	`PRAGMA wal_autocheckpoint=4000`,
}

// schema is the DR-6 §6.2 DDL for traceiq.db, reconstructed for the tables
// HotIndex's method set actually touches. Tables named in DR-6's table list
// but not read/written by any HotIndex method in this pass (topology_edge_meta,
// anomaly_baseline) are intentionally omitted — see docs/reports/w9-store.md.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS trace (
		tenant_id      TEXT    NOT NULL,
		trace_id       BLOB    NOT NULL,
		root_service   TEXT    NOT NULL,
		start_unix_nano INTEGER NOT NULL,
		duration_nanos INTEGER NOT NULL,
		error_count    INTEGER NOT NULL,
		path_signature INTEGER NOT NULL,
		keep_reason    INTEGER NOT NULL,
		cold_state     INTEGER NOT NULL DEFAULT 0,
		block_id       TEXT,
		row_group      INTEGER,
		row_offset     INTEGER,
		wal_segment    TEXT,
		PRIMARY KEY (tenant_id, trace_id)
	)`,
	`CREATE INDEX IF NOT EXISTS trace_by_time ON trace(tenant_id, start_unix_nano DESC)`,
	`CREATE INDEX IF NOT EXISTS trace_by_dur ON trace(tenant_id, root_service, duration_nanos DESC)`,
	`CREATE INDEX IF NOT EXISTS trace_by_err ON trace(tenant_id, start_unix_nano DESC) WHERE error_count > 0`,
	`CREATE INDEX IF NOT EXISTS trace_by_path ON trace(tenant_id, path_signature, start_unix_nano DESC)`,
	`CREATE INDEX IF NOT EXISTS trace_by_coldstate ON trace(tenant_id, cold_state, wal_segment) WHERE cold_state = 0`,

	`CREATE TABLE IF NOT EXISTS span (
		tenant_id       TEXT    NOT NULL,
		trace_id        BLOB    NOT NULL,
		span_id         BLOB    NOT NULL,
		service         TEXT    NOT NULL,
		operation       TEXT    NOT NULL,
		start_unix_nano INTEGER NOT NULL,
		duration_nanos  INTEGER NOT NULL,
		error_sig_id    TEXT,
		PRIMARY KEY (tenant_id, trace_id, span_id)
	)`,
	`CREATE INDEX IF NOT EXISTS span_by_svc_op_time ON span(tenant_id, service, operation, start_unix_nano DESC)`,
	`CREATE INDEX IF NOT EXISTS span_by_errsig ON span(tenant_id, error_sig_id, start_unix_nano DESC) WHERE error_sig_id IS NOT NULL`,

	// attr_index / attr_dict: DR-6 §6.2's DDL, verbatim column list.
	`CREATE TABLE IF NOT EXISTS attr_index (
		tenant_id  TEXT    NOT NULL,
		key_id     INTEGER NOT NULL,
		value_hash INTEGER NOT NULL,
		bucket     INTEGER NOT NULL,
		trace_id   BLOB    NOT NULL,
		span_id    BLOB    NOT NULL,
		PRIMARY KEY (tenant_id, key_id, value_hash, bucket, trace_id, span_id)
	) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS attr_dict (
		tenant_id  TEXT    NOT NULL,
		key_id     INTEGER NOT NULL,
		value_hash INTEGER NOT NULL,
		value      TEXT    NOT NULL,
		first_seen INTEGER NOT NULL,
		last_seen  INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, key_id, value_hash)
	) WITHOUT ROWID`,

	// span_text_fts: plain table + LIKE search, not FTS5 (see doc note in
	// sqlite.go's ftsEnabled comment / docs/reports/w9-store.md deviation).
	// ts (w10 review fix) carries the span's start_unix_nano so ExpireBefore
	// can honor T0's 24h dev retention for this table, matching span/attr_index.
	`CREATE TABLE IF NOT EXISTS span_text_fts (
		tenant_id TEXT NOT NULL,
		trace_id  BLOB NOT NULL,
		span_id   BLOB NOT NULL,
		text      TEXT NOT NULL,
		ts        INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS span_text_fts_by_tenant ON span_text_fts(tenant_id)`,
	`CREATE INDEX IF NOT EXISTS span_text_fts_by_ts ON span_text_fts(tenant_id, ts)`,

	`CREATE TABLE IF NOT EXISTS red_rollup (
		tenant_id  TEXT NOT NULL, service TEXT NOT NULL, operation TEXT NOT NULL,
		bucket_start INTEGER NOT NULL, calls INTEGER NOT NULL, errors INTEGER NOT NULL,
		duration_sum_nanos INTEGER NOT NULL, hist BLOB NOT NULL,
		exemplar_trace_ids BLOB, kept_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tenant_id, service, operation, bucket_start)
	) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS red_rollup_5m (
		tenant_id  TEXT NOT NULL, service TEXT NOT NULL, operation TEXT NOT NULL,
		bucket_start INTEGER NOT NULL, calls INTEGER NOT NULL, errors INTEGER NOT NULL,
		duration_sum_nanos INTEGER NOT NULL, hist BLOB NOT NULL,
		exemplar_trace_ids BLOB, kept_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tenant_id, service, operation, bucket_start)
	) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS red_rollup_1h (
		tenant_id  TEXT NOT NULL, service TEXT NOT NULL, operation TEXT NOT NULL,
		bucket_start INTEGER NOT NULL, calls INTEGER NOT NULL, errors INTEGER NOT NULL,
		duration_sum_nanos INTEGER NOT NULL, hist BLOB NOT NULL,
		exemplar_trace_ids BLOB, kept_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (tenant_id, service, operation, bucket_start)
	) WITHOUT ROWID`,

	`CREATE TABLE IF NOT EXISTS topology_edge (
		tenant_id TEXT NOT NULL, edge_id TEXT NOT NULL, caller TEXT NOT NULL, callee TEXT NOT NULL,
		protocol TEXT NOT NULL, bucket_start INTEGER NOT NULL, resolution INTEGER NOT NULL,
		calls INTEGER NOT NULL, errors INTEGER NOT NULL, duration_sum_nanos INTEGER NOT NULL,
		hist BLOB NOT NULL, first_seen INTEGER, last_seen INTEGER,
		PRIMARY KEY (tenant_id, edge_id, bucket_start)
	) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS edge_by_time ON topology_edge(tenant_id, bucket_start DESC)`,

	`CREATE TABLE IF NOT EXISTS topology_edge_op (
		tenant_id TEXT NOT NULL, edge_id TEXT NOT NULL, callee_operation TEXT NOT NULL,
		bucket_start INTEGER NOT NULL, calls INTEGER NOT NULL, errors INTEGER NOT NULL, p99_nanos INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, edge_id, callee_operation, bucket_start)
	) WITHOUT ROWID`,

	`CREATE TABLE IF NOT EXISTS error_signature (
		tenant_id TEXT NOT NULL, id TEXT NOT NULL, service TEXT NOT NULL, signature INTEGER NOT NULL,
		first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, samples INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, id)
	)`,

	`CREATE TABLE IF NOT EXISTS path_signature (
		tenant_id TEXT NOT NULL, signature INTEGER NOT NULL,
		first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, signature)
	) WITHOUT ROWID`,

	`CREATE TABLE IF NOT EXISTS resource (
		tenant_id TEXT NOT NULL, id TEXT NOT NULL, attrs TEXT,
		PRIMARY KEY (tenant_id, id)
	)`,

	`CREATE TABLE IF NOT EXISTS exemplar (
		tenant_id TEXT NOT NULL, trace_id BLOB NOT NULL, span_id BLOB NOT NULL,
		service TEXT, operation TEXT, ts INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, trace_id, span_id)
	)`,

	// block_manifest: DR-6/DR-7, table names only cited by F03 (owned by 01 §5.1).
	`CREATE TABLE IF NOT EXISTS block_manifest (
		tenant_id TEXT NOT NULL, block_id TEXT NOT NULL, tier INTEGER NOT NULL,
		hour_bucket INTEGER NOT NULL, spans_path TEXT, traces_path TEXT, meta_path TEXT,
		checksum TEXT, sealed_at INTEGER, row_count INTEGER,
		PRIMARY KEY (tenant_id, block_id)
	)`,

	// ingest_bytes_bucket: not in DR-6's table list; added so IngestedBytes
	// (DR-12's cost-controller RATE input) has something to sum over a
	// window without re-deriving raw ingest size from row counts on every
	// call. Flagged as a documented addition in docs/reports/w9-store.md.
	`CREATE TABLE IF NOT EXISTS ingest_bytes_bucket (
		tenant_id TEXT NOT NULL, bucket_start INTEGER NOT NULL, bytes INTEGER NOT NULL,
		PRIMARY KEY (tenant_id, bucket_start)
	) WITHOUT ROWID`,
}
