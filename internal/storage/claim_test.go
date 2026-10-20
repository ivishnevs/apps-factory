package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/tasks"
)

func dropped(t *testing.T, err error, reason string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || !r.Drop || r.Reason != reason {
		t.Fatalf("want drop %s, got %v", reason, err)
	}
}

// TestClaimRechecksTheTask (trigger-log-scheduling D6): what changed between dispatch and claim decides the
// claim. A reassigned, cancelled or no longer runnable task drops the command and its run never starts.
func TestClaimRechecksTheTask(t *testing.T) {
	ctx := context.Background()
	s := engineStore(t)

	// Reassigned after dispatch: the queued run of the previous assignee ends stale.
	moved := readyEngineTask(t, s, createEngineTask(t, s, "moved"))
	dispatchEngineTask(t, s, moved, "moved-run")
	moved, _ = s.Task(ctx, DefaultCompany, "moved")
	other := "other"
	if moved, err := s.UpdateTask(ctx, DefaultCompany, "moved", tasks.Update{Version: moved.Version, Assignee: &other}); err != nil || moved.Status != "READY" || moved.Assignee != "other" {
		t.Fatal(moved, err)
	}
	_, err := s.ClaimRun(ctx, DefaultCompany, "moved-run")
	dropped(t, err, "stale")
	if st, out := outcomeOf(t, s, "moved-run"); st != "ENDED" || out != "stale" {
		t.Fatal(st, out)
	}
	if v := verdict(t, s, "moved"); v.Reason != tasks.ReasonRunnable {
		t.Fatalf("the new assignee's task does not run: %+v", v)
	}

	// Cancelled after dispatch.
	cancelled := readyEngineTask(t, s, createEngineTask(t, s, "cancelled"))
	dispatchEngineTask(t, s, cancelled, "cancelled-run")
	cancelled, _ = s.Task(ctx, DefaultCompany, "cancelled")
	if _, err = s.TaskAction(ctx, DefaultCompany, "cancelled", tasks.Action{Version: cancelled.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimRun(ctx, DefaultCompany, "cancelled-run")
	dropped(t, err, "stale")

	// A dependency reopened after dispatch: the task waits for it again.
	dep := readyEngineTask(t, s, createEngineTask(t, s, "dep"))
	finishEngineRun(t, s, dispatchEngineTask(t, s, dep, "dep-run"), "succeeded")
	dep, _ = s.Task(ctx, DefaultCompany, "dep")
	if _, err = s.ReviewTask(ctx, DefaultCompany, "dep", tasks.Review{Version: dep.Version, Accept: true}); err != nil {
		t.Fatal(err)
	}
	waiting := readyEngineTask(t, s, createEngineTask(t, s, "waiting", "dep"))
	dispatchEngineTask(t, s, waiting, "waiting-run")
	if _, err = s.DB.Exec(`UPDATE tasks SET status='READY',body=json_set(body,'$.status','READY') WHERE id='dep'`); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimRun(ctx, DefaultCompany, "waiting-run")
	dropped(t, err, "not_runnable")
	if st, out := outcomeOf(t, s, "waiting-run"); st != "ENDED" || out != "stale" {
		t.Fatal(st, out)
	}
	if v := verdict(t, s, "waiting"); v.Status != "READY" || v.Reason != tasks.ReasonDependencies {
		t.Fatalf("%+v", v)
	}
	var started int64
	if err = s.DB.QueryRow("SELECT started_at FROM runs WHERE id='waiting-run'").Scan(&started); err != nil || started != 0 {
		t.Fatal("stale run started", started, err)
	}
	if v, _ := s.Feed(ctx, DefaultCompany, FeedFilter{Task: "waiting"}, 0, 100); !hasFeed(v.Items, "run.stale") {
		t.Fatalf("no run.stale feed event: %+v", v)
	}

	// A duplicate command of a claimed run is dropped as well.
	dup := readyEngineTask(t, s, createEngineTask(t, s, "dup"))
	dispatchEngineTask(t, s, dup, "dup-run")
	if _, err = s.ClaimRun(ctx, DefaultCompany, "dup-run"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimRun(ctx, DefaultCompany, "dup-run")
	dropped(t, err, "duplicate")
}

// TestBriefBuiltAtClaim: input that arrives between dispatch and claim is in the brief and in its window.
func TestBriefBuiltAtClaim(t *testing.T) {
	ctx := context.Background()
	s := engineStore(t)
	v := readyEngineTask(t, s, createEngineTask(t, s, "late"))
	spec := dispatchEngineTask(t, s, v, "late-run")
	if spec.Prompt != "" {
		t.Fatal("prompt built at dispatch:", spec.Prompt)
	}
	if _, err := s.CommentTask(ctx, DefaultCompany, "late", tasks.Comment{Text: "use the new API", RequestID: "late"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimRun(ctx, DefaultCompany, "late-run")
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := s.TaskSnapshot(ctx, DefaultCompany, "late")
	if !strings.Contains(c.RunSpec.Prompt, "owner_comment by owner (authority): use the new API") || c.Brief.To != snap.WakeSeq || c.Brief.From != 0 {
		t.Fatalf("%+v\n%s", c.Brief, c.RunSpec.Prompt)
	}
	if saved, _ := s.Get(ctx, DefaultCompany, "late-run"); saved.Spec.Prompt != c.RunSpec.Prompt {
		t.Fatal("brief not stored on the run")
	}
}

func hasFeed(items []FeedItem, kind string) bool {
	for _, i := range items {
		if i.Type == kind {
			return true
		}
	}
	return false
}
