package worker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/workspace"
)

// TestJetStreamConversationBesideWriter (hierarchy D5): while a writer run holds the shared project workspace
// (its writer lock and its .factory/manifest.json), a conversation run of the same project starts and
// succeeds in its own conversation directory under state_dir: its project data is materialized there, the
// writer's workspace and manifest stay untouched, and the project workspace reaches it read-only by path.
// The next conversation run resumes its session in the same directory; a session recorded before
// conversation directories (no directory) starts fresh.
func TestJetStreamConversationBesideWriter(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	const proposal = "# Feature\nCore-owned\n"
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
		files = []protocol.ManifestFile{{Path: "openspec/changes/feature/proposal.md", SHA256: w.SHA256, Size: w.Size}}
	})
	const writerManifest = `{"openspec_dir":"openspec","files":[]}`
	var ws string
	var writer workspace.Allocation
	conversation := func(session string) func(*protocol.RunSpec) {
		return func(s *protocol.RunSpec) {
			ws = s.Workspace
			// The writer run of the project holds the workspace and its manifest.
			if err := os.MkdirAll(filepath.Join(ws, ".factory"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(ws, protocol.ManifestPath), []byte(writerManifest), 0o644); err != nil {
				t.Fatal(err)
			}
			var err error
			if writer, err = (workspace.Local{}).Acquire(context.Background(), ws); err != nil {
				t.Fatal(err)
			}
			s.Conversation, s.TaskID, s.SessionID = true, "conv-demo", session
			s.Materialize = &protocol.Manifest{OpenSpecDir: "openspec", Files: files}
		}
	}
	state := t.TempDir()
	store := sessions.Open(t.TempDir())
	want, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(want, "conversations", "default", "fake-project", "demo")

	run := func(session string) *fake.Driver {
		t.Helper()
		d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}, Files: map[string]string{"notes.md": "conversation"}}
		r, _ := runDriver(t, url, Worker{Driver: d, StateDir: state, Sessions: store, BlobCache: t.TempDir()}, false, prepare, conversation(session))
		defer writer.Release()
		if r.Outcome != "succeeded" || len(d.Launches()) != 1 {
			t.Fatalf("%s %q", r.Outcome, r.Result)
		}
		l := d.Launches()[0]
		if l.Cwd != want {
			t.Fatalf("cwd %q, want %q", l.Cwd, want)
		}
		if !slices.Contains(l.Env, "FACTORY_PROJECT_WORKSPACE="+ws) || !strings.Contains(l.Instructions, ws) {
			t.Fatalf("project workspace not passed: %q", l.Instructions)
		}
		if b, err := os.ReadFile(filepath.Join(want, "openspec/changes/feature/proposal.md")); err != nil || string(b) != proposal {
			t.Fatal("not materialized in the conversation directory", err)
		}
		if b, err := os.ReadFile(filepath.Join(want, protocol.ManifestPath)); err != nil || !strings.Contains(string(b), files[0].SHA256) {
			t.Fatal("conversation manifest", err)
		}
		// The writer's workspace: its manifest as it was, nothing materialized or written there, still locked.
		if b, err := os.ReadFile(filepath.Join(ws, protocol.ManifestPath)); err != nil || string(b) != writerManifest {
			t.Fatalf("writer manifest changed: %q %v", b, err)
		}
		for _, p := range []string{"openspec", "notes.md"} {
			if _, err := os.Stat(filepath.Join(ws, p)); !os.IsNotExist(err) {
				t.Fatalf("%s written into the project workspace: %v", p, err)
			}
		}
		if _, err := (workspace.Local{}).Acquire(context.Background(), ws); err == nil {
			t.Fatal("writer lock released by the conversation run")
		}
		return d
	}

	first := run("")
	if first.Launches()[0].Spec.SessionID != "" {
		t.Fatal("first run resumed")
	}
	rec, ok, err := store.Get("fake-session")
	if err != nil || !ok || rec.Cwd != want {
		t.Fatalf("session record %+v %v %v", rec, ok, err)
	}
	if l := run("fake-session").Launches()[0]; l.Spec.SessionID != "fake-session" || l.Spec.Prompt != "work" {
		t.Fatalf("not resumed in the conversation directory: %+v", l.Spec)
	}
	// A conversation session from before conversation directories ran in the project workspace.
	if err = store.Put(sessions.Record{SessionID: "old", Task: "conv-demo", Agent: "demo", Run: "r0", Started: true, Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if l := run("old").Launches()[0]; l.Spec.SessionID != "" || !strings.HasPrefix(l.Spec.Prompt, FreshSessionNote) {
		t.Fatalf("resumed a session of another directory: %+v", l.Spec)
	}
}

// TestJetStreamConversationNoWorktree: in worktree mode a conversation run gets no task worktree or branch;
// it runs in its conversation directory like in the shared mode.
func TestJetStreamConversationNoWorktree(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	remote := bareRemote(t)
	root := t.TempDir()
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, url, Worker{Driver: d, StateDir: state, Worktrees: trees}, false, func(s *protocol.RunSpec) {
		s.Conversation, s.TaskID, s.RepoURL, s.DefaultBranch, s.WorkspaceMode = true, "conv-demo", remote, "main", "worktree"
	})
	if r.Outcome != "succeeded" || len(d.Launches()) != 1 {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	if cwd := d.Launches()[0].Cwd; !strings.HasSuffix(cwd, filepath.Join("conversations", "default", "fake-project", "demo")) {
		t.Fatalf("cwd %q", cwd)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("worktree created for a conversation run: %v", entries)
	}
	if out := gitOut(t, remote, "branch", "--list", "af/*"); out != "" {
		t.Fatalf("task branch %q", out)
	}
}

// TestJetStreamConversationRelativeStateDir: a relative state_dir (a node started from its checkout with
// `state_dir: data/...`) still gives the conversation run an absolute directory.
func TestJetStreamConversationRelativeStateDir(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	t.Chdir(t.TempDir())
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, url, Worker{Driver: d, StateDir: filepath.Join("data", "factory-node-tvorog")}, false, func(s *protocol.RunSpec) {
		s.Conversation, s.TaskID = true, "conv-demo"
	})
	if r.Outcome != "succeeded" || len(d.Launches()) != 1 {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, "data", "factory-node-tvorog", "conversations", "default", "fake-project", "demo")
	if got := d.Launches()[0].Cwd; got != want {
		t.Fatalf("cwd %q, want %q", got, want)
	}
}
