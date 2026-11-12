package config

import (
	"testing"
	"time"
)

// TestLockedCategoryOf (owner-comments-handled): an owner comment's locked category is found at word starts only.
func TestLockedCategoryOf(t *testing.T) {
	for text, want := range map[string]string{
		"then upload the fix to TestFlight right away": "release",
		"залей фикс в TestFlight":                      "release",
		"check it on the simulator":                    "",
		"why didn't you check on the simulator?":       "",
		"raise the price to $5":                        "money",
		"предоплата не нужна":                          "", // "оплат" inside a word
		"оплата картой":                                "money",
		"delete the old builds":                        "deletion",
		"undelete is fine":                             "",
		"add the privacy policy link":                  "legal",
	} {
		if got := LockedCategoryOf(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

func TestCommentLimit(t *testing.T) {
	if d := (Team{}).CommentLimit(); d != time.Hour {
		t.Fatal(d)
	}
	if d := (Team{Spec: TeamSpec{CommentOutcomeWithin: "30m"}}).CommentLimit(); d != 30*time.Minute {
		t.Fatal(d)
	}
	bad := map[string]Team{"core": {Spec: TeamSpec{Lead: "x", CommentOutcomeWithin: "soon"}}}
	if err := validTeams(bad, map[string]Agent{}); err == nil {
		t.Fatal("an invalid comment_outcome_within was accepted")
	}
}
