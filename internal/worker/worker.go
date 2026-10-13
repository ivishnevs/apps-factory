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
	"strconv"
	"strings"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
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
	Binary    string
	Args      []string
	MCPBinary string
}

func (w *Worker) Run(ctx context.Context) error {
	sub, err := w.Bus.Subscribe("factory.commands.agent.run.codex", "codex-worker-v1")
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
			var e protocol.Envelope
			var spec protocol.RunSpec
			if json.Unmarshal(m.Data, &e) != nil || e.Version != 1 || e.Type != "agent.run.requested" || json.Unmarshal(e.Payload, &spec) != nil || spec.ID == "" || spec.ID != e.CorrelationID {
				_ = m.Term()
				slog.Error("invalid run command")
				continue
			}
			// Fetch authoritative input from Core, then atomically claim before runtime startup.
			var saved storage.Run
			if err = w.API.Do(ctx, "GET", "/runs/"+spec.ID, nil, &saved); err != nil {
				_ = m.NakWithDelay(time.Second)
				continue
			}
			spec = saved.Spec
			var claim struct {
				Lease string `json:"lease"`
			}
			if err = w.API.Do(ctx, "POST", "/runs/"+spec.ID+"/claim", nil, &claim); err != nil {
				var h *HTTPError
				if errors.As(err, &h) && h.Status == 409 {
					var r storage.Run
					if w.API.Do(ctx, "GET", "/runs/"+spec.ID, nil, &r) == nil && r.Status != "QUEUED" {
						_ = m.AckSync()
						continue
					}
				}
				_ = m.NakWithDelay(time.Second)
				continue
			}
			if err = m.AckSync(); err != nil {
				return err
			} // Core's lease now owns recovery, not redelivery.
			slog.Info("run claimed", "run", spec.ID)
			if err = w.execute(ctx, spec, claim.Lease, e.ID); err != nil {
				slog.Error("runtime run", "run", spec.ID, "error", err)
			}
		}
	}
	return nil
}
func (w *Worker) execute(parent context.Context, spec protocol.RunSpec, lease, cause string) (runErr error) {
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
	if spec.Provider != protocol.RuntimeCodex || spec.TimeoutSeconds < 1 {
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
	// Runtime has its own lifetime so cancellation can first send turn/interrupt.
	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()
	args := w.Args
	var runtimeEnv []string
	var factoryInstructions string
	withMCP := args == nil || w.MCPBinary != ""
	if args == nil {
		args = []string{"app-server", "--listen", "stdio://", "-c", "model_provider=\"openai\""}
	}
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
		args = append(append([]string{}, args...), "-c", "mcp_servers.factory.command="+strconv.Quote(mcp), "-c", `mcp_servers.factory.env_vars=["FACTORY_RUN_TOKEN","FACTORY_URL"]`, "-c", "mcp_servers.factory.required=true", "-c", "mcp_servers.factory.enabled=true")
		runtimeEnv = runEnvironment(os.Environ(), w.API.URL, credential.Token)
		factoryInstructions = credential.Instructions
	}
	client, err := rpc.StartEnv(runtimeCtx, w.Binary, args, runtimeEnv)
	if err != nil {
		return err
	}
	defer client.Close()
	call := func(method string, params, result any) error {
		callCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		return client.Call(callCtx, method, params, result)
	}
	if err = call("initialize", map[string]any{"clientInfo": map[string]string{"name": "appfactory", "title": "App Factory", "version": "0.1.0"}}, nil); err != nil {
		return err
	}
	if err = client.Notify("initialized", map[string]any{}); err != nil {
		return err
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = call("account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil || account.Account.Type != "chatgpt" {
		return fmt.Errorf("ChatGPT login required; API-key auth is not supported")
	}
	instructions := spec.Instructions
	if withMCP {
		instructions += factoryInstructions
	}
	params := map[string]any{"cwd": cwd, "approvalPolicy": "never", "sandbox": "danger-full-access", "developerInstructions": instructions}
	if model, ok := spec.RuntimeOptions["model"].(string); ok && model != "" && model != "default" {
		params["model"] = model
	}
	method := "thread/start"
	if spec.SessionID != "" {
		method = "thread/resume"
		params["threadId"] = spec.SessionID
		params["excludeTurns"] = true
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err = call(method, params, &thread); err != nil {
		return err
	}
	if thread.Thread.ID == "" {
		return fmt.Errorf("missing thread ID")
	}
	threadID := thread.Thread.ID
	if err = emit("started", protocol.Event{SessionID: threadID}); err != nil {
		return err
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err = call("turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": spec.Prompt, "text_elements": []any{}}}}, &turn); err != nil {
		return err
	}
	if turn.Turn.ID == "" {
		return fmt.Errorf("missing turn ID")
	}
	var result strings.Builder
	for {
		select {
		case <-ctx.Done():
			interruptCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err = client.Call(interruptCtx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turn.Turn.ID}, nil)
			stop()
			if err != nil {
				return fmt.Errorf("interrupt: %w", err)
			}
			// Acknowledgement only means interruption requested. Wait for terminal evidence.
			return w.waitInterrupted(client, emit, &result)
		case <-client.Done():
			return fmt.Errorf("app-server disconnected: %w", client.Err())
		case m := <-client.Events:
			if err = emit("progress", protocol.Event{Method: m.Method, Data: openspec.AuditData(m.Params)}); err != nil {
				return err
			}
			if len(m.ID) > 0 {
				switch m.Method {
				case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
					err = client.Respond(m.ID, map[string]string{"decision": "decline"}, nil)
				default:
					_ = client.Respond(m.ID, nil, map[string]any{"code": -32601, "message": "Foundation cannot service this request; owner intervention required"})
					return fmt.Errorf("server request requires owner: %s", m.Method)
				}
				if err != nil {
					return err
				}
				continue
			}
			switch m.Method {
			case "item/completed":
				var p struct {
					Item struct{ Type, Text string } `json:"item"`
				}
				if json.Unmarshal(m.Params, &p) == nil && p.Item.Type == "agentMessage" {
					if result.Len() > 0 {
						result.WriteString("\n")
					}
					result.WriteString(p.Item.Text)
				}
			case "turn/completed":
				return finish(m, emit, result.String())
			}
		}
	}
}
func finish(m rpc.Message, emit func(string, protocol.Event) error, result string) error {
	var p struct {
		Turn struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return err
	}
	status := ""
	switch p.Turn.Status {
	case "completed":
		status = "COMPLETED"
	case "failed":
		status = "FAILED"
		result += "\n" + string(p.Turn.Error)
	case "interrupted":
		status = "CANCELLED"
	default:
		return fmt.Errorf("unknown turn status %q", p.Turn.Status)
	}
	return emit("completed", protocol.Event{Status: status, Result: result})
}
func (w *Worker) waitInterrupted(c *rpc.Client, emit func(string, protocol.Event) error, result *strings.Builder) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return fmt.Errorf("interrupt result unknown")
		case <-c.Done():
			return c.Err()
		case m := <-c.Events:
			if err := emit("progress", protocol.Event{Method: m.Method, Data: openspec.AuditData(m.Params)}); err != nil {
				return err
			}
			if len(m.ID) > 0 {
				_ = c.Respond(m.ID, nil, map[string]any{"code": -32601, "message": "run interrupted"})
			}
			if m.Method == "turn/completed" {
				return finish(m, emit, result.String())
			}
		}
	}
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
