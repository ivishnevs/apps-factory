package core

import (
	"fmt"
	"net/url"
	"strings"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// taskFilter narrows GET /tasks (task-duplicate-warnings 3.1): open tasks only, an assignee, a change, a covered
// work key (or a key it overlaps: a change group matches its items) and title words (all of them, any case).
// compact answers only what a check before creating tasks needs.
type taskFilter struct {
	open                     bool
	assignee, change, covers string
	words                    []string
	compact                  bool
}

func taskFilterOf(q url.Values) (taskFilter, error) {
	f := taskFilter{open: q.Get("open") == "1" || q.Get("open") == "true", assignee: q.Get("assignee"), change: q.Get("openspec_change"),
		words: strings.Fields(strings.ToLower(q.Get("query"))), compact: q.Get("compact") == "1" || q.Get("compact") == "true"}
	if k := q.Get("covers"); k != "" {
		key, err := tasks.NormalizeCover(k)
		if err != nil {
			return f, fmt.Errorf("%w: %s", storage.ErrInput, err.Error())
		}
		f.covers = key
	}
	return f, nil
}

func (f taskFilter) apply(list []tasks.Task) []tasks.Task {
	out := list[:0:0]
	for _, t := range list {
		if f.open && (t.Status == "DONE" || t.Status == "CANCELLED") || f.assignee != "" && t.Assignee != f.assignee ||
			f.change != "" && t.OpenSpecChange != f.change || f.covers != "" && !tasks.CoversMatch(t.Covers, f.covers) {
			continue
		}
		title, match := strings.ToLower(t.Title), true
		for _, w := range f.words {
			if !strings.Contains(title, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, t)
		}
	}
	return out
}

// compactTask is a task as factory_task_list answers by default: no description, submission or run state.
type compactTask struct {
	ID                 string                   `json:"id"`
	Title              string                   `json:"title"`
	Status             string                   `json:"status"`
	Assignee           string                   `json:"assignee,omitempty"`
	OpenSpecChange     string                   `json:"openspec_change,omitempty"`
	Covers             []string                 `json:"covers,omitempty"`
	Dependencies       []string                 `json:"dependencies,omitempty"`
	PossibleDuplicates []tasks.DuplicateWarning `json:"possible_duplicates,omitempty"`
}

func compactTasks(list []tasks.Task) []compactTask {
	out := make([]compactTask, 0, len(list))
	for _, t := range list {
		out = append(out, compactTask{ID: t.ID, Title: t.Title, Status: t.Status, Assignee: t.Assignee, OpenSpecChange: t.OpenSpecChange, Covers: t.Covers, Dependencies: t.Dependencies, PossibleDuplicates: t.PossibleDuplicates})
	}
	return out
}

// cancelAllowed: the actor (the owner, or an agent whose policy allows factory.task.cancel) may cancel tasks it
// replaces; storage checks the authority over each.
func (c *Core) cancelAllowed(company, actor string) bool {
	if actor == "" {
		return true
	}
	a, ok := c.cat(company).Agents[actor]
	return ok && a.Authorize("factory.task.cancel").Effect == "allow"
}
