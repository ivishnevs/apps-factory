package protocol

import (
	"slices"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"2026.11.12.1530", "2026.11.12.1531", -1, true},
		{"2026.11.13", "2026.11.12.2359", 1, true},
		{"2026.11.12", "2026.11.12.0", 0, true},
		{"v1.2.3", "1.2.3+abc", 0, true},
		{"2026.11.12.1530-6cfd893", "2026.11.12.1530", 0, true},
		{"dev", "2026.11.12", 0, false},
		{"6cfd893", "2026.11.12", 0, false},
		{"", "1", 0, false},
	} {
		if cmp, ok := CompareVersions(c.a, c.b); cmp != c.cmp || ok != c.ok {
			t.Errorf("%q vs %q: %d %v", c.a, c.b, cmp, ok)
		}
	}
	for _, c := range []struct {
		v, min string
		below  bool
	}{
		{"2026.11.01", "", false}, {"", "", false}, {"", "2026.11.12", true}, {"2026.11.01", "2026.11.12", true},
		{"2026.11.12.1", "2026.11.12", false}, {"dev", "2026.11.12", false},
	} {
		if got := VersionBelow(c.v, c.min); got != c.below {
			t.Errorf("%q below %q: %v", c.v, c.min, got)
		}
	}
}

func TestNodeSettingsValidate(t *testing.T) {
	zero, many, conv := 0, 65, 2
	low, crit, neg := 10.0, 20.0, -1.0
	labels, badLabels := []string{"ios", "team-a"}, []string{"os:macos"}
	on := true
	for _, bad := range []NodeSettings{
		{Slots: &zero}, {Slots: &many}, {ConversationSlots: &many}, {Labels: &badLabels},
		{Drivers: map[string]NodeDriverSettings{"gemini": {Enabled: &on}}},
		{Disk: &NodeDiskSettings{LowGiB: &low, CriticalGiB: &crit}}, {Disk: &NodeDiskSettings{ConversationFloorGiB: &neg}},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	good := NodeSettings{ConversationSlots: &conv, Labels: &labels, Drivers: map[string]NodeDriverSettings{"claude": {Enabled: &on}}, Disk: &NodeDiskSettings{LowGiB: &crit, CriticalGiB: &low}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if k := good.Keys(); !slices.Equal(k, []string{"conversation_slots", "labels", "drivers.claude", "disk"}) {
		t.Fatal(k)
	}
	if !(NodeSettings{}).Empty() || good.Empty() {
		t.Fatal("empty")
	}
}
