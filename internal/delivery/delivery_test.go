package delivery

import "testing"

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		status int
		reason string
		want   Verdict
	}{
		{0, "", Retry},
		{500, "", Retry},
		{503, "", Retry},
		{408, "", Retry},
		{429, "", Retry},
		{401, "signature_window", Retry},
		{401, "signature_replay", Retry},
		{401, "signature_before_start", Retry},
		{401, "", Retry}, // unknown: retried, bounded by Horizon
		{401, "lease_changed", Final},
		{401, "node_key_revoked", Final},
		{401, "unknown_run", Final},
		{401, "run_not_live", Final},
		{403, "agent_unavailable", Final},
		{403, "node_forbidden", Final},
		{403, "lease_changed", Final},
		{400, "", Final},
		{404, "", Final},
		{409, "gap", Final},
		{409, "unseen_input", Final},
		{410, "not_current_run", Final},
		{422, "", Final},
	} {
		if got := Classify(c.status, c.reason); got != c.want {
			t.Errorf("Classify(%d, %q) = %v, want %v", c.status, c.reason, got, c.want)
		}
	}
}

func TestReason(t *testing.T) {
	if r := Reason(`{"error":"unauthorized","reason":"signature_window","core_time_ms":1}` + "\n"); r != "signature_window" {
		t.Fatal(r)
	}
	if r := Reason("unauthorized\n"); r != "" {
		t.Fatal(r)
	}
}
