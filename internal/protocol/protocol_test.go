package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnknownQuotaResetIsOmitted(t *testing.T) {
	b, _ := json.Marshal(Event{RunID: "r", Quota: &Quota{Type: "five_hour"}})
	if strings.Contains(string(b), "resets_at") {
		t.Fatal(string(b))
	}
}
