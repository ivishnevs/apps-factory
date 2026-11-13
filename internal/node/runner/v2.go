package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	NodeID          string `json:"node_id"`
	Name            string `json:"name"`
	Epoch           int64  `json:"epoch"`
	ProtocolVersion int    `json:"protocol_version"`
	Slots           int    `json:"slots"`
	// ConversationSlots take only conversation runs, on top of Slots (work-and-conversation-runs).
	ConversationSlots int            `json:"conversation_slots,omitempty"`
	Drivers           []DriverInfo   `json:"drivers"`
	Resources         map[string]int `json:"resources"`
	Labels            []string       `json:"labels"`
	// Worktrees: worktree tasks run in their own worktree here, so Core lets tasks of one project run in
	// parallel on this node (runtime-protocol-v2 §11).
	Worktrees bool `json:"worktrees,omitempty"`
	// Hooks names the catalog's NodeHooks document (node.yaml hooks; empty is default).
	Hooks string `json:"hooks,omitempty"`
	// Reason: start (process start, counted for node.restart_loop) or reregister (Core lost the node or its
	// incarnation changed). Driver changes ride the heartbeat's drivers and never re-register.
	Reason string `json:"reason,omitempty"`
	// Features: newer runtime options this node's drivers apply (storage.FeatureContextWindow).
	Features []string `json:"features,omitempty"`
	// Version is the node package's VERSION; DetectedLabels what the node found it can do (node-packaging-updates).
	Version        string   `json:"version,omitempty"`
	DetectedLabels []string `json:"detected_labels,omitempty"`
	// Platform is the package platform (darwin-arm64, ...) Core offers updates for; Update the node's update report
	// (node-packaging-updates 3.4).
	Platform string                     `json:"platform,omitempty"`
	Update   *protocol.NodeUpdateStatus `json:"update,omitempty"`
}

// Capacity is the heartbeat's capacity: slots and labels as they are now (settings from the web change them live).
type Capacity struct {
	Slots             int      `json:"slots"`
	ConversationSlots int      `json:"conversation_slots"`
	Labels            []string `json:"labels"`
	DetectedLabels    []string `json:"detected_labels"`
}

// DriverInfo is a driver as last checked: Version from CheckVersion, OK when no check failed; Check names the
// failing check (version, probe, auth) and Detail says why (Core's driver_unsupported event).
type DriverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Check   string `json:"check,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// TransientFailures counts the checks in a row that failed only transiently (the driver stays as it
	// was until probes.transient_failures of them); Until is when a run's auth failure hold-off ends (Unix
	// seconds, 0 when none), before which a passing probe does not re-enable the driver.
	TransientFailures int   `json:"transient_failures,omitempty"`
	Until             int64 `json:"until,omitempty"`
}

type Registered struct {
	Incarnation string `json:"incarnation"`
	// Company is the company of the node's key.
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
	// Readiness are the results of Core's project checks (node-readiness); [] when none ran yet, never omitted, so
	// Core knows this node reports them.
	Readiness []protocol.ProjectReadiness `json:"readiness"`
	// Capacity and Config: the node's slots and labels now and its report of the settings from the web.
	Capacity *Capacity                  `json:"capacity,omitempty"`
	Config   *protocol.NodeConfigReport `json:"config,omitempty"`
	// Update is the node's update report (downloading, testing, draining, switching, failed).
	Update *protocol.NodeUpdateStatus `json:"update,omitempty"`
	// Worktrees is the full worktree inventory (worktree-inventory-cleanup), sent when it changed, at least every
	// 10 minutes and when Core asked for it; otherwise only WorktreesHash.
	Worktrees     *[]protocol.WorktreeEntry `json:"worktrees,omitempty"`
	WorktreesHash string                    `json:"worktrees_hash,omitempty"`
}

type Health struct {
	Load1         float64 `json:"load1"`
	CPUs          int     `json:"cpus"`
	FreeDiskBytes uint64  `json:"free_disk_bytes"`
	BusySlots     int     `json:"busy_slots"`
	Disk          string  `json:"disk,omitempty"` // ok, low or critical; omitted when free space is unreadable
	// DiskLowBytes and DiskCriticalBytes are the watermarks Disk is judged by (node.yaml disk).
	DiskLowBytes      uint64 `json:"disk_low_bytes,omitempty"`
	DiskCriticalBytes uint64 `json:"disk_critical_bytes,omitempty"`
	// DiskConversationFloorBytes is the free space below which the node takes no conversation either
	// (disk-pause-conversations); sent whenever Disk is, so Core tells this node from an older one.
	DiskConversationFloorBytes *uint64 `json:"disk_conversation_floor_bytes,omitempty"`
	// Checks are the failing node self-checks (node.check_failed{check} on Core); [] when none fails. Driver
	// probes are reported in the heartbeat's drivers instead (driver_unsupported).
	Checks []node.CheckResult `json:"checks"`
	// StalledSeconds: no heartbeat succeeded for that long (more than StallReport); sent until one is delivered.
	StalledSeconds int64 `json:"stalled_seconds,omitempty"`
	// Security is what the machine's person set for agents here (connector-trust), shown on the Nodes page.
	Security *node.Security `json:"security,omitempty"`
	// Caches are the runtimes' directories outside the worktrees (node-runtime-caches), measured every
	// cacheScan; absent until the first measurement.
	Caches []node.RuntimeCache `json:"caches,omitempty"`
	// ClockOffsetMS is Core's clock minus this node's, learned from a signature_window refusal (node-wake-recovery):
	// the node signs with it when it is within worker.MaxClockOffset; Core raises node.clock_skew above the window.
	ClockOffsetMS int64 `json:"clock_offset_ms,omitempty"`
}

// StallReport is how long heartbeats must fail before the node reports node.heartbeat_stalled (node-agent D7).
var StallReport = 5 * time.Minute

// stop is a run Core wants stopped, with the reason.
type stop struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
}

type HeartbeatAnswer struct {
	Incarnation string   `json:"incarnation"`
	Stop        []stop   `json:"stop"`
	Acked       []string `json:"acked"`
	Cleanup     []string `json:"cleanup"`
	// Pause is the owner's pause of the node: no claim of any kind. PauseWork is Core's pause for a critical disk
	// (disk-pause-conversations): conversation runs only.
	Pause     bool `json:"pause"`
	PauseWork bool `json:"pause_work"`
	// Reset: tasks whose worktree must be reset from the remote branch (workspace_diverged, reset_workspace).
	Reset []string `json:"reset"`
	// Hooks: the NodeHooks document changed since the hash the node sent.
	Hooks *protocol.NodeHooksDelivery `json:"hooks,omitempty"`
	// Checks: the project readiness checks Core wants run (node-readiness).
	Checks []protocol.ProjectCheck `json:"checks,omitempty"`
	// ConfigVersion: the version of the node's settings from the web (0: none).
	ConfigVersion int64 `json:"config_version,omitempty"`
	// Update is the release Core offers this node (none: no update due, or one in progress is withdrawn).
	Update *protocol.NodeUpdate `json:"update,omitempty"`
	// Removals: worktrees to remove (worktree-inventory-cleanup), answered while the node reports them.
	Removals []protocol.WorktreeRemoval `json:"removals,omitempty"`
	// WorktreesResend: Core has no inventory for the hash sent; the next heartbeat carries the full list.
	WorktreesResend bool `json:"worktrees_resend,omitempty"`
}

// ErrStaleEpoch: Core knows a newer epoch of this node_id, so another process uses this state_dir's identity.
var ErrStaleEpoch = errors.New("stale epoch: another node process registered this node_id with a newer epoch")

const defaultHeartbeat = 10 * time.Second

// session is a registered v2 node.
type session struct {
	api     worker.API
	cfg     node.Config
	host    node.Host
	gate    *node.DiskGate // watermarks for health.disk
	trees   *workspace.Worktrees
	cleanup func(ctx context.Context, tasks []string) []string // node janitor, when set
	// removeWorktree removes one worktree Core answered under removals (janitor.RemoveWorktree); inv2 is the
	// worktree inventory (worktree-inventory-cleanup), listed for invCompany (the session's company when empty).
	removeWorktree func(ctx context.Context, rm protocol.WorktreeRemoval) error
	inv2           *treeInventory
	invCompany     string
	drivers        *driverHealth
	inv            *node.Inventory
	busy           func() int
	interval       time.Duration
	retryMax       time.Duration // longest pause between register attempts

	incarnation string
	company     string      // from the register answer; empty from a Core that does not send it
	paused      atomic.Bool // Core's pause (the owner paused the node): no claims
	pausedWork  atomic.Bool // Core's pause_work (critical disk): conversation claims only
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
	hooksMu sync.Mutex // hooks is read by the poll loop too
	onHooks func(protocol.NodeHooksDelivery)
	// checks returns the failing node self-checks for health.checks (git, git.user, forge.auth, ps, stack.*);
	// nil reports none. Driver probes are not among them: drivers carries them.
	checks func() []node.CheckResult
	// lastBeat is the last delivered heartbeat (or register); a longer gap than StallReport is reported once.
	lastBeat time.Time
	// ready runs the project readiness checks Core asks for (node-readiness); nil when the host cannot run commands.
	ready *projectChecks
	// caches: the last measurement of the runtime caches (cacheLoop).
	caches atomic.Pointer[[]node.RuntimeCache]
	// remote: the settings from the web (nil: node.yaml alone); cap: slots and labels now; version: the package's.
	remote  *remoteConfig
	capMu   sync.Mutex
	cap     capacity
	version string
	// update: version updates (nil: none).
	update *Updater
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
	gate := n.gate
	if gate == nil {
		gate = node.NewDiskGate(n.Host, c.Disk)
	}
	s := &session{api: n.API, cfg: c, host: n.Host, gate: gate, trees: trees, inv: node.NewInventory(id, epoch),
		drivers: h, busy: func() int { return 0 }, retryMax: 30 * time.Second, kick: make(chan struct{}, 1), inv2: newTreeInventory(), cleanups: make(chan string, 64), cleaned: map[string]bool{}, resets: make(chan string, 64), resetting: map[string]bool{},
		remote: n.remote, cap: capacity{slots: c.Slots, conversation: c.ConversationSlots, labels: c.Labels, detected: n.Detected}, version: n.Version, update: n.Update}
	if n.Host.Run != nil {
		s.ready = newProjectChecks(n.Host.Run, s.driversChanged) // a finished check pass is reported at once
	}
	if err = s.register(ctx, "start"); err != nil {
		return nil, err
	}
	return s, nil
}

// readiness is the heartbeat's project check results; nil (omitted) without checks support.
func (s *session) readiness() []protocol.ProjectReadiness {
	if s.ready == nil {
		return nil
	}
	return s.ready.reports()
}

// register registers the current epoch with the drivers as last checked, retrying while Core is
// unreachable; a refusal is final.
func (s *session) register(ctx context.Context, reason string) error {
	cp := s.capacity()
	features := []string{"context_window"}
	if s.trees != nil {
		features = append(features, protocol.FeatureWorktreeInventory)
	}
	req := Register{NodeID: s.inv.NodeID, Name: s.cfg.Name, Epoch: s.inv.Epoch(), ProtocolVersion: 2, Slots: cp.slots, ConversationSlots: cp.conversation, Features: features,
		Drivers: s.drivers.infos(), Resources: s.cfg.Resources, Labels: cp.labels, Worktrees: s.trees != nil, Hooks: s.cfg.Hooks, Reason: reason,
		Version: s.version, DetectedLabels: cp.detected, Update: s.update.Status()}
	if s.update != nil {
		req.Platform = s.update.Platform
	}
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
func (s *session) claimable() bool {
	return !s.paused.Load() && !s.switching.Load() && !node.LocallyPaused(s.cfg.StateDir) // connector-trust: factory-node pause
}

// workClaimable: Core has not paused work and review runs on the node (pause_work, a critical disk).
func (s *session) workClaimable() bool { return !s.pausedWork.Load() }

// run sends the inventory heartbeat every interval until ctx ends; it fails when the node must stop.
func (s *session) run(ctx context.Context) error {
	go s.janitor(ctx)
	go s.resetter(ctx)
	go s.cacheLoop(ctx)
	if s.inventoryOn() {
		go s.inventoryLoop(ctx)
		go s.remover(ctx)
	}
	if s.ready != nil {
		go s.ready.loop(ctx)
	}
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
	s.hooksMu.Lock()
	if s.hooks != nil && s.hooks.Hash == d.Hash {
		s.hooksMu.Unlock()
		return
	}
	s.hooks = &d
	s.hooksMu.Unlock()
	slog.Info("NodeHooks document from Core", "name", d.Name, "hash", d.Hash, "missing", d.Missing, "on_run_stop", len(d.Spec.OnRunStop),
		"on_task_closed", len(d.Spec.OnTaskClosed), "prune", len(d.Spec.Prune), "checks", len(d.Spec.Checks))
	if s.onHooks != nil {
		s.onHooks(d)
	}
}

func (s *session) hooksHash() string {
	s.hooksMu.Lock()
	defer s.hooksMu.Unlock()
	if s.hooks == nil {
		return ""
	}
	return s.hooks.Hash
}

func (s *session) health() Health {
	// Driver probes are not health checks: a failing driver reaches Core in drivers (driver_unsupported).
	h := Health{CPUs: runtime.NumCPU(), BusySlots: s.busy(), Checks: []node.CheckResult{}}
	sec := node.SecurityOf(s.cfg)
	h.Security = &sec
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
			h.DiskLowBytes, h.DiskCriticalBytes = s.gate.Watermarks()
			floor := s.gate.FloorBytes()
			h.DiskConversationFloorBytes = &floor
		}
	}
	if c := s.caches.Load(); c != nil {
		h.Caches = *c
	}
	h.ClockOffsetMS = s.api.Clock.Observed().Milliseconds()
	return h
}

// cacheScan: how often the runtime caches are measured (a walk of whole trees, off the heartbeat path).
var cacheScan = 10 * time.Minute

// cacheLoop measures the runtime caches now and every cacheScan.
func (s *session) cacheLoop(ctx context.Context) {
	t := time.NewTicker(cacheScan)
	defer t.Stop()
	for {
		c := node.RuntimeCaches(s.cfg, os.Getenv)
		s.caches.Store(&c)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
	cp := s.capacity()
	hb := Heartbeat{Epoch: s.inv.Epoch(), Inventory: s.inv.Snapshot(), Health: health, ResetDone: done,
		Drivers: s.drivers.infos(), Hooks: s.cfg.Hooks, HooksHash: s.hooksHash(), Readiness: s.readiness(),
		Capacity: &Capacity{Slots: cp.slots, ConversationSlots: cp.conversation, Labels: nonNilStrings(cp.labels), DetectedLabels: nonNilStrings(cp.detected)}}
	if s.remote != nil {
		hb.Config = s.remote.reported()
	}
	hb.Update = s.update.Status()
	var treesHash string
	var treesFull bool
	if s.inventoryOn() {
		var entries []protocol.WorktreeEntry
		if entries, treesHash, treesFull = s.worktreesForBeat(); treesFull {
			hb.Worktrees = &entries
		}
		hb.WorktreesHash = treesHash
	}
	err := s.api.Do(ctx, "POST", "/nodes/"+s.inv.NodeID+"/heartbeat", hb, &a)
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
	if treesHash != "" {
		s.worktreesDelivered(treesHash, treesFull, a.WorktreesResend)
	}
	s.queueRemovals(a.Removals)
	s.queueResets(a.Reset)
	if s.ready != nil && a.Checks != nil {
		s.ready.set(a.Checks)
	}
	if a.Hooks != nil {
		s.deliverHooks(*a.Hooks)
	}
	s.inv.Ack(a.Acked)
	s.stopRuns(a.Stop, "heartbeat")
	if was := s.paused.Swap(a.Pause); was != a.Pause {
		slog.Info("claiming paused by Core", "paused", a.Pause)
	}
	if was := s.pausedWork.Swap(a.PauseWork); was != a.PauseWork {
		slog.Info("work claims paused by Core (critical disk); conversations continue", "paused", a.PauseWork)
	}
	s.queueCleanups(a.Cleanup)
	s.fetchConfig(ctx, a.ConfigVersion)
	s.update.Offer(ctx, a.Update, true)
	return nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// stopRuns stops the listed runs of the inventory (from a heartbeat or a poll answer).
func (s *session) stopRuns(stops []stop, via string) {
	for _, x := range stops {
		if s.inv.Stop(x.RunID, x.Reason) {
			slog.Info("run stopped by Core", "run", x.RunID, "reason", x.Reason, "via", via)
		}
	}
}

// queueResets queues the worktree resets Core listed that are not queued, running or awaiting confirmation.
func (s *session) queueResets(tasks []string) {
	for _, task := range tasks {
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
}

func (s *session) queueCleanups(tasks []string) {
	for _, task := range tasks {
		select {
		case s.cleanups <- task:
		default: // full: Core lists it again
		}
	}
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
	s.pausedWork.Store(false)
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
