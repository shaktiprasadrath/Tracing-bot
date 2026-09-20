package main

import (
	"time"

	"traceiq/internal/anomaly"
	"traceiq/internal/config"
	"traceiq/internal/sampler"
)

// The feature packages under internal/{sampler,anomaly} deliberately carry
// their own package-local config structs rather than importing
// internal/config (each package's own DR-2 import-ban note): cmd/traceiq is
// where config.Config's values actually get translated into those structs.

func toSamplerAssemblyConfig(c config.SamplerAssemblyConfig) sampler.AssemblyConfig {
	return sampler.AssemblyConfig{
		IdleTimeout: c.IdleTimeout,
		HardTimeout: c.HardTimeout,
		WheelTick:   c.WheelTick,
	}
}

func toSamplerPolicyConfig(c config.SamplerPolicyConfig) sampler.PolicyConfig {
	return sampler.PolicyConfig{
		SlowMinDuration:             c.SlowMinDuration,
		RarePathLookback:            time.Duration(c.RarePathLookbackDays) * 24 * time.Hour,
		RarePathKeepsPerMin:         float64(c.RarePathKeepsPerMin),
		MaxPathSignatureCardinality: c.MaxPathSignatureCardinality,
		FloorTracesPerMinPerService: float64(c.FloorTracesPerMinPerService),
		HealthySampleRate:           c.HealthySampleRate,
		MaxKeepRate:                 c.MaxKeepRate,
	}
}

func toAnomalyConfig(c config.AnomalyConfig) anomaly.Config {
	return anomaly.Config{
		EvalInterval:  c.EvalInterval,
		EvalWindow:    c.EvalWindow,
		DebounceTicks: c.DebounceTicks,
		Baseline: anomaly.BaselineConfig{
			EstimatorTDigestCompression: c.Baseline.TDigestCompression,
			EWMAAlpha:                   c.Baseline.EWMAAlpha,
			WarmupSamples:               uint32(c.Baseline.WarmupSamples),
			WarmupSamplesPerBucket:      uint32(c.Baseline.WarmupSamplesPerBucket),
			ColdStartMultiplier:         c.Baseline.ColdStartMultiplier,
			MaxColdStart:                c.Baseline.MaxColdStart,
			MaxKeys:                     c.Baseline.MaxKeys,
			MaxKeysGlobal:               c.Baseline.MaxKeysGlobal,
			MaxErrorSignatures:          c.Baseline.MaxErrorSignatures,
			CheckpointInterval:          c.Baseline.CheckpointInterval,
			CheckpointMaxRows:           c.Baseline.CheckpointMaxRows,
			MinRPS:                      c.Baseline.MinRPS,
		},
		Thresholds: anomaly.ThresholdConfig{
			LatencyRatio:              c.Thresholds.LatencyRatio,
			LatencyAbsDeltaNanos:      float64(c.Thresholds.LatencyAbsDelta),
			ErrorRateDelta:            c.Thresholds.ErrorRateDelta,
			ErrorBurstRatio:           c.Thresholds.ErrorBurstRatio,
			ThroughputDropRatio:       c.Thresholds.ThroughputDropRatio,
			NewErrorSignatureLookback: c.Thresholds.NewErrorSignatureLookback,
			NewErrorSigMinCalls:       int64(c.Thresholds.NewErrorSigMinCalls),
			MinCalls:                  uint64(c.Thresholds.MinCalls),
			MinEventScore:             c.Thresholds.MinEventScore,
		},
		Grouping: anomaly.GroupingConfig{
			Window:               c.Grouping.Window,
			TopologyHops:         c.Grouping.TopologyHops,
			MaxEventsPerIncident: c.Grouping.MaxEventsPerIncident,
			MaxOpenIncidents:     c.Grouping.MaxOpenIncidents,
			DedupeTTL:            c.Grouping.DedupeTTL,
			NeighborCacheEntries: c.Grouping.NeighborCacheEntries,
			NeighborCacheTTL:     c.Grouping.NeighborCacheTTL,
		},
		DeployMarkers: anomaly.DeployMarkerConfig{
			Enabled:           c.DeployMarkers.Enabled,
			CorrelationWindow: c.DeployMarkers.CorrelationWindow,
			Settle:            c.DeployMarkers.Settle,
		},
	}
}
