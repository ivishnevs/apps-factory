package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/tui"
)

// screenshotsTask replays task 2509ee3e (Tracely iOS 1.2: App Store screenshots, 2026-11-12) with fakes: a
// storyboard, two approval samples (a preview and a full-res zip of six frames each), the final gallery named by
// the last submission, and the merged pull request of the done exit.
func screenshotsTask(final []storage.TaskExit) (*tui.FixtureClient, string) {
	const id, project = "2509ee3e", "tracely"
	frames := []artifacts.ArchiveEntry{{Path: "01-hero.png", Size: 9}, {Path: "02-timeline.png", Size: 9}, {Path: "03-insights.png", Size: 9},
		{Path: "04-widgets.png", Size: 9}, {Path: "05-privacy.png", Size: 9}, {Path: "06-variety.png", Size: 9}}
	rec := func(aid, version, name string, at int64, images []artifacts.ArchiveEntry) artifacts.Record {
		return artifacts.Record{Artifact: tasks.Artifact{ID: aid, Version: version, URI: "factory://artifacts/" + aid + "/" + version, SHA256: strings.Repeat(string(rune('a'+at%6)), 64), Name: name},
			ProjectID: project, TaskID: id, RunID: "r", CreatedBy: "launch-creative", CreatedAt: at, ArchiveImages: images}
	}
	records := []artifacts.Record{
		rec("storyboard", "v1", "storyboard.md", 100, nil),
		rec("screenshots-sample-preview", "v1", "sample-preview.png", 200, nil),
		rec("screenshots-sample", "v1", "screenshots-full.zip", 201, frames),
		rec("screenshots-sample-preview", "v2", "sample-preview.png", 300, nil),
		rec("screenshots-sample", "v2", "screenshots-full.zip", 301, frames),
		rec("screenshots-gallery", "ios-1.2-final-v1", "gallery-preview-1.2.png", 400, nil),
	}
	approval := func(aid, version, reason string) approvals.Approval {
		return approvals.Approval{ID: "ap-" + aid + "-" + version, ProjectID: project, Kind: "artifact", Status: "APPROVED", DecisionReason: reason,
			Artifact: &tasks.Artifact{ID: aid, Version: version}}
	}
	gallery := tasks.Artifact{ID: "screenshots-gallery", Version: "ios-1.2-final-v1", URI: "factory://artifacts/screenshots-gallery/ios-1.2-final-v1"}
	exits := []storage.TaskExit{
		{RunID: "r1", Kind: "submit", Artifacts: []tasks.Artifact{{ID: "screenshots-sample", Version: "v1", URI: "factory://artifacts/screenshots-sample/v1"}}, CreatedAt: 210},
		{RunID: "r2", Kind: "handoff", CreatedAt: 220},
		{RunID: "r3", Kind: "submit", Artifacts: []tasks.Artifact{gallery}, CreatedAt: 410},
		{RunID: "r4", Kind: "done", Facts: &storage.ExitFacts{PR: &storage.PRFacts{Number: 152, State: "merged", URL: "https://example.com/pull/152"}}, CreatedAt: 420},
	}
	exits = append(exits, final...)
	task := tui.Task{Task: tasks.Task{ID: id, ProjectID: project, Title: "Tracely iOS 1.2: App Store screenshots", Status: "DONE", Assignee: "launch-creative"}}
	f := tui.Fixture{Details: map[string]tui.TaskDetail{id: {Task: task, Exits: exits}}, ArtifactRecords: map[string][]artifacts.Record{project: records}}
	f.Overview.Tasks = []tui.Task{task}
	f.Overview.Approvals = []approvals.Approval{
		approval("screenshots-sample", "v1", "approved; keep the hero"),
		approval("screenshots-sample", "v2", "the first screenshot without the callout"),
	}
	return &tui.FixtureClient{F: f}, id
}

func taskMaterialsOf(t *testing.T, c *tui.FixtureClient, id string) []Material {
	t.Helper()
	srv := httptest.NewServer((&Server{Client: c, Mode: "demo"}).Handler())
	t.Cleanup(srv.Close)
	var ms []Material
	if code := getJSON(t, srv.URL+"/api/tasks/"+id+"/materials", &ms); code != 200 {
		t.Fatal(code)
	}
	return ms
}

// TestTaskResultFirst (task-result-first 1.1, 2.2): the final gallery the last submission named is the result and
// comes first; the samples, their frames and the storyboard are not the result, each names its artifact and
// version (the six 01-hero.png … frames of v1 and v2 stay apart) and the samples carry the board's approval comment.
func TestTaskResultFirst(t *testing.T) {
	c, id := screenshotsTask(nil)
	ms := taskMaterialsOf(t, c, id)
	if len(ms) != 6+12+1 { // six versions, twelve frames, the pull request
		t.Fatal(len(ms), names(ms))
	}
	lead := ms[0]
	if !lead.Result || lead.Name != "gallery-preview-1.2.png" || lead.ArtifactID != "screenshots-gallery" || lead.Version != "ios-1.2-final-v1" || lead.Kind != "image" {
		t.Fatalf("%+v", lead)
	}
	heroes := map[string]bool{}
	for _, m := range ms[1:] {
		if m.Result {
			t.Fatalf("not the result: %+v", m)
		}
		if m.Kind == "pull_request" {
			continue
		}
		if m.ArtifactID == "" || m.Version == "" {
			t.Fatalf("no artifact or version: %+v", m)
		}
		if m.Name == "01-hero.png" {
			heroes[m.ArtifactID+" "+m.Version] = true
			if m.Archive != "screenshots-full.zip "+m.Version {
				t.Fatalf("%+v", m)
			}
		}
		if m.ArtifactID == "screenshots-sample" && m.Source == "artifact" && (m.Approval == nil || m.Approval.Status != "APPROVED") {
			t.Fatalf("sample without its approval: %+v", m)
		}
		if m.ArtifactID == "screenshots-sample" && m.Version == "v2" && m.Source == "artifact" && m.Approval.Reason != "the first screenshot without the callout" {
			t.Fatalf("%+v", m.Approval)
		}
	}
	if !heroes["screenshots-sample v1"] || !heroes["screenshots-sample v2"] || len(heroes) != 2 {
		t.Fatal(heroes)
	}
	// The rest stays newest first: the v2 sample before v1, the storyboard last.
	rest := []string{}
	for _, m := range ms[1:] {
		if m.Source == "artifact" {
			rest = append(rest, m.ArtifactID+" "+m.Version)
		}
	}
	if strings.Join(rest, ",") != "screenshots-sample v2,screenshots-sample-preview v2,screenshots-sample v1,screenshots-sample-preview v1,storyboard v1" {
		t.Fatal(rest)
	}
}

// TestTaskResultFromDone: a later done exit that names artifacts is the result, in the order it named them, an
// archive's frames right after it; a done exit that names none leaves the last submission's.
func TestTaskResultFromDone(t *testing.T) {
	c, id := screenshotsTask([]storage.TaskExit{{RunID: "r5", Kind: "done", CreatedAt: 500, Artifacts: []tasks.Artifact{
		{ID: "screenshots-sample", Version: "v2"}, {ID: "screenshots-sample-preview", Version: "v2"}}}})
	ms := taskMaterialsOf(t, c, id)
	got := []string{}
	for _, m := range ms {
		if m.Result {
			got = append(got, m.ArtifactID+" "+m.Version+" "+m.Name)
		}
	}
	want := "screenshots-sample v2 screenshots-full.zip,screenshots-sample v2 01-hero.png,screenshots-sample v2 02-timeline.png,screenshots-sample v2 03-insights.png," +
		"screenshots-sample v2 04-widgets.png,screenshots-sample v2 05-privacy.png,screenshots-sample v2 06-variety.png,screenshots-sample-preview v2 sample-preview.png"
	if strings.Join(got, ",") != want || !ms[0].Result || ms[len(got)].Result {
		t.Fatal(strings.Join(got, ","))
	}

	// Without exits naming artifacts, a submission made outside explicit exits is the result; without any, nothing is.
	sub := tui.Task{Task: tasks.Task{ID: "s", Submission: &tasks.Submission{Artifacts: []tasks.Artifact{{ID: "a", Version: "v1"}}}}}
	if r := resultOf(sub, []storage.TaskExit{{Kind: "done"}}); len(r) != 1 {
		t.Fatal(r)
	}
	if r := resultOf(tui.Task{}, []storage.TaskExit{{Kind: "handoff", Artifacts: []tasks.Artifact{{ID: "a", Version: "v1"}}}}); len(r) != 0 {
		t.Fatal(r)
	}
}
