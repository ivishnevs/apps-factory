package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"appfactory/internal/artifacts"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func TestArtifactRegistrationPreservesLegacyReferencesAndTransactionality(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	_, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/legacy-artifacts"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Create(ctx, protocol.RunSpec{ID: "legacy", TaskID: task.ID, Project: "p", Agent: "demo", Workspace: "/tmp/legacy-artifacts"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO task_artifacts VALUES(?,?,?,?,?)", task.ID, "legacy", "layout", "1", "git:old:layout.svg"); err != nil {
		t.Fatal(err)
	}
	in := artifacts.Register{Artifact: tasks.Artifact{ID: "layout", Version: "1", URI: "git:new:layout.svg"}, TaskID: task.ID, Actor: "owner", RequestID: "bad"}
	if _, err = s.RegisterArtifact(ctx, "p", in); !errors.Is(err, ErrConflict) {
		t.Fatal("rewrote legacy version", err)
	}
	if _, err = s.Artifact(ctx, "p", "layout", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed registration left partial state", err)
	}
	in.Artifact.URI = "git:old:layout.svg"
	in.RequestID = "good"
	if _, err = s.RegisterArtifact(ctx, "p", in); err != nil {
		t.Fatal(err)
	}
	second := in.Artifact
	second.Version = "2"
	second.URI = "git:new:layout.svg"
	in.Artifact = second
	in.RequestID = "new-version"
	if _, err = s.RegisterArtifact(ctx, "p", in); err != nil {
		t.Fatal(err)
	}
	refs := []tasks.Artifact{{ID: "layout", Version: "1", URI: "git:old:layout.svg"}, second}
	compare, err := s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "Compare versions", Assignee: "qa", RequiredArtifacts: refs})
	if err != nil {
		t.Fatal(err)
	}
	compare, err = s.TaskAction(ctx, compare.ID, tasks.Action{Version: compare.Version, Action: "ready"})
	if err != nil || compare.Status != "BLOCKED" {
		t.Fatal(compare, err)
	}
	if _, err = s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "Duplicate", RequiredArtifacts: []tasks.Artifact{second, second}}); !errors.Is(err, ErrInput) {
		t.Fatal("duplicate version allowed", err)
	}
	var uri string
	if err = s.DB.QueryRow("SELECT uri FROM task_artifacts WHERE run_id='legacy'").Scan(&uri); err != nil || uri != "git:old:layout.svg" {
		t.Fatal(uri, err)
	}
}
