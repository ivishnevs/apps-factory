package tasks

import (
	"slices"
	"testing"
)

func TestNormalizeCover(t *testing.T) {
	for in, want := range map[string]string{
		"player-profile-and-xp#3.2":   "player-profile-and-xp#3.2",
		"Player-Profile-and-XP # 3.2": "player-profile-and-xp#3.2",
		"world-models 7":              "world-models#7",
		"PR #148":                     "pr#148",
		"pr#0148":                     "pr#148",
		"#166":                        "pr#166",
		"pull request 12":             "pr#12",
		"msg:0123456789abcdef":        "msg:01234567",
		"message: ABCDEF0123":         "msg:abcdef01",
	} {
		if got, err := NormalizeCover(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "world-models", "msg:abc", "pr#x", "3.2", "world models#1"} {
		if got, err := NormalizeCover(bad); err == nil {
			t.Errorf("%q: %q accepted", bad, got)
		}
	}
	if got, err := NormalizeCovers([]string{"PR 148", "pr#148", "b#1", "a#2.1"}); err != nil || !slices.Equal(got, []string{"a#2.1", "b#1", "pr#148"}) {
		t.Fatal(got, err)
	}
}

// TestInferCovers: the three duplicate pairs of 2026-11-07 (task-duplicate-warnings 1.2) and the inference rules.
func TestInferCovers(t *testing.T) {
	known := []string{"player-profile-and-xp", "world-models", "world-models-v2", "class-talents", "ui"}
	for _, c := range []struct {
		name, title, description, change string
		keys, changes                    []string
	}{
		// 438cd999 / 63947756: both name the item and the pull request.
		{"follow-up of PR 148", "M5b-P3 follow-up: XP curve tuning", "Carry-over of player-profile-and-xp task 3.2 from the review of PR #148.", "",
			[]string{"player-profile-and-xp#3.2", "pr#148"}, []string{"player-profile-and-xp"}},
		{"remainder after the archive", "player-profile-and-xp 3.2 remainder: XP curve", "Found while reviewing #166 (archive of the change); 3.2 was not done (see #148).", "",
			[]string{"player-profile-and-xp#3.2", "pr#148", "pr#166"}, []string{"player-profile-and-xp"}},
		// c9b076b8 / d2a9b701: world-models 7.1–7.3.
		{"range", "3D-WQ2: world checks", "Verify world-models 7.1–7.3 on device.", "",
			[]string{"world-models#7.1", "world-models#7.2", "world-models#7.3"}, []string{"world-models"}},
		{"tasks.md items with the change linked", "3D-WQ2: narrower check", "Check tasks.md 7.1, 7.2 and 7.3 only.", "world-models",
			[]string{"world-models#7.1", "world-models#7.2", "world-models#7.3"}, nil},
		// ae279f64 split: the parts name their groups.
		{"groups and keys", "3D-K2i: terrain", "Covers world-models#4 and world-models-v2 section 2.", "",
			[]string{"world-models#4", "world-models-v2#2"}, []string{"world-models", "world-models-v2"}},
		{"no item after the name", "Polish world-models screens", "Follows world-models in 2026.", "", nil, []string{"world-models"}},
		{"version numbers and dates are not items", "Bump", "world-models 3.2.1 shipped 2026-11-07; world-models 12345", "", nil, []string{"world-models"}},
		{"short names are not looked for", "UI 3.2", "ui 3.2", "", nil, nil},
		{"headings and change keys are not pull requests", "x", "## 3\nsee docs/a#12 and world-models#2.1 and #3.2", "", []string{"world-models#2.1"}, []string{"world-models"}},
	} {
		keys, changes := InferCovers(c.title, c.description, c.change, known)
		if !slices.Equal(keys, c.keys) || !slices.Equal(changes, c.changes) {
			t.Errorf("%s: keys %v changes %v, want %v %v", c.name, keys, changes, c.keys, c.changes)
		}
	}
}

func TestCoversOverlap(t *testing.T) {
	if !CoversOverlap("w#7", "w#7.2") || !CoversOverlap("w#7.2", "w#7") || CoversOverlap("w#7", "w#71") || CoversOverlap("w#7.1", "w#7.2") || CoversOverlap("pr#1", "pr#12") {
		t.Fatal("overlap")
	}
	if got := SharedCovers([]string{"w#7", "pr#1"}, []string{"w#7.2", "x#1"}); !slices.Equal(got, []string{"w#7.2"}) {
		t.Fatal(got)
	}
}

func TestDuplicateTitle(t *testing.T) {
	for a, b := range map[string]string{
		"3D-WQ2: Verify world models":            "verify world models",
		"3D-O4i/W1i: Terrain, part 2 (ae279f64)": "terrain part 2",
		"Fix: login":                             "fix login",
		"  Player-Profile 3.2   remainder.":      "player-profile 3.2 remainder",
	} {
		if got := DuplicateTitle(a); got != b {
			t.Errorf("%q: %q, want %q", a, got, b)
		}
	}
}
