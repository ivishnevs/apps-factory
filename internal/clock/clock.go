// Package clock supplies Core's notion of time: a replaceable wall clock and the
// continuity window that keeps a host sleep from expiring every lease at once.
package clock

import (
	"sync"
	"time"
)

type Clock interface{ Now() time.Time }

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a manually advanced clock for tests.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t} }
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Continuity tracks the start of the current run of uninterrupted ticks. A gap between
// ticks longer than Gap (the host slept or the process was paused) starts a new window,
// so liveness deadlines measured from it give workers a full TTL after a wake.
type Continuity struct {
	Gap         time.Duration
	mu          sync.Mutex
	last, start time.Time
}

func NewContinuity(gap time.Duration) *Continuity { return &Continuity{Gap: gap} }

// Observe records a tick and returns the start of the current continuity window.
func (c *Continuity) Observe(now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last.IsZero() || now.Sub(c.last) > c.Gap || now.Before(c.last) {
		c.start = now
	}
	c.last = now
	return c.start
}

// Start is the current window start; zero before the first tick.
func (c *Continuity) Start() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.start
}
