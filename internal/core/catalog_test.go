package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/testnode"
)

// TestStoredCatalogs (company-self-service D1, D2, D4): Core seeds its own company's catalog from the
// repository, serves a second company's stored catalog side by side, swaps a saved version without a restart,
// and refuses a version the loader rejects or one saved on a stale base.
func TestStoredCatalogs(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	c.Handler()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	disk, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.cat(storage.DefaultCompany); len(got.Agents) != len(disk.Agents) || got.Agents["manager"].Instructions != disk.Agents["manager"].Instructions || c.catalogVersion(storage.DefaultCompany) != 1 {
		t.Fatalf("seed: %d agents, version %d", len(got.Agents), c.catalogVersion(storage.DefaultCompany))
	}
	if h, _ := s.CatalogHistory(ctx, storage.DefaultCompany); len(h) != 1 || h[0].Author != "seed" {
		t.Fatalf("%+v", h)
	}
	// A second start loads the stored version instead of seeding again.
	again := &Core{Store: s, Root: "../..", Token: "owner"}
	if err = again.LoadCatalogs(ctx); err != nil || again.catalogVersion(storage.DefaultCompany) != 1 {
		t.Fatal(err, again.catalogVersion(storage.DefaultCompany))
	}

	// Company acme: the smoke team under configs/acme/, with its own manager prompt.
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	smoke, err := config.SeedDocuments("../..", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	acme := map[string]string{}
	for p, content := range smoke {
		acme[strings.Replace(p, "configs/smoke/", "configs/acme/", 1)] = content
	}
	acme["prompts/manager.md"] = "ACME MANAGER PROMPT"
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Docs: acme, Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	if m := c.cat("acme").Agents["manager"]; !strings.HasPrefix(m.Instructions, "ACME MANAGER PROMPT") {
		t.Fatalf("acme manager: %q", m.Instructions)
	}
	if strings.HasPrefix(c.cat(storage.DefaultCompany).Agents["manager"].Instructions, "ACME") {
		t.Fatal("acme's prompt leaked into the default company")
	}
	if got := c.companies(); strings.Join(got, ",") != "acme,default" {
		t.Fatal(got)
	}

	// Apply without restart: the next version is the catalog at once; the old one stays in history.
	acme["prompts/manager.md"] = "ACME MANAGER V2"
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 1, Docs: acme, Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	if m := c.cat("acme").Agents["manager"]; !strings.HasPrefix(m.Instructions, "ACME MANAGER V2") || c.catalogVersion("acme") != 2 {
		t.Fatal(m.Instructions, c.catalogVersion("acme"))
	}

	// A version the loader rejects is not saved; a stale base conflicts.
	broken := map[string]string{}
	for p, content := range acme {
		broken[p] = content
	}
	broken["configs/acme/agents/manager.yaml"] = regexp.MustCompile(`extends: \S+`).ReplaceAllString(acme["configs/acme/agents/manager.yaml"], "extends: missing-profile")
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 2, Docs: broken, Author: "owner"}); !errors.Is(err, storage.ErrInput) || !strings.Contains(err.Error(), "missing-profile") || strings.Contains(err.Error(), "../") {
		t.Fatal("broken catalog saved:", err)
	}
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 1, Docs: acme, Author: "owner"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale base accepted:", err)
	}
	if c.catalogVersion("acme") != 2 {
		t.Fatal(c.catalogVersion("acme"))
	}
}

// TestCatalogAPI (company-self-service 3.5): the owner reads the catalog and a document, changes a prompt on
// top of the version it read, validates a broken change without saving, conflicts on a stale base, lists
// history and rolls back.
func TestCatalogAPI(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	h := c.Handler()
	if err = c.LoadCatalogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, out any) int {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, rd)
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if out != nil && w.Code == 200 {
			_ = json.Unmarshal(w.Body.Bytes(), out)
		}
		if w.Code != 200 {
			t.Log(method, path, w.Code, w.Body.String())
		}
		return w.Code
	}
	var view CatalogView
	if code := call("GET", "/catalog", nil, &view); code != 200 || view.Version != 1 || len(view.Documents) == 0 {
		t.Fatal(code, view)
	}
	var doc struct{ Content string }
	if code := call("GET", "/catalog/document?path=prompts/manager.md", nil, &doc); code != 200 || doc.Content == "" {
		t.Fatal(code)
	}
	if code := call("POST", "/catalog", CatalogChange{Base: 1, Put: map[string]string{"configs/agents/manager.yaml": "kind: nonsense\n"}, DryRun: true}, nil); code != 400 {
		t.Fatal("broken dry run accepted", code)
	}
	if code := call("POST", "/catalog", CatalogChange{Base: 1, Put: map[string]string{"prompts/manager.md": "NEW MANAGER"}, Note: "shorter"}, nil); code != 200 {
		t.Fatal(code)
	}
	if !strings.HasPrefix(c.cat(storage.DefaultCompany).Agents["manager"].Instructions, "NEW MANAGER") {
		t.Fatal("not applied")
	}
	if code := call("POST", "/catalog", CatalogChange{Base: 1, Put: map[string]string{"prompts/manager.md": "LOST"}}, nil); code != 409 {
		t.Fatal("stale base", code)
	}
	if code := call("POST", "/catalog", CatalogChange{Base: 2, Delete: []string{"configs/agents/nowhere.yaml"}}, nil); code != 400 {
		t.Fatal("unknown delete", code)
	}
	var history []storage.CatalogVersion
	if code := call("GET", "/catalog/history", nil, &history); code != 200 || len(history) != 2 || history[0].Note != "shorter" {
		t.Fatal(code, history)
	}
	if code := call("POST", "/catalog/rollback", map[string]any{"base": 2, "to": 1}, nil); code != 200 {
		t.Fatal(code)
	}
	if c.catalogVersion(storage.DefaultCompany) != 3 || strings.HasPrefix(c.cat(storage.DefaultCompany).Agents["manager"].Instructions, "NEW MANAGER") {
		t.Fatal("rollback not applied", c.catalogVersion(storage.DefaultCompany))
	}
}

// TestAgentCatalogChangeNeedsApproval (company-self-service 3.7): an agent reads the catalog freely; its
// factory_catalog_change is applied only after the owner approved that exact call, recorded with the agent as
// author.
func TestAgentCatalogChangeNeedsApproval(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	h.c.Root, h.c.Agents = "../..", nil
	if err := h.c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, w := h.create(map[string]any{"id": "team", "name": "Team", "workspace": "/tmp/team", "team": "core"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	task := h.readyTask("team", "manager", "tune the team")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ := h.s.Get(ctx, storage.DefaultCompany, h.task(task.ID).CurrentRunID)
	lease, err := testnode.Claim(t, h.s, storage.DefaultCompany, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := h.c.runToken(storage.DefaultCompany, run.Spec.ID, lease)
	call := func(c factorytools.Call) *httptest.ResponseRecorder { return h.http(token, "POST", "/agent/call", c) }
	var doc struct{ Content string }
	if w := call(factorytools.Call{Name: "factory_catalog_get", Arguments: map[string]any{"path": "prompts/demo.md"}}); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &doc) != nil || doc.Content == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	change := factorytools.Call{Name: "factory_catalog_change", Arguments: map[string]any{"base": float64(1), "put": map[string]any{"prompts/demo.md": "DEMO, TUNED BY THE MANAGER"}, "note": "clearer demo prompt", "request_id": "tune-demo"}}
	if w := call(change); w.Code != 423 || h.c.catalogVersion(storage.DefaultCompany) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call(factorytools.Call{Name: "factory_owner_request_approval", Arguments: map[string]any{"call": change, "reason": "the demo prompt is unclear", "blocking": false, "request_id": "ask-tune"}})
	var a approvals.Approval
	if json.Unmarshal(w.Body.Bytes(), &a); w.Code != 200 || a.Status != "PENDING" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = h.http("owner", "POST", "/approvals/"+a.ID+"/decision", approvals.Decision{Version: a.Version, Action: "approve", Reason: "ok", RequestID: "d-tune"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(change); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.HasPrefix(h.c.cat(storage.DefaultCompany).Agents["demo"].Instructions, "DEMO, TUNED BY THE MANAGER") {
		t.Fatal("not applied")
	}
	if hist, _ := h.s.CatalogHistory(ctx, storage.DefaultCompany); len(hist) != 2 || hist[0].Author != "manager" || hist[0].Note != "clearer demo prompt" {
		t.Fatalf("%+v", hist)
	}
}
