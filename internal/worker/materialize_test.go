package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/gitexclude"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestJetStreamMaterializesProjectData (core-owned-project-data 2.1, openspec-in-git): before the runtime
// starts the node downloads the claim's manifest from Core with the run's lease (a pinned artifact input),
// records .factory/manifest.json, excludes only .factory from git (a "/openspec/" rule an earlier version left
// in the worktree's excludes is dropped) and caches blobs by digest. The repository's openspec/ is left
// alone. A blob Core cannot serve ends the run setup_failed before the runtime starts.
func TestJetStreamMaterializesProjectData(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	design := []byte("<svg>v1</svg>")
	var files []protocol.ManifestFile
	prepare := withStore(func(s *storage.Store) {
		ctx := context.Background()
		if _, err := s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "fake-project", Name: "p", Workspace: "/nodes/p"}); err != nil {
			t.Fatal(err)
		}
		blob, err := s.PutBlob(ctx, storage.DefaultCompany, strings.NewReader(string(design)), storage.MaxBlob)
		if err != nil {
			t.Fatal(err)
		}
		// The artifact version that holds the blob (registration itself is covered in storage and core).
		body, _ := json.Marshal(map[string]any{"id": "mockup", "version": "v1"})
		if _, err = s.DB.Exec("INSERT INTO artifact_versions(project_id,id,version,uri,body,company_id,sha256,size) VALUES('fake-project','mockup','v1','factory://artifacts/mockup/v1',?,'default',?,?)", body, blob.SHA256, blob.Size); err != nil {
			t.Fatal(err)
		}
		input := tasks.Artifact{ID: "mockup", Version: "v1", URI: "factory://artifacts/mockup/v1", SHA256: blob.SHA256, Size: blob.Size, Name: "landing.svg"}
		files = []protocol.ManifestFile{{Path: protocol.InputPath(input), SHA256: blob.SHA256, Size: blob.Size}}
	})
	const proposal = "# Feature\nIn the repository\n"
	var workspace string
	manifest := func(s *protocol.RunSpec) {
		workspace = s.Workspace
		s.OpenSpecChange = "feature"
		s.Materialize = &protocol.Manifest{Files: files}
		if out, err := exec.Command("git", "init", "-q", workspace).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
		// The checkout holds the change; an earlier node version left an openspec rule in the excludes.
		_ = os.MkdirAll(filepath.Join(workspace, "openspec/changes/feature"), 0o755)
		_ = os.WriteFile(filepath.Join(workspace, "openspec/changes/feature/proposal.md"), []byte(proposal), 0o644)
		_ = os.WriteFile(filepath.Join(workspace, ".git", gitexclude.FileName), []byte("# old\n/openspec/\n/.factory/\n"), 0o644)
	}
	cache := t.TempDir()
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, url, Worker{Driver: d, BlobCache: cache}, false, prepare, manifest)
	if r.Outcome != "succeeded" || len(d.Launches()) != 1 {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(workspace, p))
		return string(b)
	}
	if read("openspec/changes/feature/proposal.md") != proposal || read(".factory/inputs/mockup/v1/landing.svg") != string(design) {
		t.Fatal("files differ")
	}
	var saved protocol.Manifest
	if err := json.Unmarshal([]byte(read(protocol.ManifestPath)), &saved); err != nil || len(saved.Files) != 1 {
		t.Fatal(saved, err)
	}
	if exclude := read(".git/" + gitexclude.FileName); strings.Contains(exclude, "/openspec/") || !strings.Contains(exclude, "/.factory/\n") {
		t.Fatal(exclude)
	}
	if _, err := os.Stat(filepath.Join(cache, files[0].SHA256)); err != nil {
		t.Fatal("blob not cached", err)
	}

	// A blob Core does not hold: setup_failed, the runtime never starts.
	missing := func(s *protocol.RunSpec) {
		s.Materialize = &protocol.Manifest{Files: []protocol.ManifestFile{{Path: ".factory/inputs/x/v1/x.svg", SHA256: openspec.Digest([]byte("never uploaded")), Size: 14}}}
	}
	d = &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ = runDriver(t, url, Worker{Driver: d}, false, missing)
	if r.Outcome != "setup_failed" || !strings.Contains(r.Result, "materializing project data failed before runtime startup") || len(d.Launches()) != 0 {
		t.Fatalf("%s %q, %d launches", r.Outcome, r.Result, len(d.Launches()))
	}
}

// TestManifestPathsStayInside: the node refuses manifest entries outside .factory/inputs.
func TestManifestPathsStayInside(t *testing.T) {
	for _, p := range []string{"../x.md", "/etc/passwd", "openspec/changes/x/proposal.md", "src/main.go", ".factory/inputs/a/../../../x", ".factory/inputs/a/b"} {
		if validManifestPath(p) {
			t.Fatal("accepted", p)
		}
	}
	if !validManifestPath(".factory/inputs/a/v1/file.svg") {
		t.Fatal("refused an input")
	}
	for _, a := range []tasks.Artifact{{ID: "a", Version: "../..", Name: "x/y"}, {ID: "a", Version: "..", Name: "."}} {
		if got := protocol.InputPath(a); !validManifestPath(got) || strings.Contains(got, "/../") {
			t.Fatal(got)
		}
	}
}

// TestPreflightArchivedChange: the node preflight reads the checkout: a missing change fails, an active or
// archived one passes.
func TestPreflightArchivedChange(t *testing.T) {
	cwd := t.TempDir()
	spec := protocol.RunSpec{OpenSpecChange: "feature"}
	if e := preflightChange(cwd, spec); e == nil {
		t.Fatal("missing change passed")
	}
	dir := filepath.Join(cwd, "openspec", "changes", "archive", "2026-10-22-feature")
	if e := os.MkdirAll(dir, 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "proposal.md"), []byte("# Feature\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := preflightChange(cwd, spec); e != nil {
		t.Fatal(e)
	}
}

// TestExcludeFromGitOnlyThisWorktree: materialization in a task worktree keeps .factory out of the agent's
// commits while openspec/ is tracked like code (openspec-in-git); the main checkout of the same repository
// still sees the owner's new files, and the rules an earlier version left in the shared info/exclude
// (including the retired "/openspec/") are removed.
func TestExcludeFromGitOnlyThisWorktree(t *testing.T) {
	base := t.TempDir()
	gc := filepath.Join(base, "gitconfig")
	if err := os.WriteFile(gc, []byte("[user]\n\tname = T\n\temail = t@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitT := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	put := func(p string) {
		t.Helper()
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(base, "main")
	gitT(base, "init", "-q", main)
	gitT(main, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(main, ".git/info/exclude"), []byte("*.tmp\n/openspec/\n/.factory/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "task")
	gitT(main, "worktree", "add", "-q", "-b", "af/t1", wt)
	if err := (&Worker{}).Materialize(context.Background(), protocol.RunSpec{Materialize: &protocol.Manifest{}}, "", wt); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{main, wt} {
		put(filepath.Join(d, "openspec/changes/x/proposal.md"))
		put(filepath.Join(d, ".factory/notes.json"))
	}
	if s := gitT(wt, "status", "--porcelain", "--untracked-files=all"); s != "?? openspec/changes/x/proposal.md" {
		t.Fatalf("task worktree: %q", s)
	}
	if s := gitT(main, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(s, "openspec/changes/x/proposal.md") || !strings.Contains(s, ".factory/notes.json") {
		t.Fatalf("main checkout: %q", s)
	}
	if b, _ := os.ReadFile(filepath.Join(main, ".git/info/exclude")); string(b) != "*.tmp\n" {
		t.Fatalf("shared info/exclude = %q", b)
	}
}
