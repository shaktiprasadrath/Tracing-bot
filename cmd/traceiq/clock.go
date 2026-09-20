package main

import (
	"context"
	"time"

	"traceiq/internal/model"
)

// realClock is the production model.Clock (DR-31): every component that
// needs time reads it through this, constructed exactly once here in
// cmd/traceiq. internal/model.NewRealClock is an unimplemented panic stub
// in this scaffold (see internal/model/clock.go), so this pass provides the
// concrete implementation at the composition root instead of inside
// internal/model, which is out of this wave's edit scope.
type realClock struct{}

func newRealClock() model.Clock { return realClock{} }

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (realClock) NewTicker(d time.Duration) model.Ticker {
	return realTicker{t: time.NewTicker(d)}
}
func (realClock) NewTimer(d time.Duration) model.Timer {
	return realTimer{t: time.NewTimer(d)}
}
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
