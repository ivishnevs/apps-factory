package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/worker"
)

// TestCatalogCLI (company-self-service 3.6): pull the stored catalog into a directory, edit a prompt and
// delete a node hooks document, push it as the next version; a stale push conflicts; get, history and
// rollback work.
func TestCatalogCLI(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Root: "../..", Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	api := worker.API{URL: server.URL, Token: "owner"}
	cli := func(args ...string) (string, error) {
		var err error
		out := capture(t, func() error { err = catalogCommand(ctx, api, append([]string{"catalog"}, args...)); return nil })
		return out, err
	}
	dir := filepath.Join(t.TempDir(), "catalog")
	if out, err := cli("pull", dir); err != nil || !strings.Contains(out, `"version": 1`) {
		t.Fatal(out, err)
	}
	prompt := filepath.Join(dir, "prompts", "manager.md")
	if err = os.WriteFile(prompt, []byte("PUSHED MANAGER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks, _ := filepath.Glob(filepath.Join(dir, "configs", "node-hooks", "*.yaml"))
	for _, h := range hooks {
		_ = os.Remove(h)
	}
	stale := filepath.Join(t.TempDir(), "stale")
	if _, err = cli("pull", stale); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("push", dir, "edit manager"); err != nil || !strings.Contains(out, `"version": 2`) {
		t.Fatal(out, err)
	}
	got, err := cli("get", "prompts/manager.md")
	if err != nil || got != "PUSHED MANAGER\n" {
		t.Fatalf("%q %v", got, err)
	}
	if len(hooks) > 0 {
		if _, err = cli("get", "configs/node-hooks/"+filepath.Base(hooks[0])); err == nil {
			t.Fatal("deleted document still there")
		}
	}
	if _, err = cli("push", stale); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatal("stale push accepted", err)
	}
	if out, err := cli("history"); err != nil || !strings.Contains(out, "edit manager") {
		t.Fatal(out, err)
	}
	if _, err = cli("rollback", "1"); err != nil {
		t.Fatal(err)
	}
	if got, _ = cli("get", "prompts/manager.md"); got == "PUSHED MANAGER\n" {
		t.Fatal("rollback not applied")
	}
}
