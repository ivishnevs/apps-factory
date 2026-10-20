package main

import (
	"context"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/openspec"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestOpenSpecImportRoundTrip (core-owned-project-data 3.2): importing this repository's openspec/ into a
// project stores every OpenSpec document with the digest of the file; a second import changes nothing.
func TestOpenSpecImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "factory", Name: "factory", Workspace: "/nodes/factory"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "..", "openspec")
	want := map[string]string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel = filepath.ToSlash(rel); openspec.ValidPath(rel) {
			b, _ := os.ReadFile(p)
			want[rel] = openspec.Digest(b)
		}
		return nil
	})
	if err != nil || len(want) < 10 {
		t.Fatal(len(want), err)
	}
	v, err := openSpecImport(ctx, api, "factory", dir)
	if err != nil || len(v.Written) != len(want) || v.Unchanged != 0 {
		t.Fatal(len(v.Written), v.Unchanged, err)
	}
	docs, err := s.OpenSpecManifest(ctx, storage.DefaultCompany, "factory")
	if err != nil || len(docs) != len(want) {
		t.Fatal(len(docs), err)
	}
	for _, d := range docs {
		if want[d.Path] != d.SHA256 {
			t.Fatal("digest differs after import:", d.Path)
		}
	}
	// The served content round-trips too.
	doc, err := s.ReadOpenSpec(ctx, storage.DefaultCompany, "factory", "config.yaml", 0, 32768, "")
	local, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil || doc.Content != string(local) {
		t.Fatal(err)
	}
	again, err := openSpecImport(ctx, api, "factory", dir)
	if err != nil || len(again.Written) != 0 || again.Unchanged != len(want) {
		t.Fatal(again, err)
	}
	if len(v.Skipped) == 0 { // .gitkeep files are not OpenSpec documents
		t.Fatal("nothing skipped")
	}
}
