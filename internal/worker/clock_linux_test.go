package worker

import "golang.org/x/sys/unix"

// CLOCK_MONOTONIC excludes suspend on Linux; CLOCK_BOOTTIME would include it.
const sleepExcludingClock = unix.CLOCK_MONOTONIC
