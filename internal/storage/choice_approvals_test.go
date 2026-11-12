package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/tasks"
)

// choiceFixture registers versions v1..vN of artifact id on a task of project p and returns the request options
// labelled "V1".."VN".
func choiceFixture(t *testing.T, s *Store, task, id string, n int) []artifacts.ChoiceOption {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Task(ctx, DefaultCompany, task); errors.Is(err, ErrNotFound) {
		if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: task, ProjectID: "p", Title: task, Assignee: "demo", RequestID: "create-" + task}); err != nil {
			t.Fatal(err)
		}
	}
	var out []artifacts.ChoiceOption
	for i := 1; i <= n; i++ {
		v := "v" + string(rune('0'+i))
		a := tasks.Artifact{ID: id, Version: v, URI: "https://example.com/" + id + "-" + v + ".png"}
		if _, err := s.RegisterArtifact(ctx, DefaultCompany, "p", artifacts.Register{Artifact: a, TaskID: task, Actor: "owner", RequestID: "reg-" + id + v}); err != nil {
			t.Fatal(err)
		}
		out = append(out, artifacts.ChoiceOption{ArtifactID: id, ArtifactVersion: v, Label: "V" + string(rune('0'+i)), Note: "variation " + v})
	}
	return out
}

// TestChoiceApprovalRequest (artifact-choice-approvals 1.1, 1.2, 5.1): a request over 1–6 registered versions of the
// project is one approval with its options and recommendation; unknown, duplicate or too many options, a missing
// label among several and a version already waiting are refused with a hint; one option is the same model.
func TestChoiceApprovalRequest(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	opts := choiceFixture(t, s, "design", "variations", 7)
	ask := func(key string, o []artifacts.ChoiceOption, rec any) (approvals.Approval, error) {
		return s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{Options: o, Recommended: rec, Agent: "demo", TaskID: "design", Reason: "pick a variation", RequestID: key})
	}
	unknown := append([]artifacts.ChoiceOption{}, opts[:2]...)
	unknown = append(unknown, artifacts.ChoiceOption{ArtifactID: "variations", ArtifactVersion: "v9", Label: "V9"})
	for name, c := range map[string]struct {
		opts []artifacts.ChoiceOption
		want string
	}{
		"unknown":   {unknown, "option 3 (V9): variations@v9 is not a registered version"},
		"duplicate": {[]artifacts.ChoiceOption{opts[0], {ArtifactID: "variations", ArtifactVersion: "v1", Label: "again"}}, "option 2 repeats option 1"},
		"label":     {[]artifacts.ChoiceOption{opts[0], {ArtifactID: "variations", ArtifactVersion: "v2", Label: "v1"}}, "repeats the label"},
		"no label":  {[]artifacts.ChoiceOption{opts[0], {ArtifactID: "variations", ArtifactVersion: "v2"}}, "a short label required"},
		"too many":  {opts, "1 to 6 options"},
	} {
		if _, err := ask("bad-"+name, c.opts, nil); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "variations", ArtifactVersion: "v1", Options: opts[:2], Agent: "demo", TaskID: "design", Reason: "x", RequestID: "both"}); !errors.Is(err, ErrInput) {
		t.Fatal("artifact and options together accepted", err)
	}
	if _, err := ask("bad-rec", opts[:3], "V7"); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "recommended") {
		t.Fatal(err)
	}
	a, err := ask("choice", opts[:3], "V2")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Choice() || len(a.Options) != 3 || a.Recommended != 2 || a.Artifact != nil || a.Options[0].Label != "V1" || a.Options[0].Note != "variation v1" || a.Status != "PENDING" {
		t.Fatalf("%+v", a)
	}
	m, err := s.Message(ctx, co, a.MessageID)
	if err != nil || len(m.Materials) != 3 || m.Materials[1].Caption != "V2" || !strings.Contains(m.Body, "2. V2 (variations@v2) — variation v2 (recommended)") {
		t.Fatalf("%+v %v", m, err)
	}
	// Each option's version finds the choice; a version waiting in it is not asked again, alone or in another choice.
	for _, v := range []string{"v1", "v2", "v3"} {
		got, err := s.ArtifactApproval(ctx, co, "p", "variations", v)
		if err != nil || got.ID != a.ID || got.StatusOf("variations", v) != "PENDING" {
			t.Fatalf("%s: %+v %v", v, got, err)
		}
	}
	if _, err = s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "variations", ArtifactVersion: "v2", Agent: "demo", TaskID: "design", Reason: "alone", RequestID: "alone"}); !errors.Is(err, ErrConflict) {
		t.Fatal("a version waiting in a choice asked again alone", err)
	}
	if _, err = ask("overlap", []artifacts.ChoiceOption{opts[2], opts[3]}, nil); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already waits for approval "+a.ID) {
		t.Fatal(err)
	}
	// One option is an approval with one option: the same model, its version the approval's from the start.
	single, err := ask("one", opts[4:5], nil)
	if err != nil || !single.Single() || single.Options[0].Label != "V5" || single.Artifact == nil || single.Artifact.Version != "v5" {
		t.Fatalf("%+v %v", single, err)
	}
}

// TestChoiceApprovalDecisions (artifact-choice-approvals 1.3–1.5): choosing approves the chosen version and leaves the
// others not chosen (a task requiring one of them keeps waiting; it can be asked again); asking for changes and
// rejecting all approve nothing; approving without choosing is refused.
func TestChoiceApprovalDecisions(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	opts := choiceFixture(t, s, "design", "pouch", 3)
	req := func(key string, o []artifacts.ChoiceOption) approvals.Approval {
		t.Helper()
		a, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{Options: o, Agent: "demo", TaskID: "design", Reason: "pick", RequestID: key})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := req("choice", opts)
	if _, err := s.DecideApproval(ctx, co, a.ID, approvals.Decision{Version: a.Version, Action: "approve", Reason: "approved by owner", RequestID: "approve"}); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "choose one") {
		t.Fatal("a choice approved without choosing", err)
	}
	if _, err := s.DecideApproval(ctx, co, a.ID, approvals.Decision{Version: a.Version, Action: "choose", Option: 4, RequestID: "bad"}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	needV1, err := s.CreateTask(ctx, co, tasks.Create{ID: "uses-v1", ProjectID: "p", Title: "uses v1", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{optRef(opts[0])}, Ready: true, RequestID: "uses-v1"})
	if err != nil {
		t.Fatal(err)
	}
	needV2, err := s.CreateTask(ctx, co, tasks.Create{ID: "uses-v2", ProjectID: "p", Title: "uses v2", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{optRef(opts[1])}, Ready: true, RequestID: "uses-v2"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.DecideApproval(ctx, co, a.ID, approvals.Decision{Version: a.Version, Action: "choose", Option: 2, Reason: "darker paywall", RequestID: "choose"})
	if err != nil || got.Status != "APPROVED" || got.Chosen != 2 || got.Artifact == nil || got.Artifact.Version != "v2" || got.DecidedBy != "" {
		t.Fatalf("%+v %v", got, err)
	}
	for v, want := range map[string]string{"v1": approvals.NotChosen, "v2": "APPROVED", "v3": approvals.NotChosen} {
		ap, err := s.ArtifactApproval(ctx, co, "p", "pouch", v)
		if err != nil || ap.ID != a.ID || ap.StatusOf("pouch", v) != want {
			t.Fatalf("%s: %s %v", v, ap.StatusOf("pouch", v), err)
		}
	}
	if v1, _ := s.Task(ctx, co, needV1.ID); v1.Status != "BLOCKED" || !strings.Contains(v1.BlockReason, "pouch@v1") {
		t.Fatalf("a task requiring a not chosen version runs: %s %s", v1.Status, v1.BlockReason)
	}
	if v2, _ := s.Task(ctx, co, needV2.ID); v2.Status == "BLOCKED" {
		t.Fatalf("a task requiring the chosen version waits: %s", v2.BlockReason)
	}
	ans, err := s.AnswerFor(ctx, co, a.MessageID)
	if err != nil || !strings.Contains(ans.Body, "V2 (pouch@v2) chosen by the owner: darker paywall; not chosen (still usable later): V1 (pouch@v1), V3 (pouch@v3)") {
		t.Fatalf("%q %v", ans.Body, err)
	}
	// A not chosen version stays usable: it may be asked again on its own.
	again, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "pouch", ArtifactVersion: "v1", Agent: "demo", TaskID: "design", Reason: "v1 for the store after all", RequestID: "again"})
	if err != nil || !again.Single() || again.Artifact == nil || again.Artifact.Version != "v1" {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err = s.DecideApproval(ctx, co, again.ID, approvals.Decision{Version: again.Version, Action: "choose", Option: 2, RequestID: "choose-single"}); !errors.Is(err, ErrInput) {
		t.Fatal("a missing option chosen", err)
	}

	// Changes and reject all: nothing approved; the requested changes and the reason are the answer.
	more := choiceFixture(t, s, "design", "icon", 2)
	b := req("changes", more)
	if _, err = s.DecideApproval(ctx, co, b.ID, approvals.Decision{Version: b.Version, Action: "changes", RequestID: "no-text"}); !errors.Is(err, ErrInput) {
		t.Fatal("changes without text", err)
	}
	got, err = s.DecideApproval(ctx, co, b.ID, approvals.Decision{Version: b.Version, Action: "changes", Options: []int{1, 2}, Reason: "mix V1's icon with V2's palette", RequestID: "changes"})
	if err != nil || got.Status != approvals.StatusChanges || got.Artifact != nil || len(got.ChangesOn) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if ans, _ = s.AnswerFor(ctx, co, b.MessageID); !strings.Contains(ans.Body, "changes requested by the owner on V1 (icon@v1), V2 (icon@v2): mix V1's icon with V2's palette") {
		t.Fatal(ans.Body)
	}
	if ap, _ := s.ArtifactApproval(ctx, co, "p", "icon", "v1"); ap.StatusOf("icon", "v1") != approvals.StatusChanges {
		t.Fatal(ap.Status)
	}
	third := choiceFixture(t, s, "design", "cover", 2)
	c := req("reject", third)
	if _, err = s.DecideApproval(ctx, co, c.ID, approvals.Decision{Version: c.Version, Action: "reject", RequestID: "no-reason"}); !errors.Is(err, ErrInput) {
		t.Fatal("reject without a reason", err)
	}
	if got, err = s.DecideApproval(ctx, co, c.ID, approvals.Decision{Version: c.Version, Action: "reject", Reason: "none follows the brief", RequestID: "reject"}); err != nil || got.Status != "REJECTED" {
		t.Fatalf("%+v %v", got, err)
	}
	if ans, _ = s.AnswerFor(ctx, co, c.MessageID); !strings.Contains(ans.Body, "all options rejected by the owner: none follows the brief") {
		t.Fatal(ans.Body)
	}
}

// TestChoiceApprovalDrops (artifact-choice-approvals 2.3): the delegate escalating a choice may drop options, each with
// a reason, keeping at least one; dropping all is rejecting all; people may still choose a dropped option.
func TestChoiceApprovalDrops(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	opts := choiceFixture(t, s, "design", "concept", 5)
	a, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{Options: opts, Agent: "demo", TaskID: "design", Reason: "pick", RequestID: "ask", Audience: "internal", Delegate: "lead", Setting: "project p: internal artifacts agent lead"})
	if err != nil {
		t.Fatal(err)
	}
	all := []approvals.Drop{}
	for i := 1; i <= 5; i++ {
		all = append(all, approvals.Drop{Option: i, Reason: "off-brief"})
	}
	if _, err = s.EscalateApproval(ctx, co, a.ID, "lead", "the owner picks", EscalatePeople{Drops: all}); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "reject") {
		t.Fatal("every option dropped", err)
	}
	if _, err = s.EscalateApproval(ctx, co, a.ID, "lead", "the owner picks", EscalatePeople{Drops: []approvals.Drop{{Option: 2}}}); !errors.Is(err, ErrInput) {
		t.Fatal("a drop without its reason", err)
	}
	got, err := s.EscalateApproval(ctx, co, a.ID, "lead", "the owner picks", EscalatePeople{Drops: []approvals.Drop{{Option: 2, Reason: "off-brief palette"}, {Option: 4, Reason: "unreadable at icon size"}}})
	if err != nil || !got.Escalated || got.Options[1].Dropped != "off-brief palette" || got.Options[1].DroppedBy != "lead" || got.Options[3].Dropped == "" || got.Options[0].Dropped != "" {
		t.Fatalf("%+v %v", got, err)
	}
	m, _ := s.Message(ctx, co, a.MessageID)
	if !strings.Contains(m.Body, "lead dropped option 2 V2 (concept@v2): off-brief palette") {
		t.Fatal(m.Body)
	}
	// In the people topic it is chosen, sent back or rejected, never approved as is; people choose a dropped option
	// all the same.
	topic, err := s.PeopleTopic(ctx, co, m.TopicID)
	if err != nil || len(topic.Items) != 1 || strings.Join(topic.Items[0].Actions, ",") != "choose,changes,reject" {
		t.Fatalf("%+v %v", topic, err)
	}
	if _, err = s.DecideTopic(ctx, co, m.TopicID, "approve-all", []TopicDecision{{Kind: "approval", Ref: a.ID, Version: got.Version, Action: "approve"}}); !errors.Is(err, ErrInput) {
		t.Fatal("a choice approved from its topic", err)
	}
	if _, err = s.DecideTopic(ctx, co, m.TopicID, "pick", []TopicDecision{{Kind: "approval", Ref: a.ID, Version: got.Version, Action: "choose", Option: 4}}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Approval(ctx, co, a.ID); err != nil || got.Chosen != 4 || got.Status != "APPROVED" {
		t.Fatalf("%+v %v", got, err)
	}
}

// TestSingleVersionApproval (artifact-choice-approvals 5.1, 5.2, 5.5): a request for one version is an approval with
// one option, stored and found like any choice; the owner approves it (choosing its only option, the comment
// optional), asks for changes (nothing approved, the text is the answer) or rejects it; a task requiring the version
// runs only once it is approved; drop is for several options.
func TestSingleVersionApproval(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	ask := func(id string, delegate string) approvals.Approval {
		t.Helper()
		o := choiceFixture(t, s, "design", id, 1)[0]
		setting := ""
		if delegate != "" {
			setting = "project p: internal artifacts agent " + delegate
		}
		a, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: o.ArtifactID, ArtifactVersion: o.ArtifactVersion, Agent: "demo", TaskID: "design", Reason: "review " + id, RequestID: "ask-" + id, Delegate: delegate, Setting: setting})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := ask("logo", "")
	if !a.Single() || !a.Choice() || a.Options[0].Artifact.ID != "logo" || a.Options[0].Label != "" || a.Artifact == nil || a.Artifact.Version != "v1" || a.OptionName(1) != "logo@v1" {
		t.Fatalf("not one option: %+v", a)
	}
	if found, err := s.ArtifactApproval(ctx, co, "p", "logo", "v1"); err != nil || found.ID != a.ID {
		t.Fatalf("%+v %v", found, err)
	}
	needs, err := s.CreateTask(ctx, co, tasks.Create{ID: "uses-logo", ProjectID: "p", Title: "uses the logo", Assignee: "demo", RequiredArtifacts: []tasks.Artifact{*a.Artifact}, Ready: true, RequestID: "uses-logo"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, co, needs.ID); got.Status != "BLOCKED" {
		t.Fatalf("runs before the approval: %s", got.Status)
	}
	if _, err = s.DecideApproval(ctx, co, a.ID, approvals.Decision{Version: a.Version, Action: "choose", Option: 2, RequestID: "no-such"}); !errors.Is(err, ErrInput) {
		t.Fatal("a second option chosen", err)
	}
	// Approve with no comment is choosing the only option.
	got, err := s.DecideApproval(ctx, co, a.ID, approvals.Decision{Version: a.Version, Action: "approve", RequestID: "approve"})
	if err != nil || got.Status != "APPROVED" || got.Chosen != 1 || got.Artifact == nil || got.Artifact.Version != "v1" || got.StatusOf("logo", "v1") != "APPROVED" {
		t.Fatalf("%+v %v", got, err)
	}
	if t2, _ := s.Task(ctx, co, needs.ID); t2.Status == "BLOCKED" {
		t.Fatalf("an approved version still holds its task: %s", t2.BlockReason)
	}
	if ans, _ := s.AnswerFor(ctx, co, a.MessageID); !strings.Contains(ans.Body, "Approval "+a.ID+" APPROVED") {
		t.Fatal(ans.Body)
	}
	// choose 1 is the same decision.
	b := ask("banner", "")
	if got, err = s.DecideApproval(ctx, co, b.ID, approvals.Decision{Version: b.Version, Action: "choose", Option: 1, Reason: "bigger title next time", RequestID: "choose"}); err != nil || got.Status != "APPROVED" || got.Chosen != 1 || got.CommentID == "" {
		t.Fatalf("%+v %v", got, err)
	}

	// Ask for changes: it needs the text; nothing is approved; the version may be asked for again.
	c := ask("icon", "")
	if _, err = s.DecideApproval(ctx, co, c.ID, approvals.Decision{Version: c.Version, Action: "changes", RequestID: "changes-empty"}); !errors.Is(err, ErrInput) {
		t.Fatal("changes without text", err)
	}
	if got, err = s.DecideApproval(ctx, co, c.ID, approvals.Decision{Version: c.Version, Action: "changes", Reason: "a smaller mark", RequestID: "changes"}); err != nil || got.Status != approvals.StatusChanges || got.Chosen != 0 || got.CommentID == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if ans, _ := s.AnswerFor(ctx, co, c.MessageID); !strings.Contains(ans.Body, "CHANGES_REQUESTED") || !strings.Contains(ans.Body, "a smaller mark") {
		t.Fatal(ans.Body)
	}
	if again, err := s.RequestArtifactApproval(ctx, co, "p", artifacts.ApprovalRequest{ArtifactID: "icon", ArtifactVersion: "v1", Agent: "demo", TaskID: "design", Reason: "as it is after all", RequestID: "icon-again"}); err != nil || again.ID == c.ID {
		t.Fatalf("%+v %v", again, err)
	}

	// Reject needs its reason.
	d := ask("card", "")
	if _, err = s.DecideApproval(ctx, co, d.ID, approvals.Decision{Version: d.Version, Action: "reject", RequestID: "reject-empty"}); !errors.Is(err, ErrInput) {
		t.Fatal("reject without a reason", err)
	}
	if got, err = s.DecideApproval(ctx, co, d.ID, approvals.Decision{Version: d.Version, Action: "reject", Reason: "off-brand", RequestID: "reject"}); err != nil || got.Status != "REJECTED" {
		t.Fatalf("%+v %v", got, err)
	}

	// Drop leaves out some of several options: a delegate escalating one version escalates it whole.
	e := ask("poster", "lead")
	if _, err = s.EscalateApproval(ctx, co, e.ID, "lead", "people decide", EscalatePeople{Drops: []approvals.Drop{{Option: 1, Reason: "weak"}}}); !errors.Is(err, ErrInput) {
		t.Fatal("the only option dropped", err)
	}
	if got, err = s.EscalateApproval(ctx, co, e.ID, "lead", "people decide"); err != nil || !got.Escalated {
		t.Fatalf("%+v %v", got, err)
	}
}

func optRef(o artifacts.ChoiceOption) tasks.Artifact {
	return tasks.Artifact{ID: o.ArtifactID, Version: o.ArtifactVersion, URI: "https://example.com/" + o.ArtifactID + "-" + o.ArtifactVersion + ".png"}
}
