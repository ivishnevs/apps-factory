package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Exit events and the reporting policy (waits-events-cost-cli D2): every accepted exit is one feed row in
// the exit's transaction; a mandatory report missing at the exit is a feed event for the agent's manager and
// a reminder in the agent's next brief, never a refusal.

// exitFeedKinds name the feed event of each exit.
var exitFeedKinds = map[string]string{"submit": "submitted", "handoff": "handoff", "done": "done", "ask": "asked", "block": "blocked", "wait": "waiting"}

func exitFeed(ctx context.Context, tx *Tx, runID, agent string, before tasks.Task, req ExitRequest, out ExitResult) error {
	a := req.Arguments
	var summary string
	switch req.Kind {
	case "submit":
		to := "review"
		if r := str(a, "reviewer"); r != "" {
			to = r
		}
		summary = fmt.Sprintf("%s → %s (submit): %s", agent, to, str(a, "summary"))
	case "handoff":
		switch v := str(a, "verdict"); {
		case v != "":
			summary = fmt.Sprintf("%s → %s (%s): %s", agent, out.Task.Assignee, v, str(a, "note"))
		case str(a, "merge_failed") != "":
			summary = fmt.Sprintf("%s merge failed: %s", agent, str(a, "merge_failed"))
		default:
			summary = fmt.Sprintf("%s → %s (handoff): %s", agent, str(a, "to"), str(a, "note"))
		}
	case "done":
		summary = agent + " done: " + str(a, "summary")
	case "ask":
		summary = agent + " asked: " + str(a, "question")
	case "block":
		summary = agent + " blocked: " + str(a, "reason")
		if out.Note != "" {
			summary += " (" + out.Note + ")"
		}
	case "wait":
		wake, deadline, _ := waitTimes(a, tx.now())
		summary = fmt.Sprintf("%s waiting until %s (deadline %s): %s", agent, wake.UTC().Format(time.RFC3339), deadline.UTC().Format(time.RFC3339), str(a, "reason"))
	}
	if err := feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.exit." + exitFeedKinds[req.Kind], EntityID: before.ID, ProjectID: before.ProjectID, CreatedAt: tx.now(),
		TaskID: before.ID, RunID: runID, Agent: agent, Actor: agent, Status: out.Task.Status, Summary: summary}); err != nil {
		return err
	}
	return missingReports(ctx, tx, runID, agent, before, req.Kind)
}

// missingReports checks the run's reports against the agent's reporting rules for this exit.
func missingReports(ctx context.Context, tx *Tx, runID, agent string, t tasks.Task, exit string) error {
	rules := runAgent(ctx, tx, runID).RequiredReports(exit)
	if len(rules) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT kind FROM reports WHERE company_id=? AND run_id=?", tx.company, runID)
	if err != nil {
		return err
	}
	kinds := []string{}
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		kinds = append(kinds, k)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	manager := retryTarget(tx, agent)
	for _, r := range rules {
		if slices.ContainsFunc(kinds, func(k string) bool { return len(r.ReportKinds) == 0 || slices.Contains(r.ReportKinds, k) }) {
			continue
		}
		want := "a report"
		if len(r.ReportKinds) > 0 {
			want = "a report of kind " + strings.Join(r.ReportKinds, " or ")
		}
		summary := fmt.Sprintf("for %s: %s exited %s on %s without %s (reporting rule %s)", manager, agent, exit, t.ID, want, r.Name)
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "report.missing", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(),
			TaskID: t.ID, RunID: runID, Agent: agent, Actor: "system", Status: t.Status, Summary: summary, Ref: r.Name}); err != nil {
			return err
		}
		text := fmt.Sprintf("Your run %s exited %s on task %s without %s, which reporting rule %s requires: %s Record it with factory_report_create when you exit like this.", runID, exit, t.ID, want, r.Name, r.Instruction)
		if _, err = tx.ExecContext(ctx, "INSERT INTO report_reminders(company_id,id,agent,task_id,run_id,text,created_at) VALUES(?,?,?,?,?,?,?)",
			tx.company, protocol.ID(), agent, t.ID, runID, text, tx.now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// briefReminders writes the agent's unshown report reminders into the brief and marks them shown by run.
func briefReminders(ctx context.Context, tx *Tx, b *strings.Builder, agent, run string) error {
	rows, err := tx.QueryContext(ctx, "SELECT text FROM report_reminders WHERE company_id=? AND agent=? AND shown_run='' ORDER BY created_at,id LIMIT 5", tx.company, agent)
	if err != nil {
		return err
	}
	texts := []string{}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		texts = append(texts, s)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(texts) == 0 {
		return err
	}
	b.WriteString("Reporting reminders:\n")
	for _, s := range texts {
		b.WriteString("- " + s + "\n")
	}
	_, err = tx.ExecContext(ctx, "UPDATE report_reminders SET shown_run=? WHERE company_id=? AND agent=? AND shown_run=''", run, tx.company, agent)
	return err
}
