package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestArtifactApprovalIsVersionedAndSeparateFromTaskAcceptance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "db")
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	cat, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"designer", "qa"} {
		a := cat.Agents["demo"]
		a.Metadata.Name = name
		a.Spec.Role = name
		cat.Agents[name] = a
	}
	c := &Core{Store: s, Agents: cat.Agents, Token: "owner"}
	httpCall := func(auth, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	for _, id := range []string{"p", "other"} {
		if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/artifact-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	ref := tasks.Artifact{ID: "mockup", Version: "v1", URI: "git:design-commit/mockup.svg"}
	producer, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "Create mockup", Assignee: "designer"})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "QA approved mockup", Assignee: "qa", RequiredArtifacts: []tasks.Artifact{ref}})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err = s.TaskAction(ctx, storage.DefaultCompany, consumer.ID, tasks.Action{Version: consumer.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if consumer.Status != "BLOCKED" || consumer.Attempt != 0 {
		t.Fatal(consumer)
	}
	producer, err = s.TaskAction(ctx, storage.DefaultCompany, producer.ID, tasks.Action{Version: producer.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	producer, _ = s.Task(ctx, storage.DefaultCompany, producer.ID)
	lease, err := s.Claim(ctx, storage.DefaultCompany, producer.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	token := c.runToken(storage.DefaultCompany, producer.CurrentRunID, lease)
	tool := func(name string, args map[string]any) *httptest.ResponseRecorder {
		return httpCall(token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args})
	}
	register := map[string]any{"artifact": ref, "request_id": "register-v1"}
	denied := cat.Agents["designer"]
	denied.Spec.Tools.Deny = []string{"factory.artifact.register"}
	c.Agents["designer"] = denied
	if w := tool("factory_task_submit", map[string]any{"id": producer.ID, "version": producer.Version, "submission": tasks.Submission{Summary: "bypass", Artifacts: []tasks.Artifact{ref}}, "request_id": "bypass-register"}); w.Code != 403 {
		t.Fatal("submit bypasses registry deny", w.Code, w.Body.String())
	}
	denied.Spec.Tools.Deny = nil
	c.Agents["designer"] = denied
	if w := tool("factory_artifact_register", register); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := tool("factory_artifact_register", register); w.Code != 200 {
		t.Fatal("register replay", w.Code, w.Body.String())
	}
	wrong := ref
	wrong.URI = "git:other-commit/mockup.svg"
	if w := tool("factory_artifact_register", map[string]any{"artifact": wrong, "request_id": "rewrite"}); w.Code != 409 {
		t.Fatal("same version rewritten", w.Code, w.Body.String())
	}
	w := tool("factory_artifact_request_approval", map[string]any{"artifact_id": ref.ID, "artifact_version": ref.Version, "reason": "Approve exact mockup version", "request_id": "ask-v1"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var approval approvals.Approval
	json.Unmarshal(w.Body.Bytes(), &approval)
	if approval.Kind != "artifact" || approval.Artifact == nil || *approval.Artifact != ref {
		t.Fatal(approval)
	}
	if w = tool("factory_artifact_request_approval", map[string]any{"artifact_id": ref.ID, "artifact_version": ref.Version, "reason": "Duplicate", "request_id": "duplicate"}); w.Code != 409 {
		t.Fatal("duplicate active approval", w.Code)
	}
	if w = httpCall("owner", "POST", "/inbox/"+approval.MessageID+"/answer", map[string]any{"version": 1, "body": "yes", "request_id": "chat"}); w.Code != 400 {
		t.Fatal("chat approves content", w.Code)
	}
	if w = httpCall("owner", "POST", "/projects/other/artifacts", artifacts.Register{Artifact: ref, TaskID: producer.ID, RequestID: "cross"}); w.Code != 400 {
		t.Fatal("cross-project registration", w.Code, w.Body.String())
	}
	if w = tool("factory_task_submit", map[string]any{"id": producer.ID, "version": producer.Version, "submission": tasks.Submission{Summary: "mockup ready", Artifacts: []tasks.Artifact{ref}}, "request_id": "submit"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", producer.CurrentRunID, "", protocol.Event{RunID: producer.CurrentRunID, Lease: lease, Status: "ENDED", Outcome: "succeeded", SessionID: "designer-thread"})); err != nil {
		t.Fatal(err)
	}
	producer, _ = s.Task(ctx, storage.DefaultCompany, producer.ID)
	if producer.Status != "IN_REVIEW" {
		t.Fatal(producer)
	}
	producer, err = s.ReviewTask(ctx, storage.DefaultCompany, producer.ID, tasks.Review{Version: producer.Version, Accept: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	consumer, _ = s.Task(ctx, storage.DefaultCompany, consumer.ID)
	if consumer.Status != "BLOCKED" || consumer.Attempt != 0 {
		t.Fatal("task acceptance approved artifact", consumer)
	}
	// Persist pending approval and wait state across a Core/storage restart.
	s.DB.Close()
	s, err = storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c.Store = s
	approval, err = s.DecideApproval(ctx, storage.DefaultCompany, approval.ID, approvals.Decision{Version: 1, Action: "approve", Reason: "Reviewed v1", RequestID: "approve-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if approval.ExpiresAt != 0 {
		t.Fatal("approved content should not expire with inbox deadline", approval)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	consumer, _ = s.Task(ctx, storage.DefaultCompany, consumer.ID)
	run, _ := s.Get(ctx, storage.DefaultCompany, consumer.CurrentRunID)
	if run.Status != "QUEUED" || len(run.Spec.ApprovedArtifacts) != 1 || run.Spec.ApprovedArtifacts[0].Artifact != ref || run.Spec.ApprovedArtifacts[0].ApprovalID != approval.ID {
		t.Fatal(run)
	}
	// Project-scoped content decisions can be read by QA, although designer requested them.
	qaLease, err := s.Claim(ctx, storage.DefaultCompany, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token = c.runToken(storage.DefaultCompany, run.Spec.ID, qaLease)
	if w = tool("factory_approval_get", map[string]any{"approval_id": approval.ID}); w.Code != 200 {
		t.Fatal("QA cannot read content approval", w.Code, w.Body.String())
	}
	if err = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: qaLease, Status: "ENDED", Outcome: "succeeded", SessionID: "qa-thread"})); err != nil {
		t.Fatal(err)
	}
	v2 := ref
	v2.Version = "v2"
	v2.URI = "git:new-commit/mockup.svg"
	if _, err = s.RegisterArtifact(ctx, storage.DefaultCompany, "p", artifacts.Register{Artifact: v2, TaskID: producer.ID, Actor: "owner", RequestID: "register-v2"}); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "QA v2", Assignee: "qa", RequiredArtifacts: []tasks.Artifact{v2}})
	if err != nil {
		t.Fatal(err)
	}
	second, err = s.TaskAction(ctx, storage.DefaultCompany, second.ID, tasks.Action{Version: second.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "BLOCKED" {
		t.Fatal("v1 approval accepted v2", second)
	}
	a2, err := s.RequestArtifactApproval(ctx, storage.DefaultCompany, "p", artifacts.ApprovalRequest{ArtifactID: v2.ID, ArtifactVersion: v2.Version, Agent: "designer", Reason: "review v2", RequestID: "ask-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideApproval(ctx, storage.DefaultCompany, a2.ID, approvals.Decision{Version: 1, Action: "reject", Reason: "Revise layout", RequestID: "reject-v2"}); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ = s.Task(ctx, storage.DefaultCompany, second.ID)
	if second.Status != "BLOCKED" || second.Attempt != 0 {
		t.Fatal(second)
	}
	// URI identity is also enforced when dependencies or structured submissions are updated.
	refs := []tasks.Artifact{wrong}
	if _, err = s.UpdateTask(ctx, storage.DefaultCompany, second.ID, tasks.Update{Version: second.Version, RequiredArtifacts: &refs}); !errors.Is(err, storage.ErrInput) {
		t.Fatal("conflicting reference", err)
	}
	a2, err = s.RequestArtifactApproval(ctx, storage.DefaultCompany, "p", artifacts.ApprovalRequest{ArtifactID: v2.ID, ArtifactVersion: v2.Version, Agent: "designer", Reason: "Owner reconsideration", RequestID: "reconsider"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err = s.DecideApproval(ctx, storage.DefaultCompany, a2.ID, approvals.Decision{Version: 1, Action: "approve", Reason: "Reviewed unchanged v2", RequestID: "approve-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ = s.Task(ctx, storage.DefaultCompany, second.ID)
	queued, _ := s.Get(ctx, storage.DefaultCompany, second.CurrentRunID)
	if queued.Status != "QUEUED" {
		t.Fatal(queued)
	}
	if _, err = s.DecideApproval(ctx, storage.DefaultCompany, a2.ID, approvals.Decision{Version: 2, Action: "revoke", Reason: "Stop future starts", RequestID: "revoke-v2"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, storage.DefaultCompany, queued.Spec.ID); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("revoked queued run claimed", err)
	}
	queued, _ = s.Get(ctx, storage.DefaultCompany, queued.Spec.ID)
	second, _ = s.Task(ctx, storage.DefaultCompany, second.ID)
	if queued.Status != "ENDED" || second.Status != "BLOCKED" {
		t.Fatal(queued.Status, second)
	}
	// A content grant cannot satisfy a policy requirement for an arbitrary action call.
	if _, err = s.CallApproval(ctx, storage.DefaultCompany, "p", "designer", producer.ID, factorytools.Call{}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestArtifactWaitersPreserveManualBlocksQuarantineAndDeadlines(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	cat, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: cat.Agents}
	s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/artifact-waits"})
	producer, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "producer", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	ref := tasks.Artifact{ID: "layout", Version: "1", URI: "sha256:0123"}
	s.RegisterArtifact(ctx, storage.DefaultCompany, "p", artifacts.Register{Artifact: ref, TaskID: producer.ID, Actor: "owner", RequestID: "register"})
	a, err := s.RequestArtifactApproval(ctx, storage.DefaultCompany, "p", artifacts.ApprovalRequest{ArtifactID: ref.ID, ArtifactVersion: ref.Version, Agent: "demo", Reason: "review", RequestID: "ask", TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.ExpiresAt = time.Now().Unix() - 1
	b, _ := json.Marshal(a)
	s.DB.Exec("UPDATE approvals SET body=? WHERE id=?", b, a.ID)
	if err = s.ExpireApprovals(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Approval(ctx, storage.DefaultCompany, a.ID)
	if a.Status != "EXPIRED" {
		t.Fatal(a)
	}
	a, err = s.RequestArtifactApproval(ctx, storage.DefaultCompany, "p", artifacts.ApprovalRequest{ArtifactID: ref.ID, ArtifactVersion: ref.Version, Agent: "demo", Reason: "new review", RequestID: "ask-again"})
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.DecideApproval(ctx, storage.DefaultCompany, a.ID, approvals.Decision{Version: 1, Action: "approve", Reason: "yes", RequestID: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "manual", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{ref}})
	if err != nil {
		t.Fatal(err)
	}
	manual, err = s.TaskAction(ctx, storage.DefaultCompany, manual.ID, tasks.Action{Version: manual.Version, Action: "block", Reason: "Owner pause"})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "unknown", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{ref}, SideEffects: true}) // inspected, not retried
	if err != nil {
		t.Fatal(err)
	}
	unknown, err = s.TaskAction(ctx, storage.DefaultCompany, unknown.ID, tasks.Action{Version: unknown.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	unknown, _ = s.Task(ctx, storage.DefaultCompany, unknown.ID)
	s.Claim(ctx, storage.DefaultCompany, unknown.CurrentRunID)
	s.DB.Exec("UPDATE runs SET lease_until=0 WHERE id=?", unknown.CurrentRunID)
	if err = s.Expire(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	unknown, _ = s.Task(ctx, storage.DefaultCompany, unknown.ID)
	manual, _ = s.Task(ctx, storage.DefaultCompany, manual.ID)
	lostRun := unknown.CurrentRunID
	// Held by its inspection escalation: reads present BLOCKED with the escalation's reason.
	if unknown.Status != "BLOCKED" || !strings.HasPrefix(unknown.BlockReason, "escalation: inspect: ") || manual.Status != "BLOCKED" || manual.BlockReason != "manual: Owner pause" {
		t.Fatal(unknown, manual)
	}
	if _, err = s.DecideApproval(ctx, storage.DefaultCompany, a.ID, approvals.Decision{Version: 2, Action: "revoke", Reason: "stop", RequestID: "revoke"}); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	// The inspection holds the side-effect task: nothing dispatched it again.
	unknown, _ = s.Task(ctx, storage.DefaultCompany, unknown.ID)
	run, _ := s.Get(ctx, storage.DefaultCompany, unknown.CurrentRunID)
	if unknown.CurrentRunID != lostRun || run.Status != "ENDED" || run.Outcome != "lost" {
		t.Fatal(run)
	}
}
