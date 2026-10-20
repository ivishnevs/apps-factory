package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/policies"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Event feed (waits-events-cost-cli 2.1, 2.3, 2.4).

func (h *exitHarness) feedOf(task string) []storage.FeedItem {
	h.t.Helper()
	page, err := h.s.Feed(context.Background(), storage.DefaultCompany, storage.FeedFilter{Task: task}, 0, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return page.Items
}

func lastOf(items []storage.FeedItem, kind string) (storage.FeedItem, bool) {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type == kind {
			return items[i], true
		}
	}
	return storage.FeedItem{}, false
}

func (h *exitHarness) mcp(r exitRun, name string, args map[string]any) map[string]any {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
	if w.Code != 200 {
		h.t.Fatal(name, w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v
}

// TestFeedCauses: exits, run outcomes with the retry time, directives and escalations are feed rows written
// with their cause; routine handoffs need no attention, an escalation to the owner does.
func TestFeedCauses(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("f", "notes", "demo")

	// A failed run: one event naming the task, the outcome and the retry time.
	r := h.start("f")
	h.end(r, "failed", "none")
	retry, ok := lastOf(h.feedOf("f"), "task.retry")
	if !ok || !strings.Contains(retry.Summary, "f: run "+r.id+" ended failed; retry in 1m0s") || retry.Attention {
		t.Fatalf("%+v", retry)
	}
	h.fake.Advance(time.Minute)

	// A routine handoff: an exit event without attention.
	r = h.start("f")
	h.mcp(r, "factory_exit_handoff", map[string]any{"to": "reviewer", "note": "please check the copy"})
	h.end(r, "succeeded", "accepted")
	exit, ok := lastOf(h.feedOf("f"), "task.exit.handoff")
	if !ok || exit.Summary != "demo → reviewer (handoff): please check the copy" || exit.Attention || exit.RunID != r.id {
		t.Fatalf("%+v", exit)
	}

	// Authority input: the directive trigger.
	if _, err := h.s.Directive(ctx, storage.DefaultCompany, "f", "owner", "ship today", false, "d1"); err != nil {
		t.Fatal(err)
	}
	if d, ok := lastOf(h.feedOf("f"), "task.trigger.directive"); !ok || d.Summary != "ship today" {
		t.Fatalf("%+v", d)
	}

	// An escalation: attention only when it reaches the owner.
	h.create("s", "notes", "demo")
	if err := h.s.SyncEscalations(ctx, storage.DefaultCompany, "stalled", []storage.EscalationWant{{TaskID: "s", RefID: "x", Summary: "waits on x", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	if e, ok := lastOf(h.feedOf("s"), "task.escalation.stalled"); !ok || !e.Attention {
		t.Fatalf("%+v", e)
	}
}

// TestAttentionTable: the unconditional attention kinds are marked whatever the cause passes, other kinds
// keep the cause's decision.
func TestAttentionTable(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	want := map[string]bool{
		"node.low_disk": true, "node.disk_critical": true, "node.restart_loop": true, "node.check_failed": true,
		"node.heartbeat_stalled": true, "driver_unsupported": true, "quota.paused": true, "task.alert.no_progress": true,
		"task.alert.idle": true, "approval.requested": true, "task.unmerged_branch": true,
		"node.registered": false, "node.online": false, "node.disk_ok": false, "task.exit.submitted": false, "task.exit.done": false,
		"task.exit.handoff": false, "task.retry": false, "task.trigger.wait_ended": false, "report.missing": false, "owner.resumed": false,
	}
	for kind := range want {
		if err := h.s.AddFeed(ctx, storage.DefaultCompany, storage.FeedItem{EventID: "e-" + kind, Type: kind, EntityID: "x", Summary: kind}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range page.Items {
		if w, ok := want[f.Type]; ok && f.EntityID == "x" {
			seen++
			if f.Attention != w {
				t.Errorf("%s: attention %v, want %v", f.Type, f.Attention, w)
			}
		}
	}
	if seen != len(want) {
		t.Fatal(seen, len(want))
	}
	for kind := range storage.AttentionKinds {
		if !want[kind] {
			t.Errorf("attention kind %s not in the table test", kind)
		}
	}
}

// TestFeedListSubtree: a lead lists only events of tasks assigned within, or delegated by, its subtree.
func TestFeedListSubtree(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	for id, assignee := range map[string]string{"sibling": "demo", "mine": "calorie-ios"} {
		if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "notes", Title: id, Assignee: assignee, RequestID: "c-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	h.create("lead", "notes", "calorie-manager")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "delegated", ProjectID: "notes", Title: "delegated", Assignee: "demo", RequestID: "c-delegated", OriginTask: "lead"}); err != nil {
		t.Fatal(err)
	}
	r := h.start("lead")
	v := h.mcp(r, "factory_feed_list", map[string]any{"limit": 50})
	items, _ := v["items"].([]any)
	got := map[string]bool{}
	for _, it := range items {
		task, _ := it.(map[string]any)["task_id"].(string)
		got[task] = true
	}
	if !got["mine"] || !got["lead"] || !got["delegated"] || got["sibling"] {
		t.Fatalf("%v", got)
	}
	// The owner reads any subtree over HTTP.
	w := h.do(h.c.Token, "GET", "/feed?subtree=manager&limit=100", nil, nil)
	if body := w.Body.String(); !strings.Contains(body, `"task_id":"sibling"`) || strings.Contains(body, `"task_id":"mine"`) {
		t.Fatal(body)
	}
}

// TestReportingPolicyNeverBlocks: done without the report a reporting rule requires is accepted; the manager
// sees report.missing and the agent's next brief carries a reminder. With the report nothing is missing.
func TestReportingPolicyNeverBlocks(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	a := h.c.Agents["demo"]
	a.Rules = append(append([]policies.Rule{}, a.Rules...), policies.Rule{Name: "stage-reports", Type: "reporting", Exits: []string{"done"}, ReportKinds: []string{"stage_completed"}, Instruction: "Report every completed stage."})
	h.c.Agents["demo"] = a
	h.create("a", "notes", "demo")
	r := h.start("a")
	h.mcp(r, "factory_exit_done", map[string]any{"summary": "finished"})
	h.end(r, "succeeded", "accepted")
	if got := h.task("a"); got.Status != "DONE" {
		t.Fatalf("%+v", got)
	}
	missing, ok := lastOf(h.feedOf("a"), "report.missing")
	if !ok || !strings.Contains(missing.Summary, "for manager: demo exited done on a without a report of kind stage_completed") {
		t.Fatalf("%+v", missing)
	}
	// The manager's subtree feed has it.
	if w := h.do(h.c.Token, "GET", "/feed?subtree=manager&limit=100", nil, nil); !strings.Contains(w.Body.String(), "report.missing") {
		t.Fatal(w.Body.String())
	}
	h.create("b", "notes", "demo")
	r = h.start("b")
	run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id)
	if !strings.Contains(run.Spec.Prompt, "Reporting reminders:") || !strings.Contains(run.Spec.Prompt, "reporting rule stage-reports") {
		t.Fatal(run.Spec.Prompt)
	}
	h.mcp(r, "factory_report_create", map[string]any{"kind": "stage_completed", "summary": "stage b", "purpose": "track", "changes": []any{}, "result": "ok", "request_id": "rep-b"})
	h.mcp(r, "factory_exit_done", map[string]any{"summary": "finished b"})
	h.end(r, "succeeded", "accepted")
	if _, ok := lastOf(h.feedOf("b"), "report.missing"); ok {
		t.Fatal("report counted as missing")
	}
	// The reminder was shown once.
	h.create("c", "notes", "demo")
	r = h.start("c")
	if run, _ = h.s.Get(ctx, storage.DefaultCompany, r.id); strings.Contains(run.Spec.Prompt, "Reporting reminders:") {
		t.Fatal(run.Spec.Prompt)
	}
}
