package storage

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/clock"
	"appfactory/internal/tasks"
)

// TestArtifactRetention (artifact-retention 1.1–1.4): superseded versions past the project's retention are archived
// (the latest and the latest approved stay), their rows stay, content only they use is deleted, shared content
// stays, reads answer ArchivedError, the run records one feed event, and the ceiling caps the setting.
func TestArtifactRetention(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
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
	n := 0
	reg := func(id, version, name string, blob Blob) artifacts.Record {
		t.Helper()
		n++
		rec, err := s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: id, Version: version, URI: UploadURI(id, version), SHA256: blob.SHA256, Name: name},
			TaskID: "art", Actor: "owner", RequestID: fmt.Sprint(id, version, n)})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	var zb bytes.Buffer
	z := zip.NewWriter(&zb)
	w, _ := z.Create("a.png")
	_, _ = w.Write([]byte("png"))
	_ = z.Close()
	sheetV1, shared := put([]byte("sheet v1")), put([]byte("shared logo"))
	pack := put(zb.Bytes())
	reg("sheet", "v1", "sheet.png", sheetV1)
	reg("sheet", "v2", "sheet.png", put([]byte("sheet v2")))
	reg("sheet", "v3", "sheet.png", put([]byte("sheet v3")))
	reg("pack", "v1", "pack.zip", pack)
	reg("pack", "v2", "pack.zip", put([]byte("pack v2")))
	reg("logo", "v1", "logo.png", shared)
	reg("logo", "v2", "logo.png", put([]byte("logo v2")))
	reg("badge", "v1", "badge.png", shared) // the logo's old content is also the badge's latest
	// sheet v2 is approved: the latest approved version stays though v3 is newer.
	a, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "sheet", ArtifactVersion: "v2", Agent: "demo", Reason: "v2", RequestID: "ap"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE approvals SET status='APPROVED' WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}

	// Retention is off by default: nothing happens however old the versions are.
	fake.Advance(400 * 24 * time.Hour)
	if run, err := s.ArchiveExpiredArtifacts(ctx, co); err != nil || run.Total() != 0 || run.Deleted != 0 {
		t.Fatal("retention without a setting", run, err)
	}

	// The ceiling caps the setting; 0 then means the ceiling.
	s.ArtifactRetentionMaxDays = 30
	p, _ := s.Project(ctx, co, "p")
	if _, err = s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ArtifactRetentionDays: ptr(31)}); !errors.Is(err, ErrInput) {
		t.Fatal("setting above the ceiling accepted", err)
	}
	if _, err = s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ArtifactRetentionDays: ptr(-1)}); !errors.Is(err, ErrInput) {
		t.Fatal("negative setting accepted", err)
	}
	if got := s.EffectiveRetention(p); got != 30 {
		t.Fatal("project at 0 under a ceiling of 30:", got)
	}
	p, err = s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ArtifactRetentionDays: ptr(7)})
	if err != nil || p.ArtifactRetentionDays != 7 || s.EffectiveRetention(p) != 7 {
		t.Fatal(p.ArtifactRetentionDays, err)
	}

	run, err := s.ArchiveExpiredArtifacts(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	// sheet v1 and pack v1 and logo v1 are archived; sheet v2 (approved), the latest versions and badge stay.
	if run.Archived["p"] != 3 || run.Deleted != 2 || run.BytesFreed != sheetV1.Size+pack.Size {
		t.Fatalf("run %+v", run)
	}
	for _, v := range []struct {
		id, version string
		archived    bool
	}{{"sheet", "v1", true}, {"sheet", "v2", false}, {"sheet", "v3", false}, {"pack", "v1", true}, {"logo", "v1", true}, {"logo", "v2", false}, {"badge", "v1", false}} {
		rec, err := s.Artifact(ctx, co, "p", v.id, v.version)
		if err != nil || (rec.ArchivedAt > 0) != v.archived || v.archived && rec.ArchivedReason != "retention: 7 days" {
			t.Fatalf("%s %s archived_at=%d reason=%q %v", v.id, v.version, rec.ArchivedAt, rec.ArchivedReason, err)
		}
	}
	if ok, _ := s.Blobs.Exists(ctx, co, sheetV1.SHA256); ok {
		t.Fatal("content only an archived version used is still stored")
	}
	if ok, _ := s.Blobs.Exists(ctx, co, shared.SHA256); !ok {
		t.Fatal("content a live version uses was deleted")
	}
	// Reads of archived content answer ArchivedError with the latest version; the listing keeps the rows.
	var gone *ArchivedError
	if _, err = s.ArtifactContent(ctx, co, "p", "sheet", "v1"); !errors.As(err, &gone) || gone.Latest != "v3" || gone.Reason != "retention: 7 days" {
		t.Fatal("archived content read", err)
	}
	if err = s.CopyArchiveEntry(ctx, co, "p", "pack", "v1", "a.png", &bytes.Buffer{}); !errors.As(err, &gone) {
		t.Fatal("archived archive entry read", err)
	}
	if _, _, err = s.ProjectBlob(ctx, co, "p", sheetV1.SHA256); !errors.As(err, &gone) {
		t.Fatal("archived blob read by a run", err)
	}
	if list, _ := s.Artifacts(ctx, co, "p"); len(list) != 8 {
		t.Fatal("listing lost versions", len(list))
	}
	if rec, _ := s.Artifact(ctx, co, "p", "pack", "v1"); len(rec.ArchiveImages) != 1 {
		t.Fatal("archive listing of an archived version lost", rec.ArchiveImages)
	}
	// An archived version can be neither required nor submitted for approval.
	if _, err = s.CreateTask(ctx, co, tasks.Create{ProjectID: "p", Title: "uses v1", RequiredArtifacts: []tasks.Artifact{{ID: "sheet", Version: "v1", URI: UploadURI("sheet", "v1"), SHA256: sheetV1.SHA256}}}); !errors.Is(err, ErrInput) {
		t.Fatal("archived version required", err)
	}
	if _, err = s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "logo", ArtifactVersion: "v1", Agent: "demo", Reason: "old", RequestID: "old"}); !errors.Is(err, ErrInput) {
		t.Fatal("archived version submitted for approval", err)
	}
	// One feed event per run; a second run finds nothing and records none.
	items, err := s.Feed(ctx, co, FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, it := range items.Items {
		if it.Type == "artifact.retention" {
			events++
		}
	}
	if events != 1 {
		t.Fatal("retention feed events", events)
	}
	if again, err := s.ArchiveExpiredArtifacts(ctx, co); err != nil || again.Total() != 0 || again.Deleted != 0 {
		t.Fatal("second run", again, err)
	}
	// The same content uploaded again restores the archived version.
	put([]byte("sheet v1"))
	if rec := reg("sheet", "v1", "sheet.png", sheetV1); rec.ArchivedAt != 0 {
		t.Fatal("re-upload did not restore", rec)
	}
	if _, err = s.ArtifactContent(ctx, co, "p", "sheet", "v1"); err != nil {
		t.Fatal("restored version", err)
	}
}

// TestArtifactRetentionKeepsOpenInputs: a version an open task requires is not archived, however old.
func TestArtifactRetentionKeepsOpenInputs(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "art", ProjectID: "p", Title: "art", Assignee: "demo", RequestID: "art"}); err != nil {
		t.Fatal(err)
	}
	var refs []tasks.Artifact
	for _, v := range []string{"v1", "v2"} {
		b, err := s.PutBlob(ctx, co, bytes.NewReader([]byte("map "+v)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: "map", Version: v, URI: UploadURI("map", v), SHA256: b.SHA256}, TaskID: "art", Actor: "owner", RequestID: v})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, rec.Artifact)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ProjectID: "p", Title: "compare", RequiredArtifacts: refs[:1]}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(ctx, co, "p")
	if _, err := s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ArtifactRetentionDays: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(48 * time.Hour)
	if run, err := s.ArchiveExpiredArtifacts(ctx, co); err != nil || run.Total() != 0 {
		t.Fatal("required input archived", run, err)
	}
}

func ptr[T any](v T) *T { return &v }
