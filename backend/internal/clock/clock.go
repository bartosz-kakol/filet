// Package clock is the time source for expiry decisions. Production code
// never moves it; the filettest build exposes Advance through a test endpoint
// so end-to-end tests can expire links without waiting.
package clock

import (
	"sync/atomic"
	"time"
)

var offset atomic.Int64

// Now returns the current time plus any test offset.
func Now() time.Time { return time.Now().Add(time.Duration(offset.Load())) }

// NowMs returns Now in Unix milliseconds.
func NowMs() int64 { return Now().UnixMilli() }

// Advance moves the clock forward (tests only).
func Advance(d time.Duration) { offset.Add(int64(d)) }

// Reset clears the offset (tests only).
func Reset() { offset.Store(0) }
