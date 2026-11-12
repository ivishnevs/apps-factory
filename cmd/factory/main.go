package main

import (
	"appfactory/internal/auth"
	"appfactory/internal/blobstore"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/policies"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
		return fmt.Errorf("usage: factory " + uiUsage + " | " + webUsage + " | projects/tasks (see README) | status [PROJECT] | watch [PROJECT] [--attention] | log TASK | show TASK (--json) | pause|resume --all|--project P|--agent A|--quota-key K|--node N [REASON] (scopes; one task: tasks pause|resume ID VERSION) | pauses | stop [REASON] | start | pending | comment TASK TEXT [--urgent] | run|retry TASK | costs [--task T|--agent A|--day|--model] [--since 24h] | telegram bind | escalations [--all] | direct TASK TEXT [--urgent] | abandon-branch TASK | rules company|PROJECT [TEXT|-] | time-zone [ZONE] | answer MSG TEXT|OPTION | approve|reject ID REASON | serve | config validate [--company C] | agents list | catalog [history|get|put|rm|validate|rollback|pull DIR|push DIR] | companies list|create ID TEMPLATE [NAME]|plan ID free|pro|unlimited | templates | owner-tokens list|create NAME|revoke ID | doctor | runs list | runs get ID | runs events ID | runs cancel ID | runs create PROJECT AGENT ABS_WORKSPACE PROMPT [SESSION_ID] | runs resolve ID continue|retry|cancel EVIDENCE | " + nodesUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := env("FACTORY_ROOT", ".")
	// FACTORY_COMPANY is the company this Core (and the catalog commands) serve; --company overrides it.
	company := env("FACTORY_COMPANY", storage.DefaultCompany)
	if i := slices.Index(args, "--company"); i >= 0 && i+1 < len(args) && len(args) > 2 {
		company = args[i+1]
		args = slices.Delete(args, i, i+2)
	}
	if args[0] == "config" || args[0] == "agents" {
		c, err := config.LoadCompanyCatalog(root, company)
		if err != nil {
			return err
		}
		if len(args) == 2 && args[0] == "config" && args[1] == "validate" {
			return printJSON(map[string]any{"company": company, "agents": config.Names(c.Agents), "profiles": len(c.Profiles), "policies": len(c.Policies), "skills": len(c.Skills.Skills), "node_hooks": len(c.NodeHooks)})
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

	if args[0] == "ui" {
		// The terminal UI; --demo and --fixture need no Core and no token.
		return uiCommand(ctx, args[1:])
	}
	if args[0] == "web" {
		// The web UI; --demo and --fixture need no Core and no token.
		return webCommand(ctx, args[1:])
	}
	if os.Getenv("FACTORY_TOKEN") == "" {
		return fmt.Errorf("FACTORY_TOKEN required")
	}
	api := worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: os.Getenv("FACTORY_TOKEN")}
	if args[0] == "conversations" || args[0] == "messages" || args[0] == "inbox" || args[0] == "approvals" {
		return communicationCommand(ctx, api, args)
	}
	if args[0] == "skills" && len(args) > 1 && args[1] != "list" && args[1] != "search" && args[1] != "read" {
		return skillsCommand(ctx, api, args)
	}
	if args[0] == "secrets" {
		return secretsCommand(ctx, api, args, os.Stdin)
	}
	if args[0] == "companies" || args[0] == "templates" || args[0] == "owner-tokens" {
		return companiesCommand(ctx, api, args)
	}
	if args[0] == "catalog" {
		return catalogCommand(ctx, api, args)
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
	switch args[0] {
	case "comment", "run", "retry", "telegram":
		return ownerTaskCommand(ctx, api, args)
	case "costs":
		return costsCommand(ctx, api, args)
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
			if len(args) != 5 || !slices.Contains(storage.ResolveAnswers, args[3]) {
				return fmt.Errorf("ID continue|retry|cancel EVIDENCE required")
			}
			method = "POST"
			path += "/" + args[2] + "/resolve"
			body = map[string]string{"answer": args[3], "result": args[4]}
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
	var err error
	databaseURL := os.Getenv("FACTORY_DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("FACTORY_DATABASE_URL required (postgres://user:password@host:5432/factory)")
	}
	// Core keeps no database file; its data directory holds only the incarnation copy (restore detection).
	dataDir := env("FACTORY_DATA_DIR", "data")
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	// Project contents (OpenSpec documents, artifact versions, attachments, run logs) live in the blob volume
	// (core-owned-project-data D1); Core reads no node's files and needs no workspace mount.
	blobDir := env("FACTORY_BLOB_DIR", filepath.Join(dataDir, "blobs"))
	if err = os.MkdirAll(blobDir, 0700); err != nil {
		return err
	}
	s, err := storage.OpenWith(databaseURL, storage.Options{IncarnationFile: filepath.Join(dataDir, "core.incarnation"), Blobs: blobstore.LocalFS{Root: blobDir}})
	if err != nil {
		return err
	}
	if err = s.EnsureCompany(ctx, company); err != nil {
		return fmt.Errorf("company %s: %w", company, err)
	}
	// Company secrets (core-secret-store D1): the master key from the environment must open every company key.
	keys, err := secrets.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if keys != nil {
		s.Keys = keys
	}
	if err = s.CheckMasterKey(ctx); err != nil {
		return err
	}
	// Artifact retention ceiling (artifact-retention): the hosted cloud sets 30; unset or 0 sets none.
	if v := os.Getenv("FACTORY_ARTIFACT_RETENTION_MAX_DAYS"); v != "" {
		if s.ArtifactRetentionMaxDays, err = strconv.Atoi(v); err != nil || s.ArtifactRetentionMaxDays < 0 {
			return fmt.Errorf("FACTORY_ARTIFACT_RETENTION_MAX_DAYS must be a number of days (0: no ceiling)")
		}
	}
	if keys == nil {
		slog.Warn("no " + secrets.MasterKeyEnv + ": company secrets can be neither set nor delivered")
	}
	defer s.DB.Close()
	prices, err := config.LoadPrices(root)
	if err != nil {
		return err
	}
	// Catalogs live in storage (company-self-service D1, D4): every company's current version, the own company
	// seeded from the repository on its first start. Explicit exits are on unless FACTORY_EXPLICIT_EXITS=0: without
	// them review rules (required review, default reviewers) do not apply to runs.
	c := core.Core{Store: s, Company: company, Root: root, Token: os.Getenv("FACTORY_TOKEN"), ExplicitExits: os.Getenv("FACTORY_EXPLICIT_EXITS") != "0",
		Prices: prices, Telegram: core.TelegramConfig{Project: os.Getenv("FACTORY_TELEGRAM_PROJECT"), Token: os.Getenv("FACTORY_TELEGRAM_TOKEN"),
			DigestAt: env("FACTORY_TELEGRAM_DIGEST_AT", "18:00"), Poll: os.Getenv("FACTORY_TELEGRAM_POLL") == "1"}}
	if c.Auth, err = signIn(); err != nil {
		return err
	}
	c.MultiCompany, c.SignupTemplate = os.Getenv("FACTORY_MULTI_COMPANY") == "1", os.Getenv("FACTORY_SIGNUP_TEMPLATE")
	// node-install: the connection page, Core's address for nodes and the node packages.
	c.PublicURL = env("FACTORY_PUBLIC_URL", "http://127.0.0.1:3000")
	c.NodeURL, c.NodeDownloads = os.Getenv("FACTORY_CORE_URL"), os.Getenv("FACTORY_NODE_DOWNLOADS")
	if c.Billing, err = billingConfig(os.Getenv); err != nil {
		return err
	}
	if err = c.LoadCatalogs(ctx); err != nil {
		return err
	}
	// FACTORY_LISTEN, else PORT on every interface (Railway and similar hosts), else loopback.
	listen := os.Getenv("FACTORY_LISTEN")
	if listen == "" && os.Getenv("PORT") != "" {
		listen = "0.0.0.0:" + os.Getenv("PORT")
	}
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: listen, Handler: withWebAPI(&c, os.Getenv("FACTORY_WEB_LOCAL") == "1"), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- c.Run(ctx) }()
	// Company bots (telegram-per-company) and FACTORY_TELEGRAM_TOKEN for the default company; tokens are never logged.
	go c.RunTelegram(ctx)
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

// signIn configures the website sign-in (user-accounts D3) from FACTORY_OIDC_*: off without an issuer.
func signIn() (*auth.Client, error) {
	issuer := os.Getenv("FACTORY_OIDC_ISSUER")
	if issuer == "" {
		return nil, nil
	}
	public := strings.TrimRight(env("FACTORY_PUBLIC_URL", "http://127.0.0.1:3000"), "/")
	key := hmac.New(sha256.New, []byte(os.Getenv("FACTORY_TOKEN")))
	key.Write([]byte("factory login cookie"))
	var providers []string
	for _, p := range strings.Split(env("FACTORY_OIDC_PROVIDERS", "google,github"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			providers = append(providers, p)
		}
	}
	a, err := auth.New(auth.Config{Issuer: issuer, InternalURL: os.Getenv("FACTORY_OIDC_INTERNAL_URL"), ClientID: env("FACTORY_OIDC_CLIENT_ID", "factory-core"),
		ClientSecret: os.Getenv("FACTORY_OIDC_CLIENT_SECRET"), RedirectURL: public + "/auth/callback", Providers: providers,
		CookieKey: key.Sum(nil), Secure: strings.HasPrefix(public, "https://")})
	if err != nil {
		return nil, err
	}
	slog.Info("sign-in on", "issuer", issuer, "providers", providers, "public_url", public)
	return a, nil
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
