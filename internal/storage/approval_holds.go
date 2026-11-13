package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/tasks"
)

// Approval holds the producing task (approval-gates-merge). While an artifact version a task produced (registered
// from its runs) has an open approval (PENDING: waiting for people, or for the delegate holding it, D18), Core
// refuses the task's done and the merge of its pull request; after its approved review the task waits as
// awaiting_approval (a park on the approval). The decision moves the task: approved or chosen wakes it to merge and
// finish; changes requested sends it back to its implementer on the same branch; rejected wakes it to close the pull
// request unmerged and finish without the merge (as abandon_branch). Approvals of other tasks' versions and tool-call
// (action, command) approvals do not hold it.

// parkJoins and parkRecipient read who clears a park (parkWho): the recipient of an ask or escalation question, and
// for awaiting_approval the delegate holding the approval ("" when it waits for people).
const (
	parkJoins = "LEFT JOIN messages m ON p.kind IN ('ask','escalation') AND m.id=p.ref_id" +
		" LEFT JOIN approvals ap ON p.kind='" + tasks.ParkApproval + "' AND ap.company_id=p.company_id AND ap.id=p.ref_id"
	parkRecipient = "coalesce(m.recipient, CASE WHEN coalesce((ap.body->>'escalated')::boolean,false) THEN '' ELSE ap.body->>'delegate' END, '')"
)

// ApprovalHold is an open artifact approval of a version the task produced.
type ApprovalHold struct {
	ApprovalID string `json:"approval_id"`
	ArtifactID string `json:"artifact_id"`
	Version    string `json:"version"`
	// Delegate is the agent holding the approval (approval-delegation, not escalated); "" when people decide it.
	Delegate string `json:"delegate,omitempty"`
}

// words: "artifact proto@3 waits for people's decision (approval A)".
func (h ApprovalHold) words() string {
	who := "people's decision"
	if h.Delegate != "" {
		who = h.Delegate + "'s decision"
	}
	return "artifact " + h.ArtifactID + "@" + h.Version + " waits for " + who + " (approval " + h.ApprovalID + ")"
}

func holdsWords(holds []ApprovalHold) string {
	out := make([]string, len(holds))
	for i, h := range holds {
		out[i] = h.words()
	}
	return strings.Join(out, "; ")
}

// approvalHolds lists the open artifact approvals of versions task produced, oldest approval first.
func approvalHolds(ctx context.Context, q querier, company, task string, now int64) ([]ApprovalHold, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id,o.artifact_id,o.artifact_version,
 CASE WHEN coalesce((a.body->>'escalated')::boolean,false) THEN '' ELSE coalesce(a.body->>'delegate','') END
 FROM approvals a
 JOIN approval_options o ON o.company_id=a.company_id AND o.approval_id=a.id
 JOIN artifact_versions v ON v.company_id=o.company_id AND v.project_id=o.project_id AND v.id=o.artifact_id AND v.version=o.artifact_version
 WHERE a.company_id=? AND a.kind='artifact' AND a.status='PENDING' AND (a.body->>'expires_at')::bigint>? AND v.body->>'task_id'=?
 ORDER BY a.seq,o.position`, company, now, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovalHold{}
	for rows.Next() {
		var h ApprovalHold
		if err = rows.Scan(&h.ApprovalID, &h.ArtifactID, &h.Version, &h.Delegate); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// approvalHold finds the open approval id among those holding t (a wait exit's approval_id), else the refusal.
func approvalHold(ctx context.Context, tx *Tx, t tasks.Task, id string) (ApprovalHold, error) {
	holds, err := approvalHolds(ctx, tx, tx.company, t.ID, tx.now().Unix())
	if err != nil {
		return ApprovalHold{}, err
	}
	for _, h := range holds {
		if h.ApprovalID == id {
			return h, nil
		}
	}
	if len(holds) > 0 {
		return ApprovalHold{}, refuse("approval_not_holding", "approval "+id+" does not hold this task; these do: "+holdsWords(holds))
	}
	return ApprovalHold{}, refuse("approval_not_holding", "approval "+id+" does not hold this task (it is decided, or not of a version this task produced): do not wait for it, continue")
}

// ApprovalHolds lists the open artifact approvals that hold the task (approval-gates-merge).
func (s *Store) ApprovalHolds(ctx context.Context, company, task string) ([]ApprovalHold, error) {
	return approvalHolds(ctx, s.DB, company, task, s.Now().Unix())
}

// holdHint is the refusal hint of a held done (and of a held merge).
func holdHint(holds []ApprovalHold) string {
	return holdsWords(holds) + ". The task cannot finish or merge its pull request before the decision. Finish the turn with factory_exit_wait approval_id " + holds[0].ApprovalID +
		" (no timer): the decision wakes the task. Approved or chosen: merge and exit done. Changes requested: rework on the same branch and submit again. Rejected: close the pull request unmerged and exit done."
}

// parkOnApproval parks t on the first open approval that holds it: the task waits as awaiting_approval.
func parkOnApproval(ctx context.Context, tx *Tx, t tasks.Task, holds []ApprovalHold, why string) error {
	if len(holds) == 0 {
		return nil
	}
	reason := "awaiting approval: " + holds[0].words()
	if why = strings.TrimSpace(why); why != "" {
		reason += ": " + why
	}
	return addPark(ctx, tx, t, tasks.ParkApproval, holds[0].ApprovalID, reason, holds[0].Delegate == "")
}

// heldTasks are the open tasks an artifact approval holds or held: the producers of its options' versions and the
// tasks parked on it.
func heldTasks(ctx context.Context, tx *Tx, a approvals.Approval) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT t.id FROM tasks t WHERE t.company_id=? AND t.status NOT IN ('DONE','CANCELLED') AND (
 t.id IN (SELECT v.body->>'task_id' FROM approval_options o JOIN artifact_versions v ON v.company_id=o.company_id AND v.project_id=o.project_id AND v.id=o.artifact_id AND v.version=o.artifact_version WHERE o.company_id=? AND o.approval_id=?)
 OR t.id IN (SELECT task_id FROM task_parks WHERE company_id=? AND kind=? AND ref_id=? AND status='active')) ORDER BY t.id`,
		tx.company, tx.company, a.ID, tx.company, tasks.ParkApproval, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// approvalParked tells whether t is parked on approval id.
func approvalParked(ctx context.Context, tx *Tx, task, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_parks WHERE company_id=? AND task_id=? AND kind=? AND ref_id=? AND status='active'", tx.company, task, tasks.ParkApproval, id).Scan(&n)
	return n > 0, err
}

// liveRunTx tells whether the task's current run is live (its agent sees new input as authority input instead).
func liveRunTx(ctx context.Context, tx *Tx, t tasks.Task) (bool, error) {
	if t.CurrentRunID == "" {
		return false, nil
	}
	var live bool
	err := tx.QueryRowContext(ctx, "SELECT status!='ENDED' OR unreleased=1 FROM runs WHERE company_id=? AND id=?", tx.company, t.CurrentRunID).Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return live, err
}

// moveHeldTask applies a decided artifact approval to a task it held and returns what the task's agent does next
// (appended to the approval_decided trigger). A live run is not moved: it sees the decision as authority input.
func moveHeldTask(ctx context.Context, tx *Tx, t *tasks.Task, a approvals.Approval) (string, error) {
	parked, err := approvalParked(ctx, tx, t.ID, a.ID)
	if err != nil {
		return "", err
	}
	if err = clearParks(ctx, tx, *t, tasks.ParkApproval, a.ID, "cleared"); err != nil {
		return "", err
	}
	holds, err := approvalHolds(ctx, tx, tx.company, t.ID, tx.now().Unix())
	if err != nil {
		return "", err
	}
	live, err := liveRunTx(ctx, tx, *t)
	if err != nil {
		return "", err
	}
	// back returns the task to its implementer (it reworks, or closes the pull request).
	back := func(feedback string) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM pr_approvals WHERE company_id=? AND task_id=?", tx.company, t.ID); err != nil {
			return err
		}
		if live || t.Implementer == "" || t.Status != "IN_REVIEW" && t.Status != "READY" {
			if t.ReviewChecks == nil {
				return nil
			}
			t.ReviewChecks = nil
			return saveTask(ctx, tx, t, "task.updated")
		}
		t.Assignee, t.Status, t.Reviewer, t.ReviewChecks = t.Implementer, "READY", "", nil
		if feedback != "" {
			t.ReviewFeedback = feedback
		}
		return saveTask(ctx, tx, t, "task.updated")
	}
	// The decision moves work that was submitted (in review, or reviewed and waiting for it); a task still working
	// before its first submit only gets the decision, as before (done and the merge stay refused while one is open).
	if !parked && (t.Submission == nil || t.Implementer == "") {
		return "", nil
	}
	switch a.Status {
	case "APPROVED":
		if len(holds) > 0 {
			if parked {
				if err = parkOnApproval(ctx, tx, *t, holds, ""); err != nil {
					return "", err
				}
			}
			return " The task still waits: " + holdsWords(holds) + ".", nil
		}
		return " The task's artifacts are decided: merge its pull request if it has one (update from the default branch first) and exit done.", nil
	case approvals.StatusChanges:
		if err = back("changes requested on " + approvalWhat(a) + " " + approvalDecidedBy(a) + ": " + a.DecisionReason); err != nil {
			return "", err
		}
		return " Do not merge: rework it on the same branch as asked, request approval of the new version and submit the task for review again.", nil
	case "REJECTED":
		// The branch is not merged (as abandon_branch): done needs no merge, and the command guard refuses one.
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(company_id,task_id,mode,updated_at,abandon_branch) VALUES(?,?,'worktree',?,1)
 ON CONFLICT(company_id,task_id) DO UPDATE SET abandon_branch=1,updated_at=excluded.updated_at`, tx.company, t.ID, tx.now().Unix()); err != nil {
			return "", err
		}
		if err = back(""); err != nil {
			return "", err
		}
		return " Do not merge: close the task's pull request unmerged if it has one (gh pr close <number>) and exit done; the branch is not merged and done needs no merge.", nil
	}
	return "", nil
}

// releaseApprovalParks wakes the tasks parked on an approval that left PENDING without a decision (superseded by a
// newer request, expired): a task still held by another open approval parks on that one instead.
func releaseApprovalParks(ctx context.Context, tx *Tx, a approvals.Approval, text string) error {
	ids, err := activeEventParks(ctx, tx, tasks.ParkApproval, a.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkApproval, a.ID, "cleared"); err != nil {
			return err
		}
		holds, err := approvalHolds(ctx, tx, tx.company, t.ID, tx.now().Unix())
		if err != nil {
			return err
		}
		if len(holds) > 0 {
			if err = parkOnApproval(ctx, tx, t, holds, ""); err != nil {
				return err
			}
			continue
		}
		if err = addTrigger(ctx, tx, &t, "approval_decided", false, "system", a.ID, text); err != nil {
			return err
		}
	}
	return nil
}

// decidedAgainst names a submitted artifact version whose latest approval asked for changes or rejected it (an
// approved verdict cannot pass over people's decision), "" when none.
func decidedAgainst(ctx context.Context, tx *Tx, project string, arts []tasks.Artifact) (string, error) {
	for _, art := range arts {
		var id, status string
		err := tx.QueryRowContext(ctx, `SELECT a.id,a.status FROM approvals a JOIN approval_options o ON o.company_id=a.company_id AND o.approval_id=a.id
 WHERE a.company_id=? AND a.kind='artifact' AND o.project_id=? AND o.artifact_id=? AND o.artifact_version=? ORDER BY a.seq DESC LIMIT 1`, tx.company, project, art.ID, art.Version).Scan(&id, &status)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		if status == approvals.StatusChanges || status == "REJECTED" {
			words := "asked for changes on"
			if status == "REJECTED" {
				words = "rejected"
			}
			return "the decider " + words + " " + art.ID + "@" + art.Version + " (approval " + id + "): give verdict changes_requested with that feedback", nil
		}
	}
	return "", nil
}

// MergeGuard says why the task's pull request must not be merged now ("" when it may), with the names of its default
// branch for the command guard (approval-gates-merge): an open approval of a version it produced holds it, and a
// rejected one abandoned its branch.
func (s *Store) MergeGuard(ctx context.Context, company, task string) (string, []string, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, task)
	if err != nil {
		return "", nil, err
	}
	branches := []string{}
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return "", nil, err
	}
	if p.DefaultBranch != "" {
		branches = append(branches, p.DefaultBranch)
	} else {
		var facts string
		if err = tx.QueryRowContext(ctx, "SELECT facts FROM run_exits WHERE company_id=? AND task_id=? AND facts!='' ORDER BY created_at DESC LIMIT 1", company, t.ID).Scan(&facts); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", nil, err
		}
		var f ExitFacts
		if facts != "" && json.Unmarshal([]byte(facts), &f) == nil && f.DefaultBranch != "" {
			branches = append(branches, f.DefaultBranch)
		}
	}
	if len(branches) == 0 {
		branches = []string{"main", "master"}
	}
	if t.Kind == tasks.KindConversation {
		return "", branches, nil
	}
	holds, err := approvalHolds(ctx, tx, company, t.ID, tx.now().Unix())
	if err != nil {
		return "", nil, err
	}
	if len(holds) > 0 {
		return holdHint(holds), branches, nil
	}
	var abandoned bool
	if err = tx.QueryRowContext(ctx, "SELECT abandon_branch FROM workspaces WHERE company_id=? AND task_id=?", company, t.ID).Scan(&abandoned); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if abandoned {
		return "the task's branch is abandoned (abandon_branch, or a rejected artifact): it is not merged. Close the pull request unmerged (gh pr close <number>) and exit done.", branches, nil
	}
	return "", branches, nil
}

// artifactChecks reads an approved verdict's artifact_checks.
func artifactChecks(args map[string]any) []tasks.ArtifactCheck {
	out := []tasks.ArtifactCheck{}
	if raw, ok := args["artifact_checks"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// checkArtifactChecks matches an approved verdict's artifact_checks to the submission's artifacts: every artifact
// is listed, checked, with a note. It returns the checks in the submission's order, or the refusal.
func checkArtifactChecks(t tasks.Task, args map[string]any) ([]tasks.ArtifactCheck, *ExitRefusal) {
	var arts []tasks.Artifact
	if t.Submission != nil {
		arts = t.Submission.Artifacts
	}
	checks := artifactChecks(args)
	if len(arts) == 0 {
		return nil, nil
	}
	names := make([]string, len(arts))
	for i, a := range arts {
		names[i] = a.ID + "@" + a.Version
	}
	how := " Check each one (open the file, run the prototype) and list it as {id, version, checked: true, note: what you checked}; if you cannot check one, give verdict changes_requested, or hand the review to an agent who can check it (factory_exit_handoff to that agent, without a verdict)."
	// match finds the submitted artifact a check names: id and version, "id@version" in id, or an id with one version.
	match := func(c tasks.ArtifactCheck) int {
		id, version := strings.TrimSpace(c.ID), strings.TrimSpace(c.Version)
		if version == "" {
			if i, v, ok := strings.Cut(id, "@"); ok && slices.Contains(names, id) {
				id, version = i, v
			}
		}
		found := -1
		for i, a := range arts {
			if a.ID == id && (a.Version == version || version == "") {
				if found >= 0 && version == "" {
					return -2 // several versions of the id: name the version
				}
				found = i
			}
		}
		return found
	}
	out := make([]tasks.ArtifactCheck, len(arts))
	listed := make([]bool, len(arts))
	for _, c := range checks {
		i := match(c)
		switch {
		case i == -2:
			return nil, refuse("review_incomplete", "artifact_checks: "+c.ID+" has several versions in the submission ("+strings.Join(names, ", ")+"): name the version")
		case i < 0:
			return nil, refuse("review_incomplete", "artifact_checks: "+strings.TrimSpace(c.ID+" "+c.Version)+" is not an artifact of the submission ("+strings.Join(names, ", ")+")")
		case strings.TrimSpace(c.Note) == "":
			return nil, refuse("review_incomplete", "artifact_checks: "+names[i]+" needs a note: what you checked, or why you could not."+how)
		}
		out[i] = tasks.ArtifactCheck{ID: arts[i].ID, Version: arts[i].Version, Checked: c.Checked, Note: shortText(strings.TrimSpace(c.Note), 1024)}
		listed[i] = true
	}
	var missing, unchecked []string
	for i := range arts {
		switch {
		case !listed[i]:
			missing = append(missing, names[i])
		case !out[i].Checked:
			unchecked = append(unchecked, names[i]+" ("+out[i].Note+")")
		}
	}
	if len(missing) > 0 {
		return nil, refuse("review_incomplete", "approved covers every artifact of the submission: list each in artifact_checks; missing "+strings.Join(missing, ", ")+"."+how)
	}
	if len(unchecked) > 0 {
		return nil, refuse("artifact_not_checked", "approved needs every artifact checked; not checked: "+strings.Join(unchecked, "; ")+"."+how)
	}
	return out, nil
}
