package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestDelegatedApprovalAttribution (approval-attribution): the decision of the agent an approval was delegated to
// reaches the requesting task, and the tasks that require the version, as that agent's input: its name as author,
// its rank (not the owner's 0), a text that names it and the delegation; the answer to the approval's question is
// its, not the owner's. The owner's own decision keeps the owner's authority (TestApprovalDecisionWakesRequester).
func TestDelegatedApprovalAttribution(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	delegate, setting := "manager", "team core: internal artifacts lead"
	h.create("draw", "notes", "demo")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "uses", ProjectID: "notes", Title: "uses", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{{ID: "pouch-design", Version: "v1", URI: "file:///pouch-v1.png"}}, Ready: true, RequestID: "uses"}); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct{ action, status string }{{"reject", "rejected"}, {"approve", "approved"}} {
		v := []string{"v0", "v1"}[i]
		a := tasks.Artifact{ID: "pouch-design", Version: v, URI: "file:///pouch-" + v + ".png"}
		if _, err := h.s.RegisterArtifact(ctx, co, "notes", artifacts.Register{Artifact: a, TaskID: "draw", Actor: "owner", RequestID: "reg-" + v}); err != nil {
			t.Fatal(err)
		}
		r := h.start("draw")
		req, err := h.s.RequestArtifactApproval(ctx, co, "notes", artifacts.ApprovalRequest{ArtifactID: a.ID, ArtifactVersion: v, Agent: "demo", TaskID: "draw", RunID: r.id, Reason: "internal design review", RequestID: "ask-" + v,
			Audience: "internal", Delegate: delegate, Setting: setting})
		if err != nil {
			t.Fatal(err)
		}
		h.callOK(r, "factory_exit_wait", map[string]any{"reason": "design review", "after": "6h", "deadline": h.at(72 * time.Hour)})
		h.end(r, "succeeded", "accepted")
		before := h.lastTriggerSeq("draw")
		got, err := h.s.DecideApproval(ctx, co, req.ID, approvals.Decision{Version: req.Version, Action: c.action, Reason: "matches the brief", RequestID: "decide-" + v, DecidedBy: delegate, Cites: []string{"owner answer"}})
		if err != nil || got.DecidedBy != delegate {
			t.Fatal(got, err)
		}
		want := "artifact approval " + req.ID + " of pouch-design@" + v + " " + c.status + " by " + delegate + " (delegated: " + setting + "): matches the brief"
		tr := h.lastTrigger("draw")
		if tr.kind != "approval_decided" || !tr.authority || tr.actor != delegate || tr.body != want || strings.Contains(tr.body, "owner:") {
			t.Fatalf("%s: %+v", v, tr)
		}
		input, err := h.s.AuthorityInputSince(ctx, co, "draw", before)
		if err != nil || len(input) != 1 || input[0].Author != delegate || input[0].Rank == 0 || input[0].Text != want {
			t.Fatalf("%+v %v", input, err)
		}
		// The decision still wakes the requester from its wait.
		if h.parkStatus("draw", tasks.ParkWait) != "overridden" {
			t.Fatal(h.parkStatus("draw", tasks.ParkWait))
		}
		var sender, body string
		if err = h.s.DB.QueryRow("SELECT sender,body->>'body' FROM messages WHERE message_type='answer' AND (body->>'in_reply_to')=?", req.MessageID).Scan(&sender, &body); err != nil {
			t.Fatal(err)
		}
		if sender != delegate || !strings.Contains(body, "by "+delegate+" (delegated: "+setting+")") {
			t.Fatal(sender, body)
		}
		h.end(h.start(storage.ConversationID("notes", "demo")), "succeeded", "")
	}
	// The task that requires the approved version gets it as the delegate's input too.
	if tr := h.lastTrigger("uses"); tr.kind != "artifact_approved" || tr.actor != delegate || tr.body != "artifact pouch-design@v1 approved" {
		t.Fatalf("%+v", tr)
	}
}
