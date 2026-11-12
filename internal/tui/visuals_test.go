package tui

import (
	"strings"
	"testing"

	"appfactory/internal/messaging"
)

// TestVisualLines (inbox-visual-previews): the terminal lists a message's attachments and materials as links with
// their captions, in the order given.
func TestVisualLines(t *testing.T) {
	sha := strings.Repeat("a", 64)
	msg := messaging.Message{Body: "Verdict?", Attachments: []messaging.Attachment{{Path: "shots/classic.png", Caption: "classic", SHA256: sha, Size: 2048}},
		Materials: []messaging.MaterialRef{{ArtifactID: "towers", ArtifactVersion: "v2", Name: "towers.png", Caption: "v2", SHA256: sha, Size: 10}, {ArtifactID: "spec", ArtifactVersion: "v1", Name: "spec", URI: "https://example.com/spec"}}}
	got := strings.Join(visualLines(msg, 160), "\n")
	for _, want := range []string{"classic · shots/classic.png", "2.0 KiB", "/blobs/" + sha, "v2 · towers v2 (towers.png)", "https://example.com/spec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Index(got, "classic") > strings.Index(got, "towers v2") {
		t.Fatal("order", got)
	}
	if visualLines(messaging.Message{Body: "plain"}, 80) != nil {
		t.Fatal("lines without attachments")
	}
}
