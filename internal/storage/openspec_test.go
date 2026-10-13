package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func specStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := engineStore(t)
	w := t.TempDir()
	// Project workspace is immutable through the API; this is a test fixture replacement.
	p, _ := s.Project(context.Background(), DefaultCompany, "p")
	p.Workspace = w
	b, _ := json.Marshal(p)
	if _, e := s.DB.Exec("UPDATE projects SET workspace=?,body=? WHERE id='p'", w, b); e != nil {
		t.Fatal(e)
	}
	if _, e := s.InitOpenSpec(context.Background(), DefaultCompany, "p", "owner", "", "init"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(w, "openspec/changes/feature"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w, "openspec/changes/feature/proposal.md"), []byte("# Unique product text - never store in SQLite\n"), 0644); e != nil {
		t.Fatal(e)
	}
	return s, w
}
func TestOpenSpecTaskLinksAndQueuedRecheck(t *testing.T) {
	s, w := specStore(t)
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
	// Idempotency returns the saved result even if the live file is temporarily absent.
	proposal := filepath.Join(w, "openspec/changes/feature/proposal.md")
	os.Rename(proposal, proposal+".held")
	if replay, e := s.CreateTask(ctx, DefaultCompany, in); e != nil || replay.ID != task.ID {
		t.Fatal(replay, e)
	}
	if _, e = s.TaskAction(ctx, DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(e, ErrInput) {
		t.Fatal("ready missing link", e)
	}
	os.Rename(proposal+".held", proposal)
	task = readyEngineTask(t, s, task)
	bad := "../escape"
	if _, e = s.UpdateTask(ctx, DefaultCompany, task.ID, tasks.Update{Version: task.Version, OpenSpecChange: &bad, RequestID: "bad-update"}); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	run := dispatchEngineTask(t, s, task, "spec-run")
	if run.OpenSpecChange != "feature" {
		t.Fatal(run)
	}
	os.Rename(proposal, proposal+".held")
	if _, e = s.Claim(ctx, DefaultCompany, run.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("claimed missing change", e)
	}
	current, _ := s.Task(ctx, DefaultCompany, task.ID)
	cancelled, _ := s.Get(ctx, DefaultCompany, run.ID)
	if current.Status != "BLOCKED" || !strings.HasPrefix(current.BlockReason, "openspec:") || current.RetryEligible || cancelled.Status != "CANCELLED" {
		t.Fatal(current, cancelled)
	}
	os.Rename(proposal+".held", proposal)
	current = readyEngineTask(t, s, current)
	next := dispatchEngineTask(t, s, current, "spec-next")
	finishEngineRun(t, s, next, "COMPLETED")
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
		t.Fatal("stored document content in SQLite")
	}
}
func TestOpenSpecInitializationWriterAndUnknownQuarantine(t *testing.T) {
	s, w := specStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "writer"))
	run := dispatchEngineTask(t, s, task, "writer-run")
	if _, e := s.InitOpenSpec(ctx, DefaultCompany, "p", "owner", "", "owner-active"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	lease, e := s.Claim(ctx, DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", run.ID, "agent-active"); e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(ctx, DefaultCompany, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "UNKNOWN"})); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitOpenSpec(ctx, DefaultCompany, "p", "owner", "", "owner-unknown"); !errors.Is(e, ErrConflict) {
		t.Fatal("unknown bypass", e)
	}
	if _, e = s.InitOpenSpec(ctx, DefaultCompany, "p", "demo", run.ID, "agent-unknown"); !errors.Is(e, ErrConflict) {
		t.Fatal("unknown agent bypass", e)
	}
	if e = s.Resolve(ctx, DefaultCompany, run.ID, "COMPLETED", "inspected workspace"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitOpenSpec(ctx, DefaultCompany, "p", "owner", "", "owner-resolved"); e != nil {
		t.Fatal(e)
	}
	s.OpenSpec = openspec.Access{HostRoot: "/different", LocalRoot: w}
	if _, e = s.InitOpenSpec(ctx, DefaultCompany, "p", "owner", "", "bad-mount"); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
}

func TestOpenSpecReadTelemetryHasMetadataWithoutDocument(t *testing.T) {
	s, _ := specStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "telemetry"))
	run := dispatchEngineTask(t, s, task, "telemetry-run")
	lease, e := s.Claim(ctx, DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	document, _ := json.Marshal(openspec.Document{Path: "changes/feature/proposal.md", SHA256: strings.Repeat("b", 64), Content: "PRIVATE_DOCUMENT_BODY", Bytes: 21, NextOffset: 21})
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
