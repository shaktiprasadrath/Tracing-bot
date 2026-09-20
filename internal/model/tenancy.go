package model

import "time"

// TenantID is the tenancy parameter type used in every exported method
// signature that touches tenant-scoped data (DR-4, DR-5).
type TenantID string

// KeepReason is the persisted reason a sampler decision kept or dropped a
// trace. It replaces sampler.Reason (DR-4 deletion table) as the single
// persisted value shared by sampler, store and the UI.
type KeepReason uint8

const (
	KeepError         KeepReason = 1
	KeepSlow          KeepReason = 2
	KeepRare          KeepReason = 3
	KeepInterest      KeepReason = 4
	KeepFloor         KeepReason = 5
	KeepProbabilistic KeepReason = 6
	KeepDropped       KeepReason = 7
	KeepShed          KeepReason = 8
	KeepTruncated     KeepReason = 9
)

// Window replaces store.TimeWindow, store.Window, topology.Window and
// correlate.TimeWindow — one type (DR-4, new).
type Window struct {
	Start, End time.Time
}
