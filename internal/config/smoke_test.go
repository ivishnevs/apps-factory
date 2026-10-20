package config

import (
	"testing"
	"time"

	"appfactory/internal/protocol"
)

// TestSmokeCatalog: configs/smoke/ (FACTORY_COMPANY=smoke) is the catalog of the live smoke scripts: the agents
// they use, each on the cheapest model of its runtime at low effort, with short timeouts and no retries.
func TestSmokeCatalog(t *testing.T) {
	c, err := LoadCompanyCatalog("../..", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{ // provider, model, effort
		"demo":        {"codex", "gpt-6-luna", "low"},
		"manager":     {"codex", "gpt-6-luna", "low"},
		"claude-demo": {"claude", "claude-haiku-5-5", "low"},
	}
	if len(c.Agents) != len(want) || c.DefaultReport != "manager" {
		t.Fatal(Names(c.Agents), c.DefaultReport)
	}
	for name, w := range want {
		a, ok := c.Agents[name]
		if !ok {
			t.Fatal("missing smoke agent", name)
		}
		r := a.Spec.Runtime
		if r.Provider != w[0] || r.Model != w[1] || r.Effort != w[2] || !ModelFits(protocol.RuntimeProvider(r.Provider), r.Model) {
			t.Fatalf("%s runtime %+v, want %v", name, r, w)
		}
		if a.Timeout <= 0 || a.Timeout > 5*time.Minute || a.Spec.Execution.MaxRetries != 0 {
			t.Fatalf("%s: timeout %s, max_retries %d", name, a.Timeout, a.Spec.Execution.MaxRetries)
		}
		if o := a.RuntimeOptions(); o["model"] != w[1] || o["effort"] != "low" {
			t.Fatal(name, o)
		}
	}
	if n := c.Agents["claude-demo"].Spec.Runtime.MaxTurns; n < 5 || n > 10 {
		t.Fatal("claude-demo max_turns", n)
	}
}
