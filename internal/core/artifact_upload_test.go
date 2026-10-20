package core

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/factorytools"
	"appfactory/internal/openspec"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestArtifactUploadBindsApprovalToContent (core-owned-project-data 2.4): the adapter uploads a worktree file
// as artifact version v1 (factory://artifacts/mockup/v1 with its digest); the owner approves v1. Other content
// under v1 is refused; a consumer that pins other content for v1 is not satisfied by the approval, one without
// a digest gets the approved content pinned and materialized under .factory/inputs.
func TestArtifactUploadBindsApprovalToContent(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "design/landing.svg"), "<svg>v1</svg>")
	ref := tasks.Artifact{ID: "mockup", Version: "v1", URI: "factory://artifacts/mockup/v1"}
	other := ref
	other.SHA256 = openspec.Digest([]byte("<svg>v1 changed</svg>"))
	// Declared before the upload: it pins content that v1 will not have.
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "qa-other", ProjectID: "notes", Title: "qa-other", Assignee: "reviewer", RequiredArtifacts: []tasks.Artifact{other}, Ready: true, RequestID: "create-qa-other"}); err != nil {
		t.Fatal(err)
	}
	h.create("design", "notes", "demo")
	r := h.start("design")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}
	upload := func(key string) (any, error) {
		return a.Call(ctx, factorytools.Call{Name: "factory_artifact_upload", Arguments: map[string]any{"artifact_id": "mockup", "artifact_version": "v1", "path": "design/landing.svg", "request_id": key}})
	}
	if _, err := upload("upload-v1"); err != nil {
		t.Fatal(err)
	}
	digest := openspec.Digest([]byte("<svg>v1</svg>"))
	record, err := h.s.Artifact(ctx, storage.DefaultCompany, "notes", "mockup", "v1")
	if err != nil || record.URI != "factory://artifacts/mockup/v1" || record.SHA256 != digest || record.Size != 13 || record.Name != "landing.svg" {
		t.Fatal(record, err)
	}
	// Paths outside the worktree are refused on the node.
	for _, p := range []string{"../escape.svg", "/etc/hosts"} {
		if _, err = a.Call(ctx, factorytools.Call{Name: "factory_artifact_upload", Arguments: map[string]any{"artifact_id": "x", "artifact_version": "v1", "path": p, "request_id": "esc" + p}}); err == nil {
			t.Fatal("uploaded", p)
		}
	}
	approval, err := h.s.RequestArtifactApproval(ctx, storage.DefaultCompany, "notes", artifacts.ApprovalRequest{ArtifactID: "mockup", ArtifactVersion: "v1", Agent: "demo", TaskID: "design", RunID: r.id, Reason: "review", RequestID: "ask-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.DecideApproval(ctx, storage.DefaultCompany, approval.ID, approvals.Decision{Version: 1, Action: "approve", Reason: "looks right", RequestID: "approve-v1"}); err != nil {
		t.Fatal(err)
	}
	// Same version label, other content: refused (it needs a new version, which the approval does not cover).
	writeFile(t, filepath.Join(work, "design/landing.svg"), "<svg>v1 changed</svg>")
	if _, err = upload("upload-v1-changed"); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatal(err)
	}
	// A factory:// URI cannot be registered without uploaded content.
	if _, err = h.s.RegisterArtifact(ctx, storage.DefaultCompany, "notes", artifacts.Register{Artifact: tasks.Artifact{ID: "fake", Version: "v1", URI: "factory://artifacts/fake/v1"}, TaskID: "design", Actor: "owner", RequestID: "fake"}); err == nil {
		t.Fatal("factory URI without content")
	}
	h.end(r, "succeeded", "none")

	if _, err = h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "qa", ProjectID: "notes", Title: "qa", Assignee: "reviewer", RequiredArtifacts: []tasks.Artifact{ref}, Ready: true, RequestID: "create-qa"}); err != nil {
		t.Fatal(err)
	}
	// Once v1 is registered, a reference to other content under v1 is refused outright.
	if _, err = h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "qa-late", ProjectID: "notes", Title: "qa-late", Assignee: "reviewer", RequiredArtifacts: []tasks.Artifact{other}, RequestID: "create-qa-late"}); !errors.Is(err, storage.ErrInput) {
		t.Fatal(err)
	}
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.task("qa-other"); v.CurrentRunID != "" || !strings.Contains(v.BlockReason, "artifact") {
		t.Fatalf("approval of v1 satisfied other content: %+v", v)
	}
	qa := h.task("qa")
	claim, err := h.s.ClaimRun(ctx, storage.DefaultCompany, qa.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := claim.RunSpec.ApprovedArtifacts
	if len(pinned) != 1 || pinned[0].Artifact.SHA256 != digest {
		t.Fatal(pinned)
	}
	m := claim.RunSpec.Materialize
	if m == nil || len(m.Files) != 1 || m.Files[0].Path != ".factory/inputs/mockup/v1/landing.svg" || m.Files[0].SHA256 != digest {
		t.Fatal(m)
	}
	// The owner downloads the approved content.
	w := h.do(h.c.Token, "GET", "/projects/notes/artifacts/mockup/v1/content", nil, nil)
	if w.Code != 200 || w.Body.String() != "<svg>v1</svg>" {
		t.Fatal(w.Code, w.Body.String())
	}
}
