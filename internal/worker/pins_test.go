package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
)

// TestJetStreamCorePinnedSkills (runtime-drivers 2.3) end to end: Core pins the offered skills' hashes over its
// own registry in the run's agent snapshot; a node whose registry holds the same content runs the agent with
// the verified copy, and a node whose copy of the skill differs refuses the run before the runtime starts.
func TestJetStreamCorePinnedSkills(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	coreRegistry, hash := skillRegistry(t)
	c := &core.Core{Skills: coreRegistry}
	agent := config.Agent{Metadata: config.Metadata{Name: "demo"}, AvailableSkills: []string{"openspec"}}
	snapshot, _, err := c.RunSnapshot(agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), `"skill_hashes":{"openspec":"`+hash+`"}`) {
		t.Fatalf("Core did not pin the skill: %s", snapshot)
	}
	pinned := stored(func(s *protocol.RunSpec) { s.ConfigSnapshot, s.Skills = snapshot, []string{"openspec"} })

	// The same content on the node: the run starts with the verified copy.
	sameNode, _ := skillRegistry(t)
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ := runDriver(t, url, Worker{Driver: d, Skills: sameNode}, false, pinned)
	if l := d.Launches(); r.Status != "COMPLETED" || len(l) != 1 || len(l[0].Skills) != 1 || l[0].Skills[0].Hash != hash {
		t.Fatalf("%s %q: %+v", r.Status, r.Result, l)
	}

	// The node's copy was edited: the run fails before the runtime starts, naming the pinned hash.
	staleNode, _ := skillRegistry(t)
	dir, _ := staleNode.Dir("openspec")
	if err = os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("edited on the node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d = &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "ok"}}
	r, _ = runDriver(t, url, Worker{Driver: d, Skills: staleNode}, false, pinned)
	if r.Status != "FAILED" || !strings.Contains(r.Result, "differs from the run's pinned "+hash) || !strings.Contains(r.Result, "refused before runtime startup") || len(d.Launches()) != 0 {
		t.Fatalf("%s %q, %d launches", r.Status, r.Result, len(d.Launches()))
	}
}
