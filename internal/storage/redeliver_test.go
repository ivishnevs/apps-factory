package storage

import (
	"context"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
)

func TestQueuedRunCommandIsRedeliveredWithBackoff(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	if err = s.Create(ctx, DefaultCompany, protocol.RunSpec{ID: "r", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/redeliver"}); err != nil {
		t.Fatal(err)
	}
	publish := func() []Outbox {
		t.Helper()
		out, err := s.Pending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		commands := []Outbox{}
		for _, o := range out {
			if err = s.Sent(ctx, o.ID); err != nil {
				t.Fatal(err)
			}
			if o.Subject == "factory.default.commands.agent.run.codex" {
				commands = append(commands, o)
			}
		}
		return commands
	}
	redeliver := func(after time.Duration) int {
		t.Helper()
		fake.Advance(after)
		n, err := s.RedeliverQueued(ctx, DefaultCompany)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if first := publish(); len(first) != 1 {
		t.Fatal(first)
	}
	if n := redeliver(59 * time.Second); n != 0 {
		t.Fatal("resent before a minute", n)
	}
	if n := redeliver(time.Second); n != 1 {
		t.Fatal("not resent after a minute", n)
	}
	// While the resent command is still in the outbox (broker down), nothing more is queued.
	if n := redeliver(time.Hour); n != 0 {
		t.Fatal("piled commands behind an unpublished one", n)
	}
	second := publish()
	if len(second) != 1 || second[0].ID != "r/2" {
		t.Fatal(second)
	}
	if n := redeliver(119 * time.Second); n != 0 {
		t.Fatal("backoff not doubled", n)
	}
	if n := redeliver(time.Second); n != 1 {
		t.Fatal("not resent after backoff", n)
	}
	if third := publish(); len(third) != 1 || third[0].ID != "r/3" {
		t.Fatal(third)
	}
	// Once claimed, the run is no longer resent.
	if _, err = s.Claim(ctx, DefaultCompany, "r"); err != nil {
		t.Fatal(err)
	}
	if n := redeliver(time.Hour); n != 0 {
		t.Fatal("claimed run resent", n)
	}
	if got := redeliverAfter(9); got != 15*time.Minute {
		t.Fatal(got)
	}
}
