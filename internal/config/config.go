package config

import (
	"time"

	"traceiq/internal/model"
)

// Config is the root of TraceIQ's configuration tree (01 §7, amended by
// every DR listed in doc.go). Precedence: defaults -> traceiq.yaml ->
// environment (TRACEIQ_ + "__"-separated path) -> command-line flags.
// Unknown keys are a startup error, not a warning (01 §7).
type Config struct {
	Server    ServerConfig
	Tenancy   TenancyConfig
	Ingest    IngestConfig
	Sampler   SamplerConfig
	Store     StoreConfig
	Topology  TopologyConfig
	Anomaly   AnomalyConfig
	RCA       RCAConfig
	Correlate CorrelateConfig
	Memory    MemoryConfig
	Remediate RemediateConfig
	Alerting  AlertingConfig
	NL        NLConfig
	API       APIConfig
	Auth      AuthConfig
	Eval      EvalConfig
	SelfObs   SelfObsConfig
	Cluster   ClusterConfig
	Ops       OpsConfig
}

// ServerConfig is DR-26 §26.1/§26.2: two orthogonal axes, defaults flipped
// to loopback.
type ServerConfig struct {
	Mode          string        // single | gateway | sampler | brain | api (DR-26 §26.1)
	Profile       string        // dev | prod, safety gating only (DR-26 §26.1); default "dev"
	NodeID        string        // default: hostname
	DataDir       string        // default "./traceiq-data"
	LogLevel      string        // debug | info | warn | error
	LogFormat     string        // json | text
	ShutdownGrace time.Duration // default 45s (DR-33 §33.2)
}

// TenancyConfig is 01 §7's `tenancy` block, amended by DR-5 (header removed)
// and DR-3 (FromDevDefault legality).
type TenancyConfig struct {
	Enabled       bool
	DefaultTenant string // "default"
	// Header is DELETED by DR-5 §5.2: "tenancy.header is removed from 01 §7."
	// Field intentionally absent.
}

// TLSConfig is the per-listener TLS block repeated across api/ingest/selfobs
// (DR-26 §26.2, §26.5).
type TLSConfig struct {
	Enabled      bool
	CertFile     string
	KeyFile      string
	ClientCAFile string
	MinVersion   string // default "1.3" (DR-26 §26.5); "1.2" permitted only by explicit per-listener override
}

// IngestReceiverConfig is one OTLP/Jaeger/Zipkin receiver's listener block.
type IngestReceiverConfig struct {
	Enabled  bool
	Endpoint string // e.g. "127.0.0.1:4317" (DR-26 §26.2 flips every default off 0.0.0.0)
	TLS      TLSConfig
}

// IngestAuthConfig is DR-26 §26.2, new: ingest auth was not expressible at all before.
type IngestAuthConfig struct {
	Mode         string // none | token | mtls
	TokensFile   string
	ClientCAFile string
}

// IngestLimitsConfig is DR-26 §26.4's single limits table for the ingest path.
type IngestLimitsConfig struct {
	MaxSpanBytes           int64 // 512 KiB, post-normalization
	MaxAttributesPerSpan   int
	MaxAttributeValueBytes int64  // 4 KiB (DR-26 §26.4; was 8 KiB in 01 §8.4)
	OversizeAttr           string // truncate | reject, per tenant (DR-26 §26.4)
	MaxAttributeKeyBytes   int64  // 256 B
	MaxEventsPerSpan       int
	MaxLinksPerSpan        int
	MaxSpansPerBatch       int // 10000
	MaxSpansPerTrace       int
	MaxRequestBytes        int64 // 4 MiB ingest body limit (DR-26 §26.4)
	PerTenantBytesPerSec   int64
	ClockSkewFuture        time.Duration // spans further in the future are clamped and counted
	MaxSpanAge             time.Duration // older spans rejected
}

// IngestQueueConfig is DR-28 §28.1, verbatim.
type IngestQueueConfig struct {
	Capacity       int           // BATCHES; 1024 (01 §3.1's 100000 is DELETED)
	MaxBytes       int64         // 268435456 (256 MiB) — the HARD byte bound, and the binding one
	EnqueueTimeout time.Duration // 100ms
	OverflowPolicy string        // shed | block_up_to_timeout ("block" unbounded is DELETED)
}

// IngestConfig is 01 §7's `ingest` block, amended by DR-9 (RED-before-discard
// invariant retained), DR-26 (TLS/auth), DR-28 (queue/allocation).
type IngestConfig struct {
	OTLPGRPC      IngestReceiverConfig
	OTLPHTTP      IngestReceiverConfig
	JaegerGRPC    IngestReceiverConfig
	ZipkinHTTP    IngestReceiverConfig
	DecodeWorkers int // 0 = GOMAXPROCS
	Queue         IngestQueueConfig
	Limits        IngestLimitsConfig
	Auth          IngestAuthConfig
}

// SamplerAssemblyConfig is DR-9, verbatim.
type SamplerAssemblyConfig struct {
	IdleTimeout              time.Duration // 8s
	HardTimeout              time.Duration // 30s
	MaxOpenTracesPerShard    int           // 50000
	MemoryHighWatermarkBytes int64         // 536870912 (512 MiB GLOBAL across all shards)
	WheelTick                time.Duration // 250ms
}

// SamplerWALConfig is DR-9's spill WAL, verbatim.
type SamplerWALConfig struct {
	Enabled         bool
	Dir             string
	FlushInterval   time.Duration // 1s
	MaxSegmentBytes int64         // 67108864 (64 MiB)
}

// SamplerPolicyConfig is DR-10, verbatim.
type SamplerPolicyConfig struct {
	KeepErrors                  bool
	SlowQuantile                float64       // closed enum {0.95, 0.99} (DR-39 §39.2); default 0.99
	SlowMinDuration             time.Duration // MANDATORY floor; default 250ms
	RarePathLookbackDays        int           // 7
	RarePathKeepsPerMin         int           // NEW per-tenant token bucket; 60
	MaxPathSignatureCardinality int           // NEW; 200000
	BaselineMinSamples          int           // 200
	HealthySampleRate           float64       // 0.01
	FloorTracesPerMinPerService int           // 6
	MaxKeepRate                 float64       // HARD CAP; 0.25
}

// SamplerBaselineConfig is DR-10 §"baselines are never read from SQLite".
type SamplerBaselineConfig struct {
	RefreshInterval time.Duration // 60s
}

// SamplerInterestConfig is DR-11 §11's config block, verbatim.
type SamplerInterestConfig struct {
	Enabled                 bool
	MaxPredicates           int           // per tenant, both scopes combined; 32
	MaxRecurrencePredicates int           // subset of the above; 8
	MaxTraceIDsPerPredicate int           // 1024
	ScopeTTL                time.Duration // phase A; 30m
	RecurrenceTTL           time.Duration // phase B; 24h
	NarrowAtKeepRate        float64       // == sampler.policy.max_keep_rate
	Persist                 bool          // control.db sampler_interest
}

// SamplerRedConfig carries DR-8's RED-dedupe window (§"RED is never
// double-counted") — reconstructed since the register describes it in prose
// ("sampler.red.dedupe_window (200 000)") rather than a YAML block.
type SamplerRedConfig struct {
	DedupeWindow int // 200000 entries, TTL 2x assembly.hard_timeout
}

// SamplerConfig is 01 §7's `sampler` block, amended wholesale by DR-9/10/11.
type SamplerConfig struct {
	Shards         int // 0 = GOMAXPROCS
	ShardQueueSize int // 4096
	Assembly       SamplerAssemblyConfig
	WAL            SamplerWALConfig
	Policy         SamplerPolicyConfig
	Baseline       SamplerBaselineConfig
	Interest       SamplerInterestConfig
	Red            SamplerRedConfig
}

// StoreHotSQLiteConfig is DR-6 §6.2's traceiq.db PRAGMAs plus 01 §7's
// sqlite driver block.
type StoreHotSQLiteConfig struct {
	Path               string
	BusyTimeout        time.Duration
	CacheSizeKB        int64
	MaxReadConns       int
	BatchTraces        int
	BatchInterval      time.Duration // 250ms
	CheckpointInterval time.Duration // 60s
	WALAutocheckpoint  int           // 4000
}

// StoreHotClickHouseConfig is 01 §7's clickhouse driver block ([P2],
// DR-6 §6.4: required above store.hot.max_kept_spans_per_sec).
type StoreHotClickHouseConfig struct {
	DSN          string
	Database     string
	MaxOpenConns int
}

// StoreHotConfig is DR-6 §6.2/§6.4, verbatim: the closed 8-key allowlist,
// the two-file/two-writer model's dev tuning, and the sqlite driver cap.
type StoreHotConfig struct {
	Driver                   string   // sqlite | clickhouse
	IndexedAttributeKeys     []string // exactly 8, closed allowlist (DR-6 §6.2)
	IndexAttrsForKinds       []int    // [2, 5] Server, Consumer — plus every span with IsError()
	MaxIndexedAttrValueBytes int
	MaxPathSignatures        int // LRU evict, counter on evict; 200000
	MaxKeptSpansPerSec       int // 1200 (dev, sqlite); startup error if exceeded (DR-6 §6.4)
	SQLite                   StoreHotSQLiteConfig
	ClickHouse               StoreHotClickHouseConfig
}

// StoreColdParquetConfig is 01 §7's parquet driver block, amended by DR-9
// (max_open_blocks 4 -> 2, row_group_bytes -> 32 MiB) and DR-7 (WAL dir,
// retain).
type StoreColdParquetConfig struct {
	Path             string
	ObjectStoreURL   string // "" = local disk; s3://... | gs://... | az://...
	Compression      string // zstd
	CompressionLevel int
	RowGroupBytes    int64         // 33554432 (32 MiB) on the dev profile (DR-9)
	TargetBlockBytes int64         // 536870912 (512 MiB)
	FlushInterval    time.Duration // 5m
	MaxOpenBlocks    int           // 2 on the dev profile (DR-9; was 4)
	WALDir           string
	WALFsyncInterval time.Duration // 250ms (DR-7 §7)
	WALFsyncBytes    int64         // 4 MiB (DR-7 §7)
	WALRetain        time.Duration // 10m (DR-7 §7)
}

// StoreColdCompactionConfig is DR-7's compaction block, verbatim.
type StoreColdCompactionConfig struct {
	TombstoneRatio  float64       // 0.02
	MaxTombstoneAge time.Duration // 24h
	MaxBytesPerHour int64
	OrphanGrace     time.Duration // 2h
	ReaperInterval  time.Duration
}

// StoreColdConfig is 01 §7's `store.cold` block, amended by DR-7.
type StoreColdConfig struct {
	Driver     string // parquet_local | parquet_s3 (DR-32 §32.3)
	Parquet    StoreColdParquetConfig
	Compaction StoreColdCompactionConfig
}

// StoreRetentionConfig is DR-6 §6.4's retention-tier table (T0..T6),
// verbatim key list.
type StoreRetentionConfig struct {
	HotSpanRows        time.Duration // T0; 24h dev / 72h prod
	HotTraceRows       time.Duration // T0b, NEW; 7d dev / 30d prod
	HotRollups         time.Duration // T1; 30d
	ColdAnomalous      time.Duration // T2; 30d
	ColdSampled        time.Duration // T3; 7d
	Red10s             time.Duration // T4, NEW; 48h
	Red5m              time.Duration // T4, NEW; 14d
	RedRollups         time.Duration // T4; 400d
	Topology10s        time.Duration // T4b, NEW; 6h
	Topology5m         time.Duration // T4b, NEW; 7d
	Topology1h         time.Duration // T4b, NEW; 30d
	Investigations     time.Duration // T5; 400d
	Audit              time.Duration // T6; 2555d
	CompactionInterval time.Duration
}

// StoreCostConfig is DR-12's controller block, verbatim.
type StoreCostConfig struct {
	SignalInterval        time.Duration // 60s (was 5m)
	MeasureWindow         time.Duration // 5m, a RATE
	Deadband              float64       // 0.15
	MaxStep               float64       // 0.5
	RecoveryRamp          float64       // 1.25
	AdaptiveFloorMin      float64       // 0.0001
	AdaptiveFloorMax      float64       // 1.0
	SettlingTarget        time.Duration // 30m, published gate
	ByteBudgetPerTenantGB int64         // 0 = unbounded; survives only when tenancy.enabled: false
}

// StoreBudgetConfig is DR-6 §6.4/DR-12, verbatim.
type StoreBudgetConfig struct {
	MaxDiskBytes  int64   // 26843545600 (25 GiB) dev default (DR-6 §6.4)
	HighWatermark float64 // 0.85
	ActionOnFull  string  // shed_sampled | stop_ingest
}

// StoreConfig is 01 §7's `store` block, amended wholesale by DR-6/7/12/32.
type StoreConfig struct {
	Hot       StoreHotConfig
	Cold      StoreColdConfig
	Retention StoreRetentionConfig
	Cost      StoreCostConfig
	Budget    StoreBudgetConfig
}

// TopologyConfig is 01 §7's `topology` block, amended by DR-13 (max_edges
// 200000 -> 20000, callee_operation top-N) and DR-38 (Envoy access logs).
type TopologyConfig struct {
	Bucket                 time.Duration
	Window                 time.Duration
	MaxEdges               int           // 20000 (DR-13; was 200000)
	MaxOperationsPerEdge   int           // 20, topology_edge_op top-N (DR-13)
	NewEdgeMinCalls        int           // 5
	VanishedAfter          time.Duration // 30m
	EnvoyAccessLogsEnabled bool          // DR-38 §38.1, new FR-F04-10; default false
}

// AnomalyBaselineConfig is DR-14 §14.9, verbatim.
type AnomalyBaselineConfig struct {
	Estimator              string // p2 | tdigest (tdigest is global-slot only)
	TDigestCompression     int    // 100
	EWMAAlpha              float64
	Seasonal               bool
	SeasonSlots            int           // 31 (24 hour-of-day + 7 weekday); was season_buckets: 168
	WarmupSamples          int           // 200, global slot
	WarmupSamplesPerBucket int           // 30, NEW
	ColdStartMultiplier    float64       // 1.5, NEW
	MaxColdStart           time.Duration // 24h, NEW
	MaxKeys                int           // 10000, per tenant, NEW
	MaxKeysGlobal          int           // 20000, process-wide, NEW
	MaxErrorSignatures     int           // 20000, NEW
	CheckpointInterval     time.Duration // 60s, NEW
	CheckpointMaxRows      int           // 2000, NEW
	MinRPS                 float64
}

// AnomalyThresholdsConfig is DR-14 §14.9, verbatim.
type AnomalyThresholdsConfig struct {
	LatencyRatio              float64       // 1.5, NEW, replaces latency_z
	LatencyAbsDelta           time.Duration // 50ms, NEW
	ErrorRateDelta            float64       // 0.05
	ErrorBurstRatio           float64       // 3.0, NEW, replaces error_burst_z
	ThroughputDropRatio       float64       // 0.5
	NewErrorSignatureLookback time.Duration // 7d, NEW
	NewErrorSigMinCalls       int           // 5, NEW
	MinCalls                  int           // 20, NEW
	MinEventScore             float64       // 0.55
}

// AnomalyGroupingConfig is DR-14 §14.9, verbatim.
type AnomalyGroupingConfig struct {
	Window               time.Duration
	TopologyHops         int
	MaxEventsPerIncident int
	MaxOpenIncidents     int           // 200, NEW
	DedupeTTL            time.Duration // 30m
	NeighborCacheEntries int           // 4096, NEW
	NeighborCacheTTL     time.Duration // 60s, NEW
}

// AnomalyDeployMarkersConfig is DR-14 §14.6/§14.9, verbatim.
type AnomalyDeployMarkersConfig struct {
	Enabled           bool
	CorrelationWindow time.Duration // 30m
	Settle            time.Duration // 2m, NEW
}

// AnomalyConfig is 01 §7's `anomaly` block, amended wholesale by DR-14.
type AnomalyConfig struct {
	Detectors     []string
	EvalInterval  time.Duration // 30s
	EvalWindow    time.Duration // 90s, was 5m
	DebounceTicks int           // 2, was 3
	Baseline      AnomalyBaselineConfig
	Thresholds    AnomalyThresholdsConfig
	Grouping      AnomalyGroupingConfig
	DeployMarkers AnomalyDeployMarkersConfig
}

// RCABudgetConfig is DR-17 §17.5, verbatim.
type RCABudgetConfig struct {
	WallClock                      time.Duration // 5m
	MaxStepWallClock               time.Duration // 45s, NEW
	MaxSteps                       int           // 24
	MaxToolCalls                   int           // 40
	MaxTokensIn                    int           // 120000, UNCACHED input tokens
	MaxCachedTokensIn              int           // 600000, NEW
	MaxTokensOut                   int           // 64000, was 16000
	MaxCostMicroUSD                int64         // 500000
	MaxCostMicroUSDPerTenantPerDay int64         // 20000000, NEW
	MaxCostMicroUSDGlobalPerDay    int64         // 100000000, NEW
	MaxRowsPerToolCall             int           // 500
	MaxEvidenceBytes               int           // 8192, was 32768; this is D in DR-17 §17.1
	VerbatimDigestWindow           int           // 3, NEW — K
	CompactedVerdictTokens         int           // 60, NEW — V
}

// RCAToolsConfig is DR-17 §17.5, verbatim.
type RCAToolsConfig struct {
	MaxWindow time.Duration // 6h, NEW (DR-16 §16.3)
}

// RCALLMPricingConfig is DR-34 §34.1, verbatim: micro-USD per million tokens.
type RCALLMPricingConfig struct {
	Input      int64
	CachedRead int64
	CacheWrite int64
	Output     int64
}

// RCALLMConfig is DR-34 §34.1, verbatim.
type RCALLMConfig struct {
	Provider        string // "anthropic", the only v1 value
	BaseURL         string
	Model           string // "claude-opus-5" (DR-34; was claude-sonnet-4-5)
	Effort          string // low | medium | high (replaces temperature)
	Thinking        string // "adaptive"
	APIKeyEnv       string
	APIKeyFile      string
	Timeout         time.Duration // 25s, was 60s
	MaxRetries      int           // 2, was 3
	MaxOutputTokens int           // 1600, was 4096
	PromptCache     bool
	Pricing         RCALLMPricingConfig
}

// RCAConfig is 01 §7's `rca` block, amended by DR-17 (budget) and DR-34 (llm).
type RCAConfig struct {
	Enabled                     bool
	AutoInvestigate             bool
	MinIncidentScore            float64
	MaxConcurrentInvestigations int     // 2 (DR-17 §17.3; F06's ">= 20" is deleted)
	Reasoner                    string  // auto | llm | rules
	ConfidenceThreshold         float64 // 0.75 (DR-17 §17.5; was 0.8)
	Budget                      RCABudgetConfig
	Tools                       RCAToolsConfig
	LLM                         RCALLMConfig
}

// CorrelateBackendConfig is DR-20 §20.2's per-adapter (logs/metrics) block.
type CorrelateBackendConfig struct {
	Driver       string
	URL          string
	Path         string // driver: file
	Timeout      time.Duration
	MaxLines     int           // logs only
	Step         time.Duration // metrics only
	TenantMode   string        // NEW: none | header | label | per_tenant_credential (DR-20 §20.2)
	TenantHeader string
	TenantLabel  string
}

// CorrelateCacheConfig is DR-20 §20.4, verbatim.
type CorrelateCacheConfig struct {
	Enabled    bool
	MaxEntries int
	TTL        time.Duration // 60s
}

// CorrelateConfig is 01 §7's `correlate` block, amended wholesale by DR-20.
type CorrelateConfig struct {
	Logs                 CorrelateBackendConfig
	Metrics              CorrelateBackendConfig
	EgressAllowlist      []string
	AllowPrivateNetworks bool          // DEFAULT FLIPPED to false (DR-20 §20.2; was true)
	MaxInflight          int           // 4, per tenant, NEW
	MaxInflightGlobal    int           // 16, NEW
	BreakerFailures      int           // 5, NEW
	BreakerOpen          time.Duration // 30s, NEW
	Cache                CorrelateCacheConfig
}

// MemoryEmbeddingsConfig is DR-19 §19.7, verbatim.
type MemoryEmbeddingsConfig struct {
	Driver string // lexical | hashed | anthropic | none (default CHANGED to lexical)
	Dim    int
}

// MemoryRetrievalConfig is DR-19 §19.7, verbatim.
type MemoryRetrievalConfig struct {
	TopK                int
	MinSimilarity       float64
	MaxCandidates       int           // 500, NEW — the published scan bound
	Hybrid              bool          // NEW — lexical + vector
	WeightDecayHalfLife time.Duration // 90d
}

// MemoryIndexConfig is DR-19 §19.7, verbatim.
type MemoryIndexConfig struct {
	MaxVocabulary int // 50000, NEW
}

// MemoryConsolidationConfig is DR-19 §19.7, verbatim.
type MemoryConsolidationConfig struct {
	Interval            time.Duration
	HourOfDay           int
	PruneAfterDays      int           // 400, was 365; aligns with T5
	DedupeThreshold     float64       // 0.6, NEW — defines "fingerprint family"
	MinhashPermutations int           // 128, NEW
	MinhashBands        int           // 32, NEW
	MaxDuration         time.Duration // 15m, NEW
}

// MemoryConfig is 01 §7's `memory` block, amended wholesale by DR-19.
type MemoryConfig struct {
	Enabled       bool
	Embeddings    MemoryEmbeddingsConfig
	Retrieval     MemoryRetrievalConfig
	Index         MemoryIndexConfig
	Consolidation MemoryConsolidationConfig
}

// RemediateConfig is 01 §7's `remediate` block, amended wholesale by DR-23.
type RemediateConfig struct {
	Enabled                       bool
	Mode                          string   // dryrun | execute
	Executor                      string   // dryrun | kubectl
	Allowlist                     []string // subset of the five model.ActionType values
	TargetAllowlist               []string
	NamespaceAllowlist            []string // NEW; also per tenant
	RequireApproval               bool     // cannot be false while mode: execute
	ApproverRoles                 []string
	AutoExecuteOnApprove          bool // DEFAULT FLIPPED to false (DR-23 §23.5); forbidden at RiskTier 3
	ActionBudgetPerIncident       int
	MaxBudgetOverridesPerIncident int           // 1, NEW
	ApprovalTTL                   time.Duration // 15m, was 30m
	ProposalTTL                   time.Duration // 60m, NEW
	VerifyWindow                  time.Duration // 10m
	AutoRollback                  bool          // NEW
	ExecTimeout                   time.Duration // 30s, NEW
	Kubeconfig                    string
	SnapshotDir                   string
}

// AlertingDeadmanConfig is DR-21 §21.4, verbatim.
type AlertingDeadmanConfig struct {
	Enabled  bool
	Interval time.Duration // 10m
	Target   string
}

// AlertingSinkConfig is a generic {enabled, ...} sink block (pagerduty /
// opsgenie / slack), per 01 §7 and DR-21 §21.4.
type AlertingSinkConfig struct {
	Enabled       bool
	RoutingKeyEnv string // pagerduty
	APIKeyEnv     string // opsgenie
	Channel       string // slack
}

// AlertingConfig is 01 §7's `alerting` block, amended wholesale by DR-21.
type AlertingConfig struct {
	Gate                string                    // "investigation"; "immediate" is DELETED, exit 2 at startup
	MaxWaitForRCA       time.Duration             // 5m, NEW — the P2 hard ceiling
	MinSeverity         string                    // applies to P1/P2 only; P3 ignores it
	CriticalSLOBreaches []model.CriticalSLOBreach // NEW — P3 (DR-21 §21.2)
	Deadman             AlertingDeadmanConfig
	PagerDuty           AlertingSinkConfig
	Opsgenie            AlertingSinkConfig
	Slack               AlertingSinkConfig
	Routes              []string // [{match: {...}, target: ...}]; kept as opaque strings in this skeleton
}

// NLChatConfig is a generic {enabled, ...} chat platform block (slack/teams).
type NLChatConfig struct {
	Enabled          bool
	SigningSecretEnv string
	BotTokenEnv      string
	SecretEnv        string // teams
}

// NLConfig is 01 §7's `nl` block, amended by DR-35.
type NLConfig struct {
	Enabled             bool
	Interpreter         string // auto | llm | rules (auto -> rules without a key)
	ContextTurns        int    // 5, NEW
	MaxQuestionBytes    int
	AnswerEvidenceLimit int
	Slack               NLChatConfig
	Teams               NLChatConfig
}

// APIConfig is 01 §7's `api` block, amended by DR-26 (endpoint default)
// and DR-29 (mcp_path).
type APIConfig struct {
	Endpoint       string // "127.0.0.1:8443" (DR-26 §26.2; was 0.0.0.0:8080)
	UI             bool
	MCP            bool
	MCPPath        string // "/v1/mcp" (DR-29 §29.2)
	CORSOrigins    []string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxBodyBytes   int64 // 1 MiB (DR-26 §26.4)
	RateLimitRPS   int   // 100 (DR-26 §26.4)
	RateLimitBurst int   // 200
}

// AuthOIDCConfig is 01 §7's oidc block, amended by DR-25 §25.3.
type AuthOIDCConfig struct {
	Issuer      string
	Audience    string
	RoleClaim   string
	TenantClaim string
}

// AuthAuditConfig is DR-27 §27.3, verbatim.
type AuthAuditConfig struct {
	Enabled            bool
	Path               string // NDJSON mirror
	HashChain          bool
	AnchorInterval     time.Duration // 5m, NEW
	AnchorSink         string        // NEW: file | object_lock | syslog
	AnchorDir          string        // dev
	AnchorObjectPrefix string        // prod: an object-lock / WORM bucket prefix
	AnchorKeyRef       string
	AnchorSyslogAddr   string
	RetentionDays      int // 2555
}

// AuthRateLimitConfig backs auth.RateLimiter (DR-25 §25.5).
type AuthRateLimitConfig struct {
	MaxKeys int64 // 65536
}

// AuthConfig is 01 §7's `auth` block, amended by DR-25/26/27.
type AuthConfig struct {
	Mode                   string // none | token | mtls | oidc
	TokensFile             string
	BootstrapAdminTokenEnv string
	TokenHash              string // argon2id
	TLS                    TLSConfig
	OIDC                   AuthOIDCConfig
	Audit                  AuthAuditConfig
	RateLimit              AuthRateLimitConfig
}

// EvalLiveConfig is DR-36 §36.7, verbatim.
type EvalLiveConfig struct {
	Enabled          bool
	Confirm          string // must equal "i-know-this-mutates-a-cluster"
	Kubeconfig       string // distinct from remediate.kubeconfig
	ContextAllowlist []string
}

// EvalConfig is 01 §7's `eval` block, amended wholesale by DR-36.
type EvalConfig struct {
	Enabled            bool
	ScenariosDir       string
	OutputDir          string
	Mode               string // offline | live
	Parallelism        int    // 4, was parallel: 1
	Isolation          string // process | shared
	Seed               int64
	Reasoner           string // "rules" in CI; "llm" for published accuracy runs
	RegressionBaseline string
	FailOnRegression   bool
	Live               EvalLiveConfig
}

// SelfObsConfig is 01 §7's `selfobs` block, amended by DR-26 (metrics
// endpoint default) and DR-31 (allowlisted for direct time reads).
type SelfObsConfig struct {
	MetricsEndpoint string // "127.0.0.1:9464" (DR-26 §26.2; was 0.0.0.0:9464)
	OTLPExportURL   string
	Pprof           bool
	HealthPath      string
	ReadyPath       string
}

// ClusterBusConfig is DR-32 §32.3, verbatim: the in-process ring is the
// default in every mode, including Kubernetes.
type ClusterBusConfig struct {
	Driver     string // "none" in every mode by default; kafka | redpanda
	Brokers    []string
	Topic      string
	Partitions int // fixed at 32 (DR-8)
}

// ClusterLeaderElectionConfig is 01 §7's leader_election block, used by
// DR-27 §27.2's audit-append forwarding rule.
type ClusterLeaderElectionConfig struct {
	Driver    string // none | ... (Kubernetes Lease in-cluster)
	Namespace string
	LeaseName string
}

// ClusterConfig is 01 §7's `cluster` block.
type ClusterConfig struct {
	Enabled        bool
	Bus            ClusterBusConfig
	LeaderElection ClusterLeaderElectionConfig
}

// OpsBackupConfig is DR-33 §33.3, verbatim.
type OpsBackupConfig struct {
	Enabled           bool
	Dir               string
	ControlInterval   time.Duration // 5m — control.db, the irreplaceable state
	TelemetryInterval time.Duration // 6h — traceiq.db, large and re-derivable from the cold tier
	ColdWALShip       bool
	Retain            time.Duration // 7d
	VerifyOnWrite     bool          // each VACUUM INTO output is reopened and integrity_check'd
}

// OpsConfig is 01 §7's `ops` block, amended by DR-33.
type OpsConfig struct {
	ShutdownGrace time.Duration // 45s
	Backup        OpsBackupConfig
}
