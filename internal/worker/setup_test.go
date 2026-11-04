package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// gitT runs git in dir for a test.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// repoWithOpenSpec is a repository that tracks openspec/ (one spec) with a bare origin holding its main.
func repoWithOpenSpec(t *testing.T) (dir, origin string) {
	t.Helper()
	base := t.TempDir()
	origin, dir = filepath.Join(base, "origin.git"), filepath.Join(base, "app")
	gitT(t, base, "init", "--quiet", "--bare", "-b", "main", origin)
	gitT(t, base, "init", "--quiet", "-b", "main", dir)
	if err := os.MkdirAll(filepath.Join(dir, "openspec/specs/login"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"openspec/specs/login/spec.md": "# Login\n", "openspec/notes.txt": "not a document\n", "main.go": "package main\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", ".")
	if err := commit(context.Background(), dir, "app"); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "remote", "add", "origin", origin)
	gitT(t, dir, "push", "--quiet", "-u", "origin", "main")
	gitT(t, dir, "fetch", "--quiet", "origin")
	gitT(t, dir, "remote", "set-head", "origin", "main")
	return dir, origin
}

// TestSetupLocalRepository (project-creation 1.1): a local project gets a git repository with an initial
// commit on main under projects_root; a retry reuses it; a directory that is not the project's is refused.
func TestSetupLocalRepository(t *testing.T) {
	ctx := context.Background()
	w := &Worker{ProjectsRoot: t.TempDir(), NodeID: "mini"}
	spec := protocol.RunSpec{Project: "advent-gift", ProjectSetup: &protocol.ProjectSetup{Mode: "local", Name: "Advent gift", Description: "A gift"}}
	rep, err := w.prepareRepository(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(w.ProjectsRoot, "advent-gift")
	if rep.Workspace != dir || rep.RepoURL != "" {
		t.Fatal(rep)
	}
	if b := gitT(t, dir, "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Fatal(b)
	}
	if n := gitT(t, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatal(n)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(ignore) != "/.factory/\n" {
		t.Fatalf("openspec/ is tracked like code: %q", ignore)
	}
	if _, err = w.prepareRepository(ctx, spec); err != nil {
		t.Fatal("retry", err)
	}
	if n := gitT(t, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatal("retry committed again", n)
	}
	// Someone else's directory is never adopted.
	other := protocol.RunSpec{Project: "mine", ProjectSetup: &protocol.ProjectSetup{Mode: "local"}}
	_ = os.MkdirAll(filepath.Join(w.ProjectsRoot, "mine"), 0o755)
	_ = os.WriteFile(filepath.Join(w.ProjectsRoot, "mine", "keep.txt"), []byte("x"), 0o644)
	if _, err = w.prepareRepository(ctx, other); err == nil || !strings.Contains(err.Error(), "repo_mode existing") {
		t.Fatal(err)
	}
}

// fakeGH writes a gh stand-in: auth status (failing when FAKE_GH_UNAUTH is set), api user, repo view and
// repo create, which makes a bare repository under the fake GitHub; every call is logged.
func fakeGH(t *testing.T) (gh, github, log string) {
	t.Helper()
	dir := t.TempDir()
	github, log = filepath.Join(dir, "github"), filepath.Join(dir, "gh.log")
	gh = filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1 $2" in
"auth status") [ -n "$FAKE_GH_UNAUTH" ] && { echo "not logged in" >&2; exit 1; }; exit 0 ;;
"api user") echo owner1 ;;
"repo view") [ -d "` + github + `/$3.git" ] || { echo "not found" >&2; exit 1; }; echo "` + github + `/$3.git" ;;
"repo create") mkdir -p "` + github + `/$3.git" && git init --quiet --bare -b main "` + github + `/$3.git" ;;
*) echo "unexpected gh $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh, github, log
}

// TestSetupGitHubRepository (project-creation 1.1): github mode creates the private repository with the node's
// gh, pushes main and reports repo_url; a retry creates nothing again; gh missing or not logged in refuses
// with the reason.
func TestSetupGitHubRepository(t *testing.T) {
	ctx := context.Background()
	gh, github, log := fakeGH(t)
	w := &Worker{ProjectsRoot: t.TempDir(), NodeID: "mini", GH: gh}
	spec := protocol.RunSpec{Project: "advent-gift", ProjectSetup: &protocol.ProjectSetup{Mode: "github", Name: "Advent gift"}}
	rep, err := w.prepareRepository(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(github, "owner1/advent-gift.git")
	if rep.RepoURL != remote || rep.DefaultBranch != "main" || rep.Workspace != filepath.Join(w.ProjectsRoot, "advent-gift") {
		t.Fatal(rep)
	}
	if n := gitT(t, remote, "rev-list", "--count", "main"); n != "1" {
		t.Fatal("main not pushed", n)
	}
	if _, err = w.prepareRepository(ctx, spec); err != nil {
		t.Fatal("retry", err)
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "repo create owner1/advent-gift --private") != 1 {
		t.Fatal(string(b))
	}
	t.Setenv("FAKE_GH_UNAUTH", "1")
	if _, err = w.prepareRepository(ctx, protocol.RunSpec{Project: "other", ProjectSetup: &protocol.ProjectSetup{Mode: "github"}}); err == nil || !strings.Contains(err.Error(), "gh is not authenticated on node mini") {
		t.Fatal(err)
	}
	w.GH = filepath.Join(t.TempDir(), "no-gh")
	if _, err = w.prepareRepository(ctx, protocol.RunSpec{Project: "other", ProjectSetup: &protocol.ProjectSetup{Mode: "github"}}); err == nil || !strings.Contains(err.Error(), "not installed on node mini") {
		t.Fatal(err)
	}
}

// TestSetupExistingRepository (project-creation 1.3, openspec-in-git): an existing workspace is reported as
// it is: a tracked openspec/ stays tracked, nothing is committed or pushed; a repo_url alone is cloned under
// projects_root (and reused on a retry); a directory below the repository's top is refused.
func TestSetupExistingRepository(t *testing.T) {
	ctx := context.Background()
	w := &Worker{ProjectsRoot: t.TempDir(), NodeID: "mini"}
	dir, origin := repoWithOpenSpec(t)
	head := gitT(t, dir, "rev-parse", "HEAD")
	spec := protocol.RunSpec{Project: "kickclimb", TaskID: "t1", ProjectSetup: &protocol.ProjectSetup{Mode: "existing", Workspace: dir}}
	rep, err := w.prepareRepository(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Workspace != dir {
		t.Fatalf("%+v", rep)
	}
	if out := gitT(t, dir, "ls-files", "openspec"); !strings.Contains(out, "openspec/specs/login/spec.md") {
		t.Fatal("openspec/ no longer tracked", out)
	}
	if now := gitT(t, dir, "rev-parse", "HEAD"); now != head {
		t.Fatal("setup committed", now)
	}
	if out := gitT(t, origin, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(out, "openspec/specs/login/spec.md") {
		t.Fatal("origin main", out)
	}

	// A repo_url alone is cloned under projects_root.
	clone := protocol.RunSpec{Project: "remote-app", TaskID: "t2", ProjectSetup: &protocol.ProjectSetup{Mode: "existing", RepoURL: origin}}
	if rep, err = w.prepareRepository(ctx, clone); err != nil {
		t.Fatal(err)
	}
	if rep.Workspace != filepath.Join(w.ProjectsRoot, "remote-app") || rep.RepoURL != origin {
		t.Fatalf("%+v", rep)
	}
	if _, err = os.Stat(filepath.Join(rep.Workspace, "openspec/specs/login/spec.md")); err != nil {
		t.Fatal("clone lacks openspec/", err)
	}
	if _, err = w.prepareRepository(ctx, clone); err != nil {
		t.Fatal("retry", err)
	}
	// A workspace that is not a repository's top directory is refused.
	spec.ProjectSetup.Workspace = filepath.Join(dir, "openspec")
	if _, err = w.prepareRepository(ctx, spec); err == nil || !strings.Contains(err.Error(), "top directory") {
		t.Fatal(err)
	}
}

// pendingSetup creates project id in Core's store with its setup taken by run (as Dispatch records it).
func pendingSetup(t *testing.T, s *storage.Store, in tasks.ProjectCreate, run string) {
	t.Helper()
	p, err := s.CreateProject(context.Background(), storage.DefaultCompany, in)
	if err != nil {
		t.Fatal(err)
	}
	p.Setup.State, p.Setup.RunID = tasks.SetupRunning, run
	b, _ := json.Marshal(p)
	if _, err = s.DB.Exec("UPDATE projects SET body=? WHERE id=?", b, p.ID); err != nil {
		t.Fatal(err)
	}
}

// TestFirstTaskRunsInFreshRepository (project-creation 1.1): the first run of a project created
// without a workspace sets up a local repository on main, reports it to Core and runs there.
func TestFirstTaskRunsInFreshRepository(t *testing.T) {
	var store *storage.Store
	prepare := withStore(func(s *storage.Store) {
		store = s
		pendingSetup(t, s, tasks.ProjectCreate{ID: "fake-project", Name: "Fresh"}, "fake-run")
	})
	setup := stored(func(s *protocol.RunSpec) {
		s.Workspace, s.ProjectSetup = "", &protocol.ProjectSetup{Mode: "local", Name: "Fresh"}
	})
	root := t.TempDir()
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, Worker{Driver: d, ProjectsRoot: root, NodeID: "mini"}, false, prepare, setup)
	if r.Outcome != "succeeded" || len(d.Launches()) != 1 {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	dir := filepath.Join(root, "fake-project")
	if cwd := d.Launches()[0].Cwd; !samePath(cwd, dir) {
		t.Fatal(cwd)
	}
	if b := gitT(t, dir, "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Fatal(b)
	}
	p, err := store.Project(context.Background(), storage.DefaultCompany, "fake-project")
	if err != nil || p.Workspace != dir || p.Setup.State != tasks.SetupReady || p.Setup.NodeID != "mini" {
		t.Fatalf("%+v %v", p, err)
	}
	if r.Spec.Workspace != dir {
		t.Fatal("run spec keeps no workspace", r.Spec.Workspace)
	}
}

// TestOnboardsExistingRepository (project-creation 1.3, openspec-in-git): an existing repository
// that tracks openspec/ is set up before the first run, which runs in it with its openspec/ as committed and
// still tracked. A failing setup ends the run setup_failed with the reason in the project.
func TestOnboardsExistingRepository(t *testing.T) {
	dir, _ := repoWithOpenSpec(t)
	var store *storage.Store
	prepare := withStore(func(s *storage.Store) {
		store = s
		pendingSetup(t, s, tasks.ProjectCreate{ID: "fake-project", Name: "Kick", RepoMode: "existing", Workspace: dir}, "fake-run")
	})
	setup := stored(func(s *protocol.RunSpec) {
		s.Workspace, s.ProjectSetup = "", &protocol.ProjectSetup{Mode: "existing", Workspace: dir}
	})
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, Worker{Driver: d, NodeID: "mini", ProjectsRoot: t.TempDir()}, false, prepare, setup)
	if r.Outcome != "succeeded" || len(d.Launches()) != 1 || !samePath(d.Launches()[0].Cwd, dir) {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "openspec/specs/login/spec.md")); string(got) != "# Login\n" {
		t.Fatal("wiped", string(got))
	}
	if out := gitT(t, dir, "ls-files", "openspec"); !strings.Contains(out, "openspec/specs/login/spec.md") {
		t.Fatal(out)
	}
	p, _ := store.Project(context.Background(), storage.DefaultCompany, "fake-project")
	if p.Setup.State != tasks.SetupReady {
		t.Fatalf("%+v", p.Setup)
	}

	// A workspace that does not exist on the node: setup_failed, the reason in the project, no runtime.
	missing := filepath.Join(t.TempDir(), "nowhere")
	prepare = withStore(func(s *storage.Store) {
		store = s
		pendingSetup(t, s, tasks.ProjectCreate{ID: "fake-project", Name: "Gone", RepoMode: "existing", Workspace: missing}, "fake-run")
	})
	setup = stored(func(s *protocol.RunSpec) {
		s.Workspace, s.ProjectSetup = "", &protocol.ProjectSetup{Mode: "existing", Workspace: missing}
	})
	d = &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ = runDriver(t, Worker{Driver: d, NodeID: "mini"}, false, prepare, setup)
	if r.Outcome != "setup_failed" || !strings.Contains(r.Result, "project setup failed") || len(d.Launches()) != 0 {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	p, _ = store.Project(context.Background(), storage.DefaultCompany, "fake-project")
	if p.Setup.State != tasks.SetupFailed || !strings.Contains(p.Setup.Reason, "not a git repository") {
		t.Fatalf("%+v", p.Setup)
	}
}
