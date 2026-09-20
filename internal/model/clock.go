package model

import (
	"context"
	"time"
)

// Ticker and Timer abstract time.Ticker/time.Timer so every component reads
// time only through Clock (DR-4, DR-31).
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

// Clock is the single time source constructed once in cmd/traceiq and
// injected into every component that reads time (DR-4, DR-31). DR-31 binds
// internal/archtest to fail the build on any direct time.Now/time.Since/
// time.After/time.Tick/time.NewTimer/time.NewTicker call outside this
// package and cmd/traceiq.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
	Sleep(ctx context.Context, d time.Duration) error
}

// Barrier lets a virtual clock (eval.VirtualClock, DR-31) know when a
// component is idle, so quiescence — not a sleep — advances simulated time.
type Barrier interface {
	Name() string
	Pending() int
}

// NewRealClock constructs the production Clock backed by the standard
// library's time package (DR-31).
func NewRealClock() Clock { panic("not implemented") }
