package worker

import (
	"encoding/json"
	"testing"

	"appfactory/internal/protocol"
)

// TestPointer: v2 takes only the run id from a command, a pointer or (during the transition) a full RunSpec.
func TestPointer(t *testing.T) {
	cmd := func(correlation string, payload any) []byte {
		e := protocol.Wrap("agent.run.requested", correlation, "", payload)
		b, _ := json.Marshal(e)
		return b
	}
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"pointer":  {cmd("r1", map[string]string{"run_id": "r1", "company": "default", "agent": "a", "provider": "codex"}), "r1"},
		"runspec":  {cmd("r2", protocol.RunSpec{ID: "r2", Prompt: "ignored"}), "r2"},
		"mismatch": {cmd("r3", map[string]string{"run_id": "other"}), ""},
		"no run":   {cmd("r4", map[string]string{"agent": "a"}), ""},
		"other type": {func() []byte {
			b, _ := json.Marshal(protocol.Wrap("agent.started", "r5", "", map[string]string{"run_id": "r5"}))
			return b
		}(), ""},
		"not json":    {[]byte("{"), ""},
		"no envelope": {[]byte(`{"run_id":"r6"}`), ""},
	} {
		if _, run := pointer(c.data); run != c.want {
			t.Errorf("%s: %q, want %q", name, run, c.want)
		}
	}
}
