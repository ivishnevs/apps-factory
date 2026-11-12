package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/blobstore"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

func fullBrief(title string) *messaging.Brief {
	return &messaging.Brief{Title: title, Ask: "Approve " + title + "?", After: "the team builds on it", Recommendation: "approve"}
}

func topicOf(t *testing.T, s *Store, messageID string) string {
	t.Helper()
	m, err := s.Message(context.Background(), DefaultCompany, messageID)
	if err != nil {
		t.Fatal(err)
	}
	return m.TopicID
}

// TestPeopleGate (people-topics D1–D4): an agent's item for people needs its brief and nothing is sent without it;
// a version line keeps its topic and needs what changed; a delegated request has no topic until escalated; Core's
// escalations of one agent and reason share a topic; a notification joins its task's open topic only.
func TestPeopleGate(t *testing.T) {
	s := engineStore(t)
	if s.Blobs == nil {
		s.Blobs = &blobstore.Memory{}
	}
	ctx := context.Background()
	co := DefaultCompany
	for _, id := range []string{"art", "rework", "other"} {
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	version := func(task, id, v string) {
		t.Helper()
		blob, err := s.PutBlob(ctx, co, bytes.NewReader([]byte(id+v)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: id, Version: v, URI: UploadURI(id, v), SHA256: blob.SHA256, Name: id + ".png"}, TaskID: task, Actor: "owner", RequestID: id + v}); err != nil {
			t.Fatal(err)
		}
	}

	// Without a brief nothing is sent.
	version("art", "kf2", "v3")
	req := artifacts.ApprovalRequest{ArtifactID: "kf2", ArtifactVersion: "v3", Agent: "demo", TaskID: "art", Reason: "v3", RequestID: "a1", Strict: true}
	var be *BriefError
	if _, err := s.RequestArtifactApproval(ctx, co, "p", req); !errors.As(err, &be) || strings.Join(be.Missing, ",") != "title,ask,after,recommendation" {
		t.Fatalf("no brief: %v", err)
	}
	if list, _ := s.Approvals(ctx, co, "p"); len(list) != 0 {
		t.Fatalf("an approval was stored without its brief: %+v", list)
	}
	req.Brief = fullBrief("P3-9a key frames")
	a1, err := s.RequestArtifactApproval(ctx, co, "p", req)
	if err != nil {
		t.Fatal(err)
	}
	topic := topicOf(t, s, a1.MessageID)
	if topic == "" {
		t.Fatal("no topic")
	}
	tp, err := s.Topic(ctx, co, topic)
	if err != nil || tp.Brief.Title != "P3-9a key frames" || tp.Key != "line:kf2" {
		t.Fatalf("topic %+v %v", tp, err)
	}

	// The owner rejects v3; v4 comes from another task: it needs what changed and lands in the same topic.
	if _, err = s.DecideApproval(ctx, co, a1.ID, approvalsDecision(a1.Version, "reject", "no tap to play")); err != nil {
		t.Fatal(err)
	}
	version("rework", "kf2", "v4")
	req = artifacts.ApprovalRequest{ArtifactID: "kf2", ArtifactVersion: "v4", Agent: "demo", TaskID: "rework", Reason: "v4", RequestID: "a2", Strict: true, Note: &messaging.ItemNote{What: "kf2 lobby"}}
	if _, err = s.RequestArtifactApproval(ctx, co, "p", req); !errors.As(err, &be) || strings.Join(be.Missing, ",") != "note.changes" || !be.Joining {
		t.Fatalf("a seen line needs changes: %v", err)
	}
	req.Note.Changes = "illustrations for each mode"
	a2, err := s.RequestArtifactApproval(ctx, co, "p", req)
	if err != nil {
		t.Fatal(err)
	}
	if got := topicOf(t, s, a2.MessageID); got != topic {
		t.Fatalf("v4 opened topic %s, want %s", got, topic)
	}
	items, err := s.TopicItems(ctx, co, topic)
	if err != nil || len(items) != 2 || items[0].State != "replaced" || items[1].State != "open" || items[1].Note.Changes != "illustrations for each mode" {
		t.Fatalf("items %+v %v", items, err)
	}

	// A delegated request is not for people: no brief, no topic; escalating it needs the delegate's brief.
	version("other", "zones", "v1")
	d, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "zones", ArtifactVersion: "v1", Agent: "demo", TaskID: "other", Reason: "v1", RequestID: "d1", Strict: true, Delegate: "manager"})
	if err != nil || topicOf(t, s, d.MessageID) != "" {
		t.Fatalf("delegated: %v", err)
	}
	if _, err = s.EscalateApproval(ctx, co, d.ID, "manager", "the plan reserves it", EscalatePeople{Strict: true}); !errors.As(err, &be) {
		t.Fatalf("escalation without a brief: %v", err)
	}
	if _, err = s.EscalateApproval(ctx, co, d.ID, "manager", "the plan reserves it", EscalatePeople{Strict: true, Brief: fullBrief("Aim zones")}); err != nil {
		t.Fatal(err)
	}
	if topicOf(t, s, d.MessageID) == "" {
		t.Fatal("escalated approval has no topic")
	}

	// A notification joins its task's open topic; one of a task without a topic stays a chat message.
	n, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "other", Sender: "demo", Recipient: "owner", MessageType: "notification", Body: "zones v1 rendered", RequestID: "n1"})
	if err != nil || n.TopicID == "" || n.TopicID != topicOf(t, s, d.MessageID) {
		t.Fatalf("update: %+v %v", n, err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "quiet", ProjectID: "p", Title: "quiet", Assignee: "demo", RequestID: "quiet"}); err != nil {
		t.Fatal(err)
	}
	if n, err = s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "quiet", Sender: "demo", Recipient: "owner", MessageType: "notification", Body: "done", RequestID: "n2"}); err != nil || n.TopicID != "" {
		t.Fatalf("lone update got a topic: %+v %v", n, err)
	}

	// Core's escalations of one agent and reason share a topic, with Core's brief.
	for _, id := range []string{"e1", "e2"} {
		if _, err = s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: "conversation " + id, Assignee: "producer", RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SyncEscalations(ctx, co, "retries_exhausted", []EscalationWant{{TaskID: "e1", Summary: "3 attempts in a row without progress"}, {TaskID: "e2", Summary: "3 attempts in a row without progress"}}); err != nil {
		t.Fatal(err)
	}
	open, err := s.Escalations(ctx, co, true)
	if err != nil || len(open) != 2 {
		t.Fatalf("escalations %+v %v", open, err)
	}
	t1, t2 := topicOf(t, s, open[0].MessageID), topicOf(t, s, open[1].MessageID)
	if t1 == "" || t1 != t2 {
		t.Fatalf("escalations of one agent and reason in topics %q and %q", t1, t2)
	}
	if tp, _ = s.Topic(ctx, co, t1); !strings.HasPrefix(tp.Brief.Title, "producer: ") || !strings.Contains(tp.Brief.After, "retry: ") || tp.Key != "agent:producer:retries_exhausted" {
		t.Fatalf("Core brief %+v", tp)
	}
}

// TestPeopleGateIsTheOnlyWay (people-topics D1): a message is created in sendMessageTx and the reply path only, and
// its recipient changes only in forwardTx, so nothing reaches people around the gate.
func TestPeopleGateIsTheOnlyWay(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	inserts, moves := 0, 0
	insert := regexp.MustCompile(`INSERT INTO messages[ (]`)
	move := regexp.MustCompile(`UPDATE messages SET[^"]*recipient=`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inserts += len(insert.FindAll(b, -1))
		moves += len(move.FindAll(b, -1))
	}
	if inserts != 2 || moves != 1 {
		t.Fatalf("messages inserted in %d places (want sendMessageTx and the reply), recipient moved in %d (want forwardTx): route the new path through toPeople", inserts, moves)
	}
}

func approvalsDecision(version int, action, reason string) approvals.Decision {
	return approvals.Decision{Version: version, Action: action, Reason: reason, RequestID: action + "-" + reason}
}

// TestTopicDecideAndComposites (people-topics D5, D11, D13): "approve all" decides the topic's approvals together or
// not at all; a composite goes when a part is rejected; a bringer moves an item into a topic of its own.
func TestTopicDecideAndComposites(t *testing.T) {
	s := engineStore(t)
	if s.Blobs == nil {
		s.Blobs = &blobstore.Memory{}
	}
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "kf", ProjectID: "p", Title: "key frames", Assignee: "demo", RequestID: "kf"}); err != nil {
		t.Fatal(err)
	}
	reg := func(id string, parts ...artifacts.Part) {
		t.Helper()
		blob, err := s.PutBlob(ctx, co, bytes.NewReader([]byte(id)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: id, Version: "v1", URI: UploadURI(id, "v1"), SHA256: blob.SHA256, Name: id + ".png"}, TaskID: "kf", Actor: "owner", RequestID: "r-" + id, Includes: parts}); err != nil {
			t.Fatal(err)
		}
	}
	reg("kf1")
	reg("kf2")
	if _, err := s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: "sheet", Version: "v1", URI: "https://example.com/sheet.png"}, TaskID: "kf", Actor: "owner", RequestID: "bad", Includes: []artifacts.Part{{ID: "kf9", Version: "v1"}}}); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown part: %v", err)
	}
	reg("sheet", artifacts.Part{ID: "kf1", Version: "v1"}, artifacts.Part{ID: "kf2", Version: "v1"})
	ids := map[string]string{}
	for _, id := range []string{"kf1", "kf2", "sheet"} {
		a, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: id, ArtifactVersion: "v1", Agent: "demo", TaskID: "kf", Reason: id, RequestID: "a-" + id, Delegate: "manager"})
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = a.ID
	}
	out, err := s.EscalateApprovals(ctx, co, []string{ids["kf1"], ids["kf2"], ids["sheet"]}, "manager", "the plan reserves it", "esc", EscalatePeople{Strict: true, Brief: fullBrief("P3-9a key frames")}, nil)
	if err != nil || len(out) != 3 {
		t.Fatalf("batch escalate %v", err)
	}
	a1, _ := s.Approval(ctx, co, ids["kf1"])
	topic := topicOf(t, s, a1.MessageID)
	v, err := s.PeopleTopic(ctx, co, topic)
	if err != nil || v.Waiting != 3 || len(v.Items) != 3 || v.Items[0].Actions[0] != "approve" || len(v.Tasks) != 1 {
		t.Fatalf("view %+v %v", v, err)
	}

	// All or none: one item that cannot be decided leaves the others pending.
	a2, _ := s.Approval(ctx, co, ids["kf2"])
	if _, err = s.DecideTopic(ctx, co, topic, "all-bad", []TopicDecision{{Kind: "approval", Ref: a1.ID, Version: a1.Version, Action: "approve"}, {Kind: "approval", Ref: a2.ID, Version: a2.Version + 7, Action: "approve"}}); err == nil {
		t.Fatal("a stale version decided")
	}
	if a1, _ = s.Approval(ctx, co, ids["kf1"]); a1.Status != "PENDING" {
		t.Fatalf("partial decision: %s", a1.Status)
	}
	if _, err = s.DecideTopic(ctx, co, topic, "rej", []TopicDecision{{Kind: "approval", Ref: a2.ID, Version: a2.Version, Action: "reject"}}); !errors.Is(err, ErrInput) {
		t.Fatalf("a rejection without its reason: %v", err)
	}
	// Rejecting kf2 withdraws the contact sheet; approving kf1 settles the topic.
	if _, err = s.DecideTopic(ctx, co, topic, "mixed", []TopicDecision{{Kind: "approval", Ref: a1.ID, Version: a1.Version, Action: "approve"}, {Kind: "approval", Ref: a2.ID, Version: a2.Version, Action: "reject", Reason: "redo the lobby"}}); err != nil {
		t.Fatal(err)
	}
	sheet, _ := s.Approval(ctx, co, ids["sheet"])
	if sheet.Status != "SUPERSEDED" {
		t.Fatalf("composite %s after its part was rejected", sheet.Status)
	}
	if v, _ = s.PeopleTopic(ctx, co, topic); v.Open || v.Waiting != 0 {
		t.Fatalf("topic still open: %+v", v)
	}

	// Moving an item opens a topic of its own that needs a brief.
	moved, err := s.BriefTopic(ctx, co, topic, "manager", "mv", nil, nil, []string{ids["sheet"]})
	if err != nil || len(moved.Items) != 2 {
		t.Fatalf("after move %+v %v", moved, err)
	}
	if _, err = s.BriefTopic(ctx, co, topic, "manager", "b1", &messaging.Brief{After: "x"}, nil, nil); err != nil {
		t.Fatalf("a partial brief merged into a full one: %v", err)
	}
}
