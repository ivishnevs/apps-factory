package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
)

// TestJetStreamAuthFailureReported (runtime-drivers 3.2): a run that ends on the runtime's authentication
// failure (at launch, as a FAILED turn, or without terminal evidence) ends paused_out and calls AuthFailed
// once; other failures, a quota hit included, do not. Only a COMPLETED run calls RunSucceeded (the node resets
// the driver's auth hold-off).
func TestJetStreamAuthFailureReported(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	for name, c := range map[string]struct {
		script         fake.Script
		launch         error
		event, outcome string
		auth           bool
	}{
		"failed turn":   {script: fake.Script{Status: "FAILED", Outcome: driver.OutcomePausedOut, Auth: "authentication_failed"}, event: "ENDED", outcome: "paused_out", auth: true},
		"no evidence":   {script: fake.Script{Auth: "logged out"}, event: "ENDED", outcome: "paused_out", auth: true},
		"launch":        {launch: fake.AuthError("ChatGPT login required"), event: "ENDED", outcome: "paused_out", auth: true},
		"quota":         {script: fake.Script{Status: "FAILED", Outcome: driver.OutcomePausedOut}, event: "ENDED", outcome: "paused_out"},
		"plain failure": {script: fake.Script{Status: "FAILED"}, event: "ENDED", outcome: "failed"},
		"launch error":  {launch: errors.New("app-server refused the thread"), event: "ENDED", outcome: "failed"},
		"completed":     {script: fake.Script{Status: "COMPLETED"}, event: "ENDED", outcome: "succeeded"},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var got []error
			succeeded := 0
			d := &fake.Driver{Script: c.script}
			w := Worker{Driver: authLaunch{d, c.launch}, AuthFailed: func(err error) { mu.Lock(); got = append(got, err); mu.Unlock() },
				RunSucceeded: func() { mu.Lock(); succeeded++; mu.Unlock() }}
			_, events := runDriver(t, url, w, false)
			var terminal protocol.Event
			if err := json.Unmarshal(events[len(events)-1].Payload, &terminal); err != nil {
				t.Fatal(err)
			}
			if terminal.Status != c.event || terminal.Outcome != c.outcome {
				t.Fatalf("terminal %s/%s, want %s/%s (%s)", terminal.Status, terminal.Outcome, c.event, c.outcome, terminal.Result)
			}
			mu.Lock()
			defer mu.Unlock()
			if c.auth && (len(got) != 1 || !driver.IsAuthFailure(got[0])) || !c.auth && len(got) != 0 {
				t.Fatalf("AuthFailed calls %v, want auth %v", got, c.auth)
			}
			if want := map[bool]int{true: 1}[c.outcome == "succeeded"]; succeeded != want {
				t.Fatalf("RunSucceeded calls %d, want %d", succeeded, want)
			}
		})
	}
}

// authLaunch makes Launch fail with err when set (the fake's LaunchErr is a plain error).
type authLaunch struct {
	*fake.Driver
	err error
}

func (a authLaunch) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.Driver.Launch(ctx, l)
}
