package config

import (
	"time"

	"appfactory/internal/policies"
)

// Wait limits (waits-events-cost-cli D1) when no wait policy rule sets them.
const (
	DefaultMinWaitInterval = 15 * time.Minute
	DefaultMaxWaits        = 12
)

// WaitPolicy is the agent's shortest wait and how many consecutive waits without progress a task may make,
// from its resolved wait rules (the last rule that sets a value wins).
func (a Agent) WaitPolicy() (time.Duration, int) {
	interval, max := DefaultMinWaitInterval, DefaultMaxWaits
	for _, r := range a.Rules {
		if r.Type != "wait" {
			continue
		}
		if d, err := time.ParseDuration(r.MinWaitInterval); err == nil && d > 0 {
			interval = d
		}
		if r.MaxWaits > 0 {
			max = r.MaxWaits
		}
	}
	return interval, max
}

// RequiredReports are the agent's reporting rules that expect a report on the exit kind (D2).
func (a Agent) RequiredReports(exit string) []policies.Rule {
	out := []policies.Rule{}
	for _, r := range a.Rules {
		if r.Type == "reporting" && policies.Match(r.Exits, exit) {
			out = append(out, r)
		}
	}
	return out
}
