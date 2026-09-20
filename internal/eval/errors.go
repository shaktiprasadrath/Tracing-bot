package eval

import "errors"

// Sentinel errors Run() returns for CI-gate failures (F11 §4.4 / DR-36
// §36.6). Each is checked with errors.Is by callers; the accompanying
// Report is still fully populated so a failed run can be inspected, not
// just retried blind.
var (
	// ErrBelowAbsoluteFloor is returned when the run's Top1Accuracy or
	// MeanTimeToRCA misses its absolute floor/ceiling, independent of any
	// baseline (FR-F11-11).
	ErrBelowAbsoluteFloor = errors.New("eval: run below absolute floor")

	// ErrRegressionTop1Accuracy is returned when FailOnRegression is set and
	// Top1Accuracy drops by more than 5 percentage points vs the baseline
	// (FR-F11-9).
	ErrRegressionTop1Accuracy = errors.New("eval: top-1 accuracy regressed vs baseline")

	// ErrRegressionTimeToRCA is returned when FailOnRegression is set and
	// MeanTimeToRCA increases by more than 25% vs the baseline (FR-F11-9).
	ErrRegressionTimeToRCA = errors.New("eval: time-to-RCA regressed vs baseline")
)
