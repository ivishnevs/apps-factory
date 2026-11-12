package storage

import (
	"context"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/tasks"
)

func reasonsFor(ws []tasks.DuplicateWarning, id string) []string {
	for _, w := range ws {
		if w.TaskID == id {
			return w.Reasons
		}
	}
	return nil
}

func strongWarnings(ws []tasks.DuplicateWarning) int {
	n := 0
	for _, w := range ws {
		if w.Strong() {
			n++
		}
	}
	return n
}

// duplicateFeed counts the attention and quiet task.possible_duplicate rows of a task.
func duplicateFeed(t *testing.T, s *Store, task string) (attention, quiet int) {
	t.Helper()
	if err := s.DB.QueryRow("SELECT COALESCE(sum(attention),0),count(*)-COALESCE(sum(attention),0) FROM feed_events WHERE company_id=? AND kind='task.possible_duplicate' AND task_id=?", DefaultCompany, task).Scan(&attention, &quiet); err != nil {
		t.Fatal(err)
	}
	return attention, quiet
}

// TestDuplicateWarnings (task-duplicate-warnings 1.2, 2.2, 2.3): a follow-up's item is inferred; a later run's
// second follow-up for the same item warns same_work_item, shows on both tasks and in the creator's feed until
// the pair is marked not duplicate; a decomposition does not warn about its own siblings; same_title and
// same_change_open; a close ends the warning.
func TestDuplicateWarnings(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	create := func(in tasks.Create) tasks.Task {
		t.Helper()
		in.ProjectID = "p"
		if in.RequestID == "" {
			in.RequestID = "create-" + in.ID
		}
		v, err := s.CreateTask(ctx, co, in)
		if err != nil {
			t.Fatalf("%s: %v", in.ID, err)
		}
		return v
	}
	// The change is known from a task linking it.
	create(tasks.Create{ID: "impl", Title: "Implement player profile", Assignee: "dev", OpenSpecChange: "player-profile-and-xp"})

	a := create(tasks.Create{ID: "a", Title: "M5b-P3 follow-up: XP curve", Description: "carry-over of player-profile-and-xp task 3.2 (PR #148)", Assignee: "dev",
		CreatedBy: "producer", Actor: "producer", OriginRun: "run-1", OriginTask: "conv", Kind: "work"})
	if !slices.Equal(a.Covers, []string{"player-profile-and-xp#3.2", "pr#148"}) || !a.CoversInferred || a.OpenSpecChange != "player-profile-and-xp" || !a.OpenSpecChangeInferred {
		t.Fatalf("inferred: %+v", a)
	}
	if strongWarnings(a.Warnings) != 0 || a.DuplicateHint != "" {
		t.Fatalf("the first follow-up warns: %+v", a.Warnings)
	}
	if !slices.Equal(reasonsFor(a.Warnings, "impl"), []string{tasks.ReasonSameChangeOpen}) {
		t.Fatalf("same_change_open context: %+v", a.Warnings)
	}

	// A later run creates the same follow-up under another name.
	b := create(tasks.Create{ID: "b", Title: "player-profile-and-xp 3.2 remainder", Description: "Nothing was tracking them", Assignee: "dev",
		CreatedBy: "producer", Actor: "producer", OriginRun: "run-2", OriginTask: "conv"})
	if !slices.Contains(reasonsFor(b.Warnings, "a"), tasks.ReasonSameWorkItem) || b.DuplicateHint == "" {
		t.Fatalf("second follow-up: %+v", b.Warnings)
	}
	if len(b.PossibleDuplicates) != 1 || b.PossibleDuplicates[0].TaskID != "a" {
		t.Fatalf("b's badge: %+v", b.PossibleDuplicates)
	}
	if got := taskOf(t, s, "a").PossibleDuplicates; len(got) != 1 || got[0].TaskID != "b" || !slices.Equal(got[0].Keys, []string{"player-profile-and-xp#3.2"}) {
		t.Fatalf("a's badge: %+v", got)
	}
	list, err := s.Tasks(ctx, co, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if (x.ID == "a" || x.ID == "b") != (len(x.PossibleDuplicates) == 1) {
			t.Fatalf("list badge of %s: %+v", x.ID, x.PossibleDuplicates)
		}
	}
	// In the producer's (subtree) feed with attention.
	page, err := s.Feed(ctx, co, FeedFilter{Agents: []string{"producer"}, Attention: true}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Items, func(f FeedItem) bool { return f.Type == "task.possible_duplicate" && f.TaskID == "b" }) {
		t.Fatalf("feed: %+v", page.Items)
	}
	// The warning is in the answer of the replayed request too, and the body stores none of it.
	again, err := s.CreateTask(ctx, co, tasks.Create{ID: "b", ProjectID: "p", Title: "player-profile-and-xp 3.2 remainder", Description: "Nothing was tracking them", Assignee: "dev",
		CreatedBy: "producer", Actor: "producer", OriginRun: "run-2", OriginTask: "conv", RequestID: "create-b"})
	if err != nil || len(again.Warnings) == 0 {
		t.Fatalf("replay: %+v %v", again.Warnings, err)
	}
	var body string
	if err = s.DB.QueryRow("SELECT body FROM tasks WHERE company_id=? AND id='b'", co).Scan(&body); err != nil || strings.Contains(body, "warnings") || strings.Contains(body, "possible_duplicates") {
		t.Fatalf("stored body: %s %v", body, err)
	}

	// Keep both: the pair leaves both tasks and the feed, and never warns again.
	if _, err = s.UpdateTask(ctx, co, "b", tasks.Update{NotDuplicateOf: []string{"a"}, Actor: "producer"}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "a"); len(got.PossibleDuplicates) != 0 || !slices.Equal(got.NotDuplicateOf, []string{"b"}) {
		t.Fatalf("after not_duplicate_of: %+v %v", got.PossibleDuplicates, got.NotDuplicateOf)
	}
	if att, quiet := duplicateFeed(t, s, "b"); att != 0 || quiet != 1 {
		t.Fatalf("feed attention after not_duplicate_of: %d %d", att, quiet)
	}
	bv := taskOf(t, s, "b")
	title := "player-profile-and-xp 3.2 remainder (XP curve)"
	if bv, err = s.UpdateTask(ctx, co, "b", tasks.Update{Version: bv.Version, Title: &title}); err != nil || reasonsFor(bv.Warnings, "a") != nil {
		t.Fatalf("a marked pair warned again: %+v %v", bv.Warnings, err)
	}

	// A decomposition of ten tasks of one change by one run: no warnings among them.
	for i := range 10 {
		id := "d" + string(rune('0'+i))
		v := create(tasks.Create{ID: id, Title: "World models part " + id, Description: "world-models-new task 7.1", Assignee: "dev", Covers: []string{"world-models-new#7"},
			OpenSpecChange: "world-models-new", Kind: "work", CreatedBy: "producer", Actor: "producer", OriginRun: "run-3", OriginTask: "conv"})
		if len(v.Warnings) != 0 {
			t.Fatalf("%s warns about its siblings: %+v", id, v.Warnings)
		}
	}

	// same_title: equal normalised titles and the same assignee (another run, no shared key).
	create(tasks.Create{ID: "t1", Title: "3D-WQ2: Verify the river shader", Assignee: "qa"})
	t2 := create(tasks.Create{ID: "t2", Title: "Verify the river shader (c9b076b8)", Assignee: "qa"})
	if !slices.Equal(reasonsFor(t2.Warnings, "t1"), []string{tasks.ReasonSameTitle}) {
		t.Fatalf("same_title: %+v", t2.Warnings)
	}
	t3 := create(tasks.Create{ID: "t3", Title: "Verify the river shader", Assignee: "dev"})
	if reasonsFor(t3.Warnings, "t1") != nil {
		t.Fatalf("same title, other assignee: %+v", t3.Warnings)
	}
	// Closing one task ends the pair's warning.
	if _, err = s.TaskAction(ctx, co, "t1", tasks.Action{Version: taskOf(t, s, "t1").Version, Action: "cancel", Reason: "duplicate of t2"}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "t2").PossibleDuplicates; len(got) != 0 {
		t.Fatalf("closed pair: %+v", got)
	}
	if att, _ := duplicateFeed(t, s, "t2"); att != 0 {
		t.Fatalf("closed pair keeps attention: %d", att)
	}

	// An explicit key that is not a work key is refused with the formats.
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "bad", ProjectID: "p", Title: "x", Covers: []string{"item 3"}, RequestID: "bad"}); err == nil || !strings.Contains(err.Error(), "pr#<number>") {
		t.Fatalf("bad covers: %v", err)
	}
}

// TestReplaces (task-duplicate-warnings 2.1): a split cancels the original when the actor may cancel it, links both
// ways and moves its dependents to the parts; without authority the original stays open and warns original_open;
// a split that leaves its READY parent to run again warns once per run.
func TestReplaces(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	p := createEngineTask(t, s, "P")
	dep := createEngineTask(t, s, "D", "P")
	if !slices.Equal(dep.Dependencies, []string{"P"}) {
		t.Fatal(dep.Dependencies)
	}
	k, err := s.CreateTask(ctx, co, tasks.Create{ID: "K", ProjectID: "p", Title: "3D-K2i: terrain part 1", Assignee: "demo", Replaces: []string{"P"}, RequestID: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "P"); got.Status != "CANCELLED" || !slices.Equal(got.ReplacedBy, []string{"K"}) || !slices.Equal(k.Replaces, []string{"P"}) {
		t.Fatalf("split: %+v / %+v", got, k)
	}
	if strongWarnings(k.Warnings) != 0 {
		t.Fatalf("a done replacement warns: %+v", k.Warnings)
	}
	o, err := s.CreateTask(ctx, co, tasks.Create{ID: "O", ProjectID: "p", Title: "3D-O4i: terrain part 2", Assignee: "demo", Replaces: []string{"P"}, Dependencies: []string{"K"}, RequestID: "o"})
	if err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "P"); !slices.Equal(got.ReplacedBy, []string{"K", "O"}) {
		t.Fatalf("second part: %+v", got.ReplacedBy)
	}
	if got := taskOf(t, s, "D"); !slices.Equal(got.Dependencies, []string{"K", "O"}) {
		t.Fatalf("dependents move to the parts: %v", got.Dependencies)
	}
	comments, err := s.Comments(ctx, co, "P")
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, c := range comments {
		text += c.Text + "\n"
	}
	if !strings.Contains(text, "replaced by K") || !strings.Contains(text, "also replaced by O") {
		t.Fatalf("P's comments: %s", text)
	}
	_ = o
	_ = p

	// Without authority: created, the original stays open, original_open.
	q := createEngineTask(t, s, "Q")
	n, err := s.CreateTask(ctx, co, tasks.Create{ID: "N", ProjectID: "p", Title: "narrower check", Assignee: "qa", CreatedBy: "qa", Actor: "qa", CancelAllowed: true, Replaces: []string{"Q"}, RequestID: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if taskOf(t, s, "Q").Status != q.Status || !slices.Contains(reasonsFor(n.Warnings, "Q"), tasks.ReasonOriginalOpen) {
		t.Fatalf("no authority: %s %+v", taskOf(t, s, "Q").Status, n.Warnings)
	}
	// The agent's own task with the policy allowing cancel: cancelled; without the policy: stays open.
	mine, err := s.CreateTask(ctx, co, tasks.Create{ID: "M", ProjectID: "p", Title: "mine", Assignee: "demo", CreatedBy: "qa", Actor: "qa", RequestID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "M2", ProjectID: "p", Title: "mine, narrower", Assignee: "demo", CreatedBy: "qa", Actor: "qa", Replaces: []string{"M"}, RequestID: "m2"}); err != nil {
		t.Fatal(err)
	}
	if taskOf(t, s, "M").Status != mine.Status {
		t.Fatal("cancelled without the cancel policy")
	}
	m3 := taskOf(t, s, "M2")
	if _, err = s.UpdateTask(ctx, co, "M2", tasks.Update{Version: m3.Version, Replaces: []string{"M"}, Actor: "qa", CancelAllowed: true}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "M"); got.Status != "CANCELLED" || !slices.Equal(got.ReplacedBy, []string{"M2"}) {
		t.Fatalf("update replaces: %+v", got)
	}
	if got := taskOf(t, s, "M2").PossibleDuplicates; len(got) != 0 {
		t.Fatalf("the cancelled original still warns: %+v", got)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "X", ProjectID: "p", Title: "x", Replaces: []string{"X"}, RequestID: "x"}); err == nil {
		t.Fatal("a task replaces itself")
	}

	// A run splits a READY task under it as parent: one original_open for the run, none for its siblings.
	parent := readyEngineTask(t, s, createEngineTask(t, s, "R"))
	var warned int
	for _, id := range []string{"R1", "R2", "R3"} {
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: "part " + id, Assignee: "demo", ParentTaskID: parent.ID, CreatedBy: "producer", Actor: "producer", OriginRun: "run-9", OriginTask: "conv", RequestID: id})
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(reasonsFor(v.Warnings, "R"), tasks.ReasonOriginalOpen) {
			warned++
		}
		if len(v.Warnings) > 1 {
			t.Fatalf("%s: %+v", id, v.Warnings)
		}
	}
	if warned != 1 {
		t.Fatalf("original_open for the parent: %d", warned)
	}
	// The run working on its own task decomposes it: no warning.
	own, err := s.CreateTask(ctx, co, tasks.Create{ID: "R4", ProjectID: "p", Title: "part R4", Assignee: "demo", ParentTaskID: parent.ID, CreatedBy: "demo", Actor: "demo", OriginRun: "run-10", OriginTask: "R", RequestID: "R4"})
	if err != nil || len(own.Warnings) != 0 {
		t.Fatalf("own decomposition: %+v %v", own.Warnings, err)
	}
}
