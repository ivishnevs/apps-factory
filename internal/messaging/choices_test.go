package messaging

import "testing"

func TestListsChoices(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		// 2026-11-11, App Release on task 44b64e7d.
		{"Which App Store Connect account (asc profile) should I use for the Tracely TestFlight upload? Profiles: ASC Key, Cheflet, Cheflet-H7N3F3AN5P (default), Stashy.", true},
		{"which profile: ASC Key, Cheflet, Stashy", true},
		{"Which map rotation do you want?\n- Forest first\n- Desert first\n- Random", true},
		{"Pick one:\n1. Keep the old icon\n2. Use the new icon", true},
		{"Should I ship to TestFlight now, wait for QA or skip this build?", true},
		{"Should I use Postgres or SQLite?", true},
		{"Which store: App Store or Google Play", true},
		{"What should the onboarding headline say?", false},
		{"Done so far:\n- built the app\n- ran the tests\nShall I go on?", false},
		{"Changes: icons, colors, fonts.\nAnything else you want me to look at?", false},
		{"Please review the build notes.", false},
	} {
		if got := ListsChoices(c.text); got != c.want {
			t.Errorf("ListsChoices(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
