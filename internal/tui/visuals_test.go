package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// TestMessageArtifactPreview (artifact-links): v on a message that carries artifact versions previews them one per
// press, as an approval's artifact; the preview of a message decides nothing.
func TestMessageArtifactPreview(t *testing.T) {
	f := DemoFixture()
	f.Artifacts["project-2/pouch-design/v1"] = "# Pouch v1\n"
	f.Artifacts["project-2/pouch-notes/v3"] = "# Notes v3\n"
	sum := strings.Repeat("b", 64)
	msg := messaging.Message{ID: "q-art", ProjectID: "project-2", Sender: "lead", Recipient: "owner", MessageType: "question", Status: "PENDING", Version: 1, Body: "Is pouch-design ready to go to print?", CreatedAt: DemoAt.Unix(),
		Materials: []messaging.MaterialRef{{ArtifactID: "pouch-design", ArtifactVersion: "v1", Name: "pouch.md", SHA256: sum}, {ArtifactID: "pouch-notes", ArtifactVersion: "v3", Name: "notes.md", SHA256: sum}}}
	f.Overview.Inbox = append([]messaging.Message{msg}, f.Overview.Inbox...)
	c := &FixtureClient{F: f}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true, Notify: NotifyOff, Local: LocalTranscripts{Off: true}})
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(overviewMsg{ov: f.Overview})
	press(m, "5")
	if got := m.ov.Inbox[m.inbox.cursor]; got.ID != "q-art" {
		t.Fatal("cursor on", got.ID)
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "v previews the artifacts") {
		t.Fatal(s)
	}
	for _, want := range []string{"Preview · pouch-design v1", "Preview · pouch-notes v3", "Preview · pouch-design v1"} {
		run(m, key(m, "v"))
		if m.preview == nil || m.preview.err != nil {
			t.Fatalf("%+v", m.preview)
		}
		s := ansi.Strip(m.Render())
		if !strings.Contains(s, want) || strings.Contains(s, "approve") {
			t.Fatal(want, s)
		}
		key(m, "y")
		if m.input != nil || m.preview == nil {
			t.Fatal("y decided a message's artifact")
		}
		key(m, "esc")
	}
}
