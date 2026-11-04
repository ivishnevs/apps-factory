package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// eventsCore is Core's events route in memory: in-order sequences per run, a gap refused, another lease refused.
type eventsCore struct {
	mu      sync.Mutex
	down    bool
	lease   string
	applied map[string][]storage.SeqEvent
	posts   int
}

func (c *eventsCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.posts++
	if c.down {
		http.Error(w, "unavailable", 503)
		return
	}
	var b struct {
		Lease  string             `json:"lease"`
		Events []storage.SeqEvent `json:"events"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	run := filepath.Base(filepath.Dir(r.URL.Path))
	w.Header().Set("Content-Type", "application/json")
	if b.Lease != c.lease {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]string{"reason": "lease"})
		return
	}
	have := int64(len(c.applied[run]))
	for _, e := range b.Events {
		if e.Seq <= have {
			continue
		}
		if e.Seq != have+1 {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"reason": "gap", "expected_seq": have + 1})
			return
		}
		c.applied[run] = append(c.applied[run], e)
		have = e.Seq
	}
	_ = json.NewEncoder(w).Encode(map[string]int64{"applied_seq": have})
}

func (c *eventsCore) seqs(run string) []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []int64
	for _, e := range c.applied[run] {
		out = append(out, e.Seq)
	}
	return out
}

func event(run, lease string, terminal bool) protocol.Envelope {
	p := protocol.Event{RunID: run, Lease: lease}
	if terminal {
		p.Status, p.Outcome = "ENDED", "succeeded"
	}
	return protocol.Wrap("agent.progress", run, "", p)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestEventsSpool (http-node-transport 4.2): events are spooled and sent in order; while Core is down they wait
// and the run goes on; once Core is back every event arrives in order and the spool is gone after the terminal
// event; a spool left by a previous process is resumed; another lease drops the spool.
func TestEventsSpool(t *testing.T) {
	core := &eventsCore{lease: "L", applied: map[string][]storage.SeqEvent{}}
	srv := httptest.NewServer(core)
	defer srv.Close()
	dir := t.TempDir()
	ev := &Events{API: API{URL: srv.URL}, Dir: dir, SendTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// In order while Core is up.
	for i := 0; i < 3; i++ {
		if err := ev.Append(ctx, "r1", "L", event("r1", "L", false), false); err != nil {
			t.Fatal(err)
		}
	}
	if got := core.seqs("r1"); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("applied %v", got)
	}
	spool := filepath.Join(dir, "r1", "events.jsonl")
	if _, err := os.Stat(spool); err != nil {
		t.Fatal("no spool before the terminal event", err)
	}

	// Core down: events wait, Append still succeeds and does not retry on every event.
	core.mu.Lock()
	core.down = true
	core.mu.Unlock()
	for i := 0; i < 3; i++ {
		if err := ev.Append(ctx, "r1", "L", event("r1", "L", i == 2), i == 2); err != nil {
			t.Fatal(err)
		}
	}
	core.mu.Lock()
	posts := core.posts
	core.down = false
	core.mu.Unlock()
	if posts > 4 { // 3 delivered + 1 failed attempt; the backoff holds the rest for Run
		t.Fatalf("%d posts while Core was down", posts)
	}
	go ev.Run(ctx, 50*time.Millisecond)
	waitFor(t, "spool delivered", func() bool { return len(core.seqs("r1")) == 6 })
	for i, s := range core.seqs("r1") {
		if s != int64(i+1) {
			t.Fatalf("out of order: %v", core.seqs("r1"))
		}
	}
	waitFor(t, "spool removed", func() bool { _, err := os.Stat(spool); return os.IsNotExist(err) && ev.Pending() == 0 })

	// A spool a previous process left is resumed and sent; Core skips what it already applied.
	core.mu.Lock()
	core.down = true
	core.mu.Unlock()
	if err := ev.Append(ctx, "r2", "L", event("r2", "L", false), false); err != nil {
		t.Fatal(err)
	}
	if err := ev.Append(ctx, "r2", "L", event("r2", "L", true), true); err != nil {
		t.Fatal(err)
	}
	cancel()
	core.mu.Lock()
	core.down = false
	core.mu.Unlock()
	next := &Events{API: API{URL: srv.URL}, Dir: dir, SendTimeout: time.Second}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go next.Run(ctx2, 50*time.Millisecond)
	waitFor(t, "resumed spool delivered", func() bool { return len(core.seqs("r2")) == 2 })
	waitFor(t, "resumed spool removed", func() bool { return next.Pending() == 0 })

	// Another lease: the run is no longer this node's, its spool is dropped.
	if err := next.Append(ctx2, "r3", "other", event("r3", "other", false), false); err != nil {
		t.Fatal(err)
	}
	if next.Pending() != 0 {
		t.Fatal("a refused spool is kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "r3", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatal("refused spool file kept", err)
	}
}

// TestEventsGap: a gap answer resends from the sequence Core expects.
func TestEventsGap(t *testing.T) {
	core := &eventsCore{lease: "L", applied: map[string][]storage.SeqEvent{}}
	srv := httptest.NewServer(core)
	defer srv.Close()
	ev := &Events{API: API{URL: srv.URL}}
	ctx := context.Background()
	if err := ev.Append(ctx, "g", "L", event("g", "L", false), false); err != nil {
		t.Fatal(err)
	}
	// Core lost what it applied (e.g. restored): it expects 1 again, the spool still has 1.
	core.mu.Lock()
	core.applied["g"] = nil
	core.mu.Unlock()
	r, _ := ev.open("g", "L")
	r.mu.Lock()
	r.pending = append([]storage.SeqEvent{{Seq: 1, Event: event("g", "L", false)}}, r.pending...)
	r.mu.Unlock()
	if err := ev.Append(ctx, "g", "L", event("g", "L", true), true); err != nil {
		t.Fatal(err)
	}
	if got := core.seqs("g"); len(got) != 2 || got[1] != 2 {
		t.Fatalf("applied %v", got)
	}
	if ev.Pending() != 0 {
		t.Fatal("spool kept after the terminal event")
	}
}
