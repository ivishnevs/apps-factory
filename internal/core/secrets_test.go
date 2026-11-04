package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/testnode"
)

// TestSecretRoutesAndWait (core-secret-store 1.2, 2.2): owners set, list (names, versions, used_by; never a value)
// and delete secrets; nothing works without a master key; another company's owner sees none of them; a task whose
// agent needs a missing secret waits (secret_missing) with no run and no attempt, and runs once the secret is set;
// POST /runs refuses a run whose secret is missing.
func TestSecretRoutesAndWait(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	dev := c.Agents["dev"]
	dev.Spec.Secrets = []config.SecretUse{{Name: "API_TOKEN", Env: "API_TOKEN"}, {Name: "SIGNING_KEY", Env: "SIGNING_KEY_PATH", File: true}}
	dev.Timeout = 60e9
	c.Agents["dev"] = dev
	put := func(name, kind, value string) (int, string) {
		return call(t, c, "PUT", "/secrets/"+name, c.Token, SecretPut{Kind: kind, Value: value})
	}
	if code, _ := put("API_TOKEN", "string", "token-value-123"); code != 503 {
		t.Fatal("set without a master key", code)
	}
	keys, _ := secrets.NewMasterKeys(bytes.Repeat([]byte{7}, secrets.KeySize), nil)
	s.Keys = keys

	// A ready task waits for its agent's secrets: no run, no attempt.
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "needs", ProjectID: "p", Title: "needs secrets", Assignee: "dev", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(ctx, co, "needs")
	snap, _ := s.TaskSnapshot(ctx, co, "needs")
	if v := c.evaluateIn(co, snap); task.CurrentRunID != "" || task.Attempt != 0 || v.Reason != tasks.ReasonSecretMissing || !strings.Contains(v.Detail, "API_TOKEN (dev)") {
		t.Fatalf("%+v %+v", task, v)
	}

	if code, out := put("API_TOKEN", "string", "short"); code != 400 {
		t.Fatal("short string", code, out)
	}
	if code, out := put("API_TOKEN", "string", "token-value-123"); code != 200 || strings.Contains(out, "token-value") {
		t.Fatal(code, out)
	}
	// Still waiting: the file secret is missing; a string under its name is the wrong kind.
	if code, out := put("SIGNING_KEY", "string", "not-a-file-value"); code != 200 {
		t.Fatal(code, out)
	}
	snap, _ = s.TaskSnapshot(ctx, co, "needs")
	if v := c.evaluateIn(co, snap); v.Reason != tasks.ReasonSecretMissing || !strings.Contains(v.Detail, "SIGNING_KEY") {
		t.Fatalf("%+v", v)
	}
	if code, out := put("SIGNING_KEY", "file", base64.StdEncoding.EncodeToString([]byte("-----BEGIN KEY-----\nfile-secret-body\n"))); code != 200 {
		t.Fatal(code, out)
	}
	code, out := call(t, c, "GET", "/secrets", c.Token, nil)
	if code != 200 || strings.Contains(out, "token-value") || strings.Contains(out, "file-secret-body") || !strings.Contains(out, `"used_by":["agent dev"]`) || !strings.Contains(out, `"version":2`) {
		t.Fatal(code, out)
	}
	if code, out = call(t, c, "GET", "/secrets/needed", c.Token, nil); code != 200 || !strings.Contains(out, `"name":"SIGNING_KEY","kind":"file","set":true`) {
		t.Fatal(code, out)
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ = s.Task(ctx, co, "needs"); task.CurrentRunID == "" || task.Attempt != 1 {
		t.Fatalf("not dispatched once the secrets were set: %+v", task)
	}

	// Another company's owner sees and touches nothing of this company.
	if err := s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	issued, err := s.CreateOwnerToken(ctx, "acme", "acme owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if code, out = call(t, c, "GET", "/secrets", issued.Token, nil); code != 200 || strings.Contains(out, "API_TOKEN") {
		t.Fatal(code, out)
	}
	if code, _ = call(t, c, "DELETE", "/secrets/API_TOKEN", issued.Token, nil); code != 404 {
		t.Fatal("another company deleted a secret", code)
	}
	if code, _ = call(t, c, "POST", "/secrets/rotate-master", issued.Token, nil); code != 403 {
		t.Fatal("an owner token rotated the master key", code)
	}

	// Deleted: POST /runs refuses a run of the agent.
	if code, _ = call(t, c, "DELETE", "/secrets/API_TOKEN", c.Token, nil); code != 200 {
		t.Fatal(code)
	}
	code, out = call(t, c, "POST", "/runs", c.Token, map[string]string{"project": "p2", "agent": "dev", "workspace": "/tmp/p2", "prompt": "work"})
	if code != 409 || !strings.Contains(out, "secret_missing") || !strings.Contains(out, "API_TOKEN") {
		t.Fatal(code, out)
	}
}

// TestClaimDeliversSealedSecrets (core-secret-store 3.1, 3.4): the claim answer carries the run's secrets sealed to
// the claiming node, opening only with its key and the run's info; nothing stores the answer; a repeated claim seals
// again and records one secret.delivered; without a master key the run stays QUEUED; a missing secret ends the run
// before it starts, no attempt spent, and the task waits for it.
func TestClaimDeliversSealedSecrets(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	dev := c.Agents["dev"]
	dev.Spec.Secrets = []config.SecretUse{{Name: "API_TOKEN", Env: "API_TOKEN"}}
	dev.Timeout = 60e9
	c.Agents["dev"] = dev
	keys, _ := secrets.NewMasterKeys(bytes.Repeat([]byte{5}, secrets.KeySize), nil)
	s.Keys = keys
	if _, err := s.SetSecret(ctx, co, "API_TOKEN", storage.SecretString, []byte("delivered-value-1"), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "sec", ProjectID: "p", Title: "uses a secret", Assignee: "dev", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(ctx, co, "sec")
	run := task.CurrentRunID
	n := testnode.Enroll(t, s, co)
	if code, out := signedCall(t, c, n, "POST", "/nodes/register", registration(n.ID, 1)); code != 200 {
		t.Fatal(code, out)
	}
	claim := storage.ClaimRequest{NodeID: n.ID, Epoch: 1, ClaimID: "c1"}

	// No master key: retry, the run stays QUEUED.
	s.Keys = nil
	if code, out := signedCall(t, c, n, "POST", "/runs/"+run+"/claim", claim); code != 409 || !strings.Contains(out, "no_master_key") || !strings.Contains(out, `"retry":true`) {
		t.Fatal(code, out)
	}
	if r, _ := s.Get(ctx, co, run); r.Status != "QUEUED" {
		t.Fatal(r.Status)
	}
	s.Keys = keys

	var answer storage.Claimed
	for i := 0; i < 2; i++ { // the same claim twice: sealed again each time
		code, out := signedCall(t, c, n, "POST", "/runs/"+run+"/claim", claim)
		if code != 200 || strings.Contains(out, "delivered-value") {
			t.Fatal(code, out)
		}
		if err := json.Unmarshal([]byte(out), &answer); err != nil {
			t.Fatal(err)
		}
		if len(answer.Secrets) != 1 || answer.Secrets[0].Name != "API_TOKEN" || answer.Secrets[0].Version != 1 {
			t.Fatalf("%+v", answer.Secrets)
		}
	}
	x := answer.Secrets[0]
	if v, err := secrets.OpenOnNode(n.Box, secrets.DeliveryInfo(co, run, n.ID, x.Name, x.Version), x.Sealed); err != nil || string(v) != "delivered-value-1" {
		t.Fatal(string(v), err)
	}
	if _, err := secrets.OpenOnNode(n.Box, secrets.DeliveryInfo(co, "other-run", n.ID, x.Name, x.Version), x.Sealed); err == nil {
		t.Fatal("opened for another run")
	}
	var delivered, stored int
	_ = s.DB.QueryRow("SELECT count(*) FROM feed_events WHERE kind='secret.delivered' AND run_id=$1", run).Scan(&delivered)
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE spec::text LIKE '%delivered-value%' OR result LIKE '%delivered-value%'").Scan(&stored)
	if delivered != 1 || stored != 0 {
		t.Fatal("delivered events", delivered, "stored", stored)
	}

	// A secret deleted after dispatch: the next run ends before it starts, no attempt, and the task waits.
	if err := s.DeleteSecret(ctx, co, "API_TOKEN", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p2", Name: "p2", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "sec2", ProjectID: "p2", Title: "uses a secret too", Assignee: "dev", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecret(ctx, co, "API_TOKEN", storage.SecretString, []byte("delivered-value-2"), "owner"); err != nil {
		t.Fatal(err)
	}
	dev.Spec.Execution.MaxConcurrentRuns, dev.Spec.Execution.MaxConcurrentRunsPerProject = 2, 2
	c.Agents["dev"] = dev
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	t2, _ := s.Task(ctx, co, "sec2")
	if t2.CurrentRunID == "" {
		snap, _ := s.TaskSnapshot(ctx, co, "sec2")
		t.Fatalf("second task not dispatched: %+v", c.evaluateIn(co, snap))
	}
	if err := s.DeleteSecret(ctx, co, "API_TOKEN", "owner"); err != nil {
		t.Fatal(err)
	}
	code, out := signedCall(t, c, n, "POST", "/runs/"+t2.CurrentRunID+"/claim", storage.ClaimRequest{NodeID: n.ID, Epoch: 1, ClaimID: "c2", Slot: 1})
	if code != 409 || !strings.Contains(out, "secret_missing") {
		t.Fatal(code, out)
	}
	r, _ := s.Get(ctx, co, t2.CurrentRunID)
	t2, _ = s.Task(ctx, co, "sec2")
	snap, _ := s.TaskSnapshot(ctx, co, "sec2")
	if r.Status != "ENDED" || r.Outcome != "setup_failed" || !strings.Contains(r.Result, "API_TOKEN") || t2.Status != "READY" || c.evaluateIn(co, snap).Reason != tasks.ReasonSecretMissing {
		t.Fatalf("%+v %+v", r, t2)
	}
}
