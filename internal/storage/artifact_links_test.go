package storage

import (
	"reflect"
	"testing"
)

// TestArtifactMentions (artifact-links): registered artifacts named in a text, in order: a URI and id@version name
// a version, a bare id (with '-', '_' or a digit) its latest, "id version" that version; plain-word ids, unknown
// ids and unregistered versions link nothing; a bare mention of an artifact named with a version adds nothing.
func TestArtifactMentions(t *testing.T) {
	known := map[string][]string{"stashy-lens-pouch-design": {"v1", "v2"}, "logo": {"1"}, "icon_set": {"2026-11-01"}, "spec2": {"a", "b"}}
	for _, c := range []struct {
		text string
		want []artifactMention
	}{
		{"Please approve artifact stashy-lens-pouch-design.", []artifactMention{{"stashy-lens-pouch-design", "v2", false}}},
		{"Compare stashy-lens-pouch-design v1 with factory://artifacts/stashy-lens-pouch-design/v2.", []artifactMention{{"stashy-lens-pouch-design", "v1", true}, {"stashy-lens-pouch-design", "v2", true}}},
		{"see `stashy-lens-pouch-design@v1`, and the stashy-lens-pouch-design pack", []artifactMention{{"stashy-lens-pouch-design", "v1", true}}},
		{"the logo is fine; logo@1 is the one", []artifactMention{{"logo", "1", true}}},
		{"icon_set and spec2 b and spec2", []artifactMention{{"icon_set", "2026-11-01", false}, {"spec2", "b", true}}},
		{"stashy-lens-pouch-design@v9 and factory://artifacts/other-thing/v1 and mail me@stashy-lens-pouch-design", nil},
		{"my-stashy-lens-pouch-design-2 is another word", nil},
	} {
		if got := artifactMentions(known, c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %+v, want %+v", c.text, got, c.want)
		}
	}
	// Several texts keep their order; nothing is known, nothing links.
	if got := artifactMentions(known, "spec2 a", "stashy-lens-pouch-design"); len(got) != 2 || got[0].ID != "spec2" || got[1].ID != "stashy-lens-pouch-design" {
		t.Fatalf("%+v", got)
	}
	if artifactMentions(nil, "stashy-lens-pouch-design") != nil {
		t.Fatal("linked without registered artifacts")
	}
}
