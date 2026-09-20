// Package config holds the Config type — the one Go structure for every
// key TraceIQ's YAML configuration exposes — plus the loader seam. DR-0
// assigns "every config key and default" to 01 §7 as sole owner ("feature
// docs cite key paths, never values"); this package is that owner's Go
// shape, amended wherever a DR in 06-decision-register.md changed a
// default, added a key or replaced a block wholesale.
//
// The register never prints one single `type Config struct` — it gives
// dozens of YAML fragments, one or more per DR (DR-1 toolchain env,
// DR-3 tenancy, DR-6 store.hot/cold/retention, DR-9 sampler.assembly/wal,
// DR-10 sampler.policy/baseline, DR-11 sampler.interest, DR-12
// store.cost, DR-14 anomaly.*, DR-17 rca.budget, DR-19 memory.*, DR-20
// correlate.*, DR-21 alerting.*, DR-23 remediate.*, DR-25/26/27 auth.*,
// DR-26 server/api/ingest.tls/limits, DR-28 ingest.queue, DR-32
// store.*.driver/cluster.bus.driver, DR-33 ops.*, DR-34 rca.llm.*, DR-35
// nl.*, DR-36 eval.*). Config assembles all of them into nested structs
// named after the top-level YAML key, field names in Go CamelCase over the
// YAML snake_case path, so `store.hot.max_kept_spans_per_sec` becomes
// `Config.Store.Hot.MaxKeptSpansPerSec`. Every field's doc comment cites the
// owning DR (or 01 §7 where a DR did not amend it) and states the default
// as prose, since defaults are data the loader assigns, not part of the
// type declaration itself (no business logic per this scaffold's scope).
//
// Feature IDs: cross-cutting — every F0x feature and X-SEC/X-OPS read
// their configuration through this package.
//
// Allowed imports (DR-2's adjacency table): internal/model only.
package config
