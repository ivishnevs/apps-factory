package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// runtimeHarness is a Core on the repository's catalog with a fake clock.
type runtimeHarness struct {
	t    *testing.T
	c    *Core
	s    *storage.Store
	fake *clock.Fake
}

func newRuntimeHarness(t *testing.T) *runtimeHarness {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: catalog.Agents, Skills: catalog.Skills, Token: "owner-secret"}
	c.Handler()
	if _, err = s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-runtime"}); err != nil {
		t.Fatal(err)
	}
	return &runtimeHarness{t: t, c: c, s: s, fake: fake}
}

func (h *runtimeHarness) call(method, path string, body any, v any) (int, string) {
	h.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+h.c.Token)
	w := httptest.NewRecorder()
	h.c.Handler().ServeHTTP(w, r)
	if v != nil {
		_ = json.Unmarshal(w.Body.Bytes(), v)
	}
	return w.Code, w.Body.String()
}

func (h *runtimeHarness) create(id, assignee string) tasks.Task {
	h.t.Helper()
	// One project per task: tasks of a project without worktrees share its workspace, one run at a time.
	if _, err := h.s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "p-" + id, Name: id, Workspace: "/tmp/core-runtime-" + id}); err != nil {
		h.t.Fatal(err)
	}
	v, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "p-" + id, Title: id, Assignee: assignee, Ready: true, RequestID: "create-" + id})
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *runtimeHarness) task(id string) tasks.Task {
	h.t.Helper()
	v, err := h.s.Task(context.Background(), storage.DefaultCompany, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// run is the task's current run.
func (h *runtimeHarness) run(task string) storage.Run {
	h.t.Helper()
	r, err := h.s.Get(context.Background(), storage.DefaultCompany, h.task(task).CurrentRunID)
	if err != nil {
		h.t.Fatal(task, err)
	}
	return r
}

func (h *runtimeHarness) feed(kind string) []storage.FeedItem {
	h.t.Helper()
	page, err := h.s.Feed(context.Background(), storage.DefaultCompany, storage.FeedFilter{}, 0, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []storage.FeedItem
	for _, item := range page.Items {
		if item.Type == kind {
			out = append(out, item)
		}
	}
	return out
}

// TestSkillHashesPinnedOnTheRun (runtime-drivers 2.3): Core records the content hash of every offered skill
// in the run's agent snapshot (skill_hashes), computed with skills.Hash over Core's registry.
func TestSkillHashesPinnedOnTheRun(t *testing.T) {
	h := newRuntimeHarness(t)
	h.create("t", "demo")
	if err := h.c.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := h.run("t")
	var snap struct {
		SkillHashes map[string]string `json:"skill_hashes"`
	}
	if err := json.Unmarshal(run.Spec.ConfigSnapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if len(run.Spec.Skills) == 0 || len(snap.SkillHashes) != len(run.Spec.Skills) {
		t.Fatalf("offered %v, pinned %v", run.Spec.Skills, snap.SkillHashes)
	}
	for _, name := range run.Spec.Skills {
		dir, err := h.c.Skills.Dir(name)
		if err != nil {
			t.Fatal(err)
		}
		want, err := skills.Hash(dir)
		if err != nil || snap.SkillHashes[name] != want || !strings.HasPrefix(want, skills.HashPrefix) {
			t.Fatalf("%s pinned %q, content %q (%v)", name, snap.SkillHashes[name], want, err)
		}
	}
	// The live agent config is unchanged: only the run's snapshot carries the pins.
	if len(h.c.Agents["demo"].SkillHashes) != 0 {
		t.Fatal("pins leaked into the catalog")
	}
	// Runs created over the API carry them too.
	var spec protocol.RunSpec
	if code, body := h.call("POST", "/runs", map[string]any{"agent": "demo", "project": "adhoc", "workspace": "/tmp/core-runtime-adhoc", "prompt": "x"}, &spec); code != 201 || !strings.Contains(string(spec.ConfigSnapshot), `"skill_hashes"`) {
		t.Fatal(code, body)
	}
}

// TestTaskModelOverride (runtime-drivers 2.7): an authority sets a task's model; one that does not fit the
// assignee's provider is refused; the override reaches runtime_options; after a reassignment to another
// provider it is ignored (the agent's model runs) with one feed event per run.
func TestTaskModelOverride(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	set := func(task, actor string, body map[string]any) (int, string) {
		t.Helper()
		body["version"] = h.task(task).Version
		body["request_id"] = protocol.ID()
		if actor != "" {
			body["actor"] = actor
		}
		return h.call("PATCH", "/tasks/"+task, body, nil)
	}
	h.create("m", "demo") // codex
	if code, body := set("m", "", map[string]any{"model": "opus"}); code != 400 || !strings.Contains(body, "does not fit provider codex") {
		t.Fatal("a Claude model accepted for a Codex assignee:", code, body)
	}
	if code, body := set("m", "claude-demo", map[string]any{"model": "gpt-5-codex"}); code != 403 {
		t.Fatal("a non-authority set the model:", code, body)
	}
	if code, body := set("m", "manager", map[string]any{"model": "gpt-5-codex"}); code != 200 || h.task("m").Model != "gpt-5-codex" {
		t.Fatal("the assignee's manager could not set the model:", code, body)
	}
	if code, body := set("m", "", map[string]any{"model": "o4-mini"}); code != 200 || h.task("m").Model != "o4-mini" {
		t.Fatal("the owner could not set the model:", code, body)
	}
	// Set together with a reassignment, the model is checked against the new assignee.
	if code, body := set("m", "", map[string]any{"assignee": "claude-demo", "model": "o4-mini"}); code != 400 {
		t.Fatal("model checked against the old assignee:", code, body)
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.run("m").Spec.RuntimeOptions["model"]; got != "o4-mini" {
		t.Fatal("override not in runtime_options:", got)
	}
	if len(h.feed("task.model_ignored")) != 0 {
		t.Fatal("a fitting override reported as ignored")
	}

	// A reassignment to a Claude agent leaves the Codex override behind: ignored, with an event.
	h.create("r", "demo")
	if code, body := set("r", "", map[string]any{"model": "gpt-5-codex"}); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := set("r", "", map[string]any{"assignee": "claude-demo"}); code != 200 || h.task("r").Model != "gpt-5-codex" {
		t.Fatal("reassignment refused or dropped the override:", code, body)
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.run("r").Spec.RuntimeOptions["model"]; got != h.c.Agents["claude-demo"].Spec.Runtime.Model {
		t.Fatal("an override of another provider reached the run:", got)
	}
	ignored := h.feed("task.model_ignored")
	if len(ignored) != 1 || ignored[0].TaskID != "r" || ignored[0].RunID != h.run("r").Spec.ID || !strings.Contains(ignored[0].Summary, "provider claude") {
		t.Fatalf("%+v", ignored)
	}
	// "" clears the override.
	h.create("c", "claude-demo")
	if code, body := set("c", "", map[string]any{"model": "sonnet"}); code != 200 || h.task("c").Model != "sonnet" {
		t.Fatal(code, body)
	}
	if code, body := set("c", "", map[string]any{"model": ""}); code != 200 || h.task("c").Model != "" {
		t.Fatal(code, body)
	}
}

// TestQuotaKeyPausesOnlyItsAgents (runtime-drivers 3.2): a quota event on key A (codex-subscription) pauses
// the agents on A with reason paused:quota_key and opens one quota_paused escalation to the owner; agents on
// key B (claude-subscription) keep running. The escalation clears itself at the reset; answering resume lifts
// the pause at once.
func TestQuotaKeyPausesOnlyItsAgents(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("hit", "demo")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run := h.run("hit")
	lease, err := h.s.Claim(ctx, co, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset := h.fake.Now().Add(2 * time.Hour)
	for range 2 { // a runtime may report one limit twice
		if err = h.s.Apply(ctx, co, protocol.Wrap("agent.progress", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Quota: &protocol.Quota{ResetsAt: reset, Type: "five_hour"}})); err != nil {
			t.Fatal(err)
		}
	}
	if err = h.s.Apply(ctx, co, protocol.Wrap("agent.ended", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Status: "ENDED", Outcome: "paused_out", Result: "usage limit"})); err != nil {
		t.Fatal(err)
	}
	h.create("a2", "demo")       // key A
	h.create("b", "claude-demo") // key B
	h.create("b2", "calorie-qa") // key B, another agent
	var verdict tasks.Verdict
	h.call("GET", "/tasks/a2/why", nil, &verdict)
	if verdict.Reason != tasks.ReasonPaused+"quota_key:codex-subscription" {
		t.Fatalf("%+v", verdict)
	}
	h.call("GET", "/tasks/b/why", nil, &verdict)
	if strings.HasPrefix(verdict.Reason, tasks.ReasonPaused) {
		t.Fatalf("key B paused: %+v", verdict)
	}
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if h.task("a2").Status != "READY" || h.task("b").Status != "IN_PROGRESS" || h.task("b2").Status != "IN_PROGRESS" {
		t.Fatal("quota on key A: ", h.task("a2").Status, h.task("b").Status, h.task("b2").Status)
	}
	var open []storage.Escalation
	h.call("GET", "/escalations", nil, &open)
	if len(open) != 1 || open[0].Reason != storage.QuotaPausedReason || open[0].TaskID != "" || open[0].RefID != "codex-subscription" || open[0].Target != "owner" {
		t.Fatalf("%+v", open)
	}
	q, err := h.s.Message(ctx, co, open[0].MessageID)
	if err != nil || q.Status != "PENDING" || q.Recipient != "owner" || len(q.Questions) != 1 || strings.Join(q.Questions[0].Options, ",") != "wait,resume" {
		t.Fatalf("%+v %v", q, err)
	}

	// At the reset the pause ends and the escalation clears itself (its question is withdrawn).
	h.fake.Advance(2*time.Hour + time.Second)
	if err = h.s.ExpirePauses(ctx, co); err != nil {
		t.Fatal(err)
	}
	if q, _ = h.s.Message(ctx, co, q.ID); q.Status != "WITHDRAWN" {
		t.Fatal("question not withdrawn at the reset:", q.Status)
	}
	h.call("GET", "/escalations", nil, &open)
	if len(open) != 0 {
		t.Fatalf("%+v", open)
	}
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	// hit's trigger is older than a2's, so it runs first (trigger-log-scheduling D4).
	if h.task("hit").Status != "IN_PROGRESS" || h.task("a2").Status != "READY" {
		t.Fatal("key A not resumed at the reset:", h.task("hit").Status, h.task("a2").Status)
	}

	// A second limit opens a new escalation; the owner answers resume: the pause ends now.
	run = h.run("hit")
	if lease, err = h.s.Claim(ctx, co, run.Spec.ID); err != nil {
		t.Fatal(err)
	}
	if err = h.s.Apply(ctx, co, protocol.Wrap("agent.progress", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Quota: &protocol.Quota{ResetsAt: h.fake.Now().Add(5 * time.Hour)}})); err != nil {
		t.Fatal(err)
	}
	h.call("GET", "/escalations", nil, &open)
	if len(open) != 1 {
		t.Fatalf("%+v", open)
	}
	q, _ = h.s.Message(ctx, co, open[0].MessageID)
	if _, err = h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "resume", Answers: map[string]any{storage.EscalationAnswerID: "resume"}, RequestID: "resume-1"}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := h.s.Pauses(ctx, co); len(ps) != 0 {
		t.Fatalf("resume left the pause: %+v", ps)
	}
	h.call("GET", "/escalations", nil, &open)
	if len(open) != 0 {
		t.Fatalf("%+v", open)
	}
}

// TestDefaultQuotaKeyIsTheProviders (runtime-drivers D5): agents without spec.runtime.quota_key use their
// provider's key (here <provider>-default: no key declared for it), so one quota hit by one Codex agent pauses
// another Codex agent without an explicit key too, while a Claude agent without one keeps running.
func TestDefaultQuotaKeyIsTheProviders(t *testing.T) {
	h := newRuntimeHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	none := &config.Catalog{}
	for name, from := range map[string]string{"cx1": "demo", "cx2": "demo", "cl": "claude-demo"} {
		a := h.c.Agents[from]
		a.Metadata.Name = name
		a.Spec.Runtime.QuotaKey = ""
		a.Spec.Runtime.DefaultQuotaKey = none.DefaultQuotaKey(a.Spec.Runtime.Provider)
		h.c.Agents[name] = a
	}
	h.create("hit", "cx1")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run := h.run("hit")
	lease, err := h.s.Claim(ctx, co, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.Apply(ctx, co, protocol.Wrap("agent.progress", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Quota: &protocol.Quota{ResetsAt: h.fake.Now().Add(time.Hour)}})); err != nil {
		t.Fatal(err)
	}
	h.create("other-codex", "cx2")
	h.create("claude", "cl")
	var verdict tasks.Verdict
	h.call("GET", "/tasks/other-codex/why", nil, &verdict)
	if verdict.Reason != tasks.ReasonPaused+"quota_key:codex-default" {
		t.Fatalf("%+v", verdict)
	}
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if h.task("other-codex").Status != "READY" || h.task("claude").Status != "IN_PROGRESS" {
		t.Fatal("one Codex quota hit: ", h.task("other-codex").Status, h.task("claude").Status)
	}
}
