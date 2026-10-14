package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/driver"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/shim"
	"appfactory/internal/openspec"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
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
	var e protocol.Envelope
	var spec protocol.RunSpec
	if json.Unmarshal(m.Data, &e) != nil || e.Version != 1 || e.Type != "agent.run.requested" || json.Unmarshal(e.Payload, &spec) != nil || spec.ID == "" || spec.ID != e.CorrelationID {
		_ = m.Term()
		slog.Error("invalid run command")
		return nil
	}
	// Fetch authoritative input from Core, then atomically claim before runtime startup.
	var saved storage.Run
	if err := w.API.Do(ctx, "GET", "/runs/"+spec.ID, nil, &saved); err != nil {
		_ = m.NakWithDelay(time.Second)
		return nil
	}
	spec = saved.Spec
	var claim struct {
		Lease string `json:"lease"`
	}
	if err := w.API.Do(ctx, "POST", "/runs/"+spec.ID+"/claim", nil, &claim); err != nil {
		var h *HTTPError
		if errors.As(err, &h) && h.Status == 409 {
			var r storage.Run
			if w.API.Do(ctx, "GET", "/runs/"+spec.ID, nil, &r) == nil && r.Status != "QUEUED" {
				_ = m.AckSync()
				return nil
			}
		}
		_ = m.NakWithDelay(time.Second)
		return nil
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
	ctx, cancel := context.WithTimeout(parent, time.Duration(spec.TimeoutSeconds)*time.Second)
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
	var wt *workspace.Worktree
	var req workspace.Request
	sealed := false
	// seal stops the run's process group and snapshots the worktree once, before the terminal event.
	seal := func() {
		if wt == nil || sealed {
			return
		}
		sealed = true
		if session != nil {
			session.Close()
		}
		if err := wt.Stop(); err != nil {
			slog.Error("worktree run group", "run", spec.ID, "error", err)
		}
		sealCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		res, err := w.Worktrees.Seal(sealCtx, req)
		if err != nil {
			slog.Error("worktree seal", "run", spec.ID, "task", req.Task, "path", wt.Path, "error", err)
			return
		}
		slog.Info("worktree sealed", "run", spec.ID, "task", req.Task, "path", wt.Path, "branch", wt.Branch, "sealed_head", res.SealedHead, "wip_sha", res.WIPSHA)
		// The WIP ref stays local (task-worktrees D3); Core records the sealed head. A failed report is logged only.
		report := workspaceReport{Lease: lease, Mode: "worktree", Branch: wt.Branch, NodeID: w.NodeID, SealedHead: res.SealedHead, SealedAt: res.SealedAt.Unix()}
		if err := w.reportWorkspace(sealCtx, spec.ID, report); err != nil {
			slog.Error("workspace report", "run", spec.ID, "task", req.Task, "error", err)
		}
	}
	emit := func(kind string, p protocol.Event) error {
		if p.Status != "" {
			seal()
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
	// Errors after claiming are conservatively unknown; only an explicit turn result is terminal evidence.
	defer func() {
		if runErr != nil {
			if err := emit("failed", protocol.Event{Status: "UNKNOWN", Result: runErr.Error()}); err != nil {
				slog.Error("terminal event publication failed; lease recovery required", "error", err)
			}
		}
	}()
	drv, withMCP := w.driver()
	if spec.Provider != drv.Name() || spec.TimeoutSeconds < 1 {
		return fmt.Errorf("invalid RunSpec")
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
			return emit("failed", protocol.Event{Status: "FAILED", Result: diverged.Error()})
		}
		if err != nil {
			return fmt.Errorf("worktree: %w", err)
		}
		stopped = t.Path
		wt, req, cwd = &t, r, t.Path
		defer func() {
			seal()
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
			return err
		}
		defer allocation.Release()
		cwd = allocation.Path
	}
	if spec.OpenSpecChange != "" {
		if _, e := (openspec.Access{}).CheckChange(cwd, spec.OpenSpecChange); e != nil {
			return emit("failed", protocol.Event{Status: "FAILED", Result: "OpenSpec preflight failed before runtime startup: " + e.Error()})
		}
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
					cancel()
					return
				}
				if res.Cancel {
					cancel()
					return
				}
			}
		}
	}()
	launch := driver.Launch{Spec: spec, Cwd: cwd, Instructions: spec.Instructions}
	shimDir, err := w.shims(spec)
	if err != nil {
		return err
	}
	if shimDir != "" {
		defer os.RemoveAll(shimDir)
		launch.Guards, _ = snapshotRules(spec) // parsed by shims
	}
	if withMCP || shimDir != "" {
		// Approval shims ask Core with the run credential even when the runtime has no Factory MCP.
		var credential struct {
			Token        string `json:"token"`
			Instructions string `json:"instructions"`
		}
		if err := w.API.Do(ctx, "POST", "/runs/"+spec.ID+"/credential", map[string]string{"lease": lease}, &credential); err != nil {
			return err
		}
		if credential.Token == "" || credential.Instructions == "" {
			return fmt.Errorf("missing run credential")
		}
		launch.Env = runEnvironment(os.Environ(), w.API.URL, credential.Token)
		if withMCP {
			mcp, e := sibling(w.MCPBinary, "factory-mcp")
			if e != nil {
				return e
			}
			launch.MCP = &driver.MCPConfig{Command: mcp, EnvVars: []string{"FACTORY_RUN_TOKEN", "FACTORY_URL"}}
			launch.Instructions += credential.Instructions
		}
		if shimDir != "" {
			launch.Env = shim.Env(launch.Env, shimDir)
		}
	}
	if wt != nil {
		env := launch.Env
		if env == nil {
			env = os.Environ()
		}
		launch.Env = setEnv(env, "FACTORY_SCRATCH", wt.Scratch)
	}
	session, err = drv.Launch(ctx, launch)
	if err != nil {
		return err
	}
	if group := session.ProcessGroup(); wt != nil && group > 0 {
		if err = wt.Track(group); err != nil {
			session.Close()
			return err
		}
	}
	if group := session.ProcessGroup(); w.StateDir != "" && group > 0 {
		path, e := writePidfile(w.StateDir, spec.ID, group)
		if e != nil {
			session.Close()
			return e
		}
		defer os.Remove(path)
	}
	defer session.Close()
	if v2 {
		// Phase 2 of the claim: workspace allocated and runtime launched (protocol v2 §6). Start is retry-safe.
		if err = w.API.Retry(ctx, 8, "/runs/"+spec.ID+"/start", map[string]string{"lease": lease}, nil); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		w.Inventory.Set(spec.ID, node.RunRunning)
	}
	if err = emit("started", protocol.Event{SessionID: session.ID()}); err != nil {
		return err
	}
	if err = session.Start(ctx); err != nil {
		return err
	}
	events := session.Events()
	cancelled := ctx.Done()
	var interruptTimeout <-chan time.Time
	for events != nil {
		select {
		case <-cancelled:
			cancelled = nil
			interruptCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err = session.Interrupt(interruptCtx)
			stop()
			if err != nil {
				return fmt.Errorf("interrupt: %w", err)
			}
			// Acknowledgement only means interruption requested. Wait for terminal evidence.
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			interruptTimeout = timer.C
		case <-interruptTimeout:
			return fmt.Errorf("interrupt result unknown")
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
	if result.Err != nil {
		return result.Err
	}
	return emit("completed", protocol.Event{Status: result.Status, Result: result.Text})
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
func (w *Worker) worktreeRequest(spec protocol.RunSpec) (workspace.Request, bool) {
	if spec.WorkspaceMode != "worktree" {
		return workspace.Request{}, false
	}
	if w.Worktrees == nil || spec.TaskID == "" || spec.RepoURL == "" {
		slog.Warn("worktree run in the project workspace", "run", spec.ID, "worktrees", w.Worktrees != nil, "task", spec.TaskID, "repo_url", spec.RepoURL != "")
		return workspace.Request{}, false
	}
	// RunSpec has no company yet; the node serves the default company.
	return workspace.Request{Company: storage.DefaultCompany, Project: spec.Project, Task: spec.TaskID, RepoURL: spec.RepoURL,
		DefaultBranch: spec.DefaultBranch, Branch: workspace.BranchName(spec.TaskID, "")}, true
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

// The runtime receives only a run-scoped Factory credential. Host access remains trusted.
func runEnvironment(env []string, base, token string) []string {
	out := make([]string, 0, len(env)+2)
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "FACTORY_TOKEN", "FACTORY_RUN_TOKEN", "FACTORY_URL", "NATS_URL", "NATS_PASSWORD":
			continue
		}
		out = append(out, v)
	}
	return append(out, "FACTORY_URL="+base, "FACTORY_RUN_TOKEN="+token)
}
