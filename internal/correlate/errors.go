package correlate

import "errors"

// ErrAdapterBusy is returned immediately (never queued) when a call would
// exceed the bounded fan-out cap (FR-F07-7, DR-20 §20.4).
var ErrAdapterBusy = errors.New("correlate: adapter busy (fan-out cap reached)")

// ErrAdapterUnavailable is returned when an adapter's circuit breaker is
// open (FR-F07-7, DR-20 §20.4).
var ErrAdapterUnavailable = errors.New("correlate: adapter unavailable (breaker open)")
