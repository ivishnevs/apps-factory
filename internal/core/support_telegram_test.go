package core

import (
	"strings"
	"testing"
	"time"
)

// TestTelegramDoctor (setup-assistant 3.1): /doctor answers an owner with the company doctor, and the daily digest
// carries its problems with their fixes.
func TestTelegramDoctor(t *testing.T) {
	th := newTelegramHarness(t)
	th.bot.push(text(20, "/doctor"))
	th.tick()
	got := th.bot.texts()
	if len(got) != 1 || !strings.Contains(got[0], "no node is registered") || !strings.Contains(got[0], "runbook: factory-setup runbook adding-a-machine") {
		t.Fatal(got)
	}
	th.bot.reset()
	th.n.DigestAt = th.fake.Now().UTC().Add(-time.Minute).Format("15:04")
	th.tick()
	got = th.bot.texts()
	if len(got) != 1 || !strings.Contains(got[0], "Needs fixing:") || !strings.Contains(got[0], "no node is registered") {
		t.Fatal(got)
	}
}
