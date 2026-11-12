package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/testnode"
)

// gitServer serves bare repositories under root over git's smart HTTP (git http-backend).
func gitServer(t *testing.T, root string) *httptest.Server {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	h := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// skillRepo creates a work repository with skills/brand and skills/broken (an invalid name) and a bare clone
// served under root/acme-skills.git; it returns a function that commits a new body of brand and pushes it.
func skillRepo(t *testing.T, root string) func(body string) {
	t.Helper()
	work := t.TempDir()
	write := func(p, data string) {
		full := filepath.Join(work, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/brand/SKILL.md", "---\nname: brand\ndescription: Brand voice\nmetadata:\n  categories: writing\n---\nv1\n")
	write("skills/brand/references/tone.md", "calm\n")
	write("skills/broken/SKILL.md", "---\nname: other\ndescription: wrong name\n---\n")
	run(t, work, "git", "init", "-q", "-b", "main")
	run(t, work, "git", "add", "-A")
	run(t, work, "git", "commit", "-q", "-m", "v1")
	run(t, root, "git", "clone", "-q", "--bare", work, "acme-skills.git")
	run(t, filepath.Join(root, "acme-skills.git"), "git", "config", "http.receivepack", "true")
	run(t, work, "git", "remote", "add", "origin", filepath.Join(root, "acme-skills.git"))
	return func(body string) {
		write("skills/brand/SKILL.md", "---\nname: brand\ndescription: Brand voice\nmetadata:\n  categories: writing\n---\n"+body+"\n")
		write("skills/brand/references/new.md", "added\n")
		run(t, work, "git", "add", "-A")
		run(t, work, "git", "commit", "-q", "-m", body)
		run(t, work, "git", "push", "-q", "origin", "main")
	}
}

// TestSkillSources (skill-registry 4.1–4.6, 5.1, 7.1): a public git source is fetched by Core and indexed (an invalid
// skill does not block the others); library, files and install work and installing saves a catalog version; a pinned
// source shows an update with its file diff, a track source installs it; rollback restores the old content; a source
// on a non-public address is refused; a private source waits for a node, then a node's upload indexes it; uploads
// index an archive; a run's node gets only hashes pinned on its run.
func TestSkillSources(t *testing.T) {
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
	co := storage.DefaultCompany
	repos := t.TempDir()
	push := skillRepo(t, repos)
	srv := gitServer(t, repos)
	url := srv.URL + "/acme-skills.git"
	do := func(method, path string, body any) (int, string) {
		t.Helper()
		return call(t, c, method, path, c.Token, body)
	}

	// SSRF: Core never fetches from a non-public address.
	skills.AllowPrivate = false
	if code, out := do("POST", "/skills/sources", SourceCreate{ID: "local", URL: "https://127.0.0.1:1/x.git"}); code != 200 || !strings.Contains(out, `"status":"failed"`) || !strings.Contains(out, "not public") {
		t.Fatal(code, out)
	}
	if code, out := do("POST", "/skills/sources", SourceCreate{ID: "plain", URL: url}); code != 400 || !strings.Contains(out, "https") {
		t.Fatal(code, out)
	}
	skills.AllowPrivate = true
	t.Cleanup(func() { skills.AllowPrivate = false })

	code, out := do("POST", "/skills/sources", SourceCreate{ID: "acme-skills", URL: url, Ref: "main"})
	if code != 200 || !strings.Contains(out, `"status":"ok"`) || !strings.Contains(out, `"skills":1`) {
		t.Fatal(code, out)
	}
	code, out = do("GET", "/skills/library?source=acme-skills", nil)
	if code != 200 || !strings.Contains(out, `"name":"brand"`) || !strings.Contains(out, `"name":"broken"`) || !strings.Contains(out, "differs from its directory") {
		t.Fatal(code, out)
	}
	if code, out = do("GET", "/skills/library/acme-skills/brand/files/references/tone.md", nil); code != 200 || out != "calm\n" {
		t.Fatal(code, out)
	}
	if code, out = do("GET", "/skills/library?q=openspec", nil); code != 200 || !strings.Contains(out, `"source":"factory"`) || !strings.Contains(out, `"installed":true`) {
		t.Fatal("built-in skills in the library", code, out)
	}

	// Install: a catalog version; the skill is resolved from the index.
	base := c.catalogVersion(co)
	if code, out = do("POST", "/skills/install", SkillInstall{Base: base, Source: "acme-skills", Name: "brand"}); code != 200 {
		t.Fatal(code, out)
	}
	installed := c.cat(co).Skills.Skills["brand"]
	if installed.Source != "acme-skills" || installed.Hash == "" || installed.Description != "Brand voice" || c.catalogVersion(co) != base+1 {
		t.Fatalf("%+v", installed)
	}
	if code, out = do("POST", "/skills/install", SkillInstall{Base: base, Source: "acme-skills", Name: "brand"}); code != 409 {
		t.Fatal("install on an old base", code, out)
	}
	if code, _ = do("DELETE", "/skills/sources/acme-skills", nil); code != 409 {
		t.Fatal("source with an installed skill deleted", code)
	}

	// A new commit: the pinned source shows the update and its files; installing it is a new version.
	push("v2")
	if code, out = do("POST", "/skills/sources/acme-skills/sync", nil); code != 200 {
		t.Fatal(code, out)
	}
	var list []InstalledSkill
	code, out = do("GET", "/skills/installed", nil)
	_ = json.Unmarshal([]byte(out), &list)
	var brand InstalledSkill
	for _, x := range list {
		if x.Name == "brand" {
			brand = x
		}
	}
	if brand.Update == nil || brand.Update.Hash == installed.Hash || !strings.Contains(out, `{"path":"SKILL.md","change":"changed"}`) || !strings.Contains(out, `{"path":"references/new.md","change":"added"}`) {
		t.Fatal(code, out)
	}
	v1 := c.catalogVersion(co)
	if code, out = do("POST", "/skills/install", SkillInstall{Base: v1, Source: "acme-skills", Name: "brand"}); code != 200 || c.cat(co).Skills.Skills["brand"].Hash != brand.Update.Hash {
		t.Fatal(code, out)
	}
	// Rollback restores the old version, whose content is still served.
	if code, out = do("POST", "/catalog/rollback", map[string]any{"base": c.catalogVersion(co), "to": v1}); code != 200 {
		t.Fatal(code, out)
	}
	if got := c.cat(co).Skills.Skills["brand"].Hash; got != installed.Hash {
		t.Fatal("rollback", got)
	}
	if body, err := c.skillFile(ctx, co, "brand", ""); err != nil || !strings.Contains(body, "v1") {
		t.Fatal(body, err)
	}

	// A track source installs new versions on sync (a version authored sync).
	if err = s.SaveSkillSource(ctx, storage.SkillSource{Scope: co, ID: "acme-skills", Kind: storage.SkillSourceGit, URL: url, Ref: "main", Access: "public", Update: "track"}); err != nil {
		t.Fatal(err)
	}
	push("v3")
	if code, out = do("POST", "/skills/sources/acme-skills/sync", nil); code != 200 {
		t.Fatal(code, out)
	}
	if body, err := c.skillFile(ctx, co, "brand", ""); err != nil || !strings.Contains(body, "v3") {
		t.Fatal("track", body, err)
	}
	if h, _ := s.CatalogHistory(ctx, co); h[0].Author != "sync" {
		t.Fatalf("%+v", h[0])
	}

	// Uninstall is refused while an agent includes the skill; otherwise a version without it.
	if code, out = do("POST", "/skills/uninstall", SkillInstall{Base: c.catalogVersion(co), Name: "openspec"}); code != 409 {
		t.Fatal(code, out)
	}
	if code, out = do("POST", "/skills/uninstall", SkillInstall{Base: c.catalogVersion(co), Name: "brand"}); code != 200 {
		t.Fatal(code, out)
	}

	// Upload: an archive is indexed as an upload source.
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	md := "---\nname: notes\ndescription: Notes\n---\nuploaded\n"
	_ = tw.WriteHeader(&tar.Header{Name: "notes/SKILL.md", Mode: 0o644, Size: int64(len(md)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(md))
	_ = tw.Close()
	_ = gz.Close()
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("name", "uploaded")
	fw, _ := mw.CreateFormFile("archive", "skills.tgz")
	_, _ = fw.Write(tgz.Bytes())
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/skills/sources/upload", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.Token)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"upload"`) || !strings.Contains(w.Body.String(), `"skills":1`) {
		t.Fatal(w.Code, w.Body.String())
	}

	// A private source waits for an online node; then the node's upload of the tree indexes it.
	if code, out = do("POST", "/skills/sources", SourceCreate{ID: "private", URL: "git@example.test:acme/private.git", Access: "node"}); code != 200 || !strings.Contains(out, `"status":"waiting_for_node"`) {
		t.Fatal(code, out)
	}
	n := testnode.Enroll(t, s, co)
	if code, out = signedCall(t, c, n, "POST", "/nodes/register", registration(n.ID, 1)); code != 200 {
		t.Fatal(code, out)
	}
	if code, out = do("POST", "/skills/sources/private/sync", nil); code != 200 || !strings.Contains(out, `"status":"syncing"`) {
		t.Fatal(code, out)
	}
	work, err := s.NodeWork(ctx, co, n.ID, 1)
	if err != nil || len(work.SyncJobs) != 1 || work.SyncJobs[0].Source != "private" {
		t.Fatalf("%+v %v", work.SyncJobs, err)
	}
	tree := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tree, "skills", "secret-sauce"), 0o755)
	_ = os.WriteFile(filepath.Join(tree, "skills", "secret-sauce", "SKILL.md"), []byte("---\nname: secret-sauce\ndescription: Private\n---\nprivate\n"), 0o644)
	packed, err := skills.PackTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	other := testnode.Enroll(t, s, co)
	sr := httptest.NewRequest("POST", "/skills/sync-jobs/"+work.SyncJobs[0].ID+"?commit=abc123", bytes.NewReader(packed))
	other.Sign(t, s, sr, packed)
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, sr)
	if w.Code != 404 {
		t.Fatal("another node's job", w.Code)
	}
	sr = httptest.NewRequest("POST", "/skills/sync-jobs/"+work.SyncJobs[0].ID+"?commit=abc123", bytes.NewReader(packed))
	n.Sign(t, s, sr, packed)
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, sr)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if src, err := s.SkillSource(ctx, co, "private"); err != nil || src.Status != "ok" || src.Commit != "abc123" || src.Skills != 1 {
		t.Fatalf("%+v %v", src, err)
	}
	// A finished job takes no other tree (security-review-2026-11 F7).
	sr = httptest.NewRequest("POST", "/skills/sync-jobs/"+work.SyncJobs[0].ID+"?commit=evil", bytes.NewReader(packed))
	n.Sign(t, s, sr, packed)
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, sr)
	if w.Code != 409 {
		t.Fatal("a finished job re-posted", w.Code, w.Body.String())
	}
	if src, _ := s.SkillSource(ctx, co, "private"); src.Commit != "abc123" {
		t.Fatalf("%+v", src)
	}
	if work, _ = s.NodeWork(ctx, co, n.ID, 1); len(work.SyncJobs) != 0 {
		t.Fatal("done job still listed")
	}

	// A node gets a skill only by a hash pinned on its run.
	q := httptest.NewRequest("GET", "/runs/no-such-run/skills/"+installed.Hash, nil)
	n.Sign(t, s, q, nil)
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, q)
	if w.Code != 404 && w.Code != 403 {
		t.Fatal("foreign run", w.Code)
	}
	_ = http.StatusOK
}
