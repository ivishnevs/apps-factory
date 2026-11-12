package storage

import (
	"bytes"
	"context"
	"testing"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/clock"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

func TestNamesVisuals(t *testing.T) {
	for text, want := range map[string]bool{
		"The image is docs/qa/towers/side_by_side_v2.jpg on that branch": true,
		"see (shots/v1.PNG), then v2":                                    true,
		"clip: `demo.mp4`.":                                              true,
		"icons in assets/ui/*.webp":                                      true,
		"the .png files are ready":                                       false,
		"Ship it on Friday?":                                             false,
		"docs/design.md and main.go":                                     false,
	} {
		if got := NamesVisuals(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

// TestRetentionKeepsMessageAttachments (inbox-visual-previews): a message's attachment keeps its content although
// the only artifact version with the same content is archived by retention.
func TestRetentionKeepsMessageAttachments(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "art", ProjectID: "p", Title: "art", Assignee: "demo", RequestID: "art"}); err != nil {
		t.Fatal(err)
	}
	put := func(b string) Blob {
		blob, err := s.PutBlob(ctx, co, bytes.NewReader([]byte(b)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	v1 := put("towers v1")
	for i, b := range []Blob{v1, put("towers v2")} {
		version := []string{"v1", "v2"}[i]
		if _, err := s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: "towers", Version: version, URI: UploadURI("towers", version), SHA256: b.SHA256, Name: "towers.png"}, TaskID: "art", Actor: "owner", RequestID: "reg-" + version}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "notification", Body: "towers before and after", RequestID: "m",
		Attachments: []messaging.Attachment{{Path: "shots/before.png", Caption: "before", SHA256: v1.SHA256}}})
	if err != nil || len(m.Attachments) != 1 || m.Attachments[0].Size != v1.Size {
		t.Fatal(m, err)
	}
	p, _ := s.Project(ctx, co, "p")
	if _, err = s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ArtifactRetentionDays: ptr(7)}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(30 * 24 * time.Hour)
	run, err := s.ArchiveExpiredArtifacts(ctx, co)
	if err != nil || run.Archived["p"] != 1 || run.Deleted != 0 {
		t.Fatalf("%+v %v", run, err)
	}
	if ok, _ := s.Blobs.Exists(ctx, co, v1.SHA256); !ok {
		t.Fatal("a message's attachment was deleted with the archived version")
	}
}
