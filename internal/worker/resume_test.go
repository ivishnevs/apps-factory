package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
)

// TestJetStreamResumePolicy: a run resumes its session only when this node ran it for the same
// (task, agent) and its prompt reached the runtime; otherwise it starts a fresh session with the note
// that the context comes from Core. The node records the session it ran.
func TestJetStreamResumePolicy(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
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
			r, _ := runDriver(t, url, Worker{Driver: d, Sessions: store}, false, func(s *protocol.RunSpec) { s.TaskID, s.SessionID = "t1", "s-prev" })
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

// skillRegistry is a factory root with the openspec skill registered.
func skillRegistry(t *testing.T) (*skills.Registry, string) {
	t.Helper()
	root := t.TempDir()
	for p, data := range map[string]string{
		"configs/skills.yaml":                 "skills:\n  openspec:\n    path: ../skills/openspec/SKILL.md\n    description: Specs.\n    categories: [planning]\n",
		"skills/openspec/SKILL.md":            "---\nname: openspec\ndescription: Specs.\n---\nbody\n",
		"skills/openspec/references/guide.md": "guide\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := skills.Load(filepath.Join(root, "configs"))
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := r.Dir("openspec")
	hash, err := skills.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, hash
}

// TestJetStreamSkillsPinned: offered skills reach the driver as a verified copy; a pinned hash that
// differs from the node's skill, or a skill the node lacks, fails the run before the runtime starts.
func TestJetStreamSkillsPinned(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	registry, hash := skillRegistry(t)
	snapshot := func(pin string) stored {
		return func(s *protocol.RunSpec) {
			s.ConfigSnapshot, _ = json.Marshal(map[string]any{"skill_hashes": map[string]string{"openspec": pin}})
		}
	}
	offer := func(names ...string) func(*protocol.RunSpec) { return func(s *protocol.RunSpec) { s.Skills = names } }
	for name, c := range map[string]struct {
		mods   []any
		status string
		result string
	}{
		"pinned":   {[]any{snapshot(hash), offer("openspec")}, "succeeded", "ok"},
		"unpinned": {[]any{offer("openspec")}, "succeeded", "ok"},
		"mismatch": {[]any{snapshot("sha256:0000"), offer("openspec")}, "setup_failed", "differs from the run's pinned sha256:0000"},
		"missing":  {[]any{offer("openspec", "absent")}, "setup_failed", "absent is not in this node's skills registry"},
	} {
		t.Run(name, func(t *testing.T) {
			d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
			r, _ := runDriver(t, url, Worker{Driver: d, Skills: registry}, false, c.mods...)
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
			if len(l) != 1 || len(l[0].Skills) != 1 || l[0].Skills[0].Name != "openspec" || l[0].Skills[0].Hash != hash || l[0].SkillsRoot == "" {
				t.Fatalf("launch skills %+v in %q", l[0].Skills, l[0].SkillsRoot)
			}
			if _, err := os.Stat(l[0].SkillsRoot); !os.IsNotExist(err) {
				t.Fatalf("staged skills left after the run: %v", err)
			}
		})
	}
}
