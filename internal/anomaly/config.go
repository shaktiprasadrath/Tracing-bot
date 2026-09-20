package anomaly

import "time"

// Config is anomaly's runtime configuration (DR-14 §14.9). The register
// gives config key *paths* only ("01 §7 owns values", DR-0); DefaultConfig
// reproduces DR-14 §14.9's YAML block's own numbers verbatim since F05's FR
// table (FR-F05-5..15) and DR-14 §14.3/14.8/14.9 cite them directly, and
// this package needs concrete defaults to be testable standalone without
// internal/config wiring (out of scope here).
type Config struct {
	EvalInterval  time.Duration
	EvalWindow    time.Duration
	DebounceTicks int

	Baseline      BaselineConfig
	Thresholds    ThresholdConfig
	Grouping      GroupingConfig
	DeployMarkers DeployMarkerConfig
}

// BaselineConfig is anomaly.baseline.* (DR-14 §14.9).
type BaselineConfig struct {
	EstimatorTDigestCompression int
	EWMAAlpha                   float64
	WarmupSamples               uint32
	WarmupSamplesPerBucket      uint32
	ColdStartMultiplier         float64
	MaxColdStart                time.Duration
	MaxKeys                     int
	MaxKeysGlobal               int
	MaxErrorSignatures          int
	CheckpointInterval          time.Duration
	CheckpointMaxRows           int
	MinRPS                      float64
}

// ThresholdConfig is anomaly.thresholds.* (DR-14 §14.4/§14.9).
type ThresholdConfig struct {
	LatencyRatio              float64
	LatencyAbsDeltaNanos      float64
	ErrorRateDelta            float64
	ErrorBurstRatio           float64
	ThroughputDropRatio       float64
	NewErrorSignatureLookback time.Duration
	NewErrorSigMinCalls       int64
	MinCalls                  uint64
	MinEventScore             float64
	NewEdgeMinCalls           uint64
	VanishedEdgeMinCalls24h   uint64
}

// GroupingConfig is anomaly.grouping.* (DR-14 §14.7/§14.9).
type GroupingConfig struct {
	Window               time.Duration
	TopologyHops         int
	MaxEventsPerIncident int
	MaxOpenIncidents     int
	DedupeTTL            time.Duration
	NeighborCacheEntries int
	NeighborCacheTTL     time.Duration
}

// DeployMarkerConfig is anomaly.deploy_markers.* (DR-14 §14.6/§14.9).
type DeployMarkerConfig struct {
	Enabled           bool
	CorrelationWindow time.Duration
	Settle            time.Duration
}

// DefaultConfig reproduces DR-14 §14.9's YAML block.
func DefaultConfig() Config {
	return Config{
		EvalInterval:  30 * time.Second,
		EvalWindow:    90 * time.Second,
		DebounceTicks: 2,
		Baseline: BaselineConfig{
			EstimatorTDigestCompression: 100,
			EWMAAlpha:                   0.2,
			WarmupSamples:               200,
			WarmupSamplesPerBucket:      30,
			ColdStartMultiplier:         1.5,
			MaxColdStart:                24 * time.Hour,
			MaxKeys:                     10000,
			MaxKeysGlobal:               20000,
			MaxErrorSignatures:          20000,
			CheckpointInterval:          60 * time.Second,
			CheckpointMaxRows:           2000,
			MinRPS:                      0.1,
		},
		Thresholds: ThresholdConfig{
			LatencyRatio:              1.5,
			LatencyAbsDeltaNanos:      float64(50 * time.Millisecond),
			ErrorRateDelta:            0.05,
			ErrorBurstRatio:           3.0,
			ThroughputDropRatio:       0.5,
			NewErrorSignatureLookback: 7 * 24 * time.Hour,
			NewErrorSigMinCalls:       5,
			MinCalls:                  20,
			MinEventScore:             0.55,
			NewEdgeMinCalls:           5,
			VanishedEdgeMinCalls24h:   1000,
		},
		Grouping: GroupingConfig{
			Window:               5 * time.Minute,
			TopologyHops:         2,
			MaxEventsPerIncident: 200,
			MaxOpenIncidents:     200,
			DedupeTTL:            30 * time.Minute,
			NeighborCacheEntries: 4096,
			NeighborCacheTTL:     60 * time.Second,
		},
		DeployMarkers: DeployMarkerConfig{
			Enabled:           true,
			CorrelationWindow: 30 * time.Minute,
			Settle:            2 * time.Minute,
		},
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func min1(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}
