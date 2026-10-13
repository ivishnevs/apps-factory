package worker

import "golang.org/x/sys/unix"

// mach_absolute_time; CLOCK_MONOTONIC on macOS keeps counting during sleep.
const sleepExcludingClock = unix.CLOCK_UPTIME_RAW
