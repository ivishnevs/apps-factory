package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// endAs ends a started run the way Core records an outcome, then applies it to the task (syncTaskRun).
func endAs(t *testing.T, s *Store, id, status, outcome, result string) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,outcome=?,result=?,finished_at=? WHERE id=?", status, outcome, result, tx.now().Unix(), id); err != nil {
		t.Fatal(err)
	}
	if err = syncTaskRun(ctx, tx, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func budgetUsed(t *testing.T, s *Store, task string) int {
	t.Helper()
	var used int
	if err := s.DB.QueryRow("SELECT coalesce(sum(used),0) FROM task_budget WHERE task_id=?", task).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

// TestOutcomeTable (reliable-recovery 1.2): each outcome, applied in one place, moves the attempt counter,
// the backoff (fail_streak, not_before) and the daily budget exactly as the outcome table says.
func TestOutcomeTable(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	for _, c := range []struct {
		outcome, status, result string
		attempts, streak        int
		wait                    time.Duration
		budget                  int
		task                    string // the task's status afterwards
	}{
		{"succeeded", "COMPLETED", "", 0, 0, 0, 1, "IN_REVIEW"},
		{"no_exit", "COMPLETED", "", 1, 0, 0, 1, "IN_PROGRESS"}, // escalated no_exit (to the manager)
		{"failed", "FAILED", "", 1, 1, time.Minute, 1, "READY"},
		{"timed_out", "FAILED", "", 1, 1, time.Minute, 1, "READY"},
		{"idle_timeout", "FAILED", "", 1, 1, time.Minute, 1, "READY"},
		{"max_turns", "FAILED", "", 1, 1, time.Minute, 1, "READY"},
		{"setup_failed", "FAILED", "OpenSpec preflight failed", 1, 1, time.Minute, 1, "READY"},
		{"lost", "FAILED", "lost: worker lease expired", 0, 1, time.Minute, 1, "READY"},
		{"lost", "FAILED", "lost: node n1 restarted (epoch 2)", 0, 1, time.Minute, 0, "READY"}, // new epoch: no budget
		{"revoked", "CANCELLED", "", 0, 0, 0, 0, "CANCELLED"},
		{"paused_out", "FAILED", "", 0, 0, 0, 0, "READY"},
		{"stale", "FAILED", "", 0, 0, 0, 0, "READY"},
		{"unclaimed", "FAILED", "", 0, 0, 0, 0, "READY"},
	} {
		name := c.outcome + "-" + c.status
		if c.budget == 0 && c.outcome == "lost" {
			name += "-epoch"
		}
		t.Run(name, func(t *testing.T) {
			s.ExplicitExits = c.outcome == "no_exit"
			task := readyEngineTask(t, s, createEngineTask(t, s, name))
			spec := dispatchEngineTask(t, s, task, name+"-run")
			if _, err := s.Claim(ctx, DefaultCompany, spec.ID); err != nil {
				t.Fatal(err)
			}
			endAs(t, s, spec.ID, c.status, c.outcome, c.result)
			got := taskOf(t, s, name)
			wantNotBefore := int64(0)
			if c.wait > 0 {
				wantNotBefore = fake.Now().Add(c.wait).Unix()
			}
			if got.AttemptsNoProgress != c.attempts || got.FailStreak != c.streak || got.NotBefore != wantNotBefore || budgetUsed(t, s, name) != c.budget || got.Status != c.task {
				t.Fatalf("attempts %d streak %d not_before %d budget %d status %s; want %d %d %d %d %s",
					got.AttemptsNoProgress, got.FailStreak, got.NotBefore, budgetUsed(t, s, name), got.Status, c.attempts, c.streak, wantNotBefore, c.budget, c.task)
			}
			// The table says it: an outcome that is no attempt leaves the counter alone, and so on.
			rule := Outcomes[c.outcome]
			if rule.Attempt != (c.attempts == 1) || rule.Backoff != (c.streak == 1) || rule.Budget != (c.budget == 1) && c.result != "lost: node n1 restarted (epoch 2)" {
				t.Fatalf("test row disagrees with the table: %+v", rule)
			}
			// Applying the same end again changes nothing.
			endAs(t, s, spec.ID, c.status, c.outcome, c.result)
			if again := taskOf(t, s, name); again.AttemptsNoProgress != got.AttemptsNoProgress || again.FailStreak != got.FailStreak || budgetUsed(t, s, name) != c.budget {
				t.Fatalf("applied twice: %+v", again)
			}
		})
	}
	s.ExplicitExits = false
}

// TestBudgetDayInCompanyTimeZone (1.5): the daily budget counts in the company's time zone: a run at 22:30 in
// New York counts on New York's day, the next one after its midnight on the next day.
func TestBudgetDayInCompanyTimeZone(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Date(2027, 1, 15, 3, 30, 0, 0, time.UTC)) // 22:30 on Jan 14 in New York
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	if err := s.SetCompanyTimeZone(ctx, co, "Mars/Olympus"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if err := s.SetCompanyTimeZone(ctx, co, "America/New_York"); err != nil {
		t.Fatal(err)
	}
	if zone, err := s.CompanyTimeZone(ctx, co); err != nil || zone != "America/New_York" {
		t.Fatal(zone, err)
	}
	readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	if _, err := s.DB.Exec("INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,?)", co, "t1", "2027-01-14", DailyBudget-1); err != nil {
		t.Fatal(err)
	}
	fail := func(id string) {
		t.Helper()
		spec := dispatchEngineTask(t, s, taskOf(t, s, "t1"), id)
		lease, err := s.Claim(ctx, co, spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = end(t, s, spec.ID, lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
			t.Fatal(err)
		}
	}
	fail("r1")
	open, _ := s.Escalations(ctx, co, true)
	if len(open) != 1 || open[0].Reason != "budget_exhausted" {
		t.Fatalf("the run did not count on New York's day: %+v", open)
	}
	q, _ := s.Message(ctx, co, open[0].MessageID)
	if _, err := s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "retry", Answers: map[string]any{"answer": "retry"}, RequestID: "retry"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(2 * time.Hour) // 00:30 on Jan 15 in New York
	fail("r2")
	var days []string
	rows, _ := s.DB.Query("SELECT day||'='||used FROM task_budget WHERE task_id='t1' ORDER BY day")
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		days = append(days, d)
	}
	rows.Close()
	if len(days) != 1 || days[0] != "2027-01-15=1" {
		t.Fatal(days) // the retry answer reset Jan 14; the run after midnight counts on Jan 15
	}
	if open, _ = s.Escalations(ctx, co, true); len(open) != 0 {
		t.Fatalf("%+v", open)
	}
}

// TestDirectiveDuringBudgetRun (1.5): authority input that arrives while the run that reaches the daily
// budget is running counts as a retry: no budget_exhausted, today's budget and the counters reset, and the
// task runs again with that input at once.
func TestDirectiveDuringBudgetRun(t *testing.T) {
	s, _ := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	if _, err := s.DB.Exec("INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,?)", co, "t1", s.Now().UTC().Format("2006-01-02"), DailyBudget-1); err != nil {
		t.Fatal(err)
	}
	spec := dispatchEngineTask(t, s, taskOf(t, s, "t1"), "r1")
	lease, err := s.Claim(ctx, co, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommentTask(ctx, co, "t1", tasks.Comment{Text: "use the staging bucket", RequestID: "directive"}); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	got := taskOf(t, s, "t1")
	open, _ := s.Escalations(ctx, co, true)
	if len(open) != 0 || got.Status != "READY" || got.NotBefore != 0 || got.AttemptsNoProgress != 0 || budgetUsed(t, s, "t1") != 0 {
		t.Fatalf("%+v %+v budget %d", got, open, budgetUsed(t, s, "t1"))
	}
	if r := reason(t, s, "t1"); r != tasks.ReasonRunnable {
		t.Fatal(r)
	}
	// The input the run was briefed on does not count again: the next failure at the limit escalates.
	if _, err = s.DB.Exec("INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,?) ON CONFLICT DO UPDATE SET used=excluded.used", co, "t1", s.Now().UTC().Format("2006-01-02"), DailyBudget-1); err != nil {
		t.Fatal(err)
	}
	spec = dispatchEngineTask(t, s, taskOf(t, s, "t1"), "r2")
	if lease, err = s.Claim(ctx, co, spec.ID); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	if open, _ = s.Escalations(ctx, co, true); len(open) != 1 || open[0].Reason != "budget_exhausted" {
		t.Fatalf("%+v", open)
	}
}

// TestNewEpochLossSkipsBudget (3.1): a run lost because its node restarted with a new epoch backs off but
// is not counted against the daily budget; a run lost to a silent node is.
func TestNewEpochLossSkipsBudget(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1)
	start := func(id string, epoch int64, claim string) {
		t.Helper()
		spec := dispatchEngineTask(t, s, readyEngineTask(t, s, createEngineTask(t, s, id)), id+"-run")
		c, err := claimV2(s, spec.ID, "n1", epoch, claim)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartRun(ctx, co, spec.ID, c.Lease); err != nil {
			t.Fatal(err)
		}
	}
	start("restarted", 1, "1-0-1")
	register(t, s, "n1", 2)
	if got := taskOf(t, s, "restarted"); got.Status != "READY" || got.FailStreak != 1 || budgetUsed(t, s, "restarted") != 0 {
		t.Fatalf("%+v budget %d", got, budgetUsed(t, s, "restarted"))
	}
	start("silent", 2, "2-0-1")
	fake.Advance(NodeOffline + time.Second)
	if err := s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2}); err != nil { // no longer lists it: gone
		t.Fatal(err)
	}
	if got := taskOf(t, s, "silent"); got.Status != "READY" || got.FailStreak != 1 || budgetUsed(t, s, "silent") != 1 {
		t.Fatalf("%+v budget %d", got, budgetUsed(t, s, "silent"))
	}
}
