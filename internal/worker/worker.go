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
	"appfactory/internal/openspec"
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
	// StateDir keeps pidfiles of running runtimes; on start the worker kills the process
	// groups left there by its previous runs. It must not be shared by live workers.
	StateDir string
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
	emit := func(kind string, p protocol.Event) error {
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
	allocator := w.Allocator
	if allocator == nil {
		allocator = workspace.Local{}
	}
	allocation, err := allocator.Acquire(ctx, spec.Workspace)
	if err != nil {
		return err
	}
	defer allocation.Release()
	cwd := allocation.Path
	if spec.OpenSpecChange != "" {
		if _, e := (openspec.Access{}).CheckChange(cwd, spec.OpenSpecChange); e != nil {
			return emit("failed", protocol.Event{Status: "FAILED", Result: "OpenSpec preflight failed before runtime startup: " + e.Error()})
		}
	}

	// Heartbeats also carry cancellation. Failure stops execution instead of running without ownership.
	heartbeatCtx, stopHeartbeat := context.WithCancel(parent)
	defer stopHeartbeat()
	go func() {
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
	if withMCP {
		mcp := w.MCPBinary
		if mcp == "" {
			executable, e := os.Executable()
			if e != nil {
				return e
			}
			mcp = filepath.Join(filepath.Dir(executable), "factory-mcp")
		}
		if info, e := os.Stat(mcp); e != nil || info.IsDir() {
			return fmt.Errorf("factory-mcp executable required; run make build")
		}
		var credential struct {
			Token        string `json:"token"`
			Instructions string `json:"instructions"`
		}
		if err = w.API.Do(ctx, "POST", "/runs/"+spec.ID+"/credential", map[string]string{"lease": lease}, &credential); err != nil {
			return err
		}
		if credential.Token == "" || credential.Instructions == "" {
			return fmt.Errorf("missing run credential")
		}
		launch.MCP = &driver.MCPConfig{Command: mcp, EnvVars: []string{"FACTORY_RUN_TOKEN", "FACTORY_URL"}}
		launch.Env = runEnvironment(os.Environ(), w.API.URL, credential.Token)
		launch.Instructions += credential.Instructions
	}
	session, err := drv.Launch(ctx, launch)
	if err != nil {
		return err
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
			if err = emit("progress", protocol.Event{Method: e.Method, Data: openspec.AuditData(e.Data)}); err != nil {
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
