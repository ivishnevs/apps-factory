package storage

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"appfactory/internal/artifacts"
	"appfactory/internal/blobstore"
	"appfactory/internal/tasks"
)

// TestArtifactDuplicates (artifact-duplicates 1.1–1.3): the same content as the latest version is that version; a zip
// lists its images (unsafe paths skipped) and serves one; a newer version's request supersedes the older open one.
func TestArtifactDuplicates(t *testing.T) {
	s := engineStore(t)
	if s.Blobs == nil {
		s.Blobs = &blobstore.Memory{}
	}
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "art", ProjectID: "p", Title: "art", Assignee: "demo", RequestID: "art"}); err != nil {
		t.Fatal(err)
	}
	put := func(b []byte) Blob {
		t.Helper()
		blob, err := s.PutBlob(ctx, co, bytes.NewReader(b), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	var zb bytes.Buffer
	z := zip.NewWriter(&zb)
	for _, name := range []string{"sheets/rumbler.png", "notes.txt", "../escape.png", "poses/a.jpg"} {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte("img " + name))
	}
	_ = z.Close()
	blob := put(zb.Bytes())
	reg := func(version, sha, key string) artifacts.Record {
		t.Helper()
		rec, err := s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: "pack", Version: version, URI: UploadURI("pack", version), SHA256: sha, Name: "pack.zip"}, TaskID: "art", Actor: "owner", RequestID: key})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	v1 := reg("v1", blob.SHA256, "r1")
	if v1.Unchanged || len(v1.ArchiveImages) != 2 {
		t.Fatalf("v1 %+v", v1)
	}
	if again := reg("v2", blob.SHA256, "r2"); !again.Unchanged || again.Version != "v1" {
		t.Fatalf("duplicate content made a new version: %+v", again)
	}
	var out bytes.Buffer
	if err := s.CopyArchiveEntry(ctx, co, "p", "pack", "v1", "sheets/rumbler.png", &out); err != nil || out.String() != "img sheets/rumbler.png" {
		t.Fatal(out.String(), err)
	}
	if err := s.CopyArchiveEntry(ctx, co, "p", "pack", "v1", "../escape.png", &out); err == nil {
		t.Fatal("unsafe entry served")
	}
	a1, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "pack", ArtifactVersion: "v1", Agent: "demo", Reason: "v1", RequestID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	other := put([]byte("another pack"))
	reg("v2", other.SHA256, "r3")
	if _, err = s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "pack", ArtifactVersion: "v2", Agent: "demo", Reason: "v2", RequestID: "a2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Approval(ctx, co, a1.ID); got.Status != "SUPERSEDED" {
		t.Fatalf("older request %s", got.Status)
	}
	inbox, _ := s.Inbox(ctx, co, "")
	for _, m := range inbox {
		if m.ApprovalID == a1.ID {
			t.Fatal("superseded request stays in the Inbox")
		}
	}
}
