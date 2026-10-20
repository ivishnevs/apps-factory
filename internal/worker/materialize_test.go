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
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestJetStreamMaterializesProjectData (core-owned-project-data 2.1): before the runtime starts the node
// downloads the claim's manifest from Core with the run's lease: the project's OpenSpec tree (a document Core
// no longer has is removed, other files stay) and a pinned artifact input; it records .factory/manifest.json,
// excludes both directories from git and caches blobs by digest. A blob Core cannot serve ends the run
// setup_failed before the runtime starts.
func TestJetStreamMaterializesProjectData(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	const proposal = "# Feature\nCore-owned\n"
	design := []byte("<svg>v1</svg>")
	var files []protocol.ManifestFile
	prepare := withStore(func(s *storage.Store) {
		ctx := context.Background()
		if _, err := s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "fake-project", Name: "p", Workspace: "/nodes/p"}); err != nil {
			t.Fatal(err)
		}
		w, err := s.WriteOpenSpec(ctx, storage.DefaultCompany, "fake-project", storage.OpenSpecChange{Path: "changes/feature/proposal.md", Content: []byte(proposal), Actor: "owner"})
		if err != nil {
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
		files = []protocol.ManifestFile{{Path: "openspec/changes/feature/proposal.md", SHA256: w.SHA256, Size: w.Size}, {Path: protocol.InputPath(input), SHA256: blob.SHA256, Size: blob.Size}}
	})
	var workspace string
	manifest := func(s *protocol.RunSpec) {
		workspace = s.Workspace
		s.OpenSpecChange = "feature"
		s.Materialize = &protocol.Manifest{OpenSpecDir: "openspec", Files: files}
		// A document a previous run left that Core no longer has, and an unrelated file beside the tree.
		_ = os.MkdirAll(filepath.Join(workspace, "openspec/specs/gone"), 0o755)
		_ = os.WriteFile(filepath.Join(workspace, "openspec/specs/gone/spec.md"), []byte("old"), 0o644)
		_ = os.WriteFile(filepath.Join(workspace, "openspec/notes.txt"), []byte("mine"), 0o644)
		if out, err := exec.Command("git", "init", "-q", workspace).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
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
	if read("openspec/changes/feature/proposal.md") != proposal || read(".factory/inputs/mockup/v1/landing.svg") != string(design) || read("openspec/notes.txt") != "mine" {
		t.Fatal("materialized files differ")
	}
	if _, err := os.Stat(filepath.Join(workspace, "openspec/specs/gone/spec.md")); !os.IsNotExist(err) {
		t.Fatal("stale document kept", err)
	}
	for _, dir := range []string{"openspec/specs", "openspec/changes/archive"} {
		if info, err := os.Stat(filepath.Join(workspace, dir)); err != nil || !info.IsDir() {
			t.Fatal(dir, err)
		}
	}
	var saved protocol.Manifest
	if err := json.Unmarshal([]byte(read(protocol.ManifestPath)), &saved); err != nil || len(saved.Files) != 2 || saved.OpenSpecDir != "openspec" {
		t.Fatal(saved, err)
	}
	if exclude := read(".git/info/exclude"); !strings.Contains(exclude, "/openspec/\n") || !strings.Contains(exclude, "/.factory/\n") {
		t.Fatal(exclude)
	}
	if _, err := os.Stat(filepath.Join(cache, files[1].SHA256)); err != nil {
		t.Fatal("blob not cached", err)
	}

	// A blob Core does not hold: setup_failed, the runtime never starts.
	missing := func(s *protocol.RunSpec) {
		s.Materialize = &protocol.Manifest{OpenSpecDir: "openspec", Files: []protocol.ManifestFile{{Path: "openspec/specs/x/spec.md", SHA256: openspec.Digest([]byte("never uploaded")), Size: 14}}}
	}
	d = &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ = runDriver(t, url, Worker{Driver: d}, false, missing)
	if r.Outcome != "setup_failed" || !strings.Contains(r.Result, "materializing project data failed before runtime startup") || len(d.Launches()) != 0 {
		t.Fatalf("%s %q, %d launches", r.Outcome, r.Result, len(d.Launches()))
	}
}

// TestManifestPathsStayInside: the node refuses manifest entries outside the OpenSpec tree and inputs.
func TestManifestPathsStayInside(t *testing.T) {
	m := &protocol.Manifest{OpenSpecDir: "openspec"}
	for _, p := range []string{"../x.md", "/etc/passwd", "openspec/../.git/config", "src/main.go", "openspec/notes.txt", ".factory/inputs/a/../../../x", ".factory/inputs/a/b"} {
		if validManifestPath(m, p) {
			t.Fatal("accepted", p)
		}
	}
	for _, p := range []string{"openspec/config.yaml", "openspec/changes/x/proposal.md", ".factory/inputs/a/v1/file.svg"} {
		if !validManifestPath(m, p) {
			t.Fatal("refused", p)
		}
	}
	for _, a := range []tasks.Artifact{{ID: "a", Version: "../..", Name: "x/y"}, {ID: "a", Version: "..", Name: "."}} {
		if got := protocol.InputPath(a); !validManifestPath(m, got) || strings.Contains(got, "/../") {
			t.Fatal(got)
		}
	}
}
