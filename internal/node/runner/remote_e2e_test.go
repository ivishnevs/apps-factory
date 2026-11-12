package runner

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestSettingsFromTheWebRealCore (node-packaging-updates B, C, 3.6) runs a node against the real Core: it registers
// its version and detected labels; the owner's settings reach it through the poll, it applies them live (slots
// capped at slots_max, labels refused by a node.yaml pin, the disk watermarks) and Core shows each setting's state;
// the bigger pool runs two runs at once, a smaller one lets a running run finish and takes no new run beyond it; a
// restarted node starts with the settings from remote-config.json over node.yaml.
func TestSettingsFromTheWebRealCore(t *testing.T) {
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go c.Run(ctx)
	co := storage.DefaultCompany
	create := func(id string) {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 120}
		if err := s.Create(ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id string) string {
		r, _ := s.Get(ctx, co, id)
		return r.Status
	}
	cfg := node.Defaults()
	cfg.Slots, cfg.ConversationSlots, cfg.SlotsMax, cfg.StateDir = 1, 0, 2, t.TempDir()
	cfg.Remote = node.Remote{Pins: map[string]bool{"labels": false}}
	api := enrolled(t, s, cfg.StateDir, server.URL)
	start := func() (context.CancelFunc, chan error) {
		nodeCtx, stopNode := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{&fake.Driver{}}}, Detected: []string{"linux", "headless"}, Version: "2026.11.12.1500"})
		}()
		return stopNode, done
	}
	stopNode, done := start()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case err := <-done:
				t.Fatal("node stopped", err)
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	nodeOf := func() storage.Node {
		ns, _ := s.Nodes(ctx, co)
		if len(ns) != 1 {
			return storage.Node{}
		}
		return ns[0]
	}
	wait("register", func() bool { return nodeOf().Epoch == 1 })
	if n := nodeOf(); n.Version != "2026.11.12.1500" || !slices.Equal(n.DetectedLabels, []string{"linux", "headless"}) || n.Slots != 1 {
		t.Fatalf("%+v", n)
	}
	id := nodeOf().NodeID
	slots, conv, low, critical := 3, 1, 30.0, 2.0
	labels := []string{"gpu"}
	if _, err = s.SetNodeConfig(ctx, co, id, protocol.NodeSettings{Slots: &slots, ConversationSlots: &conv, Labels: &labels,
		Disk: &protocol.NodeDiskSettings{LowGiB: &low, CriticalGiB: &critical}}, 0, "owner", ""); err != nil {
		t.Fatal(err)
	}
	wait("version 1 applied", func() bool {
		n := nodeOf()
		return n.Config.Report != nil && n.Config.Report.Version == 1 && n.Slots == 2
	})
	n := nodeOf()
	if n.ConversationSlots != 1 || n.Config.States["slots"].Reason != "capped at slots_max 2" || n.Config.States["labels"].State != protocol.SettingRefused ||
		n.Config.States["disk"].State != protocol.SettingApplied || len(n.Labels) != 0 || *n.Config.Report.Local.Slots != 1 {
		t.Fatalf("%+v %+v", n, n.Config)
	}
	if f, err := node.LoadRemote(cfg.StateDir); err != nil || f.Version != 1 || *f.Applied.Slots != 2 || f.Applied.Labels != nil {
		t.Fatalf("%+v %v", f, err)
	}
	// The pool grew without a restart: two runs at once.
	create("a")
	create("b")
	wait("two runs", func() bool { return status("a") == "RUNNING" && status("b") == "RUNNING" })
	if n := nodeOf(); n.Epoch != 1 {
		t.Fatal("re-registered", n.Epoch)
	}
	create("c")
	time.Sleep(1500 * time.Millisecond)
	if st := status("c"); st != "QUEUED" {
		t.Fatal("third run on two slots", st)
	}
	// Down to one slot: both runs go on, a freed slot above the new count takes nothing.
	one := 1
	if _, err = s.SetNodeConfig(ctx, co, id, protocol.NodeSettings{Slots: &one, ConversationSlots: &conv}, 1, "owner", ""); err != nil {
		t.Fatal(err)
	}
	wait("version 2 applied", func() bool {
		n := nodeOf()
		return n.Config.Report != nil && n.Config.Report.Version == 2 && n.Slots == 1
	})
	if status("a") != "RUNNING" || status("b") != "RUNNING" {
		t.Fatal("a resize stopped a run")
	}
	if err = s.Cancel(ctx, co, "a"); err != nil {
		t.Fatal(err)
	}
	wait("a ended", func() bool { return status("a") == "ENDED" })
	time.Sleep(1500 * time.Millisecond)
	if st := status("c"); st != "QUEUED" {
		t.Fatal("the removed slot took c", st)
	}
	if err = s.Cancel(ctx, co, "b"); err != nil {
		t.Fatal(err)
	}
	wait("c runs in the one slot", func() bool { return status("c") == "RUNNING" })
	stopNode()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	// A restart reads remote-config.json over node.yaml (no conversation slot there): it registers with one slot and
	// the conversation slot.
	stopNode, done = start()
	wait("epoch 2", func() bool { n := nodeOf(); return n.Epoch == 2 && n.Online })
	if n := nodeOf(); n.Slots != 1 || n.ConversationSlots != 1 || n.Config.States["slots"].State != protocol.SettingApplied {
		t.Fatalf("%+v %+v", n, n.Config)
	}
	stopNode()
	<-done
}
