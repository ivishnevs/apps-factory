//go:build darwin || linux

package worker

import (
	"testing"
	_ "unsafe"

	"golang.org/x/sys/unix"
)

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// Run timeouts, idle timeouts and the heartbeat schedule use Go timers and context deadlines,
// which run on runtime.nanotime. This pins that clock to one that stops during system sleep
// (macOS CLOCK_UPTIME_RAW = mach_absolute_time, Linux CLOCK_MONOTONIC), so a laptop never wakes
// up into a timeout. If a Go upgrade moves nanotime to a sleep-counting clock, this fails.
func TestTimersUseSleepExcludingClock(t *testing.T) {
	var ts unix.Timespec
	for i := 0; i < 5; i++ {
		before := nanotime()
		if err := unix.ClockGettime(sleepExcludingClock, &ts); err != nil {
			t.Fatal(err)
		}
		after := nanotime()
		if c := ts.Nano(); c >= before-1000 && c <= after+1000 {
			return
		}
	}
	t.Fatalf("runtime clock is not clock %d: nanotime %d, clock %d", sleepExcludingClock, nanotime(), ts.Nano())
}
