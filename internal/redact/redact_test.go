package redact

import (
	"strings"
	"testing"
)

func TestKnownValuesAndPatterns(t *testing.T) {
	env := []string{"GH_TOKEN=forge-token-1234567890", "NATS_URL=nats://node:nats-pass-123@nats:4222", "HOME=/home/agent", "FACTORY_RUN_TOKEN=run.x.abcdef0123", "SHORT_TOKEN=abc"}
	r := New(append(FromEnv(env), "node-token-value")...)
	in := "push with forge-token-1234567890 to nats-pass-123 as node-token-value; key ghp_" + strings.Repeat("A", 36) + " home /home/agent abc"
	out, changed := r.Bytes([]byte(in))
	s := string(out)
	if !changed || strings.Contains(s, "forge-token") || strings.Contains(s, "nats-pass") || strings.Contains(s, "node-token-value") || strings.Contains(s, "ghp_") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "/home/agent") || !strings.Contains(s, " abc") || strings.Count(s, Marker) != 4 {
		t.Fatal("redacted ordinary text:", s)
	}
	if _, changed = r.Bytes([]byte("nothing secret here")); changed {
		t.Fatal("changed clean text")
	}
}
