package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"github.com/nats-io/nats.go"
)

// fakeCore serves the node endpoints of runtime protocol v2 (docs/runtime-protocol-v2.md) and fails
// the test on any other request, per-run heartbeats included.
type fakeCore struct {
	t           *testing.T
	stateDir    string
	workspace   string
	mu          sync.Mutex
	incarnation string
	epoch       int64 // last registered
	registers   []Register
	diskEpochs  []string // <state_dir>/epoch when each register arrived
	beats       []Heartbeat
	answers     []HeartbeatAnswer // next heartbeat answers, in order
	claims      []worker.ClaimRequest
	byRun       map[string][]worker.ClaimRequest
	claimed     map[string]worker.Claim // by (node_id, epoch, claim_id)
	refuse      map[string][]worker.Refusal
	unavailable int // claims answered 503 before any other answer
	starts      map[string]string
	ended       map[string]bool // runs whose terminal event "Core applied"
}

func newFakeCore(t *testing.T, stateDir string) (*fakeCore, worker.API) {
	f := &fakeCore{t: t, stateDir: stateDir, workspace: t.TempDir(), incarnation: "inc-1", claimed: map[string]worker.Claim{}, byRun: map[string][]worker.ClaimRequest{},
		refuse: map[string][]worker.Refusal{}, starts: map[string]string{}, ended: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /nodes/register", f.register)
	mux.HandleFunc("POST /nodes/{id}/heartbeat", f.heartbeat)
	mux.HandleFunc("POST /runs/{id}/claim", f.claim)
	mux.HandleFunc("POST /runs/{id}/start", f.start)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return f, worker.API{URL: s.URL, Token: "test-token"}
}

func answer(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeCore) decode(r *http.Request, v any) {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields() // as Core
	if err := d.Decode(v); err != nil {
		f.t.Errorf("%s: %v", r.URL.Path, err)
	}
}

func (f *fakeCore) register(w http.ResponseWriter, r *http.Request) {
	var req Register
	f.decode(r, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Epoch <= f.epoch {
		answer(w, 409, map[string]string{"reason": "stale_epoch"})
		return
	}
	b, _ := os.ReadFile(filepath.Join(f.stateDir, "epoch"))
	f.registers, f.diskEpochs, f.epoch = append(f.registers, req), append(f.diskEpochs, strings.TrimSpace(string(b))), req.Epoch
	answer(w, 200, Registered{Incarnation: f.incarnation, HeartbeatSeconds: 1, StartWindowSeconds: 60})
}

func (f *fakeCore) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req Heartbeat
	f.decode(r, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Epoch != f.epoch {
		f.t.Errorf("heartbeat of epoch %d, registered %d", req.Epoch, f.epoch)
	}
	f.beats = append(f.beats, req)
	var a HeartbeatAnswer
	if len(f.answers) > 0 {
		a, f.answers = f.answers[0], f.answers[1:]
	}
	for _, x := range req.Inventory {
		if x.State == node.RunEnded && f.ended[x.RunID] {
			a.Acked = append(a.Acked, x.RunID)
		}
	}
	a.Incarnation = f.incarnation
	answer(w, 200, a)
}

func (f *fakeCore) claim(w http.ResponseWriter, r *http.Request) {
	run := r.PathValue("id")
	var req worker.ClaimRequest
	f.decode(r, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims, f.byRun[run] = append(f.claims, req), append(f.byRun[run], req)
	key := fmt.Sprint(req.NodeID, "/", req.Epoch, "/", req.ClaimID)
	if f.unavailable > 0 {
		f.unavailable--
		answer(w, 503, map[string]string{"error": "busy"})
		return
	}
	if c, ok := f.claimed[key]; ok { // a retried claim gets the same answer
		answer(w, 200, c)
		return
	}
	if rs := f.refuse[run]; len(rs) > 0 {
		f.refuse[run] = rs[1:]
		answer(w, 409, rs[0])
		return
	}
	if req.Epoch != f.epoch {
		answer(w, 409, worker.Refusal{Drop: true, Reason: "old_epoch"})
		return
	}
	ws := filepath.Join(f.workspace, run) // one project per run: the Local allocator admits one writer
	if err := os.MkdirAll(ws, 0o755); err != nil {
		f.t.Error(err)
	}
	c := worker.Claim{Lease: "lease-" + run, StartBy: time.Now().Add(time.Minute), Brief: json.RawMessage(`{"from":1,"to":2}`),
		RunSpec: protocol.RunSpec{ID: run, Project: "p-" + run, Agent: "a-" + run, Provider: protocol.RuntimeCodex, Workspace: ws, Prompt: "work", TimeoutSeconds: 60}}
	f.claimed[key] = c
	answer(w, 200, c)
}

func (f *fakeCore) start(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lease string `json:"lease"`
	}
	f.decode(r, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts[r.PathValue("id")] = req.Lease
	answer(w, 200, map[string]string{"status": "RUNNING"})
}

func (f *fakeCore) lastBeat() Heartbeat {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.beats) == 0 {
		return Heartbeat{}
	}
	return f.beats[len(f.beats)-1]
}

func (f *fakeCore) inventory() map[string]node.RunState {
	r := map[string]node.RunState{}
	for _, x := range f.lastBeat().Inventory {
		r[x.RunID] = x.State
	}
	return r
}

func (f *fakeCore) claimsOf(run string) []worker.ClaimRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]worker.ClaimRequest(nil), f.byRun[run]...)
}

func (f *fakeCore) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func v2Node(t *testing.T, api worker.API, stateDir string) Node {
	c := node.Defaults()
	c.Name, c.Slots, c.StateDir, c.ProtocolVersion = "mini-1", 2, stateDir, 2
	c.Disk.Path = stateDir
	c.Resources, c.Labels = map[string]int{"simulator": 2}, []string{"macos"}
	return Node{Config: c, API: api, Drivers: []driver.Driver{&fake.Driver{}},
		Host: node.Host{FreeBytes: func(string) (uint64, error) { return 10 * node.GiB, nil }, Load1: func() (float64, error) { return 1.5, nil }}}
}

// TestV2RegisterIdentity: every start registers the stable node_id with the next epoch, which is on disk
// before register; a lower epoch than Core's is refused as stale.
func TestV2RegisterIdentity(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx := context.Background()
	for range 2 {
		if _, err := newSession(ctx, v2Node(t, api, dir), nil); err != nil {
			t.Fatal(err)
		}
	}
	a, b := f.registers[0], f.registers[1]
	if a.NodeID == "" || a.NodeID != b.NodeID || a.Epoch != 1 || b.Epoch != 2 || f.diskEpochs[0] != "1" || f.diskEpochs[1] != "2" {
		t.Fatalf("%+v %+v %v", a, b, f.diskEpochs)
	}
	if a.Name != "mini-1" || a.ProtocolVersion != 2 || a.Slots != 2 || a.Resources["simulator"] != 2 || len(a.Labels) != 1 ||
		len(a.Drivers) != 1 || a.Drivers[0] != (DriverInfo{Name: "codex", Version: "fake", OK: true}) {
		t.Fatalf("%+v", a)
	}
	f.epoch = 10 // Core saw a newer epoch of this node_id
	if _, err := newSession(ctx, v2Node(t, api, dir), nil); !errors.Is(err, ErrStaleEpoch) {
		t.Fatal(err)
	}
	failing := v2Node(t, api, dir)
	failing.Drivers = []driver.Driver{&fake.Driver{Checks: []node.CheckResult{{Check: "codex.auth", Status: node.Fail}}}}
	f.epoch = 0
	if _, err := newSession(ctx, failing, nil); err != nil {
		t.Fatal(err)
	}
	if d := f.registers[len(f.registers)-1].Drivers[0]; d.OK {
		t.Fatalf("failed probe reported ok: %+v", d)
	}
}

// TestV2Heartbeat: the inventory and health are reported; stop, acked, pause and cleanup are applied; a
// changed incarnation stops every run and re-registers a new epoch that reports nothing older.
func TestV2Heartbeat(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx := context.Background()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.busy = func() int { return 2 }
	inv := s.inv
	stopped := map[string]bool{}
	var mu sync.Mutex
	for _, run := range []string{"r-claimed", "r-running", "r-done"} {
		inv.Add(run, 1, func() { mu.Lock(); stopped[run] = true; mu.Unlock(); inv.End(run) })
	}
	inv.Set("r-running", node.RunRunning)
	inv.End("r-done")
	f.ended["r-done"] = true
	f.answers = []HeartbeatAnswer{{Pause: true, Cleanup: []string{"task-1"}, Stop: []struct {
		RunID  string `json:"run_id"`
		Reason string `json:"reason"`
	}{{RunID: "r-running", Reason: "revoked"}, {RunID: "unknown", Reason: "unknown_run"}}}}
	if err = s.beat(ctx); err != nil {
		t.Fatal(err)
	}
	hb := f.lastBeat()
	if hb.Epoch != 1 || len(hb.Inventory) != 3 || hb.Inventory[0].RunID != "r-claimed" || hb.Inventory[0].State != node.RunClaimed ||
		hb.Inventory[1].State != node.RunEnded || hb.Inventory[2].State != node.RunRunning {
		t.Fatalf("%+v", hb)
	}
	if h := hb.Health; h.Load1 != 1.5 || h.CPUs < 1 || h.FreeDiskBytes != 10*node.GiB || h.BusySlots != 2 || h.Disk != "low" {
		t.Fatalf("%+v", h)
	}
	if !stopped["r-running"] || stopped["r-claimed"] || s.claimable() || <-s.cleanups != "task-1" {
		t.Fatalf("stop %v claimable %v", stopped, s.claimable())
	}
	if snap := inv.Snapshot(); len(snap) != 2 { // r-done acked
		t.Fatalf("%+v", snap)
	}
	f.answers = []HeartbeatAnswer{{Pause: false}}
	if err = s.beat(ctx); err != nil || !s.claimable() {
		t.Fatal("pause not lifted", err)
	}
	f.mu.Lock()
	f.incarnation = "inc-2" // database restored
	f.mu.Unlock()
	if err = s.beat(ctx); err != nil {
		t.Fatal(err)
	}
	if !stopped["r-claimed"] || inv.Epoch() != 2 || len(inv.Snapshot()) != 0 || s.incarnation != "inc-2" || f.epoch != 2 || !s.claimable() {
		t.Fatalf("stopped %v epoch %d inventory %v", stopped, inv.Epoch(), inv.Snapshot())
	}
	if _, id := inv.Claim(0); id != "2-0-1" {
		t.Fatal(id)
	}
	if err = s.beat(ctx); err != nil || f.lastBeat().Epoch != 2 {
		t.Fatal(err, f.lastBeat())
	}
}

// TestV2ReregisterStopsLateClaim: a claim of the old epoch recorded after re-registering began (the slot
// was between its claim answer and Inventory.Add) is stopped too, instead of blocking the re-register
// until that run ends on its own.
func TestV2ReregisterStopsLateClaim(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	inv := s.inv
	// r-early ends only after r-late is recorded, so r-late is added after StopAll.
	release := make(chan struct{})
	inv.Add("r-early", 1, func() {
		go func() {
			<-release
			inv.End("r-early")
		}()
	})
	f.set(func() { f.incarnation = "inc-2" })
	done := make(chan error, 1)
	go func() { done <- s.beat(ctx) }()
	eventually(t, "re-register started", func() bool { return s.switching.Load() })
	if inv.Add("r-late", 1, func() {}) {
		t.Error("run of the closing epoch recorded during re-register; StopAll missed it")
	}
	close(release)
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("re-register blocked")
	}
	if err != nil || inv.Epoch() != 2 || !inv.Add("r-new", 2, func() {}) {
		t.Fatal(err, inv.Epoch())
	}
}

// TestV2HeartbeatRefusals: 409 stops the node, 404 re-registers.
func TestV2HeartbeatRefusals(t *testing.T) {
	dir := t.TempDir()
	status := 409
	var registers int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /nodes/register", func(w http.ResponseWriter, r *http.Request) {
		registers++
		answer(w, 200, Registered{Incarnation: "i", HeartbeatSeconds: 1})
	})
	mux.HandleFunc("POST /nodes/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		answer(w, status, map[string]string{"reason": "stale_epoch"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()
	s, err := newSession(ctx, v2Node(t, worker.API{URL: srv.URL, Token: "t"}, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.beat(ctx); !errors.Is(err, ErrStaleEpoch) {
		t.Fatal(err)
	}
	status = 404
	if err = s.beat(ctx); err != nil || registers != 2 || s.inv.Epoch() != 2 {
		t.Fatal(err, registers, s.inv.Epoch())
	}
}

// TestV2Claim: 200 claims with a lease and the RunSpec, 409 retry and drop are refusals, and a transient
// failure is retried with the same claim_id while every new attempt gets a new one.
func TestV2Claim(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx := context.Background()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{API: api, Inventory: s.inv}
	f.unavailable = 1
	epoch, c, err := w.ClaimV2(ctx, "run-1", 1)
	if err != nil || epoch != 1 || c.Lease != "lease-run-1" || c.RunSpec.ID != "run-1" || c.StartBy.IsZero() {
		t.Fatal(err, c)
	}
	if len(f.claims) != 2 || f.claims[0] != f.claims[1] || f.claims[0] != (worker.ClaimRequest{NodeID: s.inv.NodeID, Epoch: 1, ClaimID: "1-1-1", Slot: 1}) {
		t.Fatalf("retry changed the claim: %+v", f.claims)
	}
	f.refuse["run-2"] = []worker.Refusal{{Retry: true, Reason: "capacity"}, {Drop: true, Reason: "stale"}}
	var r *worker.Refusal
	if _, _, err = w.ClaimV2(ctx, "run-2", 0); !errors.As(err, &r) || !r.Retry || r.Reason != "capacity" {
		t.Fatal(err)
	}
	if _, _, err = w.ClaimV2(ctx, "run-2", 0); !errors.As(err, &r) || !r.Drop || r.Reason != "stale" {
		t.Fatal(err)
	}
	if ids := []string{f.claims[2].ClaimID, f.claims[3].ClaimID}; ids[0] != "1-0-2" || ids[1] != "1-0-3" {
		t.Fatal(ids)
	}
}

// TestJetStreamNodeV2 runs a v2 node against the fake Core: pointer and RunSpec commands are claimed,
// started and reported running; a retry refusal is redelivered and claimed with a new claim_id, a drop
// is acknowledged; a stop ends a run, which leaves the inventory once Core acknowledged its end; a new
// incarnation stops everything and re-registers epoch 2, whose claims carry the new epoch.
func TestJetStreamNodeV2(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	delay := worker.RetryDelay
	worker.RetryDelay = 100 * time.Millisecond
	defer func() { worker.RetryDelay = delay }()
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	events, err := b.Subscribe("factory.events.>", "fake-core-v2")
	if err != nil {
		t.Fatal(err)
	}
	go func() { // Core applies terminal events; the next heartbeat answer acknowledges them
		for ctx.Err() == nil {
			msgs, err := events.Fetch(1, nats.MaxWait(200*time.Millisecond))
			if err != nil {
				continue
			}
			var e protocol.Envelope
			var p protocol.Event
			if json.Unmarshal(msgs[0].Data, &e) == nil && json.Unmarshal(e.Payload, &p) == nil && p.Status != "" {
				f.set(func() { f.ended[p.RunID] = true })
			}
			_ = msgs[0].AckSync()
		}
	}()
	publish := func(run string, payload any) {
		body, _ := json.Marshal(protocol.Wrap("agent.run.requested", run, "", payload))
		if err := b.Publish(ctx, "factory.commands.agent.run.codex", "cmd-"+run, body); err != nil {
			t.Fatal(err)
		}
	}
	pointer := func(run string) map[string]string {
		return map[string]string{"run_id": run, "company": "default", "agent": "a-" + run, "provider": "codex"}
	}
	f.set(func() {
		f.refuse["v2-b"] = []worker.Refusal{{Retry: true, Reason: "capacity"}}
		f.refuse["v2-c"] = []worker.Refusal{{Drop: true, Reason: "stale"}}
	})
	publish("v2-a", pointer("v2-a"))
	publish("v2-b", protocol.RunSpec{ID: "v2-b", Project: "ignored", Provider: protocol.RuntimeCodex, Prompt: "ignored", TimeoutSeconds: 1})
	publish("v2-c", pointer("v2-c"))
	n := v2Node(t, api, dir)
	n.Bus, n.FetchWait, n.Config.Slots = b, 200*time.Millisecond, 3
	n.Drivers = []driver.Driver{perRunSessions{&fake.Driver{}}}
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Run(nodeCtx, n) }()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case err = <-done:
				t.Fatal("node stopped", err)
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what, f.lastBeat())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	wait("both runs running", func() bool {
		inv := f.inventory()
		return inv["v2-a"] == node.RunRunning && inv["v2-b"] == node.RunRunning
	})
	f.set(func() {
		if f.starts["v2-a"] != "lease-v2-a" || f.starts["v2-b"] != "lease-v2-b" {
			t.Errorf("starts %v", f.starts)
		}
	})
	wait("dropped command", func() bool { return len(f.claimsOf("v2-c")) > 0 })
	time.Sleep(300 * time.Millisecond) // an unacknowledged command would come back
	if c := f.claimsOf("v2-c"); len(c) != 1 {
		t.Fatalf("dropped command claimed %d times", len(c))
	}
	if c := f.claimsOf("v2-b"); len(c) != 2 || c[0].ClaimID == c[1].ClaimID || c[0].Epoch != 1 {
		t.Fatalf("retried command claims %+v", c)
	}
	f.set(func() {
		f.answers = []HeartbeatAnswer{{Stop: []struct {
			RunID  string `json:"run_id"`
			Reason string `json:"reason"`
		}{{RunID: "v2-a", Reason: "revoked"}}}}
	})
	wait("stopped run acknowledged", func() bool {
		inv := f.inventory()
		_, listed := inv["v2-a"]
		return !listed && inv["v2-b"] == node.RunRunning
	})
	f.set(func() { f.incarnation = "inc-2" })
	wait("re-registered", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.epoch == 2
	})
	wait("epoch 2 inventory", func() bool { hb := f.lastBeat(); return hb.Epoch == 2 && len(hb.Inventory) == 0 })
	f.set(func() {
		if !f.ended["v2-b"] {
			t.Error("v2-b not ended by the incarnation change")
		}
	})
	publish("v2-d", pointer("v2-d"))
	wait("epoch 2 run", func() bool { return f.inventory()["v2-d"] == node.RunRunning })
	if c := f.claimsOf("v2-d"); len(c) != 1 || c[0].Epoch != 2 || !strings.HasPrefix(c[0].ClaimID, "2-") {
		t.Fatalf("%+v", c)
	}
	stopNode()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

// TestV2Reset: a reset listed by Core is done by the node and confirmed in the next heartbeat, once.
func TestV2Reset(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var reset []string
	s.reset = func(_ context.Context, task string) error {
		mu.Lock()
		reset = append(reset, task)
		mu.Unlock()
		return nil
	}
	go s.resetter(ctx)
	f.answers = []HeartbeatAnswer{{Reset: []string{"t1"}}}
	if err = s.beat(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reset done", func() bool { s.resetMu.Lock(); defer s.resetMu.Unlock(); return len(s.resetDone) == 1 })
	if err = s.beat(ctx); err != nil || len(f.lastBeat().ResetDone) != 1 || f.lastBeat().ResetDone[0] != "t1" {
		t.Fatal(err, f.lastBeat())
	}
	if err = s.beat(ctx); err != nil || len(f.lastBeat().ResetDone) != 0 {
		t.Fatal("confirmation sent twice", f.lastBeat())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reset) != 1 {
		t.Fatal(reset)
	}
}
