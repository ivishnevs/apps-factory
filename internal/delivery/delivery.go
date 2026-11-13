// Package delivery is the one rule a node uses to decide what to do with Core's refusal of something it must
// deliver: a run's events (the node's event spool) or an agent's exit (factory-mcp's pending exit and the node's
// redelivery). Retry what a later attempt can pass; drop only what can never apply (node-wake-recovery).
package delivery

import (
	"encoding/json"
	"strings"
	"time"
)

// Verdict says what to do with a refused delivery.
type Verdict int

const (
	// Retry: a later attempt can pass (network, Core busy or down, a temporary auth refusal).
	Retry Verdict = iota
	// Final: the delivery can never apply (another node's or an unknown run, invalid data, new input, a closed
	// run, a permanent auth refusal); the data is dropped and the refusal recorded.
	Final
)

func (v Verdict) String() string {
	if v == Final {
		return "final"
	}
	return "retry"
}

// Horizon bounds how long a node keeps retrying one delivery. Past it the node raises a health event naming the
// run and what is undelivered instead of dropping the data silently.
const Horizon = 30 * time.Minute

// Temporary auth refusals: a fresh request can pass (a new signature, a corrected clock, a new nonce).
var temporary = map[string]bool{
	"signature_window":       true,
	"signature_before_start": true,
	"signature_replay":       true,
	"signature_invalid":      true, // a node signs correctly; a corrupted request is resent whole
}

// Permanent auth refusals: no later attempt passes.
var permanent = map[string]bool{
	"lease_changed":      true, // the run token is an HMAC of the lease: after a new claim it never passes again
	"node_key_revoked":   true,
	"node_key_unknown":   true,
	"node_key_invalid":   true,
	"agent_unavailable":  true, // the agent was deleted
	"unknown_run":        true,
	"run_not_live":       true,
	"credential_missing": true,
}

// Reason reads the machine-readable reason of Core's refusal body ({"reason": ...}); "" when there is none.
func Reason(body string) string {
	var v struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &v) != nil {
		return ""
	}
	return v.Reason
}

// Classify decides a refusal by its HTTP status and reason. Status 0 is a transport error (Core unreachable).
//
//   - retry: transport errors, 5xx, 408, 429, and 401/403 with a temporary reason. A 401/403 without a known
//     reason is retried too: the retry is bounded by Horizon, and dropping data on a guess is worse.
//   - final: 400, 404, 409, 410, 422 and any other 4xx, and 401/403 with a permanent reason.
func Classify(status int, reason string) Verdict {
	switch {
	case status == 0 || status >= 500 || status == 408 || status == 429:
		return Retry
	case status == 401 || status == 403:
		if permanent[reason] {
			return Final
		}
		return Retry
	}
	return Final
}

// Temporary reports whether reason is a known temporary auth refusal.
func Temporary(reason string) bool { return temporary[reason] }
