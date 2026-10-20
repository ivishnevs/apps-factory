package storage

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestBriefGolden pins the brief of a task's second run (trigger-log-scheduling D8): the previous run's
// outcome with its result, directives and triggers with author and authority, comments since the previous
// run, tasks created by earlier runs, the branch log from the last exit, dependencies, rules and the exit
// instruction. go test ./internal/storage -run TestBriefGolden -update rewrites testdata/brief.golden.
func TestBriefGolden(t *testing.T) {
	ctx := context.Background()
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	co := DefaultCompany
	if err := s.SetCompanyRules(ctx, co, "Prices in EUR."); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(ctx, co, "p")
	rules := "Metric units only."
	if _, err := s.UpdateProject(ctx, co, "p", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	dep := readyEngineTask(t, s, createEngineTask(t, s, "spec"))
	finishEngineRun(t, s, dispatchEngineTask(t, s, dep, "spec-run"), "COMPLETED")
	dep, _ = s.Task(ctx, co, "spec")
	if _, err := s.ReviewTask(ctx, co, "spec", tasks.Review{Version: dep.Version, Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE tasks SET body=json_set(body,'$.result','API spec v2 at docs/api.md') WHERE id='spec'`); err != nil {
		t.Fatal(err)
	}
	s.ExplicitExits = true
	task := readyEngineTask(t, s, createEngineTask(t, s, "golden", "spec"))
	first := dispatchEngineTask(t, s, task, "golden-1")
	lease, err := s.Claim(ctx, co, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute)
	if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", first.ID, "", protocol.Event{RunID: first.ID, Lease: lease, Status: "FAILED", Result: "tests failed: 3 of 120", Outcome: "failed"})); err != nil {
		t.Fatal(err)
	}
	facts := `{"head":"abc123","branch":"af/golden","default_branch":"main","ahead_of_default":2,"pushed":true,"pr":{"number":7,"state":"open","head_sha":"abc123"}}`
	if _, err = s.DB.Exec(`INSERT INTO run_exits(company_id,run_id,task_id,kind,arguments,ack_seq,facts,result,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, co, first.ID, "golden", "handoff", "{}", 0, facts, "{}", fake.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute)
	// A handoff away and back: the first assignment trigger was addressed to another agent.
	for _, to := range []string{"other", "demo"} {
		task, _ = s.Task(ctx, co, "golden")
		if _, err = s.UpdateTask(ctx, co, "golden", tasks.Update{Version: task.Version, Assignee: &to}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CommentTask(ctx, co, "golden", tasks.Comment{Text: "flaky on CI, see run 812", Actor: "qa", RequestID: "qa"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Directive(ctx, co, "golden", "owner", "fix the tests before the refactor", false, "directive"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "follow-up", ProjectID: "p", Title: "Remove the old client", Assignee: "demo", OriginTask: "golden", RequestID: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, co, "golden")
	second := dispatchEngineTask(t, s, task, "golden-2")
	c, err := s.ClaimRun(ctx, co, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "brief.golden")
	if *update {
		if err = os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(golden, []byte(c.RunSpec.Prompt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if c.RunSpec.Prompt != string(want) {
		t.Fatalf("brief differs from %s (rerun with -update to accept):\n%s", golden, c.RunSpec.Prompt)
	}
}
