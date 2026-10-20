package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

const proposalPath = "changes/feature/proposal.md"

// putSpec writes path as the owner over whatever is current.
func putSpec(t *testing.T, s *Store, project, path, content string) openspec.Written {
	t.Helper()
	ctx := context.Background()
	d, _, _, err := specDoc(ctx, s.DB, DefaultCompany, project, path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.WriteOpenSpec(ctx, DefaultCompany, project, OpenSpecChange{Path: path, Base: d.SHA256, Content: []byte(content), Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// dropSpec deletes path as the owner.
func dropSpec(t *testing.T, s *Store, project, path string) {
	t.Helper()
	d, _, _, err := specDoc(context.Background(), s.DB, DefaultCompany, project, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteOpenSpec(context.Background(), DefaultCompany, project, OpenSpecChange{Path: path, Base: d.SHA256, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
}

func specStore(t *testing.T) *Store {
	t.Helper()
	s := engineStore(t)
	if _, e := s.InitOpenSpec(context.Background(), DefaultCompany, "p", "owner", "", "init"); e != nil {
		t.Fatal(e)
	}
	putSpec(t, s, "p", proposalPath, "# Unique product text - kept as a blob, never in task records\n")
	return s
}

func TestOpenSpecTaskLinksAndQueuedRecheck(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	in := tasks.Create{ProjectID: "p", Title: "linked", Assignee: "demo", OpenSpecChange: "missing", RequestID: "create"}
	if _, e := s.CreateTask(ctx, DefaultCompany, in); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	in.OpenSpecChange = "feature"
	task, e := s.CreateTask(ctx, DefaultCompany, in)
	if e != nil {
		t.Fatal(e)
	}
	// Idempotency returns the saved result even if the proposal is temporarily absent.
	dropSpec(t, s, "p", proposalPath)
	if replay, e := s.CreateTask(ctx, DefaultCompany, in); e != nil || replay.ID != task.ID {
		t.Fatal(replay, e)
	}
	if _, e = s.TaskAction(ctx, DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(e, ErrInput) {
		t.Fatal("ready missing link", e)
	}
	putSpec(t, s, "p", proposalPath, "# Feature\n")
	task = readyEngineTask(t, s, task)
	bad := "../escape"
	if _, e = s.UpdateTask(ctx, DefaultCompany, task.ID, tasks.Update{Version: task.Version, OpenSpecChange: &bad, RequestID: "bad-update"}); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	run := dispatchEngineTask(t, s, task, "spec-run")
	if run.OpenSpecChange != "feature" {
		t.Fatal(run)
	}
	dropSpec(t, s, "p", proposalPath)
	if _, e = s.Claim(ctx, DefaultCompany, run.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("claimed missing change", e)
	}
	current, _ := s.Task(ctx, DefaultCompany, task.ID)
	cancelled, _ := s.Get(ctx, DefaultCompany, run.ID)
	if current.Status != "BLOCKED" || !strings.HasPrefix(current.BlockReason, "openspec:") || cancelled.Status != "ENDED" || cancelled.Outcome != "stale" {
		t.Fatal(current, cancelled)
	}
	putSpec(t, s, "p", proposalPath, "# Unique product text - kept as a blob, never in task records\n")
	current = readyEngineTask(t, s, current)
	next := dispatchEngineTask(t, s, current, "spec-next")
	finishEngineRun(t, s, next, "succeeded")
	current, _ = s.Task(ctx, DefaultCompany, task.ID)
	if current.Status != "IN_REVIEW" {
		t.Fatal(current)
	}
	var combined string
	rows, e := s.DB.Query("SELECT cast(body AS TEXT) FROM entity_events UNION ALL SELECT cast(response AS TEXT) FROM mutation_results UNION ALL SELECT cast(spec AS TEXT) FROM runs")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var b string
		rows.Scan(&b)
		combined += b
	}
	rows.Close()
	if strings.Contains(combined, "Unique product text") {
		t.Fatal("stored document content in task, run or event records")
	}
}

// TestOpenSpecReadsFromStorage (core-owned-project-data 1.2): list, paged read with digest continuation and
// check work from spec_documents and the blob store, with the old path rules and bounds; history is kept.
func TestOpenSpecReadsFromStorage(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	putSpec(t, s, "p", "specs/service/spec.md", "# Requirement\nПривет мир 漢字\n")
	putSpec(t, s, "p", "changes/alpha/proposal.md", "# Alpha\n")
	page, e := s.ListOpenSpec(ctx, DefaultCompany, "p", "changes", "", 2)
	if e != nil || len(page.Items) != 2 || !page.HasMore || page.Items[0].Name != "alpha" || page.Items[1].Name != "archive" {
		t.Fatal(page, e)
	}
	if page, e = s.ListOpenSpec(ctx, DefaultCompany, "p", "changes", page.NextAfter, 10); e != nil || len(page.Items) != 1 || page.Items[0].Name != "feature" {
		t.Fatal(page, e)
	}
	d, e := s.ReadOpenSpec(ctx, DefaultCompany, "p", "specs/service/spec.md", 0, 17, "")
	if e != nil || !d.HasMore {
		t.Fatal(d, e)
	}
	putSpec(t, s, "p", "specs/service/spec.md", "# Updated\n")
	if _, e = s.ReadOpenSpec(ctx, DefaultCompany, "p", "specs/service/spec.md", d.NextOffset, 17, d.SHA256); !errors.Is(e, openspec.ErrConflict) {
		t.Fatal(e)
	}
	if d, e = s.ReadOpenSpec(ctx, DefaultCompany, "p", "specs/service/spec.md", 0, 8192, ""); e != nil || d.Content != "# Updated\n" {
		t.Fatal(d, e)
	}
	if c, e := s.CheckOpenSpec(ctx, DefaultCompany, "p", "alpha"); e != nil || !c.Valid {
		t.Fatal(c, e)
	}
	for _, name := range []string{"../secret.md", "/etc/passwd", "notes.md"} {
		if _, e = s.ReadOpenSpec(ctx, DefaultCompany, "p", name, 0, 8192, ""); e == nil {
			t.Fatal("read", name)
		}
		if _, e = s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: name, Content: []byte("x"), Actor: "owner"}); !errors.Is(e, ErrInput) {
			t.Fatal("write", name, e)
		}
	}
	if _, e = s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: "specs/x/spec.md", Content: []byte{0xff}, Actor: "owner"}); !errors.Is(e, ErrInput) {
		t.Fatal("non UTF-8", e)
	}
	var versions int
	if e = s.DB.QueryRow("SELECT count(*) FROM spec_documents WHERE project_id='p' AND path='specs/service/spec.md'").Scan(&versions); e != nil || versions != 2 {
		t.Fatal(versions, e)
	}
	// A disabled project has no OpenSpec tree in Factory.
	p, _ := s.Project(ctx, DefaultCompany, "p")
	off := false
	if _, e = s.UpdateProject(ctx, DefaultCompany, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, OpenSpec: &tasks.ProjectOpenSpec{Enabled: &off}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadOpenSpec(ctx, DefaultCompany, "p", proposalPath, 0, 8192, ""); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
}

// TestOpenSpecCompareAndSet (core-owned-project-data 2.2): two writers from the same base, concurrently: one
// stores its version, the other gets a conflict naming the current digest; moves and deletes check digests too.
func TestOpenSpecCompareAndSet(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	base, _, _, _ := specDoc(ctx, s.DB, DefaultCompany, "p", proposalPath)
	results := make([]error, 2)
	written := make([]openspec.Written, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			written[i], results[i] = s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: proposalPath, Base: base.SHA256, Content: []byte("# Version " + string(rune('A'+i)) + "\n"), Actor: "owner"})
		}()
	}
	wg.Wait()
	var won, lost int
	for i, e := range results {
		var c *openspec.ConflictError
		switch {
		case e == nil:
			won = i
		case errors.As(e, &c):
			lost++
			if c.Current != written[1-i].SHA256 || c.Path != proposalPath {
				t.Fatal(c, written)
			}
		default:
			t.Fatal(e)
		}
	}
	if lost != 1 {
		t.Fatal(results)
	}
	// A retry of the winning write succeeds unchanged.
	if w, e := s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: proposalPath, Base: base.SHA256, Content: []byte("# Version " + string(rune('A'+won)) + "\n"), Actor: "owner"}); e != nil || !w.Unchanged {
		t.Fatal(w, e)
	}
	// A new document requires an empty base.
	if _, e := s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: "specs/new/spec.md", Base: base.SHA256, Content: []byte("x"), Actor: "owner"}); !errors.Is(e, openspec.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.DeleteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: proposalPath, Base: base.SHA256, Actor: "owner"}); !errors.Is(e, openspec.ErrConflict) {
		t.Fatal(e)
	}
	// Archive: the change directory moves below changes/archive; the active change is gone.
	putSpec(t, s, "p", "changes/feature/tasks.md", "- [x] done\n")
	moved, e := s.MoveOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{From: "changes/feature", Path: "changes/archive/2026-10-20-feature", Actor: "owner"})
	if e != nil || len(moved) != 2 || moved[0].From != proposalPath || moved[0].Path != "changes/archive/2026-10-20-feature/proposal.md" {
		t.Fatal(moved, e)
	}
	if _, e = s.CheckOpenSpec(ctx, DefaultCompany, "p", "feature"); e == nil {
		t.Fatal("archived change still active")
	}
	if _, e = s.MoveOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{From: "changes/archive/2026-10-20-feature", Path: "changes/archive/2026-10-20-feature", Actor: "owner"}); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	// Writes come from the owner or an active run of the project only.
	if _, e = s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: "specs/a/spec.md", Content: []byte("x"), Actor: "demo"}); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	if _, e = s.WriteOpenSpec(ctx, DefaultCompany, "p", OpenSpecChange{Path: "specs/a/spec.md", Content: []byte("x"), Actor: "demo", RunID: "no-such-run"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

// TestOpenSpecInitializationNeedsOwnerOrActiveRun: initialization creates nothing in storage; an agent may
// call it only inside its own active run.
func TestOpenSpecInitializationNeedsOwnerOrActiveRun(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "writer"))
	run := dispatchEngineTask(t, s, task, "writer-run")
	if v, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "owner", "", "owner-any-time"); e != nil || len(v.Directories) != 4 || v.Directories[3] != "openspec/changes/archive" {
		t.Fatal(v, e)
	}
	if _, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", run.ID, "agent-queued"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.Claim(ctx, DefaultCompany, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", run.ID, "agent-active"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("UPDATE runs SET lease_until=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", run.ID, "agent-expired"); !errors.Is(e, ErrConflict) {
		t.Fatal("expired lease", e)
	}
	if _, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", "", "agent-without-run"); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
}

func TestOpenSpecReadTelemetryHasMetadataWithoutDocument(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "telemetry"))
	run := dispatchEngineTask(t, s, task, "telemetry-run")
	lease, e := s.Claim(ctx, DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	document, _ := json.Marshal(openspec.Document{Path: proposalPath, SHA256: strings.Repeat("b", 64), Content: "PRIVATE_DOCUMENT_BODY", Bytes: 21, NextOffset: 21})
	data, _ := json.Marshal(map[string]any{"item": map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_openspec_read", "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": string(document)}}}}})
	event := protocol.Wrap("agent.progress", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Method: "item/completed", Data: data})
	if e = s.Apply(ctx, DefaultCompany, event); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(ctx, DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(events)
	if strings.Contains(string(b), "PRIVATE_DOCUMENT_BODY") || !strings.Contains(string(b), "document_body_omitted") || !strings.Contains(string(b), "proposal.md") {
		t.Fatal(string(b))
	}
}
