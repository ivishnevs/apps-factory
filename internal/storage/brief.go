package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/messaging"
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
	if t.OpenSpecChange != "" && t.Kind == tasks.KindSpec {
		fmt.Fprintf(&b, "OpenSpec change: %s. This is a spec task: you write or amend this change in openspec/changes/%s (proposal, design when needed, delta specs, tasks.md); it need not exist before you start. Implementation tasks of the change come after its pull request is merged.\n", t.OpenSpecChange, t.OpenSpecChange)
	} else if t.OpenSpecChange != "" {
		fmt.Fprintf(&b, "OpenSpec change: %s (openspec/changes/%s, or changes/archive/ once archived). The specs are files of the repository: read the proposal and relevant specs in your checkout before implementing. Spec edits are committed on your branch and reviewed with your code in the pull request; do not change requirements (proposal, specs deltas) without the product owner. If this task is marked as the last one of the change, archive it (openspec archive %s) in your pull request.\n", t.OpenSpecChange, t.OpenSpecChange, t.OpenSpecChange)
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
		if (g.kind == "message" || g.kind == "message_received") && g.ref != "" {
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
	// owner-comments-handled: the board comments this run owes an outcome for; the lead's quiet copy.
	if err = commentsBrief(ctx, tx, &b, t, spec.Agent); err != nil {
		return "", err
	}
	if err = leadCopy(ctx, tx, &b, t, spec.Agent, since); err != nil {
		return "", err
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
	// The branch log, from the git facts of the task's last exit from its branch (not a review copy).
	var facts string
	if err = tx.QueryRowContext(ctx, "SELECT e.facts FROM run_exits e WHERE e.company_id=? AND e.task_id=? AND "+branchFacts+" ORDER BY e.created_at DESC,e.seq DESC LIMIT 1", tx.company, t.ID).Scan(&facts); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
		if who := personLabel(ctx, tx, d.Assignee); who != "" && d.Status != "DONE" {
			// people-assignees: a person does it; this task waits for it and starts when it is done.
			fmt.Fprintf(&b, "Dependency %s [%s]: waits for %s, with %s (a person of the company; do not change or redo it; message about it if needed).\n", d.ID, d.Status, d.ID, who)
			continue
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
	if strings.TrimSpace(p.Direction) != "" {
		fmt.Fprintf(&b, "Project goal and direction (decide what fits it yourself; ask people only about what does not):\n%s\n", strings.TrimSpace(p.Direction))
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
		if t.Submission != nil && len(t.Submission.Artifacts) > 0 {
			// approval-gates-merge: approved covers every submitted artifact.
			b.WriteString("Approved covers every artifact of the submission: open each one (run a prototype) and list it in artifact_checks as {id, version, checked: true, note: what you checked}. If you cannot check one, give changes_requested, or hand the review to an agent who can check it (factory_exit_handoff to that agent, without a verdict); approved with an artifact missing or not checked is refused.\n")
		}
	}
	if t.Kind == tasks.KindConversation {
		b.WriteString("This is your conversation task: it stays open. Handle the input above, then end with factory_exit_done (handled; summarize what you did). Do not poll or wait within a turn.\n")
		if t.Counterpart == "owner" {
			// lead-brainstorm-mode: think with the person first, without interrogating them.
			b.WriteString("Working with the person in this chat: a clear request (\"create a task: …\" with what it needs) is done as asked. An idea, a goal or a wish is discussed first: propose options and trade-offs, challenge weak points, state the plan you would follow. Ask only when it is genuinely unclear and the answer changes what you would do; then ask once, all questions together, each with your recommended answer so they can just say ok. Follow-ups, small changes and anything that fits the project's direction and recorded decisions are decided by you without asking; when in doubt act on your best reading and say what you assumed. Before or when work starts, post a short summary: what you understood, the plan, the decisions you took yourself and the tasks you created (with their ids), so they can stop or change them. A plan of more than about 5 tasks waits for their explicit go; smaller ones start at once. If they reply stop or change to just-created tasks, cancel or update those tasks.\n")
			// people-assignees: a person may take a piece of the work themselves.
			b.WriteString("When the person says they will do something themselves (\"I'll do X myself\"), give that task to them: create it, or reassign the existing one, with assignee member:<their user id> (shown as member:… with their message) and assign_reason citing their message id; do not plan that work for an agent. Tasks that depend on it keep their dependencies and start when they mark it done. Never change, cancel or take back a person's task; message about it instead.\n")
			// decision-approval-ux, inbox-chat-replies: what needs the person comes as something they can act on in the chat.
			b.WriteString("When you need the person to choose or confirm, ask with factory_owner_ask and list the options (they get buttons in this chat); do not end a reply with a question they cannot answer there. For a decision with options, use factory_decision_propose with the recommendation as value and the rest as alternatives; they approve, pick or reject it here and you get the outcome, so do not also request approval of factory_decision_record. When their answer here already is the decision, record it with factory_decision_decide only in a delegated category; otherwise propose it once.\n")
			// agent-questions-in-chat: the team's questions to people show in this chat; the lead knows them.
			if err := teamQuestions(ctx, tx, &b, t.ProjectID, spec.Agent, since); err != nil {
				return "", err
			}
		}
	} else if tx.store.ExplicitExits {
		b.WriteString("End the run with exactly one factory_exit_* call: submit (for review, clean tree), done (finished; merge your pull request first), handoff (another agent or yourself later), ask (a question you cannot continue without), block (wait on other tasks or your children) or wait (on a pending decision with decision_id, on a message with from, or until a time). Read authority_input in tool results: directives win over the plan.\n")
	} else {
		b.WriteString("Finish with a concise submission summary, changed files and checks. Your completed turn submits this task for review; it does not accept the task.\n")
	}
	return b.String(), nil
}

func briefExcerpt(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return cutUTF8(s, limit) + "\n[truncated; read the full task/attempt through Factory CLI when needed]"
}

// diskBriefNote tells a conversation run on a node paused for a critical disk what that means for the owner
// (disk-pause-conversations 1.3): the agent answers, but new work and review runs wait until space is freed.
func diskBriefNote(node string, d nodeDisk) string {
	space := "below its critical watermark"
	if d.Critical > 0 {
		space = tasks.GiB(d.Free) + " free, critical watermark " + tasks.GiB(d.Critical)
	}
	return fmt.Sprintf("Node status: the disk of node %s is critical (%s). Factory paused new work and review runs on %s until disk space is freed; it resumes by itself %s. Conversations like this one continue. "+
		"If you start or hand off work, tell the owner that new work waits until disk space is freed on %s (or another node takes it) instead of promising it starts now.\n\n",
		node, space, node, d.tasksDisk().Resumes(false), node)
}

// briefClaim builds and stores the brief of a task run being claimed, with its brief window. A run without
// a task (a message delivery or a direct run) keeps the prompt it was created with. With history the brief
// starts with the conversation's recent history (a conversation starting a fresh session on another node); a note
// (the node's disk state) comes first.
func briefClaim(ctx context.Context, tx *Tx, id string, spec *protocol.RunSpec, history bool, note string) error {
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
	if spec.FreshSession {
		// session-continuity: the agent is told its earlier session is gone (the node adds the note only when
		// it drops a session itself).
		spec.Prompt = protocol.FreshSessionNote + spec.Prompt
	}
	if history {
		h, err := conversationHistory(ctx, tx, t.ID, id, from)
		if err != nil {
			return err
		}
		spec.Prompt = h + spec.Prompt
	}
	spec.Prompt = note + spec.Prompt
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	// The open board comments of the task are shown to this run (owner-comments-handled).
	if _, err = tx.ExecContext(ctx, "UPDATE comment_outcomes SET shown_run=? WHERE company_id=? AND task_id=? AND status='open'", id, tx.company, t.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE runs SET spec=?,brief_from=?,brief_to=? WHERE company_id=? AND id=?", b, from, to, tx.company, id)
	return err
}

// teamQuestions (agent-questions-in-chat) tells the agent people talk to in a project which questions and
// approvals the project's other agents have open with people (they show in its chat as cards) and which people
// answered since its last run, so it knows what was asked and decided.
func teamQuestions(ctx context.Context, tx *Tx, b *strings.Builder, project, agent string, since int64) error {
	open, err := listDocs[messaging.Message](ctx, tx, `SELECT body FROM messages WHERE company_id=? AND project_id=? AND message_type='question' AND sender NOT IN (?,'owner','factory')
 AND (status='ESCALATED' OR recipient='owner' AND status='PENDING' AND coalesce(body->>'delegated_to','')='') ORDER BY created_at,seq LIMIT 10`, tx.company, project, agent)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.body,a.body FROM messages a JOIN messages q ON q.company_id=a.company_id AND q.id=(a.body->>'in_reply_to')
 WHERE a.company_id=? AND a.project_id=? AND a.message_type='answer' AND a.sender='owner' AND a.created_at>=? AND q.message_type='question' AND q.sender NOT IN (?,'owner','factory')
 ORDER BY a.created_at,a.seq LIMIT 10`, tx.company, project, since, agent)
	if err != nil {
		return err
	}
	type pair struct{ q, a messaging.Message }
	var answered []pair
	for rows.Next() {
		var qb, ab []byte
		var p pair
		if err = rows.Scan(&qb, &ab); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(qb, &p.q) == nil && json.Unmarshal(ab, &p.a) == nil {
			answered = append(answered, p)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(open) == 0 && len(answered) == 0 {
		return nil
	}
	on := func(m messaging.Message) string {
		if m.TaskID == "" || strings.HasPrefix(m.TaskID, "conv-") {
			return ""
		}
		return " on task " + m.TaskID
	}
	b.WriteString("Your team's questions to people in this project (they show in this chat and are answered there, on the task or in the Inbox; do not answer them for the person):\n")
	for _, m := range open {
		line := fmt.Sprintf("- open: %s asks%s (message %s): %s", m.Sender, on(m), m.ID, briefExcerpt(m.Body, 1024))
		for _, q := range m.Questions {
			if len(q.Options) > 0 {
				line += " [options: " + strings.Join(q.Options, ", ") + "]"
			}
		}
		b.WriteString(line + "\n")
	}
	for _, p := range answered {
		who := "the owner"
		if p.a.Person != "" {
			who = p.a.Person
		}
		fmt.Fprintf(b, "- answered: %s asked%s: %s\n  %s answered: %s\n", p.q.Sender, on(p.q), briefExcerpt(p.q.Body, 512), who, briefExcerpt(p.a.Body, 1024))
	}
	return nil
}
