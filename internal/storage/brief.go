package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// The run brief (trigger-log-scheduling D8) is built at claim, in the claim's transaction: it shows the task
// as the run starts and exactly the triggers of the run's brief window (from, to], which the claim records
// as brief_from/brief_to. Dispatch leaves a task run's prompt empty.

// briefTx builds the prompt of a run of task t: the task, why this run (previous outcome, directives,
// triggers, answers, review feedback, comments), what earlier runs left (tasks they created, the branch),
// what it builds on (dependencies, approved artifacts), the standing rules and how to end the turn.
func briefTx(ctx context.Context, tx *Tx, t tasks.Task, spec protocol.RunSpec, from, to int64) (string, error) {
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return "", err
	}
	review := t.Status == "IN_REVIEW" && t.Reviewer != "" && t.Assignee == t.Reviewer
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s (%s)\nTask: %s — %s\n\n%s\n", p.Name, p.ID, t.ID, t.Title, t.Description)
	if t.OpenSpecChange != "" {
		fmt.Fprintf(&b, "OpenSpec change: %s (openspec/changes/%s). Read proposal and relevant specs with factory_openspec_read before implementation; list current openspec/specs as needed. Documents remain current in files; this is a reference, not a copied snapshot.\n", t.OpenSpecChange, t.OpenSpecChange)
	}
	// The previous run of the task (the latest that started), whatever its outcome.
	var prev struct {
		id, status, outcome, result string
		started                     int64
	}
	err = tx.QueryRowContext(ctx, "SELECT id,status,outcome,result,started_at FROM runs WHERE company_id=? AND task_id=? AND id!=? AND started_at>0 ORDER BY started_at DESC,seq DESC LIMIT 1", tx.company, t.ID, spec.ID).Scan(&prev.id, &prev.status, &prev.outcome, &prev.result, &prev.started)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if prev.id != "" {
		outcome := prev.outcome
		if outcome == "" {
			outcome = strings.ToLower(prev.status)
		}
		if outcome == "succeeded" {
			fmt.Fprintf(&b, "Previous run %s: succeeded.\n", prev.id)
		} else {
			fmt.Fprintf(&b, "Previous run %s: %s. Inspect the workspace before continuing: the previous run may have changed files or completed part of the work. Do not repeat completed side effects.\n", prev.id, outcome)
			if strings.TrimSpace(prev.result) != "" {
				fmt.Fprintf(&b, "Its last result: %s\n", briefExcerpt(prev.result, 16*1024))
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT seq,kind,actor,authority,ref_id,body,addressee FROM task_triggers WHERE company_id=? AND task_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT 20", tx.company, t.ID, from, to)
	if err != nil {
		return "", err
	}
	type trigger struct {
		kind, actor, ref, body, to string
		authority                  bool
	}
	var triggers []trigger
	for rows.Next() {
		var g trigger
		var seq int64
		if err = rows.Scan(&seq, &g.kind, &g.actor, &g.authority, &g.ref, &g.body, &g.to); err != nil {
			rows.Close()
			return "", err
		}
		triggers = append(triggers, g)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	if tx.store.ExplicitExits {
		for _, g := range triggers {
			// Authority input with text; an assignment's text is generated, not an instruction.
			if g.authority && strings.TrimSpace(g.body) != "" && g.kind != "assignment" && g.kind != "created_ready" {
				fmt.Fprintf(&b, "Directive from %s (%s): %s\n", g.actor, g.kind, briefExcerpt(g.body, 4*1024))
			}
		}
	}
	// Why this run: the triggers since the task was last handled, with author, authority and addressee.
	if len(triggers) > 0 {
		b.WriteString("Since your last run:\n")
		for _, g := range triggers {
			who := g.actor
			if g.authority {
				who += " (authority)"
			}
			line := "- " + g.kind + " by " + who
			if g.to != "" && g.to != spec.Agent {
				line += " (addressed to " + g.to + ")" // written before the task came to this agent
			}
			if g.body != "" {
				line += ": " + briefExcerpt(g.body, 1024)
			}
			b.WriteString(line + "\n")
		}
	}
	for _, g := range triggers {
		if g.kind != "answer" || g.ref == "" {
			continue
		}
		answer, err := getDoc[struct {
			Sender  string         `json:"sender"`
			Body    string         `json:"body"`
			Answers map[string]any `json:"answers,omitempty"`
		}](ctx, tx, "SELECT body FROM messages WHERE company_id=? AND message_type='answer' AND (body->>'in_reply_to')=? ORDER BY seq DESC LIMIT 1", tx.company, g.ref)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		a, _ := json.Marshal(answer)
		fmt.Fprintf(&b, "Answer to your blocking question %s: %s\nContinue the original task using this explicit answer. Do not ask the same question again.\n", g.ref, a)
	}
	// Messages addressed to the agent (conversation task triggers, hierarchy D5).
	var messages []string
	for _, g := range triggers {
		if g.kind == "message" && g.ref != "" {
			messages = append(messages, g.ref)
		}
	}
	if err = conversationBrief(ctx, tx, &b, messages); err != nil {
		return "", err
	}
	// A manager sees its subtree's closed, escalated and failed tasks since its last run (hierarchy D6).
	since := prev.started
	if since == 0 {
		since = t.CreatedAt
	}
	if err = subtreeSummary(ctx, tx, &b, t.ProjectID, spec.Agent, since); err != nil {
		return "", err
	}
	if t.ReviewFeedback != "" {
		fmt.Fprintf(&b, "Previous review feedback: %s\n", t.ReviewFeedback)
	}
	// Comments since the previous run (non-authority comments wake nothing; the brief carries them).
	comments, err := listComments(ctx, tx, t.ID)
	if err != nil {
		return "", err
	}
	for _, cm := range comments {
		if cm.CreatedAt > prev.started && cm.Author != t.Assignee {
			fmt.Fprintf(&b, "Comment by %s: %s\n", cm.Author, briefExcerpt(cm.Text, 2*1024))
		}
	}
	created, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND (body->>'origin_task')=? ORDER BY created_at,id", tx.company, t.ID)
	if err != nil {
		return "", err
	}
	if len(created) > 0 {
		b.WriteString("Tasks your earlier runs created:\n")
		for _, ct := range created {
			fmt.Fprintf(&b, "- %s [%s] %s (assignee %s)\n", ct.ID, ct.Status, ct.Title, ct.Assignee)
		}
	}
	// The branch log, from the git facts of the task's last exit (when a run exited from a repository).
	var facts string
	if err = tx.QueryRowContext(ctx, "SELECT facts FROM run_exits WHERE company_id=? AND task_id=? AND facts!='' ORDER BY created_at DESC,seq DESC LIMIT 1", tx.company, t.ID).Scan(&facts); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var f ExitFacts
	if facts != "" && json.Unmarshal([]byte(facts), &f) == nil && f.Branch != "" {
		fmt.Fprintf(&b, "Branch %s: %d commit(s) ahead of %s, head %s", f.Branch, f.AheadOfDefault, f.DefaultBranch, f.Head)
		if f.Pushed {
			b.WriteString(", pushed")
		}
		if f.Dirty {
			b.WriteString(", uncommitted changes")
		}
		if f.PR != nil {
			fmt.Fprintf(&b, ", pull request #%d (%s)", f.PR.Number, f.PR.State)
		}
		b.WriteString(" at the last exit.\n")
	}
	for _, dep := range t.Dependencies {
		d, err := taskTx(ctx, tx, dep)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Dependency %s [%s]: %s\n", d.ID, d.Status, briefExcerpt(d.Result, 4*1024))
	}
	if len(spec.ApprovedArtifacts) > 0 {
		a, _ := json.Marshal(spec.ApprovedArtifacts)
		fmt.Fprintf(&b, "Owner-approved artifact inputs (use these exact versions and URIs): %s\n", a)
	}
	// Standing rules: conventions, facts and fixed decisions (factory_settings_get reads them again).
	var rules string
	if err = tx.QueryRowContext(ctx, "SELECT rules FROM companies WHERE id=?", tx.company).Scan(&rules); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if strings.TrimSpace(rules) != "" {
		fmt.Fprintf(&b, "Company rules:\n%s\n", strings.TrimSpace(rules))
	}
	if strings.TrimSpace(p.Rules) != "" {
		fmt.Fprintf(&b, "Project rules:\n%s\n", strings.TrimSpace(p.Rules))
	}
	if err = briefReminders(ctx, tx, &b, spec.Agent, spec.ID); err != nil {
		return "", err
	}
	if review {
		s, _ := json.Marshal(t.Submission)
		fmt.Fprintf(&b, "You review this task, submitted by %s: %s\nInspect the changes (the branch and its pull request), then end with factory_exit_handoff to %s with verdict approved, or changes_requested with feedback.\n", t.Implementer, s, t.Implementer)
	}
	if t.Kind == tasks.KindConversation {
		b.WriteString("This is your conversation task: it stays open. Handle the input above, then end with factory_exit_done (handled; summarize what you did). Do not poll or wait within a turn.\n")
	} else if tx.store.ExplicitExits {
		b.WriteString("End the run with exactly one factory_exit_* call: submit (for review, clean tree), done (finished; merge your pull request first), handoff (another agent or yourself later), ask (a question you cannot continue without), block (wait on other tasks or your children) or wait (until a time). Read authority_input in tool results: directives win over the plan.\n")
	} else {
		b.WriteString("Finish with a concise submission summary, changed files and checks. Your completed turn submits this task for review; it does not accept the task.\n")
	}
	return b.String(), nil
}

func briefExcerpt(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n[truncated; read the full task/attempt through Factory CLI when needed]"
}

// briefClaim builds and stores the brief of a task run being claimed, with its brief window. A run without
// a task (a message delivery or a direct run) keeps the prompt it was created with.
func briefClaim(ctx context.Context, tx *Tx, id string, spec *protocol.RunSpec) error {
	if spec.TaskID == "" {
		return nil
	}
	t, err := taskTx(ctx, tx, spec.TaskID)
	if err != nil {
		return err
	}
	var from, to int64
	if err = tx.QueryRowContext(ctx, "SELECT handled_seq,wake_seq FROM tasks WHERE company_id=? AND id=?", tx.company, t.ID).Scan(&from, &to); err != nil {
		return err
	}
	if spec.Prompt, err = briefTx(ctx, tx, t, *spec, from, to); err != nil {
		return err
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE runs SET spec=?,brief_from=?,brief_to=? WHERE company_id=? AND id=?", b, from, to, tx.company, id)
	return err
}
