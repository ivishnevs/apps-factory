package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node"
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
}

// DriverInfo is a driver as probed at register: Version from CheckVersion, OK when no probe failed.
type DriverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
}

type Registered struct {
	Incarnation        string `json:"incarnation"`
	HeartbeatSeconds   int    `json:"heartbeat_seconds"`
	StartWindowSeconds int    `json:"start_window_seconds"`
}

// Heartbeat is the body of POST /nodes/{node_id}/heartbeat.
type Heartbeat struct {
	Epoch     int64               `json:"epoch"`
	Inventory []node.InventoryRun `json:"inventory"`
	Health    Health              `json:"health"`
}

type Health struct {
	Load1         float64 `json:"load1"`
	CPUs          int     `json:"cpus"`
	FreeDiskBytes uint64  `json:"free_disk_bytes"`
	BusySlots     int     `json:"busy_slots"`
	Disk          string  `json:"disk,omitempty"` // ok, low or critical; omitted when free space is unreadable
}

type HeartbeatAnswer struct {
	Incarnation string `json:"incarnation"`
	Stop        []struct {
		RunID  string `json:"run_id"`
		Reason string `json:"reason"`
	} `json:"stop"`
	Acked   []string `json:"acked"`
	Cleanup []string `json:"cleanup"`
	Pause   bool     `json:"pause"`
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
	drivers  []DriverInfo
	inv      *node.Inventory
	busy     func() int
	interval time.Duration
	retryMax time.Duration // longest pause between register attempts

	incarnation string
	paused      atomic.Bool // Core's pause
	switching   atomic.Bool // re-registering: no claims
	cleanups    chan string
	cleaned     map[string]bool // janitor's
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
	s := &session{api: n.API, cfg: c, host: n.Host, gate: node.NewDiskGate(n.Host, c.Disk), trees: trees, inv: node.NewInventory(id, epoch),
		busy: func() int { return 0 }, retryMax: 30 * time.Second, cleanups: make(chan string, 64), cleaned: map[string]bool{}}
	for _, d := range n.Drivers {
		s.drivers = append(s.drivers, probe(ctx, d))
	}
	if err = s.register(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func probe(ctx context.Context, d driver.Driver) DriverInfo {
	v, err := d.CheckVersion(ctx)
	return DriverInfo{Name: string(d.Name()), Version: v, OK: err == nil && !node.Failed(d.Probe(ctx))}
}

// register registers the current epoch, retrying while Core is unreachable; a refusal is final.
func (s *session) register(ctx context.Context) error {
	req := Register{NodeID: s.inv.NodeID, Name: s.cfg.Name, Epoch: s.inv.Epoch(), ProtocolVersion: 2, Slots: s.cfg.Slots,
		Drivers: s.drivers, Resources: s.cfg.Resources, Labels: s.cfg.Labels}
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
			if s.interval <= 0 {
				s.interval = defaultHeartbeat
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
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.beat(ctx); err != nil {
				return err
			}
			t.Reset(s.interval)
		}
	}
}

func (s *session) health() Health {
	h := Health{CPUs: runtime.NumCPU(), BusySlots: s.busy()}
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
	err := s.api.Do(ctx, "POST", "/nodes/"+s.inv.NodeID+"/heartbeat", Heartbeat{Epoch: s.inv.Epoch(), Inventory: s.inv.Snapshot(), Health: s.health()}, &a)
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
	s.inv.Ack(a.Acked)
	for _, x := range a.Stop {
		if s.inv.Stop(x.RunID) {
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
	if err = s.register(ctx); ctx.Err() != nil {
		return nil
	}
	return err
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
