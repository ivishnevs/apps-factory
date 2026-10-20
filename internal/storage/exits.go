package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Explicit exits, Core side (docs/explicit-exits-contract.md). A run ends its task's turn with exactly one
// accepted exit; Core checks run identity, the sequences the agent has seen and the node's git facts, then
// applies the exit in one transaction. Enabled with Store.ExplicitExits.

// ExitFacts are the node's git and forge facts at the exit (collected by the MCP adapter).
type ExitFacts struct {
	Head           string     `json:"head"`
	Dirty          bool       `json:"dirty"`
	Branch         string     `json:"branch"`
	DefaultBranch  string     `json:"default_branch"`
	AheadOfDefault int        `json:"ahead_of_default"`
	Pushed         bool       `json:"pushed"`
	PR             *PRFacts   `json:"pr"`
	Scan           *ScanFacts `json:"scan,omitempty"`
}

type PRFacts struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	HeadSHA string `json:"head_sha"`
	URL     string `json:"url,omitempty"`
}

type ScanFacts struct {
	OK     bool   `json:"ok"`
	Report string `json:"report,omitempty"`
}

type ExitRequest struct {
	Kind      string         `json:"kind"`
	Arguments map[string]any `json:"arguments"`
	AckSeq    int64          `json:"ack_seq"`
	Facts     *ExitFacts     `json:"facts,omitempty"`
	// Preflight checks without changing anything (the adapter calls it before pushing).
	Preflight bool `json:"-"`
	// Known reports whether an agent name is in Core's catalog (handoff targets, reviewers).
	Known func(agent string) bool `json:"-"`
}

type ExitTask struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
	Version  int    `json:"version"`
}

type ExitResult struct {
	Exit   string   `json:"exit"`
	Task   ExitTask `json:"task"`
	AckSeq int64    `json:"ack_seq"`
	// Note explains a converted exit (a block that could not apply became an ask).
	Note string `json:"note,omitempty"`
}

// UnseenTrigger is a trigger above the agent's ack_seq, returned with a 409 so the agent sees it.
type UnseenTrigger struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Author    string `json:"author"`
	Authority bool   `json:"authority"`
	Text      string `json:"text"`
}

// ExitRefusal is a 409 or 410 answer to an exit; nothing changed.
type ExitRefusal struct {
	Status int             `json:"-"`
	Reason string          `json:"reason"`
	Unseen []UnseenTrigger `json:"unseen,omitempty"`
	Hint   string          `json:"hint,omitempty"`
}

func (r *ExitRefusal) Error() string { return "exit refused: " + r.Reason + " " + r.Hint }

func refuse(reason, hint string) *ExitRefusal {
	return &ExitRefusal{Status: 409, Reason: reason, Hint: hint}
}

var exitKinds = map[string]bool{"submit": true, "handoff": true, "done": true, "ask": true, "block": true, "wait": true}

// Exit checks and (unless preflight) applies the run's exit.
func (s *Store) Exit(ctx context.Context, company, runID string, req ExitRequest) (ExitResult, error) {
	var out ExitResult
	if !exitKinds[req.Kind] {
		return out, refuse("invalid_exit", "unknown exit "+req.Kind)
	}
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var status, agent, taskID string
	err = tx.QueryRowContext(ctx, "SELECT status,agent,coalesce(task_id,'') FROM runs WHERE company_id=? AND id=?", company, runID).Scan(&status, &agent, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &ExitRefusal{Status: 410, Reason: "not_current_run"}
	}
	if err != nil {
		return out, err
	}
	if taskID == "" {
		return out, refuse("invalid_exit", "this run has no task; finish the turn")
	}
	// A retried exit returns its stored result.
	var kind, args, stored string
	err = tx.QueryRowContext(ctx, "SELECT kind,arguments,result FROM run_exits WHERE run_id=?", runID).Scan(&kind, &args, &stored)
	if err == nil {
		want, _ := json.Marshal(req.Arguments)
		if kind != req.Kind || !sameJSON(args, string(want)) {
			return out, refuse("exit_already_accepted", "this run already ended with factory_exit_"+kind+"; finish the turn")
		}
		err = json.Unmarshal([]byte(stored), &out)
		return out, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	t, err := taskTx(ctx, tx, taskID)
	if err != nil {
		return out, err
	}
	if status != "RUNNING" || t.CurrentRunID != runID {
		return out, &ExitRefusal{Status: 410, Reason: "not_current_run", Hint: "this run was stopped or replaced; finish the turn"}
	}
	if err = validExit(req, t, agent); err != nil {
		return out, err
	}
	if str(req.Arguments, "merge_failed") != "" {
		// A failed merge is reported by the implementer of an approved task (task-worktrees D7).
		var approvals int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pr_approvals WHERE company_id=? AND task_id=?", company, t.ID).Scan(&approvals); err != nil {
			return out, err
		}
		if approvals == 0 || t.Implementer != agent {
			return out, refuse("invalid_exit", "merge_failed is for the implementer of an approved task; otherwise hand off, ask or block")
		}
	}
	if unseen, err := unseenTriggers(ctx, tx, t.ID, req.AckSeq, req.Kind == "done"); err != nil {
		return out, err
	} else if len(unseen) > 0 {
		return out, &ExitRefusal{Status: 409, Reason: "unseen_input", Unseen: unseen, Hint: "new input arrived for this task; read it, act on it, then exit again"}
	}
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return out, err
	}
	var abandoned bool
	if err = tx.QueryRowContext(ctx, "SELECT abandon_branch FROM workspaces WHERE company_id=? AND task_id=?", company, t.ID).Scan(&abandoned); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if r := checkFacts(req, p.RepoURL != "", abandoned); r != nil {
		return out, r
	}
	if req.Kind == "done" && p.ReviewAfterConflicts == "required" && !abandoned && req.Facts != nil && req.Facts.PR != nil {
		// The merged head must be the approved one; a head changed after the approval needs a new review.
		var approved string
		err = tx.QueryRowContext(ctx, "SELECT approved_head FROM pr_approvals WHERE company_id=? AND task_id=? AND pr_number=? ORDER BY created_at DESC, rowid DESC LIMIT 1", company, t.ID, req.Facts.PR.Number).Scan(&approved)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if approved == "" || approved != req.Facts.PR.HeadSHA {
			return out, refuse("needs_approval", "the pull request head changed after its approval (project policy review_after_conflicts: required): submit again for review before merging")
		}
	}
	if req.Preflight {
		return ExitResult{Exit: req.Kind, Task: exitTask(t), AckSeq: req.AckSeq}, nil
	}
	out, err = applyExit(ctx, tx, runID, agent, t, req)
	if err != nil {
		return out, err
	}
	argsJSON, _ := json.Marshal(req.Arguments)
	factsJSON := ""
	exitSHA := ""
	if req.Facts != nil {
		b, _ := json.Marshal(req.Facts)
		factsJSON, exitSHA = string(b), req.Facts.Head
	}
	result, _ := json.Marshal(out)
	if _, err = tx.ExecContext(ctx, "INSERT INTO run_exits(company_id,run_id,task_id,kind,arguments,ack_seq,facts,exit_sha,result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		company, runID, t.ID, req.Kind, string(argsJSON), req.AckSeq, factsJSON, exitSHA, string(result), tx.now().Unix()); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET handled_seq=max(handled_seq,?) WHERE company_id=? AND id=?", req.AckSeq, company, t.ID); err != nil {
		return out, err
	}
	if exitSHA != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE workspaces SET exit_sha=?,updated_at=? WHERE company_id=? AND task_id=?", exitSHA, tx.now().Unix(), company, t.ID); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func exitTask(t tasks.Task) ExitTask {
	return ExitTask{ID: t.ID, Status: t.Status, Assignee: t.Assignee, Version: t.Version}
}

func str(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return strings.TrimSpace(v)
}

func strs(args map[string]any, k string) []string {
	raw, _ := args[k].([]any)
	out := []string{}
	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func validExit(req ExitRequest, t tasks.Task, agent string) error {
	a := req.Arguments
	known := func(name string) bool { return req.Known == nil || req.Known(name) }
	switch req.Kind {
	case "submit", "done":
		if str(a, "summary") == "" {
			return refuse("invalid_exit", "summary required")
		}
		if r := str(a, "reviewer"); r != "" && (r == agent || !known(r)) {
			return refuse("invalid_exit", "reviewer must be another configured agent")
		}
	case "handoff":
		verdict := str(a, "verdict")
		switch verdict {
		case "":
			if str(a, "to") == "" || !known(str(a, "to")) {
				return refuse("invalid_exit", "to must be a configured agent")
			}
		case "approved", "changes_requested":
			if t.Status != "IN_REVIEW" || t.Implementer == "" || t.Assignee != agent {
				return refuse("invalid_exit", "a verdict is given by the reviewer of a task in review")
			}
			if verdict == "changes_requested" && str(a, "feedback") == "" {
				return refuse("invalid_exit", "changes_requested needs feedback")
			}
		default:
			return refuse("invalid_exit", "verdict must be approved or changes_requested")
		}
		if str(a, "note") == "" {
			return refuse("invalid_exit", "note required")
		}
		if str(a, "merge_failed") != "" && (verdict != "" || str(a, "to") != agent) {
			return refuse("invalid_exit", "merge_failed hands the task back to yourself (to: "+agent+"), without a verdict")
		}
	case "ask":
		if str(a, "question") == "" {
			return refuse("invalid_exit", "question required")
		}
	case "block":
		if str(a, "reason") == "" {
			return refuse("invalid_exit", "reason required")
		}
	case "wait":
		at, err := time.Parse(time.RFC3339, str(a, "wake_at"))
		if err != nil || str(a, "reason") == "" {
			return refuse("invalid_exit", "reason and wake_at (RFC 3339) required")
		}
		_ = at
	}
	return nil
}

// checkFacts applies the clean-tree, secret-scan and merged-PR rules (contract §3, §6).
func checkFacts(req ExitRequest, repo, abandoned bool) *ExitRefusal {
	code := req.Kind == "submit" || req.Kind == "handoff" || req.Kind == "done"
	f := req.Facts
	if f == nil {
		if code && repo {
			return refuse("facts_required", "this project has a repository: the MCP adapter must send exit facts (update factory-mcp)")
		}
		return nil
	}
	if f.Scan != nil && !f.Scan.OK {
		return refuse("secret_found", "the secret scan found credentials in your commits: "+shortText(f.Scan.Report, 2048))
	}
	if !code {
		return nil
	}
	if f.Dirty {
		return refuse("dirty_tree", "commit or discard your changes, or move notes to $FACTORY_SCRATCH, then exit again")
	}
	if req.Kind == "done" && !abandoned && f.AheadOfDefault > 0 && (f.PR == nil || f.PR.State != "merged") {
		return refuse("pr_not_merged", "merge your PR first: rebase on the default branch, resolve conflicts, build, merge")
	}
	return nil
}

// unseenTriggers are the task's triggers above ackSeq that the exit must not pass: authority input, and for
// done any trigger.
func unseenTriggers(ctx context.Context, tx *Tx, task string, ackSeq int64, all bool) ([]UnseenTrigger, error) {
	rows, err := tx.QueryContext(ctx, "SELECT seq,kind,actor,authority,body FROM task_triggers WHERE company_id=? AND task_id=? AND seq>? AND (? OR authority=1) ORDER BY seq LIMIT 20", tx.company, task, ackSeq, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnseenTrigger{}
	for rows.Next() {
		var u UnseenTrigger
		if err = rows.Scan(&u.Seq, &u.Kind, &u.Author, &u.Authority, &u.Text); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func applyExit(ctx context.Context, tx *Tx, runID, agent string, t tasks.Task, req ExitRequest) (ExitResult, error) {
	a := req.Arguments
	out := ExitResult{Exit: req.Kind, AckSeq: req.AckSeq}
	now := tx.now()
	t.NotBefore = 0
	event := "task.updated"
	escalate := "" // a failed merge that exhausted the attempts
	switch req.Kind {
	case "submit":
		sub := tasks.Submission{Summary: str(a, "summary"), Checks: strs(a, "checks")}
		if req.Facts != nil {
			sub.Commit = req.Facts.Head
		}
		raw, _ := json.Marshal(a["artifacts"])
		_ = json.Unmarshal(raw, &sub.Artifacts)
		for _, art := range sub.Artifacts {
			if _, err := registerArtifactTx(ctx, tx, t.ProjectID, artifacts.Register{Artifact: art, TaskID: t.ID, RunID: runID, Actor: agent}); err != nil {
				return out, err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO task_artifacts VALUES(?,?,?,?,?,?)", t.ID, runID, art.ID, art.Version, art.URI, tx.company); err != nil {
				return out, err
			}
		}
		t.Submission, t.Status, t.Result, t.Implementer = &sub, "IN_REVIEW", sub.Summary, agent
		t.Reviewer = str(a, "reviewer")
		if t.Reviewer != "" {
			t.Assignee = t.Reviewer
		}
		event = "task.submitted"
	case "handoff":
		switch str(a, "verdict") {
		case "approved":
			if f := req.Facts; f != nil && f.PR != nil {
				if _, err := tx.ExecContext(ctx, "INSERT INTO pr_approvals(company_id,task_id,pr_number,approved_head,approver,run_id,created_at) VALUES(?,?,?,?,?,?,?)", tx.company, t.ID, f.PR.Number, f.PR.HeadSHA, agent, runID, now.Unix()); err != nil {
					return out, err
				}
			}
			t.Assignee, t.Status, t.ReviewFeedback, t.Reviewer = t.Implementer, "READY", "", ""
		case "changes_requested":
			t.Assignee, t.Status, t.ReviewFeedback, t.Reviewer = t.Implementer, "READY", str(a, "feedback"), ""
		default:
			if to := str(a, "to"); to != t.Assignee {
				t.Assignee, t.AttemptsNoProgress, t.FailStreak = to, 0, 0
			}
			t.Status = "READY"
			if why := str(a, "merge_failed"); why != "" {
				// A failed merge is an ordinary attempt (task-worktrees D7): backoff, and the third in a row
				// without a merge escalates retries_exhausted.
				t.AttemptsNoProgress++
				t.FailStreak++
				t.NotBefore = now.Add(backoff(t.FailStreak)).Unix()
				t.Result = "merge failed: " + why
				if t.AttemptsNoProgress >= RetryAttempts {
					t.Status, t.NotBefore = "FAILED", 0
					escalate = fmt.Sprintf("%d merge attempts in a row failed; last: %s", t.AttemptsNoProgress, excerpt512(why))
				}
				if err := comment(ctx, tx, t.ID, agent, "merge failed: "+why); err != nil {
					return out, err
				}
			}
		}
		if err := comment(ctx, tx, t.ID, agent, "handoff: "+str(a, "note")); err != nil {
			return out, err
		}
	case "done":
		t.Status, t.Result = "DONE", str(a, "summary")
	case "ask":
		if err := exitAsk(ctx, tx, runID, agent, &t, str(a, "question"), strs(a, "options"), str(a, "evidence"), str(a, "category")); err != nil {
			return out, err
		}
	case "block":
		note, err := exitBlock(ctx, tx, runID, agent, &t, strs(a, "on"), a["on_children"] == true, str(a, "reason"))
		if err != nil {
			return out, err
		}
		out.Note = note
	case "wait":
		at, _ := time.Parse(time.RFC3339, str(a, "wake_at"))
		t.Status, t.NotBefore = "READY", at.Unix()
		if err := addPark(ctx, tx, t, tasks.ParkWait, strconv.FormatInt(at.Unix(), 10), "waiting until "+at.UTC().Format(time.RFC3339)+": "+str(a, "reason"), false); err != nil {
			return out, err
		}
	}
	if err := saveTask(ctx, tx, &t, event); err != nil {
		return out, err
	}
	switch {
	case req.Kind == "done":
		if err := closedTask(ctx, tx, t); err != nil {
			return out, err
		}
	case req.Kind == "submit" && t.Reviewer != "":
		if err := addTrigger(ctx, tx, t, "assignment", false, agent, runID, "review requested by "+agent); err != nil {
			return out, err
		}
	case escalate != "":
		if err := openEscalation(ctx, tx, "retries_exhausted", escalationAnswers["retries_exhausted"], EscalationWant{TaskID: t.ID, Summary: escalate, Target: retryTarget(tx, t.Assignee)}); err != nil {
			return out, err
		}
	case req.Kind == "handoff":
		if err := addTrigger(ctx, tx, t, "assignment", false, agent, runID, "handoff from "+agent+": "+str(a, "note")); err != nil {
			return out, err
		}
	}
	t, err := taskTx(ctx, tx, t.ID)
	out.Task = exitTask(t)
	return out, err
}

// exitAsk asks the owner (the manager chain after hierarchy) a blocking question for the task.
// exitAsk asks the agent's manager (reports_to; the owner at the top and for locked categories), who
// answers or forwards it up the chain (hierarchy D3).
func exitAsk(ctx context.Context, tx *Tx, runID, agent string, t *tasks.Task, question string, options []string, evidence, category string) error {
	body := question
	if evidence != "" {
		body += "\n\nEvidence: " + evidence
	}
	in := messaging.Send{ProjectID: t.ProjectID, TaskID: t.ID, Sender: agent, Recipient: routeUp(tx, agent, category), MessageType: "question", Body: body, Blocking: true, RunID: runID, RequestID: "exit-ask-" + runID, Category: category, UpChain: true}
	if len(options) > 1 {
		in.Questions = []messaging.Question{{ID: "answer", Type: "single_choice", Question: question, Options: options}}
	}
	if _, err := sendMessageTx(ctx, tx, in); err != nil {
		return err
	}
	fresh, err := taskTx(ctx, tx, t.ID) // sendMessageTx recorded the wait on the task
	if err != nil {
		return err
	}
	*t = fresh
	return nil
}

// exitBlock waits on open tasks of the project (as dependencies, so the scheduler and the stalled
// escalation already handle them) or on the task's open children; a block that cannot apply becomes an ask.
func exitBlock(ctx context.Context, tx *Tx, runID, agent string, t *tasks.Task, on []string, children bool, reason string) (string, error) {
	fallback := func(why string) (string, error) {
		if err := exitAsk(ctx, tx, runID, agent, t, "I am blocked and cannot wait on what I named ("+why+"): "+reason, nil, "", ""); err != nil {
			return "", err
		}
		return "block could not apply (" + why + "); asked your manager instead", nil
	}
	if children {
		var open int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND json_extract(body,'$.parent_task_id')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, t.ID).Scan(&open); err != nil {
			return "", err
		}
		if open == 0 {
			return fallback("no open children")
		}
		t.Status, t.BlockReason = "BLOCKED", "children: waiting for open children"
		return "", addPark(ctx, tx, *t, tasks.ParkChildren, "", reason, false)
	}
	if len(on) == 0 {
		return fallback("no tasks named")
	}
	deps := slices.Clone(t.Dependencies)
	for _, id := range on {
		d, err := taskTx(ctx, tx, id)
		if err != nil || d.ProjectID != t.ProjectID || d.Status == "DONE" || d.Status == "CANCELLED" || d.ID == t.ID {
			return fallback("task " + id + " is not an open task of this project")
		}
		if !slices.Contains(deps, id) {
			deps = append(deps, id)
		}
	}
	candidate := *t
	candidate.Dependencies = deps
	if err := dependencies(ctx, tx, candidate); errors.Is(err, ErrCycle) {
		if err = dependencies(ctx, tx, *t); err != nil {
			return "", err
		}
		return fallback("waiting would create a cycle")
	} else if err != nil {
		return "", err
	}
	t.Dependencies, t.Status = deps, "READY"
	return "", nil
}

// ExpireWaits wakes tasks whose wait exit reached its time: the wait park clears with a wake trigger.
func (s *Store) ExpireWaits(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT task_id,ref_id FROM task_parks WHERE company_id=? AND kind=? AND status='active'", company, tasks.ParkWait)
	if err != nil {
		return err
	}
	type due struct{ task, ref string }
	var wake []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.task, &d.ref); err != nil {
			rows.Close()
			return err
		}
		if at, e := strconv.ParseInt(d.ref, 10, 64); e == nil && at <= tx.now().Unix() {
			wake = append(wake, d)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, d := range wake {
		t, err := taskTx(ctx, tx, d.task)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkWait, d.ref, "cleared"); err != nil {
			return err
		}
		if err = addTrigger(ctx, tx, t, "wake", false, "system", d.ref, "wait time reached"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// runHasExit reports whether the run ended its turn with an accepted exit.
// exitMergeFailed tells whether the run's accepted exit reported a failed merge (handoff merge_failed).
func exitMergeFailed(ctx context.Context, tx *Tx, runID string) (bool, error) {
	var why string
	err := tx.QueryRowContext(ctx, "SELECT coalesce(json_extract(arguments,'$.merge_failed'),'') FROM run_exits WHERE run_id=?", runID).Scan(&why)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return strings.TrimSpace(why) != "", err
}

func runHasExit(ctx context.Context, tx *Tx, runID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM run_exits WHERE run_id=?", runID).Scan(&n)
	return n > 0, err
}

// AuthorityInput is an authority trigger shown to a running agent (contract §4, §7).
type AuthorityInput struct {
	Seq    int64  `json:"seq"`
	Author string `json:"author"`
	Rank   int    `json:"rank"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
}

// AuthorityInputSince lists the task's authority triggers above ackSeq (rank: owner 0, others 1 until the
// hierarchy gives depths).
func (s *Store) AuthorityInputSince(ctx context.Context, company, task string, ackSeq int64) ([]AuthorityInput, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,actor,kind,body FROM task_triggers WHERE company_id=? AND task_id=? AND seq>? AND authority=1 ORDER BY seq LIMIT 20", company, task, ackSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthorityInput{}
	for rows.Next() {
		var a AuthorityInput
		if err = rows.Scan(&a.Seq, &a.Author, &a.Kind, &a.Text); err != nil {
			return nil, err
		}
		if a.Author != "owner" {
			a.Rank = 1
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BriefEnd is the run's brief window end: the MCP adapter's starting ack_seq.
func (s *Store) BriefEnd(ctx context.Context, company, run string) (int64, string, error) {
	var to int64
	var task string
	err := s.DB.QueryRowContext(ctx, "SELECT brief_to,coalesce(task_id,'') FROM runs WHERE company_id=? AND id=?", company, run).Scan(&to, &task)
	return to, task, err
}

// Directive is authority input to a task (contract §7): an authority trigger that overrides the task's
// interruptible parks and retry escalations; urgent stops its running run, which then resumes with it.
func (s *Store) Directive(ctx context.Context, company, task, actor, text string, urgent bool, key string) (tasks.Task, error) {
	input := struct {
		Actor, Text string
		Urgent      bool
	}{actor, text, urgent}
	return mutate(ctx, s, company, "task.directive:"+task, key, input, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, task)
		if err != nil {
			return t, err
		}
		if key == "" || strings.TrimSpace(text) == "" || len(text) > 16*1024 {
			return t, inputError("request_id and text up to 16 KiB required")
		}
		if t.Status == "DONE" || t.Status == "CANCELLED" {
			return t, ErrConflict
		}
		if err = addTrigger(ctx, tx, t, "directive", true, actor, "", text); err != nil {
			return t, err
		}
		// Authority input counts as a retry answer and as progress.
		if err = resetRetry(ctx, tx, &t); err != nil {
			return t, err
		}
		if err = clearRetryEscalations(ctx, tx, t, "directive from "+actor); err != nil {
			return t, err
		}
		if err = clearOpenEscalation(ctx, tx, t, "no_exit", "directive from "+actor); err != nil {
			return t, err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkWait, anyRef, "overridden"); err != nil {
			return t, err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkChildren, anyRef, "overridden"); err != nil {
			return t, err
		}
		waiting, err := askPark(ctx, tx, tx.company, t.ID)
		if err != nil {
			return t, err
		}
		switch {
		case t.Status == "FAILED", t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "children:"):
			t.Status, t.BlockReason = "READY", ""
		case waiting != "" && t.Status == "READY":
			// The question of an ended run is superseded by the directive.
			if m, err := messageTx(ctx, tx, waiting); err == nil && m.Status == "PENDING" {
				m.Status, m.DeliveryStatus, m.EscalationReason = "WITHDRAWN", "NONE", "superseded by a directive from "+actor
				if err = saveMessage(ctx, tx, &m, "message.withdrawn"); err != nil {
					return t, err
				}
			}
			if err = clearParks(ctx, tx, t, tasks.ParkAsk, anyRef, "overridden"); err != nil {
				return t, err
			}
			t.BlockReason = ""
		}
		if urgent && t.CurrentRunID != "" {
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLING',revoked=1,stop_reason='directive' WHERE id=? AND status='RUNNING'", t.CurrentRunID); err != nil {
				return t, err
			}
		}
		return t, saveTask(ctx, tx, &t, "task.updated")
	})
}

// PendingDirectives are the task's directives above handled_seq, oldest first (for the next brief).
func (s *Store) PendingDirectives(ctx context.Context, company, task string) ([]AuthorityInput, error) {
	var handled int64
	if err := s.DB.QueryRowContext(ctx, "SELECT handled_seq FROM tasks WHERE company_id=? AND id=?", company, task).Scan(&handled); err != nil {
		return nil, err
	}
	return s.AuthorityInputSince(ctx, company, task, handled)
}

func clearOpenEscalation(ctx context.Context, tx *Tx, t tasks.Task, reason, why string) error {
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND task_id=? AND status='open' AND reason=?", tx.company, t.ID, reason)
	if err != nil {
		return err
	}
	for _, e := range open {
		if err = closeEscalation(ctx, tx, e, "cleared", "", "system", why); err != nil {
			return err
		}
	}
	return nil
}

// AbandonBranch records an authority's decision that the task's branch is not merged (done no longer needs a
// merged pull request).
func (s *Store) AbandonBranch(ctx context.Context, company, task, actor string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, task)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(company_id,task_id,mode,updated_at,abandon_branch) VALUES(?,?,'worktree',?,1)
 ON CONFLICT(company_id,task_id) DO UPDATE SET abandon_branch=1,updated_at=excluded.updated_at`, company, t.ID, tx.now().Unix()); err != nil {
		return err
	}
	if err = comment(ctx, tx, t.ID, actor, "abandon_branch: the task's branch will not be merged"); err != nil {
		return err
	}
	if err = addTrigger(ctx, tx, t, "answer", isOwner(actor), actor, "", "abandon_branch"); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingTriggers are the task's triggers above handled_seq, oldest first, with author and authority (the
// trigger part of the brief, trigger-log-scheduling D8).
func (s *Store) PendingTriggers(ctx context.Context, company, task string) ([]UnseenTrigger, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.seq,g.kind,g.actor,g.authority,g.body FROM task_triggers g JOIN tasks t ON t.company_id=g.company_id AND t.id=g.task_id
 WHERE g.company_id=? AND g.task_id=? AND g.seq>t.handled_seq ORDER BY g.seq LIMIT 20`, company, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnseenTrigger{}
	for rows.Next() {
		var u UnseenTrigger
		if err = rows.Scan(&u.Seq, &u.Kind, &u.Author, &u.Authority, &u.Text); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreatedBy lists the tasks created by runs of task (origin_task), with their status.
func (s *Store) CreatedBy(ctx context.Context, company, task string) ([]tasks.Task, error) {
	ts, err := listDocs[tasks.Task](ctx, s.DB, "SELECT body FROM tasks WHERE company_id=? AND json_extract(body,'$.origin_task')=? ORDER BY created_at,id", company, task)
	if err == nil {
		err = presentTasks(ctx, s.DB, company, ts)
	}
	return ts, err
}
