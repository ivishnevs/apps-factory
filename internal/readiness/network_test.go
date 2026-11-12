package readiness

import (
	"testing"

	"appfactory/internal/protocol"
)

// TestNetworkFault (setup-assistant 4.2; 2026-11-11 mac2): a run that ends on a network error to the runtime's API
// host is the node's fault, also after its agent worked; a network error to another host is the task's.
func TestNetworkFault(t *testing.T) {
	cases := []struct {
		outcome, result string
		tokens          bool
		want            bool
	}{
		{"failed", "API Error: Connection error. getaddrinfo ENOTFOUND api.anthropic.com", false, true},
		{"failed", "request to https://api.anthropic.com/v1/messages failed, reason: connect ECONNREFUSED 127.0.0.1:443", true, true},
		{"failed", "stream error: dial tcp: lookup chatgpt.com: no such host", true, true},
		{"setup_failed", "getaddrinfo EAI_AGAIN api.anthropic.com", false, true},
		// Not the API host, or not a network error, or a run that did not fail.
		{"failed", "npm ERR! getaddrinfo ENOTFOUND registry.npmjs.org", true, false},
		{"failed", "api.anthropic.com returned 529 overloaded", false, false},
		{"succeeded", "getaddrinfo ENOTFOUND api.anthropic.com", false, false},
		{"timed_out", "getaddrinfo ENOTFOUND api.anthropic.com", false, false},
	}
	for _, c := range cases {
		f := Classify(c.outcome, c.result, c.tokens)
		got := f != nil && f.Kind == protocol.FaultNetwork
		if got != c.want {
			t.Errorf("Classify(%s, %q, %v) = %+v, want network %v", c.outcome, c.result, c.tokens, f, c.want)
		}
		if got && (f.Fix != FixNetwork || f.Detail == "") {
			t.Errorf("%q: %+v", c.result, f)
		}
	}
}
