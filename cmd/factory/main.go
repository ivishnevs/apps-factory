package main

import (
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/openspec"
	"appfactory/internal/policies"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("factory", "error", err)
		os.Exit(1)
	}
}
func run() error {
	args := os.Args[1:]
	if len(args) == 0 {
		return fmt.Errorf("usage: factory projects/tasks (see README) | status [PROJECT] | watch [PROJECT] [--attention] | log TASK | show TASK (--json) | pause|resume --all|project P|agent A [REASON] | pauses | stop [REASON] | start | pending | escalations [--all] | direct TASK TEXT [--urgent] | abandon-branch TASK | rules company|PROJECT [TEXT|-] | time-zone [ZONE] | answer MSG TEXT|OPTION | approve|reject ID REASON | serve | config validate | agents list | doctor | runs list | runs get ID | runs events ID | runs cancel ID | runs create PROJECT AGENT ABS_WORKSPACE PROMPT [SESSION_ID] | runs resolve ID STATUS EVIDENCE | " + nodesUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := env("FACTORY_ROOT", ".")
	if args[0] == "config" || args[0] == "agents" {
		c, err := config.LoadCatalog(root)
		if err != nil {
			return err
		}
		if len(args) == 2 && args[0] == "config" && args[1] == "validate" {
			return printJSON(map[string]any{"agents": config.Names(c.Agents), "profiles": len(c.Profiles), "policies": len(c.Policies), "skills": len(c.Skills.Skills), "node_hooks": len(c.NodeHooks)})
		}
		if len(args) == 2 && args[0] == "agents" && args[1] == "list" {
			return printJSON(config.Names(c.Agents))
		}
		if len(args) == 3 && args[0] == "agents" && args[1] == "get" {
			a, ok := c.Agents[args[2]]
			if !ok {
				return fmt.Errorf("unknown agent")
			}
			return printJSON(a)
		}
		if len(args) == 4 && args[0] == "agents" && args[1] == "authorize" {
			a, ok := c.Agents[args[2]]
			if !ok {
				return fmt.Errorf("unknown agent")
			}
			// The effective recovery mode is shown along: it is per agent, not per operation.
			return printJSON(struct {
				policies.Decision
				Recovery string `json:"recovery"`
			}{a.Authorize(args[3]), a.RecoveryMode()})
		}
		return fmt.Errorf("usage: config validate | agents list | agents get NAME | agents authorize NAME OPERATION")
	}

	if os.Getenv("FACTORY_TOKEN") == "" {
		return fmt.Errorf("FACTORY_TOKEN required")
	}
	api := worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: os.Getenv("FACTORY_TOKEN")}
	if args[0] == "conversations" || args[0] == "messages" || args[0] == "inbox" || args[0] == "approvals" {
		return communicationCommand(ctx, api, args)
	}
	if args[0] == "openspec" {
		return openSpecCommand(ctx, api, args)
	}
	if args[0] == "reports" || args[0] == "decisions" {
		return journalCommand(ctx, api, args)
	}
	if args[0] == "artifacts" {
		return artifactCommand(ctx, api, args)
	}
	if args[0] == "nodes" {
		return nodesCommand(ctx, api, args)
	}
	switch args[0] {
	case "pause", "resume", "pauses", "stop", "start", "pending", "answer", "approve", "reject", "escalations", "direct", "abandon-branch", "rules", "time-zone":
		return ownerCommand(ctx, api, args)
	}
	if args[0] == "status" || args[0] == "watch" || args[0] == "log" || args[0] == "show" {
		return consoleCommand(ctx, api, args)
	}
	if args[0] == "projects" || args[0] == "tasks" {
		return engineCommand(ctx, api, args)
	}
	if args[0] == "skills" {
		if len(args) < 3 {
			return fmt.Errorf("usage: skills list AGENT | skills search AGENT QUERY [CATEGORY] | skills read AGENT SKILL [RESOURCE]")
		}
		path := "/agents/" + url.PathEscape(args[2]) + "/skills"
		switch args[1] {
		case "list":
			if len(args) != 3 {
				return fmt.Errorf("skills list AGENT")
			}
		case "search":
			if len(args) < 4 || len(args) > 5 {
				return fmt.Errorf("skills search AGENT QUERY [CATEGORY]")
			}
			q := url.Values{"q": []string{args[3]}}
			if len(args) == 5 {
				q.Set("category", args[4])
			}
			path += "?" + q.Encode()
		case "read":
			if len(args) < 4 || len(args) > 5 {
				return fmt.Errorf("skills read AGENT SKILL [RESOURCE]")
			}
			path += "/" + url.PathEscape(args[3])
			if len(args) == 5 {
				path += "?resource=" + url.QueryEscape(args[4])
			}
		default:
			return fmt.Errorf("unknown skills command")
		}
		var v any
		if err := api.Do(ctx, "GET", path, nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	}

	if args[0] == "doctor" {
		var v any
		if err := api.Do(ctx, "GET", "/health", nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	}
	if args[0] == "runs" && len(args) > 1 {
		method, path := "GET", "/runs"
		var body any
		switch args[1] {
		case "list":
		case "get", "events", "cancel":
			if len(args) != 3 {
				return fmt.Errorf("run ID required")
			}
			path += "/" + args[2]
			if args[1] == "events" {
				path += "/events"
			}
			if args[1] == "cancel" {
				method = "POST"
				path += "/cancel"
			}
		case "resolve":
			if len(args) != 5 {
				return fmt.Errorf("ID STATUS EVIDENCE required")
			}
			method = "POST"
			path += "/" + args[2] + "/resolve"
			body = map[string]string{"status": args[3], "result": args[4]}
		case "create":
			if len(args) < 6 || len(args) > 7 {
				return fmt.Errorf("PROJECT AGENT ABS_WORKSPACE PROMPT [SESSION_ID] required")
			}
			method = "POST"
			p := map[string]string{"project": args[2], "agent": args[3], "workspace": args[4], "prompt": args[5]}
			if len(args) == 7 {
				p["session_id"] = args[6]
			}
			body = p
		default:
			return fmt.Errorf("unknown runs command")
		}
		var v any
		if err := api.Do(ctx, method, path, body, &v); err != nil {
			return err
		}
		return printJSON(v)
	}
	if args[0] != "serve" {
		return fmt.Errorf("unknown command")
	}
	catalog, err := config.LoadCatalog(root)
	if err != nil {
		return err
	}
	dbPath := env("FACTORY_DB", "data/factory.db")
	if err = os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return err
	}
	s, err := storage.Open(dbPath)
	if err != nil {
		return err
	}
	defer s.DB.Close()
	s.OpenSpec = openspec.Access{HostRoot: os.Getenv("FACTORY_WORKSPACE_HOST_ROOT"), LocalRoot: os.Getenv("FACTORY_WORKSPACE_LOCAL_ROOT")}
	b, err := bus.Connect(env("NATS_URL", "nats://127.0.0.1:4222"))
	if err != nil {
		return err
	}
	defer b.NC.Close()
	c := core.Core{Store: s, Bus: b, Agents: catalog.Agents, Skills: catalog.Skills, NodeHooks: catalog.NodeHooks, Token: os.Getenv("FACTORY_TOKEN"), ExplicitExits: os.Getenv("FACTORY_EXPLICIT_EXITS") == "1"}
	server := &http.Server{Addr: env("FACTORY_LISTEN", "127.0.0.1:8080"), Handler: c.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- c.Run(ctx) }()
	go func() { errs <- server.ListenAndServe() }()
	slog.Info("core ready", "address", server.Addr)
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
