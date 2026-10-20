package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/factorytools"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// materializeAt writes the project's current OpenSpec tree and its manifest into dir, as the node does.
func materializeAt(t *testing.T, s *storage.Store, project, dir string) {
	t.Helper()
	ctx := context.Background()
	docs, err := s.OpenSpecManifest(ctx, storage.DefaultCompany, project)
	if err != nil {
		t.Fatal(err)
	}
	m := protocol.Manifest{OpenSpecDir: "openspec", Files: []protocol.ManifestFile{}}
	for _, d := range docs {
		doc, err := s.ReadOpenSpec(ctx, storage.DefaultCompany, project, d.Path, 0, 32768, "")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "openspec", d.Path), doc.Content)
		m.Files = append(m.Files, protocol.ManifestFile{Path: "openspec/" + d.Path, SHA256: d.SHA256, Size: d.Size})
	}
	b, _ := json.Marshal(m)
	writeFile(t, filepath.Join(dir, protocol.ManifestPath), string(b))
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ownerWrite(t *testing.T, s *storage.Store, path, content string) {
	t.Helper()
	ctx := context.Background()
	base := ""
	if d, err := s.ReadOpenSpec(ctx, storage.DefaultCompany, "notes", path, 0, 8192, ""); err == nil {
		base = d.SHA256
	}
	if _, err := s.WriteOpenSpec(ctx, storage.DefaultCompany, "notes", storage.OpenSpecChange{Path: path, Base: base, Content: []byte(content), Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
}

func current(t *testing.T, s *storage.Store, path string) (string, bool) {
	t.Helper()
	d, err := s.ReadOpenSpec(context.Background(), storage.DefaultCompany, "notes", path, 0, 32768, "")
	if err != nil {
		return "", false
	}
	return d.Content, true
}

// TestAdapterSyncsOpenSpecAtExit (core-owned-project-data 2.3): the real MCP adapter against Core. The run
// edited one document, added one and removed one; its exit uploads exactly those against the manifest's
// digests, leaving a document the owner changed meanwhile alone. A second task that edited the same document
// from the same base is refused at its exit with the current digest and Core's version beside the file; after
// merging, its exit goes through. factory_openspec_sync does the same on demand.
func TestAdapterSyncsOpenSpecAtExit(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	ownerWrite(t, h.s, "changes/feature/proposal.md", "# Feature\nv0\n")
	ownerWrite(t, h.s, "changes/feature/tasks.md", "- [ ] one\n")
	ownerWrite(t, h.s, "specs/old/spec.md", "# Old\n")
	first, second := t.TempDir(), t.TempDir()
	materializeAt(t, h.s, "notes", first)
	materializeAt(t, h.s, "notes", second) // the second task starts from the same base

	h.create("one", "notes", "demo")
	r := h.start("one")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: first}
	writeFile(t, filepath.Join(first, "openspec/changes/feature/proposal.md"), "# Feature\nv1 by one\n")
	writeFile(t, filepath.Join(first, "openspec/specs/new/spec.md"), "# New\n")
	writeFile(t, filepath.Join(first, "openspec/changes/feature/notes.txt"), "not an OpenSpec document\n")
	if err := os.Remove(filepath.Join(first, "openspec/specs/old/spec.md")); err != nil {
		t.Fatal(err)
	}
	ownerWrite(t, h.s, "changes/feature/tasks.md", "- [x] one\n") // untouched by the run
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "spec updated"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := current(t, h.s, "changes/feature/proposal.md"); v != "# Feature\nv1 by one\n" {
		t.Fatal(v)
	}
	if v, _ := current(t, h.s, "specs/new/spec.md"); v != "# New\n" {
		t.Fatal(v)
	}
	if _, ok := current(t, h.s, "specs/old/spec.md"); ok {
		t.Fatal("removed document still current")
	}
	if v, _ := current(t, h.s, "changes/feature/tasks.md"); v != "- [x] one\n" {
		t.Fatal("unchanged local copy overwrote Core's newer version:", v)
	}
	h.end(r, "succeeded", "accepted")

	h.create("two", "notes", "demo")
	r = h.start("two")
	b := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: second}
	writeFile(t, filepath.Join(second, "openspec/changes/feature/proposal.md"), "# Feature\nv1 by two\n")
	_, err := b.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "spec updated"}})
	want := openspec.Digest([]byte("# Feature\nv1 by one\n"))
	if err == nil || !strings.Contains(err.Error(), "openspec_conflict") || !strings.Contains(err.Error(), "changes/feature/proposal.md") || !strings.Contains(err.Error(), want) {
		t.Fatal(err)
	}
	theirs := filepath.Join(second, "openspec/changes/feature/proposal.md"+factorytools.ConflictSuffix)
	if got, _ := os.ReadFile(theirs); string(got) != "# Feature\nv1 by one\n" {
		t.Fatal("Core's version not saved beside the file:", string(got))
	}
	if v, _ := current(t, h.s, "changes/feature/proposal.md"); v != "# Feature\nv1 by one\n" {
		t.Fatal("refused exit stored its version:", v)
	}
	// The agent merges, removes Core's copy and syncs on demand, then exits.
	writeFile(t, filepath.Join(second, "openspec/changes/feature/proposal.md"), "# Feature\nv1 by one\nv1 by two\n")
	_ = os.Remove(theirs)
	out, err := b.Call(ctx, factorytools.Call{Name: "factory_openspec_sync", Arguments: map[string]any{}})
	if err != nil || !strings.Contains(mustJSON(out), "changes/feature/proposal.md") {
		t.Fatal(out, err)
	}
	if _, err = b.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "merged"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := current(t, h.s, "changes/feature/proposal.md"); v != "# Feature\nv1 by one\nv1 by two\n" {
		t.Fatal(v)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestMaterializationKeepsUnsyncedOpenSpecEdits (core-owned-project-data, owner decision 2026-10-20): a run
// that edited OpenSpec documents and ended without an exit leaves them unsynced. The node's next
// materialization of that worktree keeps them (an edit, a local deletion and a local addition) while
// unmodified documents take Core's newer version; the next run's exit uploads them against the digests they
// started from. If Core changed the edited document meanwhile, that exit is refused as a conflict.
func TestMaterializationKeepsUnsyncedOpenSpecEdits(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	ownerWrite(t, h.s, "changes/feature/proposal.md", "# Feature\nv0\n")
	ownerWrite(t, h.s, "specs/a/spec.md", "# A\nv0\n")
	ownerWrite(t, h.s, "specs/b/spec.md", "# B\n")
	node := &worker.Worker{API: worker.API{URL: server.URL, Token: h.c.Token}, BlobCache: t.TempDir()}
	materialize := func(r exitRun, dir string) {
		t.Helper()
		docs, err := h.s.OpenSpecManifest(ctx, storage.DefaultCompany, "notes")
		if err != nil {
			t.Fatal(err)
		}
		m := &protocol.Manifest{OpenSpecDir: "openspec", Files: []protocol.ManifestFile{}}
		for _, d := range docs {
			m.Files = append(m.Files, protocol.ManifestFile{Path: "openspec/" + d.Path, SHA256: d.SHA256, Size: d.Size})
		}
		if err = node.Materialize(ctx, protocol.RunSpec{ID: r.id, Materialize: m}, r.lease, dir); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(name)
		return string(b), err == nil
	}

	// Run 1 edits, deletes and adds documents, then ends without an exit.
	work := t.TempDir()
	h.create("one", "notes", "demo")
	r := h.start("one")
	materialize(r, work)
	writeFile(t, filepath.Join(work, "openspec/changes/feature/proposal.md"), "# Feature\nv1 unsynced\n")
	if err := os.Remove(filepath.Join(work, "openspec/specs/b/spec.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "openspec/specs/c/spec.md"), "# C\n")
	h.end(r, "succeeded", "none")
	ownerWrite(t, h.s, "specs/a/spec.md", "# A\nv1 by owner\n") // unmodified in the worktree

	// Run 2 materializes the same worktree: the unsynced state stays, the untouched document updates.
	h.create("two", "notes", "demo")
	r = h.start("two")
	materialize(r, work)
	if v, _ := read(filepath.Join(work, "openspec/changes/feature/proposal.md")); v != "# Feature\nv1 unsynced\n" {
		t.Fatal("unsynced edit overwritten:", v)
	}
	if _, ok := read(filepath.Join(work, "openspec/specs/b/spec.md")); ok {
		t.Fatal("local deletion undone")
	}
	if v, _ := read(filepath.Join(work, "openspec/specs/c/spec.md")); v != "# C\n" {
		t.Fatal("local addition removed:", v)
	}
	if v, _ := read(filepath.Join(work, "openspec/specs/a/spec.md")); v != "# A\nv1 by owner\n" {
		t.Fatal("unmodified document not updated:", v)
	}
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "spec updated"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := current(t, h.s, "changes/feature/proposal.md"); v != "# Feature\nv1 unsynced\n" {
		t.Fatal("Core missed the unsynced edit:", v)
	}
	if _, ok := current(t, h.s, "specs/b/spec.md"); ok {
		t.Fatal("Core missed the local deletion")
	}
	if v, _ := current(t, h.s, "specs/c/spec.md"); v != "# C\n" {
		t.Fatal("Core missed the local addition:", v)
	}
	if v, _ := current(t, h.s, "specs/a/spec.md"); v != "# A\nv1 by owner\n" {
		t.Fatal(v)
	}
	h.end(r, "succeeded", "accepted")

	// Core changes the document between the run that edited it and the next one: the edit is still kept,
	// and the exit meets the conflict.
	work = t.TempDir()
	h.create("three", "notes", "demo")
	r = h.start("three")
	materialize(r, work)
	writeFile(t, filepath.Join(work, "openspec/changes/feature/proposal.md"), "# Feature\nv2 unsynced\n")
	h.end(r, "succeeded", "none")
	ownerWrite(t, h.s, "changes/feature/proposal.md", "# Feature\nv2 by owner\n")
	h.create("four", "notes", "demo")
	r = h.start("four")
	materialize(r, work)
	if v, _ := read(filepath.Join(work, "openspec/changes/feature/proposal.md")); v != "# Feature\nv2 unsynced\n" {
		t.Fatal("unsynced edit overwritten:", v)
	}
	a = &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}
	_, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "spec updated"}})
	want := openspec.Digest([]byte("# Feature\nv2 by owner\n"))
	if err == nil || !strings.Contains(err.Error(), "openspec_conflict") || !strings.Contains(err.Error(), "changes/feature/proposal.md") || !strings.Contains(err.Error(), want) {
		t.Fatal(err)
	}
	if v, _ := current(t, h.s, "changes/feature/proposal.md"); v != "# Feature\nv2 by owner\n" {
		t.Fatal("refused exit stored its version:", v)
	}
	if v, _ := read(filepath.Join(work, "openspec/changes/feature/proposal.md"+factorytools.ConflictSuffix)); v != "# Feature\nv2 by owner\n" {
		t.Fatal("Core's version not saved beside the file:", v)
	}
}
