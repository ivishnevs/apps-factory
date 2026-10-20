package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"appfactory/internal/workspace"
)

// Runtime protocol v2, node side (docs/runtime-protocol-v2.md §2, §3, §7): identity, register and the
// inventory heartbeat. Claims and starts are the executor's (worker.Worker.Inventory).

// Register is the body of POST /nodes/register.
type Register struct {
	NodeID          string         `json:"node_id"`
	Name            string         `json:"name"`
	Epoch           int64          `json:"epoch"`
	ProtocolVersion int            `json:"protocol_version"`
	Slots           int            `json:"slots"`
	Drivers         []DriverInfo   `json:"drivers"`
	Resources       map[string]int `json:"resources"`
	Labels          []string       `json:"labels"`
	// Worktrees: worktree tasks run in their own worktree here, so Core lets tasks of one project run in
	// parallel on this node (runtime-protocol-v2 §11).
	Worktrees bool `json:"worktrees,omitempty"`
	// Hooks names the catalog's NodeHooks document (node.yaml hooks; empty is default).
	Hooks string `json:"hooks,omitempty"`
	// Reason: start (process start, counted for node.restart_loop) or reregister (Core lost the node or its
	// incarnation changed). Driver changes ride the heartbeat's drivers and never re-register.
	Reason string `json:"reason,omitempty"`
}

// DriverInfo is a driver as last checked: Version from CheckVersion, OK when no check failed; Check names the
// failing check (version, probe, auth) and Detail says why (Core's driver_unsupported event).
type DriverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Check   string `json:"check,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type Registered struct {
	Incarnation string `json:"incarnation"`
	// Company is the company of the node's token: the node's subjects are factory.<company>....
	Company            string `json:"company,omitempty"`
	HeartbeatSeconds   int    `json:"heartbeat_seconds"`
	StartWindowSeconds int    `json:"start_window_seconds"`
	// Hooks is the catalog's NodeHooks document for this node.
	Hooks *protocol.NodeHooksDelivery `json:"hooks,omitempty"`
}

// Heartbeat is the body of POST /nodes/{node_id}/heartbeat.
type Heartbeat struct {
	Epoch     int64               `json:"epoch"`
	Inventory []node.InventoryRun `json:"inventory"`
	Health    Health              `json:"health"`
	// ResetDone confirms the worktree resets Core asked for (answer reset).
	ResetDone []string `json:"reset_done,omitempty"`
	// Drivers is the current driver list: a driver that failed or passed its checks again reaches Core with
	// the next heartbeat, without a new epoch (runtime-drivers D4).
	Drivers []DriverInfo `json:"drivers"`
	// Hooks and HooksHash: the NodeHooks document this node uses and the hash it applied; Core answers hooks
	// when the document changed.
	Hooks     string `json:"hooks,omitempty"`
	HooksHash string `json:"hooks_hash,omitempty"`
}

type Health struct {
	Load1         float64 `json:"load1"`
	CPUs          int     `json:"cpus"`
	FreeDiskBytes uint64  `json:"free_disk_bytes"`
	BusySlots     int     `json:"busy_slots"`
	Disk          string  `json:"disk,omitempty"` // ok, low or critical; omitted when free space is unreadable
	// Checks are the failing node self-checks (node.check_failed{check} on Core); [] when none fails. Driver
	// probes are reported in the heartbeat's drivers instead (driver_unsupported).
	Checks []node.CheckResult `json:"checks"`
	// StalledSeconds: no heartbeat succeeded for that long (more than StallReport); sent until one is delivered.
	StalledSeconds int64 `json:"stalled_seconds,omitempty"`
}

// StallReport is how long heartbeats must fail before the node reports node.heartbeat_stalled (node-agent D7).
var StallReport = 5 * time.Minute

type HeartbeatAnswer struct {
	Incarnation string `json:"incarnation"`
	Stop        []struct {
		RunID  string `json:"run_id"`
		Reason string `json:"reason"`
	} `json:"stop"`
	Acked   []string `json:"acked"`
	Cleanup []string `json:"cleanup"`
	Pause   bool     `json:"pause"`
	// Reset: tasks whose worktree must be reset from the remote branch (workspace_diverged, reset_workspace).
	Reset []string `json:"reset"`
	// Hooks: the NodeHooks document changed since the hash the node sent.
	Hooks *protocol.NodeHooksDelivery `json:"hooks,omitempty"`
}

// ErrStaleEpoch: Core knows a newer epoch of this node_id, so another process uses this state_dir's identity.
var ErrStaleEpoch = errors.New("stale epoch: another node process registered this node_id with a newer epoch")

const defaultHeartbeat = 10 * time.Second

// session is a registered v2 node.
type session struct {
	api      worker.API
	cfg      node.Config
	host     node.Host
	gate     *node.DiskGate // watermarks for health.disk
	trees    *workspace.Worktrees
	cleanup  func(ctx context.Context, tasks []string) []string // node janitor, when set
	drivers  *driverHealth
	inv      *node.Inventory
	busy     func() int
	interval time.Duration
	retryMax time.Duration // longest pause between register attempts

	incarnation string
	company     string      // from the register answer; empty from a Core that does not send it
	paused      atomic.Bool // Core's pause
	switching   atomic.Bool // re-registering: no claims
	// kick asks for a heartbeat now: a driver was disabled or re-enabled (the heartbeat carries the drivers).
	kick     chan struct{}
	cleanups chan string
	cleaned  map[string]bool // janitor's
	// reset resets a task's worktree from the remote branch (janitor.ResetWorkspace); resetDone holds the
	// confirmations until a heartbeat delivered them.
	reset     func(ctx context.Context, task string) error
	resets    chan string
	resetMu   sync.Mutex
	resetDone []string
	// resetting: tasks queued, being reset, or reset with the confirmation not yet delivered; Core lists
	// a reset again until it gets the confirmation, and those listings must not reset the worktree again.
	resetting map[string]bool

	// hooks is the last NodeHooks document Core delivered (register, heartbeat); onHooks applies it.
	hooks   *protocol.NodeHooksDelivery
	onHooks func(protocol.NodeHooksDelivery)
	// checks returns the failing node self-checks for health.checks (git, git.user, forge.auth, ps, stack.*);
	// nil reports none. Driver probes are not among them: drivers carries them.
	checks func() []node.CheckResult
	// lastBeat is the last delivered heartbeat (or register); a longer gap than StallReport is reported once.
	lastBeat time.Time
}

// newSession seals what the previous epoch left, reads the identity, takes the next epoch and registers.
func newSession(ctx context.Context, n Node, trees *workspace.Worktrees) (*session, error) {
	c := n.Config
	if trees != nil {
		if err := trees.SealLeftovers(ctx); err != nil {
			slog.Error("sealing worktrees of the previous epoch", "error", err)
		}
	}
	id, err := node.NodeID(c.StateDir)
	if err != nil {
		return nil, err
	}
	epoch, err := node.NextEpoch(c.StateDir)
	if err != nil {
		return nil, err
	}
	h := n.health
	if h == nil {
		h = newDriverHealth(n.Drivers, c.Probes)
		h.check(ctx, true)
	}
	s := &session{api: n.API, cfg: c, host: n.Host, gate: node.NewDiskGate(n.Host, c.Disk), trees: trees, inv: node.NewInventory(id, epoch),
		drivers: h, busy: func() int { return 0 }, retryMax: 30 * time.Second, kick: make(chan struct{}, 1), cleanups: make(chan string, 64), cleaned: map[string]bool{}, resets: make(chan string, 64), resetting: map[string]bool{}}
	if err = s.register(ctx, "start"); err != nil {
		return nil, err
	}
	return s, nil
}

// register registers the current epoch with the drivers as last checked, retrying while Core is
// unreachable; a refusal is final.
func (s *session) register(ctx context.Context, reason string) error {
	req := Register{NodeID: s.inv.NodeID, Name: s.cfg.Name, Epoch: s.inv.Epoch(), ProtocolVersion: 2, Slots: s.cfg.Slots,
		Drivers: s.drivers.infos(), Resources: s.cfg.Resources, Labels: s.cfg.Labels, Worktrees: s.trees != nil, Hooks: s.cfg.Hooks, Reason: reason}
	if req.Resources == nil {
		req.Resources = map[string]int{}
	}
	if req.Labels == nil {
		req.Labels = []string{}
	}
	pause := time.Second
	for {
		var r Registered
		err := s.api.Do(ctx, "POST", "/nodes/register", req, &r)
		var h *worker.HTTPError
		switch {
		case err == nil:
			s.incarnation, s.interval = r.Incarnation, time.Duration(r.HeartbeatSeconds)*time.Second
			if r.Company != "" {
				s.company = r.Company
			}
			if s.interval <= 0 {
				s.interval = defaultHeartbeat
			}
			if s.lastBeat.IsZero() { // a re-register keeps a stall for the next heartbeat to report
				s.lastBeat = time.Now()
			}
			if r.Hooks != nil {
				s.deliverHooks(*r.Hooks)
			}
			slog.Info("node registered", "node_id", req.NodeID, "name", req.Name, "epoch", req.Epoch, "incarnation", r.Incarnation, "heartbeat_seconds", r.HeartbeatSeconds)
			return nil
		case errors.As(err, &h) && h.Status == 409:
			return fmt.Errorf("register epoch %d: %w (%s)", req.Epoch, ErrStaleEpoch, h.Message)
		case errors.As(err, &h) && h.Status < 500:
			return fmt.Errorf("register: %w", err)
		}
		slog.Error("register failed; retrying", "error", err, "in", pause.String())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		pause = min(2*pause, s.retryMax)
	}
}

// claimable: Core has not paused the node and it is not re-registering.
func (s *session) claimable() bool { return !s.paused.Load() && !s.switching.Load() }

// run sends the inventory heartbeat every interval until ctx ends; it fails when the node must stop.
func (s *session) run(ctx context.Context) error {
	go s.janitor(ctx)
	go s.resetter(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.kick:
		}
		if err := s.beat(ctx); err != nil {
			return err
		}
		t.Reset(s.interval)
	}
}

// deliverHooks keeps the NodeHooks document Core delivered and applies it when it changed.
func (s *session) deliverHooks(d protocol.NodeHooksDelivery) {
	if s.hooks != nil && s.hooks.Hash == d.Hash {
		return
	}
	s.hooks = &d
	slog.Info("NodeHooks document from Core", "name", d.Name, "hash", d.Hash, "missing", d.Missing, "on_run_stop", len(d.Spec.OnRunStop),
		"on_task_closed", len(d.Spec.OnTaskClosed), "prune", len(d.Spec.Prune), "checks", len(d.Spec.Checks))
	if s.onHooks != nil {
		s.onHooks(d)
	}
}

func (s *session) hooksHash() string {
	if s.hooks == nil {
		return ""
	}
	return s.hooks.Hash
}

func (s *session) health() Health {
	// Driver probes are not health checks: a failing driver reaches Core in drivers (driver_unsupported).
	h := Health{CPUs: runtime.NumCPU(), BusySlots: s.busy(), Checks: []node.CheckResult{}}
	if s.checks != nil {
		h.Checks = append(h.Checks, s.checks()...)
	}
	if gap := time.Since(s.lastBeat); !s.lastBeat.IsZero() && gap > StallReport {
		h.StalledSeconds = int64(gap.Seconds())
	}
	if s.host.Load1 != nil {
		if l, err := s.host.Load1(); err == nil {
			h.Load1 = l
		}
	}
	if s.host.FreeBytes != nil {
		if free, err := s.host.FreeBytes(s.cfg.Disk.Path); err == nil {
			h.FreeDiskBytes, h.Disk = free, s.gate.Level(free)
		}
	}
	return h
}

// beat sends one heartbeat and applies the answer.
func (s *session) beat(ctx context.Context) error {
	var a HeartbeatAnswer
	s.resetMu.Lock()
	done := append([]string(nil), s.resetDone...)
	s.resetMu.Unlock()
	health := s.health()
	if health.StalledSeconds > 0 {
		slog.Warn("heartbeat stalled; reporting it", "seconds", health.StalledSeconds)
	}
	err := s.api.Do(ctx, "POST", "/nodes/"+s.inv.NodeID+"/heartbeat", Heartbeat{Epoch: s.inv.Epoch(), Inventory: s.inv.Snapshot(), Health: health, ResetDone: done,
		Drivers: s.drivers.infos(), Hooks: s.cfg.Hooks, HooksHash: s.hooksHash()}, &a)
	if err == nil {
		s.lastBeat = time.Now()
	}
	var h *worker.HTTPError
	switch {
	case errors.As(err, &h) && h.Status == 409:
		return fmt.Errorf("heartbeat: %w (%s)", ErrStaleEpoch, h.Message)
	case errors.As(err, &h) && h.Status == 404:
		return s.reregister(ctx, "node unknown to Core")
	case err != nil:
		if ctx.Err() == nil {
			slog.Error("node heartbeat failed", "error", err)
		}
		return nil
	case a.Incarnation != "" && a.Incarnation != s.incarnation:
		return s.reregister(ctx, "Core incarnation changed")
	}
	if len(done) > 0 { // delivered
		s.resetMu.Lock()
		s.resetDone = s.resetDone[len(done):]
		for _, task := range done {
			delete(s.resetting, task)
		}
		s.resetMu.Unlock()
	}
	for _, task := range a.Reset {
		s.resetMu.Lock()
		if s.resetting[task] {
			s.resetMu.Unlock()
			continue // queued, running, or done and confirmed with the next heartbeat
		}
		select {
		case s.resets <- task:
			s.resetting[task] = true
		default: // full: Core lists it again
		}
		s.resetMu.Unlock()
	}
	if a.Hooks != nil {
		s.deliverHooks(*a.Hooks)
	}
	s.inv.Ack(a.Acked)
	for _, x := range a.Stop {
		if s.inv.Stop(x.RunID, x.Reason) {
			slog.Info("run stopped by Core", "run", x.RunID, "reason", x.Reason)
		}
	}
	if was := s.paused.Swap(a.Pause); was != a.Pause {
		slog.Info("claiming paused by Core", "paused", a.Pause)
	}
	for _, task := range a.Cleanup {
		select {
		case s.cleanups <- task:
		default: // full: Core lists it again
		}
	}
	return nil
}

// reregister stops and seals every run, then registers a new epoch that reports nothing older.
func (s *session) reregister(ctx context.Context, why string) error {
	slog.Warn("re-registering: stopping all runs", "reason", why, "epoch", s.inv.Epoch())
	// Close first: a slot between its claim answer and Inventory.Add must not record a run StopAll missed.
	s.inv.Close()
	s.switching.Store(true)
	defer s.switching.Store(false)
	s.inv.StopAll()
	for s.inv.Active() > 0 {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
	epoch, err := node.NextEpoch(s.cfg.StateDir)
	if err != nil {
		return err
	}
	s.inv.Reset(epoch)
	s.paused.Store(false)
	if err = s.register(ctx, "reregister"); ctx.Err() != nil {
		return nil
	}
	return err
}

// driversChanged asks for a heartbeat now: it carries the changed driver list to Core.
func (s *session) driversChanged() {
	select {
	case s.kick <- struct{}{}:
	default: // one is pending
	}
}

// resetter resets worktrees Core listed under reset, one at a time; a busy worktree is retried from the next
// listing. A task stays in resetting until its confirmation is delivered (beat), or is dropped from it
// when the reset failed, so Core's next listing retries it.
func (s *session) resetter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-s.resets:
			err := errors.New("no reset on this node")
			if s.reset != nil {
				err = s.reset(ctx, task)
			}
			s.resetMu.Lock()
			if err != nil {
				delete(s.resetting, task)
			} else {
				s.resetDone = append(s.resetDone, task)
			}
			s.resetMu.Unlock()
			if err != nil {
				slog.Warn("worktree reset postponed", "task", task, "error", err)
				continue
			}
			slog.Info("worktree reset from the remote branch", "task", task)
		}
	}
}

// janitor removes the worktrees of closed tasks listed under cleanup, one at a time.
func (s *session) janitor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-s.cleanups:
			if s.cleaned[task] {
				continue
			}
			if s.cleanup != nil {
				// The node janitor seals, runs on_task_closed hooks and removes; a busy worktree stays listed.
				if len(s.cleanup(ctx, []string{task})) == 0 {
					continue
				}
			} else if s.trees == nil {
				slog.Info("cleanup requested; worktrees disabled", "task", task)
			} else if err := s.trees.RemoveTask(ctx, task); errors.Is(err, workspace.ErrBusy) {
				slog.Info("cleanup postponed: worktree in use", "task", task)
				continue
			} else if err != nil {
				slog.Error("worktree cleanup", "task", task, "error", err)
				continue
			} else {
				slog.Info("worktree cleanup done", "task", task)
			}
			s.cleaned[task] = true
		}
	}
}

// control acts on a message of the node's control subject (protocol.NodeControlSubject; explicit-exits-authority
// D7): run.stop stops the run at once instead of at the next heartbeat answer, which stays the guarantee.
func (s *session) control(data []byte) {
	var e protocol.Envelope
	var stop protocol.RunStop
	if json.Unmarshal(data, &e) != nil || e.Type != "run.stop" || json.Unmarshal(e.Payload, &stop) != nil || stop.RunID == "" {
		slog.Warn("invalid control message")
		return
	}
	if s.inv.Stop(stop.RunID, stop.Reason) {
		slog.Info("run stopped by Core over the control subject", "run", stop.RunID, "reason", stop.Reason)
	}
}
