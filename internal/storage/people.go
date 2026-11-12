package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// People as assignees (people-assignees): a task assigned to a person (member:USER) has the same statuses as any
// task. Core dispatches no run for it; the person (or the owner) moves it with start, submit (with the pull
// request) and done (after the approval, with the pull request merged). Its dependents wait for it in the normal
// dependency wait, never as a stall; the person gets a reminder when it has not changed for a while.

var personAssignee = regexp.MustCompile(`^member:\S{1,128}$`)

// validAssignee is an agent name or a person (member:USER); Core checks that either exists.
func validAssignee(a string) bool { return entityName.MatchString(a) || personAssignee.MatchString(a) }

// personLabel is how a person assignee is named: the user's name, else the e-mail's local part, else the user id;
// "" for an agent.
func personLabel(ctx context.Context, q querier, assignee string) string {
	user, ok := tasks.PersonUser(assignee)
	if !ok {
		return ""
	}
	var name, email string
	if err := q.QueryRowContext(ctx, "SELECT name,email FROM users WHERE id=?", user).Scan(&name, &email); err != nil {
		return user
	}
	if name != "" {
		return name
	}
	if local, _, _ := strings.Cut(email, "@"); local != "" {
		return local
	}
	return user
}

// personRef is " (member:USER)" for a person's message, so an agent can give them a task; "" without a user.
func personRef(user string) string {
	if user == "" {
		return ""
	}
	return " (" + tasks.PersonPrefix + user + ")"
}

// PersonLabel is personLabel for Core (reminders, notices).
func (s *Store) PersonLabel(ctx context.Context, assignee string) string {
	return personLabel(ctx, s.DB, assignee)
}

// DefaultPeopleRemindAfter is how long a person's task may stay unchanged while tasks wait on it before the person
// is reminded.
const DefaultPeopleRemindAfter = 72 * time.Hour

// PeopleRemindAfter is the company's people.remind_after.
func (s *Store) PeopleRemindAfter(ctx context.Context, company string) (time.Duration, error) {
	var minutes int64
	err := s.DB.QueryRowContext(ctx, "SELECT people_remind_after_minutes FROM companies WHERE id=?", company).Scan(&minutes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && minutes <= 0 {
		return DefaultPeopleRemindAfter, nil
	}
	return time.Duration(minutes) * time.Minute, err
}

// SetPeopleRemindAfter sets people.remind_after (1 hour to 30 days).
func (s *Store) SetPeopleRemindAfter(ctx context.Context, company string, d time.Duration) error {
	if d < time.Hour || d > 30*24*time.Hour {
		return inputError("remind_after must be between 1h and 720h")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET people_remind_after_minutes=? WHERE id=?", int64(d/time.Minute), company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// ErrNotPersonTask refuses start, submit and done on an agent's task: its run moves it.
var ErrNotPersonTask = fmt.Errorf("%w: start, submit and done are for a task assigned to a person (member:USER); an agent's run moves its own task", ErrInput)

// PersonAction moves a person's task (Core checked who acts): start (READY or REWORK → IN_PROGRESS, once its
// dependencies are done), submit (→ IN_REVIEW with a summary and, in a project with a repository, the pull request;
// an agent reviewer gets a review run) and done (an approved submission whose pull request the person confirms
// merged → DONE, which releases the dependents).
func (s *Store) PersonAction(ctx context.Context, company, id string, in tasks.Action) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.person:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if t.Version != in.Version {
			return t, ErrConflict
		}
		if !tasks.IsPerson(t.Assignee) {
			return t, ErrNotPersonTask
		}
		if err = editableRun(ctx, tx, t); err != nil {
			return t, err
		}
		view := t
		if err = presentTask(ctx, tx, tx.company, &view); err != nil {
			return t, err
		}
		name := personLabel(ctx, tx, t.Assignee)
		event := "task.updated"
		switch in.Action {
		case "start":
			if view.Status != "READY" && view.Status != "REWORK" {
				return t, fmt.Errorf("%w: only a READY task (or one in rework) starts; this one is %s", ErrConflict, view.Status)
			}
			if open, err := openDependencies(ctx, tx, t); err != nil {
				return t, err
			} else if len(open) > 0 {
				return t, fmt.Errorf("%w: the task waits for its dependencies (%s); it starts when they are done", ErrConflict, strings.Join(open, ", "))
			}
			t.Status = "IN_PROGRESS"
		case "submit":
			if view.Status != "IN_PROGRESS" && view.Status != "READY" && view.Status != "REWORK" {
				return t, fmt.Errorf("%w: only a task in progress (or in rework) is submitted for review; this one is %s", ErrConflict, view.Status)
			}
			summary := strings.TrimSpace(in.Summary)
			if summary == "" {
				return t, inputError("summary required: what was done")
			}
			if t.ReviewBy == "" {
				return t, inputError("set review_by first: the task's reviewer")
			}
			p, err := projectTx(ctx, tx, t.ProjectID)
			if err != nil {
				return t, err
			}
			sub := tasks.Submission{Summary: summary}
			if strings.TrimSpace(in.PullRequest) != "" {
				pr, ok := tasks.ParsePullRequest(in.PullRequest)
				if !ok {
					return t, inputError("pull_request must be #NUMBER or the pull request's URL")
				}
				sub.PullRequest = &pr
			} else if p.RepoURL != "" {
				return t, inputError("this project has a repository: name the pull request (#NUMBER or its URL)")
			}
			t.Submission, t.Status, t.Result, t.Implementer, t.ReviewFeedback = &sub, "IN_REVIEW", summary, t.Assignee, ""
			event = "task.submitted"
			if tx.store.ExplicitExits && t.ReviewBy != "owner" && !tasks.IsPerson(t.ReviewBy) {
				// An agent reviewer gets the task for a review run, as after an agent's factory_exit_submit.
				t.Reviewer, t.Assignee = t.ReviewBy, t.ReviewBy
			}
		case "done":
			if view.Status != "READY" || t.Implementer != t.Assignee || t.Submission == nil || t.ReviewFeedback != "" {
				return t, fmt.Errorf("%w: a task is done after its submission was approved; submit it for review first", ErrConflict)
			}
			number := 0
			if pr := t.Submission.PullRequest; pr != nil {
				number = pr.Number
				if !in.Merged {
					return t, inputError("confirm that pull request " + pr.String() + " is merged (merged: true); done needs the merged pull request")
				}
			}
			var approvals int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pr_approvals WHERE company_id=? AND task_id=? AND pr_number=?", tx.company, t.ID, number).Scan(&approvals); err != nil {
				return t, err
			}
			if approvals == 0 {
				return t, fmt.Errorf("%w: the submission has no approval yet; it is done after its reviewer approves it", ErrConflict)
			}
			t.Status = "DONE"
			if s := strings.TrimSpace(in.Summary); s != "" {
				t.Result = s
			}
		default:
			return t, inputError("unknown action for a person's task (start, submit, done)")
		}
		t.NotBefore, t.BlockReason = 0, ""
		if err = endEpisode(ctx, tx, t.ID); err != nil {
			return t, err
		}
		if in.Reason != "" {
			if err = comment(ctx, tx, t.ID, in.Actor, in.Reason); err != nil {
				return t, err
			}
		}
		if err = saveTask(ctx, tx, &t, event); err != nil {
			return t, err
		}
		switch {
		case t.Status == "DONE":
			err = closedTask(ctx, tx, t)
		case t.Status == "IN_REVIEW" && t.Reviewer != "":
			err = addTrigger(ctx, tx, &t, "assignment", false, "owner", "", "review requested by "+name+": "+t.Submission.Summary+prNote(t.Submission))
		}
		return t, err
	})
}

func prNote(s *tasks.Submission) string {
	if s == nil || s.PullRequest == nil {
		return ""
	}
	return " (pull request " + s.PullRequest.String() + ")"
}

// openDependencies are the task's dependencies that are not DONE.
func openDependencies(ctx context.Context, tx *Tx, t tasks.Task) ([]string, error) {
	var open []string
	for _, id := range t.Dependencies {
		d, err := taskTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if d.Status != "DONE" {
			open = append(open, id)
		}
	}
	return open, nil
}

// personApproval records an approval of a person's submission (the pull request it names, 0 without one), which
// done requires.
func personApproval(ctx context.Context, tx *Tx, t tasks.Task, approver string) error {
	if !tasks.IsPerson(t.Implementer) || t.Submission == nil {
		return nil
	}
	number := 0
	if pr := t.Submission.PullRequest; pr != nil {
		number = pr.Number
	}
	if approver == "" {
		approver = "owner"
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO pr_approvals(company_id,task_id,pr_number,approved_head,approver,run_id,created_at) VALUES(?,?,?,?,?,?,?)", tx.company, t.ID, number, "", approver, "", tx.now().Unix())
	return err
}

// personTaskReady tells the person that their task can start: its last open dependency is done.
func personTaskReady(ctx context.Context, tx *Tx, t tasks.Task, done tasks.Task) error {
	if !tasks.IsPerson(t.Assignee) || t.Status != "READY" {
		return nil
	}
	name := personLabel(ctx, tx, t.Assignee)
	body := fmt.Sprintf("Your task is ready, %s: %s (%s). Its last open dependency %s (%q) is done; start it on the board.", name, t.Title, t.ID, done.ID, done.Title)
	_, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: t.ProjectID, TaskID: t.ID, Sender: "factory", Recipient: "owner", MessageType: "notification", Body: body, RequestID: "person-ready-" + t.ID + "-" + done.ID})
	return err
}

// RemindPerson sends the person of task t one reminder per episode (the task's updated_at) naming the tasks that
// wait on it; it reports whether it sent one now.
func (s *Store) RemindPerson(ctx context.Context, company string, t tasks.Task, waiting []tasks.Task, unchanged time.Duration) (bool, error) {
	if !tasks.IsPerson(t.Assignee) || len(waiting) == 0 {
		return false, nil
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "INSERT INTO person_reminders(company_id,task_id,episode,sent_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", company, t.ID, t.UpdatedAt, tx.now().Unix())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	names := make([]string, 0, len(waiting))
	for _, w := range waiting {
		names = append(names, fmt.Sprintf("%s (%q, %s)", w.ID, w.Title, w.Assignee))
	}
	body := fmt.Sprintf("Reminder for %s: your task %s (%q) is %s and has not changed for %dh; %d task(s) wait on it: %s.",
		personLabel(ctx, tx, t.Assignee), t.ID, t.Title, t.Status, int(unchanged.Hours()), len(waiting), strings.Join(names, ", "))
	if _, err = sendMessageTx(ctx, tx, messaging.Send{ProjectID: t.ProjectID, TaskID: t.ID, Sender: "factory", Recipient: "owner", MessageType: "notification", Body: body,
		RequestID: fmt.Sprintf("person-remind-%s-%d", t.ID, t.UpdatedAt)}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
