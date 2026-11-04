package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/driver/fake"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
)

// TestResumePolicy: a run resumes its session only when this node ran it for the same
// (task, agent) and its prompt reached the runtime; otherwise it starts a fresh session with the note
// that the context comes from Core. The node records the session it ran.
func TestResumePolicy(t *testing.T) {
	cases := map[string]struct {
		prev   *sessions.Record
		resume bool
	}{
		"same node":        {&sessions.Record{SessionID: "s-prev", Task: "t1", Agent: "demo", Run: "r0", Started: true, Outcome: "succeeded"}, true},
		"other node":       {nil, false},
		"never started":    {&sessions.Record{SessionID: "s-prev", Task: "t1", Agent: "demo", Run: "r0"}, false},
		"other agent":      {&sessions.Record{SessionID: "s-prev", Task: "t1", Agent: "reviewer", Run: "r0", Started: true}, false},
		"other task":       {&sessions.Record{SessionID: "s-prev", Task: "t0", Agent: "demo", Run: "r0", Started: true}, false},
		"reset workspace":  {&sessions.Record{SessionID: "s-prev", Task: "t1", Agent: "demo", Run: "r0", Started: true}, false},
		"failed but saved": {&sessions.Record{SessionID: "s-prev", Task: "t1", Agent: "demo", Run: "r0", Started: true, Outcome: "lost"}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store := sessions.Open(t.TempDir())
			if c.prev != nil {
				if err := store.Put(*c.prev); err != nil {
					t.Fatal(err)
				}
			}
			if name == "reset workspace" {
				if _, err := store.ForgetTask("t1"); err != nil {
					t.Fatal(err)
				}
			}
			d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
			r, _ := runDriver(t, Worker{Driver: d, Sessions: store}, false, func(s *protocol.RunSpec) { s.TaskID, s.SessionID = "t1", "s-prev" })
			l := d.Launches()
			if r.Status != "ENDED" || r.Outcome != "succeeded" || len(l) != 1 {
				t.Fatalf("%+v, %d launches", r, len(l))
			}
			session := "fake-session"
			if c.resume {
				session = "s-prev"
				if l[0].Spec.SessionID != "s-prev" || l[0].Spec.Prompt != "work" {
					t.Fatalf("not resumed: %+v", l[0].Spec)
				}
			} else if l[0].Spec.SessionID != "" || l[0].Spec.Prompt != FreshSessionNote+"work" {
				t.Fatalf("not fresh: %+v", l[0].Spec)
			}
			if r.SessionID != session {
				t.Fatalf("run session %q, want %q", r.SessionID, session)
			}
			rec, ok, err := store.Get(session)
			if err != nil || !ok || rec.Task != "t1" || rec.Agent != "demo" || rec.Run != "fake-run" || !rec.Started || rec.Outcome != "succeeded" {
				t.Fatalf("record %+v %v %v", rec, ok, err)
			}
		})
	}
}

// indexedSkill packs an openspec skill and indexes it in the store's default company (source test), as Core does
// for an installed skill; it returns the content hash.
func indexedSkill(t *testing.T, s *storage.Store, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "openspec")
	for p, data := range map[string]string{"SKILL.md": "---\nname: openspec\ndescription: Specs.\n---\n" + body + "\n", "references/guide.md": "guide\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := skills.Index(dir)
	if err != nil || len(idx) != 1 || idx[0].Error != "" {
		t.Fatal(idx, err)
	}
	ctx := context.Background()
	if err = s.SaveSkillSource(ctx, storage.SkillSource{Scope: storage.DefaultCompany, ID: "test", Kind: storage.SkillSourceUpload}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.IndexSkills(ctx, storage.DefaultCompany, "test", "c1", idx); err != nil {
		t.Fatal(err)
	}
	return idx[0].Hash
}

// TestSkillsPinned (skill-registry 3.4, 3.5): an offered skill's pinned hash is downloaded from Core into the node's
// store, verified and copied into the run's view the driver installs from; a second run uses the store; a skill
// without a pin is skipped; a malformed pin or a hash Core does not hold fails the run before the runtime starts.
func TestSkillsPinned(t *testing.T) {
	store := t.TempDir()
	var hash string
	index := withStore(func(s *storage.Store) { hash = indexedSkill(t, s, "body") })
	pin := func(p func() string) stored {
		return func(s *protocol.RunSpec) {
			s.ConfigSnapshot, _ = json.Marshal(map[string]any{"skill_hashes": map[string]string{"openspec": p()}})
		}
	}
	offer := func(names ...string) func(*protocol.RunSpec) { return func(s *protocol.RunSpec) { s.Skills = names } }
	foreign := "sha256:" + strings.Repeat("0", 64)
	for _, c := range []struct {
		name   string
		mods   []any
		status string
		result string
	}{
		{"pinned", []any{index, pin(func() string { return hash }), offer("openspec")}, "succeeded", "ok"},
		{"from the store", []any{index, pin(func() string { return hash }), offer("openspec")}, "succeeded", "ok"},
		{"unpinned", []any{index, offer("openspec")}, "succeeded", "ok"},
		{"malformed", []any{index, pin(func() string { return "sha256:0000" }), offer("openspec")}, "setup_failed", "invalid skill hash"},
		{"foreign hash", []any{index, pin(func() string { return foreign }), offer("openspec")}, "setup_failed", "download from Core"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
			r, _ := runDriver(t, Worker{Driver: d, SkillStore: store}, false, c.mods...)
			if r.Outcome != c.status || !strings.Contains(r.Result, c.result) {
				t.Fatalf("%s: %q", r.Outcome, r.Result)
			}
			l := d.Launches()
			if c.status == "setup_failed" {
				if len(l) != 0 || !strings.Contains(r.Result, "refused before runtime startup") {
					t.Fatalf("runtime launched for a refused run: %d", len(l))
				}
				return
			}
			if c.name == "unpinned" { // skipped with a warning, the run goes on without it
				if len(l) != 1 || len(l[0].Skills) != 0 {
					t.Fatalf("launch skills %+v", l)
				}
				return
			}
			if len(l) != 1 || len(l[0].Skills) != 1 || l[0].Skills[0].Name != "openspec" || l[0].Skills[0].Hash != hash || l[0].SkillsRoot == "" {
				t.Fatalf("launch skills %+v in %q", l[0].Skills, l[0].SkillsRoot)
			}
			stored := filepath.Join(store, "store", "sha256-"+strings.TrimPrefix(hash, "sha256:"))
			if info, err := os.Stat(filepath.Join(stored, "SKILL.md")); err != nil || info.Mode().Perm() != 0o444 {
				t.Fatal("not in the read-only store", err)
			}
			if _, err := os.Stat(l[0].SkillsRoot); !os.IsNotExist(err) {
				t.Fatal("the run's view outlived the run", err)
			}
		})
	}
	// The GC removes entries unused for long, never kept ones.
	if n, err := GCSkillStore(store, time.Hour, map[string]bool{hash: true}); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := GCSkillStore(store, -time.Second, nil); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
