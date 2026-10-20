package worker

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/driver"
	"appfactory/internal/driver/codex"
	"appfactory/internal/factorytools"
	"appfactory/internal/node"
	"appfactory/internal/node/sessions"
	"appfactory/internal/node/shim"
	"appfactory/internal/openspec"
	"appfactory/internal/policies"
	"appfactory/internal/procgroup"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/workspace"
	"github.com/nats-io/nats.go"
)

type API struct {
	URL, Token string
	Client     *http.Client
}

func (a API) Do(ctx context.Context, method, path string, body, result any) error {
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.URL, "/")+path, data)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return &HTTPError{res.StatusCode, string(b)}
	}
	if result != nil {
		return json.NewDecoder(res.Body).Decode(result)
	}
	return nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Core HTTP %d: %s", e.Status, e.Message) }

type Worker struct {
	Allocator workspace.Allocator
	Bus       *bus.Bus
	API       API
	// Driver runs the runtime; nil means Codex configured by Binary and Args.
	Driver    driver.Driver
	Binary    string
	Args      []string
	MCPBinary string
	// ShimBinary is factory-shim, linked per guarded command (node-agent D6); empty is next to the executable.
	ShimBinary string
	// StateDir keeps pidfiles of running runtimes; on start the worker kills the process
	// groups left there by its previous runs. It must not be shared by live workers.
	StateDir string
	// Worktrees serves runs with workspace_mode worktree; nil runs them in the project workspace.
	Worktrees *workspace.Worktrees
	// NodeID names this node in workspace reports (node.yaml name; the v2 node_id).
	NodeID string
	// Inventory, when set, makes the executor a slot of a registered node (runtime protocol v2): commands are
	// pointers, claims carry node_id, epoch, claim_id and slot, a launched run is started with POST /start, and
	// leases are renewed by the node's inventory heartbeat instead of per-run heartbeats.
	Inventory *node.Inventory
	// RunStopped, when set, is called once a claimed run has ended, with its worktree path ("" outside a
	// worktree); the node's janitor runs on_run_stop hooks there.
	RunStopped func(spec protocol.RunSpec, worktree string)
	// Skills is the node's local skills registry; a run's offered skills are copied from it and verified
	// against the hashes pinned on the run. nil runs without materialized skills (logged).
	Skills *skills.Registry
	// Sessions records the sessions this node ran (ResumePolicy); nil resumes spec.SessionID as given.
	Sessions *sessions.Store
	// AuthFailed, when set, is called when a run ends on the runtime's authentication failure
	// (driver.AuthFailure, driver.Result.Auth); the node disables the driver until its next probe passes.
	AuthFailed func(err error)
	// RunSucceeded, when set, is called when a run ends succeeded; the node resets the driver's auth hold-off.
	RunSucceeded func()
	// EnvAllow adds variable names (NAME_* for a prefix) to the runtime environment's allowlist RuntimeEnv
	// (node.yaml runtime_env); credentials stay out regardless.
	EnvAllow []string
}

// noteAuth reports a runtime authentication failure to AuthFailed.
func (w *Worker) noteAuth(err error) {
	if w.AuthFailed != nil && err != nil && driver.IsAuthFailure(err) {
		w.AuthFailed(err)
	}
}

// driver also reports whether Factory MCP is injected: when the driver supports MCP, or for
// Codex built from Binary and Args, unless Args replace the real app-server and MCPBinary is unset.
func (w *Worker) driver() (driver.Driver, bool) {
	if w.Driver != nil {
		return w.Driver, w.Driver.Capabilities().MCP
	}
	return codex.Driver{Binary: w.Binary, Args: w.Args}, w.Args == nil || w.MCPBinary != ""
}

func (w *Worker) Run(ctx context.Context) error {
	if w.StateDir != "" {
		if err := KillOrphans(w.StateDir); err != nil {
			return err
		}
	}
	sub, err := w.Subscribe()
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		msgs, err := sub.Fetch(1, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if err = w.Process(ctx, m, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// Subscribe binds the driver's run command subject with its durable consumer. Executors of one driver
// share that consumer, so node slots and standalone workers compete for the same commands.
func (w *Worker) Subscribe() (*nats.Subscription, error) {
	drv, _ := w.driver()
	name := string(drv.Name())
	return w.Bus.Subscribe("factory.commands.agent.run."+name, name+"-worker-v1")
}

// Process validates, claims and executes one run command. running, when set, receives the claimed run
// and the cancel func of its execution before the runtime starts. Process fails only when the command
// cannot be acknowledged; run failures go to Core as events.
func (w *Worker) Process(ctx context.Context, m *nats.Msg, running func(run string, cancel context.CancelFunc)) error {
	if w.Inventory != nil {
		return w.processV2(ctx, m, running)
	}
	e, run := pointer(m.Data)
	if run == "" {
		_ = m.Term()
		slog.Error("invalid run command")
		return nil
	}
	// Claim before runtime startup: the answer carries the authoritative RunSpec with the brief Core built at
	// claim (trigger-log-scheduling D6), so nothing else is taken from the command.
	var claim Claim
	if err := w.API.Do(ctx, "POST", "/runs/"+run+"/claim", nil, &claim); err != nil {
		var h *HTTPError
		if errors.As(err, &h) && h.Status == 409 {
			var r Refusal
			switch {
			case json.Unmarshal([]byte(h.Message), &r) == nil && r.Drop:
				// Stale, duplicate or revoked: the run never starts here (Core ended a stale one).
				slog.Info("run command dropped", "run", run, "reason", r.Reason)
				return m.AckSync()
			case r.Retry:
				_ = m.NakWithDelay(RetryDelay)
				return nil
			}
		}
		_ = m.NakWithDelay(time.Second)
		return nil
	}
	spec := claim.RunSpec
	if spec.ID != run {
		// A Core whose v1 claim answers only the lease: read the run it claimed.
		var saved storage.Run
		if err := w.API.Do(ctx, "GET", "/runs/"+run, nil, &saved); err != nil || saved.Spec.ID != run {
			slog.Error("claimed run unreadable", "run", run, "error", err)
			_ = m.NakWithDelay(time.Second)
			return nil
		}
		spec = saved.Spec
	}
	if err := m.AckSync(); err != nil {
		return err
	} // Core's lease now owns recovery, not redelivery.
	slog.Info("run claimed", "run", spec.ID)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if running != nil {
		running(spec.ID, cancel)
	}
	if err := w.execute(runCtx, spec, claim.Lease, e.ID); err != nil {
		slog.Error("runtime run", "run", spec.ID, "error", err)
	}
	return nil
}
func (w *Worker) execute(parent context.Context, spec protocol.RunSpec, lease, cause string) (runErr error) {
	// Timeouts and heartbeats run on Go's monotonic clock, which stops during system sleep
	// (clock_test.go); never derive them from wall-clock time.
	ctx, cancel := context.WithTimeoutCause(parent, time.Duration(spec.TimeoutSeconds)*time.Second, errRunTimeout)
	defer cancel()
	// RunStopped runs last: after the seal, the release and the terminal event.
	var stopped string
	if w.RunStopped != nil {
		defer func() { w.RunStopped(spec, stopped) }()
	}
	// The run's latest usage and quota ride on every terminal event, so a stopped, timed-out or
	// unknown run still reports what it consumed.
	var meter meter
	var session driver.Session
	// record is this node's record of the run's session, kept from launch to the terminal event.
	var record sessions.Record
	saveRecord := func() {
		if w.Sessions == nil || record.SessionID == "" {
			return
		}
		record.At = time.Now().UTC()
		if err := w.Sessions.Put(record); err != nil {
			slog.Error("session record", "run", spec.ID, "session", record.SessionID, "error", err)
		}
	}
	// stopRun stops the run with a cause stopOutcome reads: errExitGrace for a runtime still running
	// ExitGrace after its accepted exit (the exit stands), errRevoked for a v1 heartbeat cancel,
	// errHeartbeatLost when the v1 heartbeat fails.
	ctx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	var wt *workspace.Worktree
	var req workspace.Request
	// exitFile is where the MCP adapter records the run's exit (docs/explicit-exits-contract.md §5).
	var exitFile string
	// exitHead is the worktree's HEAD when the worker first saw the exit accepted: the exit's commit
	// unless the adapter names it in the exit file.
	var exitMu sync.Mutex
	var exitHead string
	noteExit := func() {
		if wt == nil {
			return
		}
		exitMu.Lock()
		defer exitMu.Unlock()
		if exitHead != "" {
			return
		}
		headCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if h, err := wt.Head(headCtx); err == nil {
			exitHead = h
		} else {
			slog.Warn("worktree HEAD at the accepted exit", "run", spec.ID, "error", err)
		}
	}
	// mcpName is the Factory MCP adapter's executable name; a pending exit holds the runtime while a
	// process of that name is in its group (awaitExitDelivery).
	var mcpName string
	awaited := false
	// awaitDelivery runs once, before the runtime's group is killed and before the exit file is read for
	// the terminal event: an exit still pending delivery to Core gets up to ExitDeliveryWait.
	awaitDelivery := func() {
		if awaited || session == nil || exitFile == "" {
			return
		}
		awaited = true
		awaitExitDelivery(ctx, spec.ID, exitFile, session.ProcessGroup(), mcpName)
	}
	finished := false
	var exitEnd exitRecord
	// finish runs once, before the terminal event and before the exit file is removed: it stops a
	// worktree run's runtime and process group, reads the run's exit, then seals the worktree with it.
	finish := func() exitRecord {
		if finished {
			return exitEnd
		}
		finished = true
		awaitDelivery()
		if wt != nil {
			if session != nil {
				session.Close()
			}
			if err := wt.Stop(); err != nil {
				slog.Error("worktree run group", "run", spec.ID, "error", err)
			}
		}
		exitEnd = exitRecord{ExitState: factorytools.ExitState{State: "none"}}
		if exitFile != "" {
			exitEnd = readExitRecord(exitFile)
		}
		if wt == nil {
			return exitEnd
		}
		var exit workspace.Exit
		if exitEnd.State == "accepted" {
			if exitEnd.ExitSHA == "" {
				noteExit()
				exitMu.Lock()
				exitEnd.ExitSHA = exitHead
				exitMu.Unlock()
			}
			exit = workspace.Exit{Kind: exitEnd.Kind, SHA: exitEnd.ExitSHA}
		}
		w.sealWorktree(spec.ID, lease, *wt, req, exit)
		return exitEnd
	}
	// noEvidence: the run ends without a runtime result or a known outcome (the deferred end below).
	var noEvidence bool
	emit := func(kind string, p protocol.Event) error {
		if p.Status != "" {
			x := finish()
			if exitFile != "" {
				p.ExitState = x.State
			}
			// After an accepted exit the run ends as the exit says, also when the executor stopped the
			// runtime meanwhile (ExitGrace, a timeout, a stop) or the end has no evidence: Core records
			// it succeeded, and a stopped or failed end would misreport it.
			if x.State == "accepted" && p.Outcome != outcomeSucceeded && (ctx.Err() != nil || noEvidence) {
				slog.Info("run ended after its accepted exit", "run", spec.ID, "exit", x.Kind, "outcome", p.Outcome)
				kind, p.Outcome = "completed", outcomeSucceeded
			}
			record.Outcome = p.Outcome
			saveRecord()
			if p.Outcome == outcomeSucceeded && w.RunSucceeded != nil {
				w.RunSucceeded()
			}
		}
		if kind != "started" && kind != "progress" {
			p.Usage, p.Quota = meter.usage(), meter.quota
		}
		p.RunID = spec.ID
		p.Lease = lease
		pubCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return w.Bus.Event(pubCtx, kind, cause, p)
	}
	// A run that ends without a runtime result reports what is known (docs/runtime-protocol-v2.md §8): an
	// endError names its outcome, or, when the run failed before the runtime started because it was being
	// stopped, the stop's outcome. Any other error after the prompt may have reached the runtime has no
	// terminal evidence: lost (Core records it lost: no attempt), unless the executor stopped the runtime
	// for a timeout (its known cause) or the subscription limit ended the turn (paused_out). A revoke,
	// a lost heartbeat or a node shutdown without evidence is lost too.
	defer func() {
		if runErr == nil {
			return
		}
		noEvidence = true
		outcome := outcomeLost
		if meter.quota != nil {
			outcome = driver.OutcomePausedOut // the subscription limit ended the turn
		}
		if stop := stopOutcome(ctx); stop == outcomeTimedOut || stop == outcomeIdleTimeout {
			outcome = stop
		}
		var end *endError
		if errors.As(runErr, &end) {
			noEvidence = false
			outcome = end.outcome
			if end.notStarted && ctx.Err() != nil {
				outcome = stopOutcome(ctx)
			}
		}
		if err := emit("failed", protocol.Event{Status: ended, Result: runErr.Error(), Outcome: outcome}); err != nil {
			slog.Error("terminal event publication failed; lease recovery required", "error", err)
		}
	}()
	drv, withMCP := w.driver()
	if spec.Provider != drv.Name() || spec.TimeoutSeconds < 1 {
		return setupFailed(fmt.Errorf("invalid RunSpec"))
	}
	v2 := w.Inventory != nil
	if v2 {
		w.Inventory.Set(spec.ID, node.RunStarting)
	}
	var cwd string
	if r, ok := w.worktreeRequest(spec); ok {
		t, err := w.Worktrees.Acquire(ctx, r)
		var diverged *workspace.DivergedError
		if errors.As(err, &diverged) {
			// Core escalates workspace_diverged; the worktree is left as it is (task-worktrees D4).
			slog.Error("worktree start check", "run", spec.ID, "task", r.Task, "path", diverged.Path, "head", diverged.Head, "sealed_head", diverged.SealedHead, "remote", diverged.Remote, "reason", diverged.Reason)
			return emit("failed", protocol.Event{Status: ended, Result: diverged.Error(), Outcome: outcomeSetupFailed})
		}
		if workspace.IsLockError(err) {
			// Git lock contention that outlived the allocator's retries is transient, not a broken setup
			// (task-worktrees D1): an ordinary retryable failure.
			return notStarted(driver.OutcomeFailed, fmt.Errorf("worktree: %w", err))
		}
		if err != nil {
			return setupFailed(fmt.Errorf("worktree: %w", err))
		}
		stopped = t.Path
		wt, req, cwd = &t, r, t.Path
		defer func() {
			finish()
			if err := t.Release(); err != nil {
				slog.Error("worktree release", "run", spec.ID, "error", err)
			}
		}()
	} else {
		allocator := w.Allocator
		if allocator == nil {
			allocator = workspace.Local{}
		}
		allocation, err := allocator.Acquire(ctx, spec.Workspace)
		if err != nil {
			return setupFailed(err)
		}
		defer allocation.Release()
		cwd = allocation.Path
	}
	if spec.OpenSpecChange != "" {
		if _, e := (openspec.Access{}).CheckChange(cwd, spec.OpenSpecChange); e != nil {
			return emit("failed", protocol.Event{Status: ended, Result: "OpenSpec preflight failed before runtime startup: " + e.Error(), Outcome: outcomeSetupFailed})
		}
	}
	skillsRoot, skillList, err := w.stageSkills(spec)
	var skillErr *SkillError
	if errors.As(err, &skillErr) {
		return emit("failed", protocol.Event{Status: ended, Result: skillErr.Error() + " (refused before runtime startup)", Outcome: outcomeSetupFailed})
	}
	if err != nil {
		return setupFailed(err)
	}
	if skillsRoot != "" {
		defer os.RemoveAll(skillsRoot)
	}

	// Heartbeats also carry cancellation. Failure stops execution instead of running without ownership.
	// A v2 run is renewed and stopped by the node's inventory heartbeat.
	heartbeatCtx, stopHeartbeat := context.WithCancel(parent)
	defer stopHeartbeat()
	go func() {
		if v2 {
			return
		}
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-t.C:
				var res struct {
					Cancel bool `json:"cancel"`
				}
				if err := w.API.Do(heartbeatCtx, "POST", "/runs/"+spec.ID+"/heartbeat", map[string]string{"lease": lease}, &res); err != nil {
					slog.Error("heartbeat failed", "run", spec.ID, "error", err)
					stopRun(errHeartbeatLost)
					return
				}
				if res.Cancel {
					stopRun(errRevoked)
					return
				}
			}
		}
	}()
	runSpec, prev := w.resumeSpec(drv, spec)
	launch := driver.Launch{Spec: runSpec, Cwd: cwd, Instructions: spec.Instructions, Skills: skillList, SkillsRoot: skillsRoot, Env: nodeEnvironment(os.Environ(), w.EnvAllow)}
	shimDir, err := w.shims(spec)
	if err != nil {
		return setupFailed(err)
	}
	if shimDir != "" {
		defer os.RemoveAll(shimDir)
		launch.Guards, _ = snapshotRules(spec) // parsed by shims
	}
	if withMCP || shimDir != "" {
		// Approval shims ask Core with the run credential even when the runtime has no Factory MCP.
		var credential struct {
			Token         string `json:"token"`
			Instructions  string `json:"instructions"`
			ExplicitExits bool   `json:"explicit_exits"`
		}
		if err := w.API.Do(ctx, "POST", "/runs/"+spec.ID+"/credential", map[string]string{"lease": lease}, &credential); err != nil {
			return setupFailed(err)
		}
		if credential.Token == "" || credential.Instructions == "" {
			return setupFailed(fmt.Errorf("missing run credential"))
		}
		launch.Env = runEnvironment(os.Environ(), w.EnvAllow, w.API.URL, credential.Token)
		if withMCP {
			mcp, e := sibling(w.MCPBinary, "factory-mcp")
			if e != nil {
				return setupFailed(e)
			}
			dir, e := os.MkdirTemp("", "factory-exit-")
			if e != nil {
				return setupFailed(e)
			}
			// The run's exit is read (and the worktree sealed with it) before its file goes away, also
			// when the run returns with an error and its terminal event is emitted afterwards.
			defer func() { finish(); os.RemoveAll(dir) }()
			exitFile, mcpName = filepath.Join(dir, "exit.json"), filepath.Base(mcp)
			launch.Env = setEnv(launch.Env, "FACTORY_EXIT_FILE", exitFile)
			launch.Env = setEnv(launch.Env, "FACTORY_WORKDIR", cwd)
			launch.Env = setEnv(launch.Env, "FACTORY_DEFAULT_BRANCH", spec.DefaultBranch)
			vars := []string{"FACTORY_RUN_TOKEN", "FACTORY_URL", "FACTORY_EXIT_FILE", "FACTORY_WORKDIR", "FACTORY_DEFAULT_BRANCH"}
			if wt != nil {
				scan, _ := json.Marshal(w.Worktrees.ScanCommand())
				launch.Env = setEnv(launch.Env, "FACTORY_SCAN_COMMAND", string(scan))
				vars = append(vars, "FACTORY_SCAN_COMMAND")
			}
			launch.MCP = &driver.MCPConfig{Command: mcp, EnvVars: vars}
			launch.Instructions += credential.Instructions
			if credential.ExplicitExits {
				file := exitFile
				launch.ExitGuard = func() bool { s := readExitState(file).State; return s == "accepted" || s == "pending" }
				launch.ExitGuardRetries, launch.ExitFile = ExitGuardRetries, file
			}
		}
		if shimDir != "" {
			launch.Env = shim.Env(launch.Env, shimDir)
		}
	}
	if wt != nil {
		launch.Env = setEnv(launch.Env, "FACTORY_SCRATCH", wt.Scratch)
	}
	session, err = drv.Launch(ctx, launch)
	if err != nil {
		// The prompt was not sent: a missing runtime or an auth failure is paused_out, the rest failed.
		w.noteAuth(err)
		return notStarted(cmp.Or(driver.OutcomeOf(err), driver.OutcomeFailed), err)
	}
	record = sessions.Record{SessionID: session.ID(), Task: spec.TaskID, Agent: spec.Agent, Project: spec.Project, Provider: string(spec.Provider), Run: spec.ID}
	if record.SessionID == prev.SessionID {
		record.Started = prev.Started // a resumed session stays resumable
	}
	saveRecord()
	if group := session.ProcessGroup(); wt != nil && group > 0 {
		if err = wt.Track(group); err != nil {
			session.Close()
			return notStarted(driver.OutcomeFailed, err)
		}
	}
	if group := session.ProcessGroup(); w.StateDir != "" && group > 0 {
		path, e := writePidfile(w.StateDir, spec.ID, group)
		if e != nil {
			session.Close()
			return notStarted(driver.OutcomeFailed, e)
		}
		defer os.Remove(path)
	}
	defer func() { awaitDelivery(); session.Close() }()
	if v2 {
		// Phase 2 of the claim: workspace allocated and runtime launched (protocol v2 §6). Start is retry-safe.
		if err = w.API.Retry(ctx, 8, "/runs/"+spec.ID+"/start", map[string]string{"lease": lease}, nil); err != nil {
			return notStarted(driver.OutcomeFailed, fmt.Errorf("start: %w", err))
		}
		w.Inventory.Set(spec.ID, node.RunRunning)
	}
	if err = emit("started", protocol.Event{SessionID: session.ID()}); err != nil {
		return notStarted(driver.OutcomeFailed, err)
	}
	if err = session.Start(ctx); err != nil {
		// The prompt may have reached the runtime: unknown unless the error names its outcome.
		w.noteAuth(err)
		if o := driver.OutcomeOf(err); o != "" {
			return &endError{outcome: o, err: err}
		}
		return err
	}
	record.Started = true
	saveRecord()
	if exitFile != "" {
		// After an accepted exit the turn may finish; a runtime still running ExitGrace later is stopped
		// (the exit stands: the run ends succeeded). The worktree's HEAD when the
		// exit is first seen accepted is the exit's commit.
		watchDone := make(chan struct{})
		defer close(watchDone)
		go watchExit(exitFile, watchDone, noteExit, func() { stopRun(errExitGrace) })
	}
	events := session.Events()
	cancelled := ctx.Done()
	var interruptTimeout <-chan time.Time
	var interruptFailed error
	// stop is the outcome of the reason the executor interrupted the runtime ("" while it did not).
	stop := ""
	for events != nil {
		select {
		case <-cancelled:
			cancelled = nil
			stop = stopOutcome(ctx)
			interruptCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			interruptErr := session.Interrupt(interruptCtx)
			stop()
			// Acknowledgement only means interruption requested; a refusal may mean the turn just ended.
			// Either way wait for terminal evidence.
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			interruptTimeout = timer.C
			if interruptErr != nil {
				slog.Warn("interrupt refused; waiting for terminal evidence", "run", spec.ID, "error", interruptErr)
				interruptFailed = interruptErr
			}
		case <-interruptTimeout:
			err := errors.New("interrupt result unknown")
			if interruptFailed != nil {
				err = fmt.Errorf("interrupt: %w", interruptFailed)
			}
			// No terminal evidence; the session is closed (its process group killed) on return.
			return endedWithout(driver.Result{Err: err}, stop, meter.quota != nil)
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			p := protocol.Event{Method: e.Method, Data: openspec.AuditData(e.Data)}
			switch e.Kind {
			case driver.EventUsage, driver.EventCost:
				meter.add(e)
				p.Usage = meter.usage()
			case driver.EventQuota:
				meter.add(e)
				p.Quota = meter.quota
			}
			if err = emit("progress", p); err != nil {
				return err
			}
		}
	}
	result := <-session.Done()
	if result.Auth != nil && w.AuthFailed != nil {
		w.AuthFailed(result.Auth)
	} else {
		w.noteAuth(result.Err)
	}
	if result.Err != nil {
		return endedWithout(result, stop, meter.quota != nil)
	}
	return emit("completed", protocol.Event{Status: ended, Result: result.Text, Outcome: runEnd(result, stop, meter.quota != nil)})
}

// ended is the status of a terminal event (the run's phase); its outcome says how the run ended.
const ended = "ENDED"

// Run outcomes the executor reports itself (reliable-recovery D1); the driver names failed,
// max_turns and paused_out.
const (
	outcomeSucceeded   = "succeeded"
	outcomeTimedOut    = "timed_out"
	outcomeIdleTimeout = "idle_timeout"
	outcomeSetupFailed = "setup_failed"
	outcomeRevoked     = "revoked"
	// outcomeLost: the run ended without terminal evidence; Core records it lost (no attempt).
	outcomeLost = "lost"
)

// errRunTimeout is the cancellation cause of the run's own hard timeout (RunSpec.timeout_seconds).
var errRunTimeout = errors.New("run timeout")

// errExitGrace is the cancellation cause of a runtime stopped ExitGrace after its accepted exit; the run
// ends succeeded (the exit stands).
var errExitGrace = errors.New("stopped after the accepted exit")

// errRevoked is the cancellation cause of a v1 heartbeat cancel (Core stopped the run); errHeartbeatLost
// of a failed v1 heartbeat (the worker no longer owns the run).
var (
	errRevoked       = errors.New("stopped by Core")
	errHeartbeatLost = errors.New("heartbeat failed")
)

// stopOutcome is the outcome of the reason ctx was cancelled ("" while it is not): timed_out for the
// run's own timeout or Core's v2 stop timed_out, idle_timeout for Core's v2 stop idle_timeout, revoked
// for any other Core stop (owner cancel, kill switch, a run Core already ended or does not know, a v1
// heartbeat cancel; Core records a v1 idle stop idle_timeout from its own stop reason), and lost for a
// stop Core did not ask for (a node shutdown, a lost heartbeat): never revoked.
func stopOutcome(ctx context.Context) string {
	if ctx.Err() == nil {
		return ""
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, errRunTimeout) {
		return outcomeTimedOut
	}
	if errors.Is(cause, errRevoked) {
		return outcomeRevoked
	}
	var c *node.StopCause
	if errors.As(cause, &c) {
		switch c.Reason {
		case outcomeTimedOut, outcomeIdleTimeout:
			return c.Reason
		}
		return outcomeRevoked
	}
	return outcomeLost
}

// runEnd is the outcome of a started run from the runtime's result. stop is the outcome of the reason
// the executor interrupted the runtime ("" when it did not); quota tells whether the runtime hit its
// subscription limit during the run.
//   - COMPLETED is succeeded, even after an interrupt.
//   - CANCELLED or FAILED after a stop carry the stop: timed_out, idle_timeout, revoked (Core's stop with
//     the runtime's evidence) or lost (a node shutdown or lost heartbeat). A CANCELLED turn nobody asked
//     for is failed.
//   - FAILED otherwise carries the driver's outcome (max_turns, paused_out), else paused_out after a quota
//     hit, else failed.
//   - Without terminal evidence (Err) the outcome is known only when the executor stopped the runtime
//     for a timeout (it kills the process group) or the error names it (auth, missing runtime, broken
//     stream). Otherwise lost (an interrupt without a result, the shim or app-server going away
//     mid-turn); execute reports paused_out instead after a quota hit.
func runEnd(r driver.Result, stop string, quota bool) string {
	if r.Err != nil {
		if stop == outcomeTimedOut || stop == outcomeIdleTimeout {
			return stop
		}
		if o := driver.OutcomeOf(r.Err); o != "" {
			return o
		}
		return outcomeLost
	}
	switch r.Status {
	case "COMPLETED":
		return outcomeSucceeded
	case "CANCELLED", "FAILED":
		switch {
		case stop != "":
			return stop
		case r.Status == "CANCELLED":
			return driver.OutcomeFailed
		case r.Outcome != "":
			return r.Outcome
		case quota:
			return driver.OutcomePausedOut
		}
		return driver.OutcomeFailed
	}
	return outcomeLost
}

// endedWithout is the error that ends a started run without terminal evidence: an endError when runEnd
// knows its outcome, else the error itself (execute reports it lost, or paused_out after a quota hit).
func endedWithout(r driver.Result, stop string, quota bool) error {
	if outcome := runEnd(r, stop, quota); outcome != outcomeLost {
		return &endError{outcome: outcome, err: r.Err}
	}
	return r.Err
}

// endError ends a run without a runtime result with a known outcome. notStarted: the prompt never
// reached the runtime, so a run stopped meanwhile ends with the stop's outcome instead.
type endError struct {
	outcome    string
	notStarted bool
	err        error
}

func (e *endError) Error() string { return e.err.Error() }
func (e *endError) Unwrap() error { return e.err }

// setupFailed ends a run that failed before its runtime started: workspace, start check, OpenSpec
// preflight, skills, shims, Factory binaries or the run credential.
func setupFailed(err error) error { return notStarted(outcomeSetupFailed, err) }

// notStarted ends a run whose prompt never reached the runtime with outcome.
func notStarted(outcome string, err error) error {
	return &endError{outcome: outcome, notStarted: true, err: err}
}

// meter keeps a run's latest usage per model, its total cost and its quota hit.
type meter struct {
	models []protocol.Usage
	cost   *driver.Cost
	quota  *protocol.Quota
}

func (m *meter) add(e driver.Event) {
	switch {
	case e.Usage != nil:
		for i := range m.models {
			if m.models[i].Model == e.Usage.Model {
				m.models[i] = *e.Usage
				return
			}
		}
		m.models = append(m.models, *e.Usage)
	case e.Cost != nil:
		c := *e.Cost
		m.cost = &c
	case e.Quota != nil:
		q := *e.Quota
		m.quota = &q
	}
}

// usage returns a copy of the per-model usage; a run total cost goes to the single model when
// no model carries its own price.
func (m *meter) usage() []protocol.Usage {
	if len(m.models) == 0 {
		return nil
	}
	out := append([]protocol.Usage(nil), m.models...)
	if m.cost != nil && len(out) == 1 && out[0].CostUSD == 0 {
		out[0].CostUSD, out[0].CostSource = m.cost.USD, m.cost.Source
	}
	return out
}

// worktreeRequest is the task worktree of a workspace_mode worktree run; shared and scratch runs, and
// runs this worker cannot place in a worktree, use the project workspace through the Allocator (Local).
// WorkspaceFreshFromDefault is the workspace mode whose task worktree starts every run from the freshly
// fetched default branch, without a task branch (task-worktrees D4; release-type agents).
const WorkspaceFreshFromDefault = "fresh_from_default"

func (w *Worker) worktreeRequest(spec protocol.RunSpec) (workspace.Request, bool) {
	if spec.WorkspaceMode != "worktree" && spec.WorkspaceMode != WorkspaceFreshFromDefault {
		return workspace.Request{}, false
	}
	if w.Worktrees == nil || spec.TaskID == "" || spec.RepoURL == "" {
		slog.Warn("worktree run in the project workspace", "run", spec.ID, "worktrees", w.Worktrees != nil, "task", spec.TaskID, "repo_url", spec.RepoURL != "")
		return workspace.Request{}, false
	}
	// RunSpec has no company yet; the node serves the default company.
	r := workspace.Request{Company: storage.DefaultCompany, Project: spec.Project, Task: spec.TaskID, RepoURL: spec.RepoURL, DefaultBranch: spec.DefaultBranch}
	if spec.WorkspaceMode == WorkspaceFreshFromDefault {
		r.Fresh = true
	} else {
		r.Branch = workspace.BranchName(spec.TaskID, "")
	}
	return r, true
}

// sealWorktree seals a run's worktree with its accepted exit (reset to the exit's commit after handoff,
// submit or done) and reports it to Core: mode, branch, node, the last exit's commit, sealed_head. The
// WIP ref stays local (task-worktrees D3). Failures are logged only.
func (w *Worker) sealWorktree(run, lease string, wt workspace.Worktree, req workspace.Request, exit workspace.Exit) {
	ctx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	res, err := w.Worktrees.SealExit(ctx, req, exit)
	if err != nil {
		slog.Error("worktree seal", "run", run, "task", req.Task, "path", wt.Path, "exit", exit.Kind, "exit_sha", exit.SHA, "error", err)
		if res.SealedHead == "" {
			return
		}
	}
	// unpushed pins the task to this node (task-worktrees D8) and is reported to Core; wip_sha is logged.
	slog.Info("worktree sealed", "run", run, "task", req.Task, "path", wt.Path, "branch", wt.Branch, "exit", exit.Kind,
		"exit_sha", res.ExitSHA, "reset", res.Reset, "sealed_head", res.SealedHead, "wip_sha", res.WIPSHA, "unpushed", res.Unpushed)
	report := workspaceReport{Lease: lease, Mode: "worktree", Branch: wt.Branch, NodeID: w.NodeID, ExitSHA: res.ExitSHA,
		SealedHead: res.SealedHead, SealedAt: res.SealedAt.Unix(), Unpushed: res.Unpushed}
	if err := w.reportWorkspace(ctx, run, report); err != nil {
		slog.Error("workspace report", "run", run, "task", req.Task, "error", err)
	}
}

// workspaceReport is the body of POST /runs/{id}/workspace.
type workspaceReport struct {
	Lease      string `json:"lease"`
	Mode       string `json:"mode"`
	Branch     string `json:"branch,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	ExitSHA    string `json:"exit_sha,omitempty"`
	SealedHead string `json:"sealed_head,omitempty"`
	SealedAt   int64  `json:"sealed_at,omitempty"`
	// Unpushed: work of the task that is not on the remote stays on this node (pinning, task-worktrees D8).
	Unpushed bool `json:"unpushed,omitempty"`
}

// reportWorkspace posts a run's workspace to Core, retrying transient failures (network errors, 5xx)
// a few times; 4xx answers are final.
func (w *Worker) reportWorkspace(ctx context.Context, run string, r workspaceReport) error {
	return w.API.Retry(ctx, 4, "/runs/"+run+"/workspace", r, nil)
}

// Retry POSTs body up to attempts times with a growing pause while the failure is transient (network
// error, 5xx); 4xx answers are final. Retried requests must be idempotent.
func (a API) Retry(ctx context.Context, attempts int, path string, body, result any) error {
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		if err = a.Do(ctx, "POST", path, body, result); err == nil {
			return nil
		}
		var h *HTTPError
		if errors.As(err, &h) && h.Status < 500 {
			return err
		}
	}
	return err
}

// setEnv replaces or adds k in env.
func setEnv(env []string, k, v string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if name, _, _ := strings.Cut(e, "="); name != k {
			out = append(out, e)
		}
	}
	return append(out, k+"="+v)
}

// sibling resolves a Factory executable: path, else name next to this executable.
func sibling(path, name string) (string, error) {
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(executable), name)
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", fmt.Errorf("%s executable required; run make build", name)
	}
	return path, nil
}

// shims materializes the run's shim directory from the captured agent rules; "" when nothing is guarded.
func (w *Worker) shims(spec protocol.RunSpec) (string, error) {
	rules, err := snapshotRules(spec)
	if err != nil || len(policies.Guarded(rules)) == 0 {
		return "", err
	}
	binary, err := sibling(w.ShimBinary, "factory-shim")
	if err != nil {
		return "", err
	}
	if binary, err = filepath.Abs(binary); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "factory-shims-")
	if err != nil {
		return "", err
	}
	if _, err = shim.Install(dir, binary, rules); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// snapshotRules are the access rules captured in the run's agent snapshot.
func snapshotRules(spec protocol.RunSpec) ([]policies.Rule, error) {
	var agent struct {
		Rules []policies.Rule `json:"rules"`
	}
	if len(spec.ConfigSnapshot) > 0 {
		if err := json.Unmarshal(spec.ConfigSnapshot, &agent); err != nil {
			return nil, fmt.Errorf("invalid agent snapshot: %w", err)
		}
	}
	return agent.Rules, nil
}

// RuntimeEnv is the allowlist of an agent runtime's environment (explicit-exits-authority D9): only these
// variables of the node's environment reach the runtime, its tools and its shell; a trailing * matches a
// prefix. Nodes add names with node.yaml runtime_env (Worker.EnvAllow).
var RuntimeEnv = []string{
	// the user's session
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "TERM", "COLORTERM", "LANG", "LC_*", "TZ",
	"XDG_*", "SSH_AUTH_SOCK", "EDITOR", "VISUAL", "__CF_USER_TEXT_ENCODING",
	// runtimes and their providers
	"CODEX_*", "OPENAI_*", "ANTHROPIC_*", "CLAUDE_*", "AWS_*", "GOOGLE_APPLICATION_CREDENTIALS", "CLOUD_ML_REGION",
	// git, forges, certificates and proxies
	"GIT_*", "GH_*", "GITHUB_TOKEN", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy",
	// toolchains
	"DEVELOPER_DIR", "SDKROOT", "JAVA_HOME", "ANDROID_HOME", "ANDROID_SDK_ROOT", "GOPATH", "GOROOT", "GOCACHE",
	"GOMODCACHE", "GOFLAGS", "GOPROXY", "GOPRIVATE", "NVM_DIR", "HOMEBREW_PREFIX", "CARGO_HOME", "RUSTUP_HOME",
	"PYENV_ROOT", "VIRTUAL_ENV",
}

// runtimeDenied never reaches a runtime, whatever an allowlist says: the owner's and the node's credentials,
// the owner CLI's configuration (FACTORY_URL with FACTORY_TOKEN) and NATS. The run's own URL and token are
// added by runEnvironment.
var runtimeDenied = []string{"FACTORY_TOKEN", "FACTORY_NODE_TOKEN", "FACTORY_RUN_TOKEN", "FACTORY_URL", "FACTORY_TELEGRAM_TOKEN", "FACTORY_NODE_CONFIG", "NATS_*"}

// The runtime receives only a run-scoped Factory credential. Host access remains trusted.
func runEnvironment(env, allow []string, base, token string) []string {
	return append(nodeEnvironment(env, allow), "FACTORY_URL="+base, "FACTORY_RUN_TOKEN="+token)
}

// nodeEnvironment is the allowed part of env (RuntimeEnv and allow, never runtimeDenied): the base of every
// runtime environment, with or without a run credential.
func nodeEnvironment(env, allow []string) []string {
	match := func(list []string, k string) bool {
		for _, p := range list {
			if p == k || strings.HasSuffix(p, "*") && strings.HasPrefix(k, strings.TrimSuffix(p, "*")) {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(env)+2)
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if match(runtimeDenied, k) || !match(RuntimeEnv, k) && !match(allow, k) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// readExitState reads the adapter's exit file; "none" when it has not recorded an exit.
func readExitState(path string) factorytools.ExitState {
	var st factorytools.ExitState
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &st) != nil || st.State == "" {
		st.State = "none"
	}
	return st
}

// exitRecord is the exit file with the exit's commit. ExitSHA (`exit_sha`, the head of the accepted
// exit's facts) is read when the adapter writes it; otherwise the worker takes the worktree's HEAD when
// it first sees the exit accepted.
type exitRecord struct {
	factorytools.ExitState // ExitSHA (exit_sha) is the adapter's exit commit
}

// readExitRecord reads the exit file like readExitState, with the exit's commit when present.
func readExitRecord(path string) exitRecord {
	var r exitRecord
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &r) != nil || r.State == "" {
		return exitRecord{ExitState: factorytools.ExitState{State: "none"}}
	}
	return r
}

// ExitGuardRetries bounds the follow-up turns of a runtime that ends its turn without an exit.
var ExitGuardRetries = 2

// ExitGrace is how long a runtime may keep running after its exit was accepted.
var ExitGrace = 30 * time.Second

// ExitDeliveryWait bounds how long a runtime whose turn ended with its exit still pending (factory-mcp
// retrying delivery to an unreachable Core, docs/explicit-exits-contract.md §12) is kept alive before its
// process group is killed.
var ExitDeliveryWait = 60 * time.Second

// awaitExitDelivery returns at once unless the exit file says pending. Then it waits until the exit
// leaves pending (accepted, refused), no process named mcp is left in the runtime's group (factory-mcp
// exited or the group is gone: killing it cannot lose the delivery), ctx ends (a revoke, timeout or
// stop kills the runtime promptly) or ExitDeliveryWait passes. It returns the exit state it last read.
func awaitExitDelivery(ctx context.Context, run, path string, group int, mcp string) string {
	state := readExitState(path).State
	if state != "pending" {
		return state
	}
	if ctx.Err() != nil {
		slog.Warn("run stopped with its exit still pending delivery; not waiting", "run", run)
		return state
	}
	if !mcpRunning(group, mcp) {
		slog.Warn("exit still pending but factory-mcp is not in the runtime's group; not waiting", "run", run)
		return state
	}
	slog.Info("exit pending delivery to Core; keeping the runtime", "run", run, "wait", ExitDeliveryWait)
	deadline := time.NewTimer(ExitDeliveryWait)
	defer deadline.Stop()
	tick := time.NewTicker(exitPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Warn("run stopped while its exit was pending delivery", "run", run)
			return state
		case <-deadline.C:
			slog.Warn("exit still pending delivery; stopping the runtime", "run", run, "waited", ExitDeliveryWait)
			return state
		case <-tick.C:
			if state = readExitState(path).State; state != "pending" {
				slog.Info("pending exit settled", "run", run, "exit_state", state)
				return state
			}
			if !mcpRunning(group, mcp) {
				slog.Warn("factory-mcp exited with the exit still pending", "run", run)
				return state
			}
		}
	}
}

// mcpRunning reports whether a process whose command line names the executable mcp is in process
// group group. When ps fails the answer is unknown and it reports true (the wait stays bounded).
func mcpRunning(group int, mcp string) bool {
	if group <= 1 || mcp == "" || !procgroup.Alive(group) {
		return false
	}
	out, err := exec.Command("ps", "-A", "-o", "pgid=", "-o", "args=").Output()
	if err != nil {
		return true
	}
	id := strconv.Itoa(group)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != id {
			continue
		}
		for _, a := range f[1:] {
			if filepath.Base(a) == mcp {
				return true
			}
		}
	}
	return false
}

// exitPoll is how often the worker reads the exit file while the runtime runs.
var exitPoll = 250 * time.Millisecond

// watchExit polls the exit file: when it first says accepted, accepted is called (the worker notes the
// worktree's HEAD as the exit's commit) and, ExitGrace later, stop.
func watchExit(path string, done <-chan struct{}, accepted, stop func()) {
	tick := time.NewTicker(exitPoll)
	defer tick.Stop()
	var deadline <-chan time.Time
	for {
		select {
		case <-done:
			return
		case <-deadline:
			slog.Info("runtime still running after its accepted exit; stopping it")
			stop()
			return
		case <-tick.C:
			if deadline == nil && readExitState(path).State == "accepted" {
				accepted()
				deadline = time.After(ExitGrace)
			}
		}
	}
}
