package runner

import (
	"context"
	"encoding/json"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/worker"
)

// TestPrivateSourceSync (skill-registry 4.3): a private source waits without a node; with one online, Core's poll
// answer gives the node the job, the node clones with its own git and uploads the tree, and the source is indexed.
func TestPrivateSourceSync(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	repos, work := t.TempDir(), t.TempDir()
	sh := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	_ = os.MkdirAll(filepath.Join(work, "skills", "secret-sauce"), 0o755)
	_ = os.WriteFile(filepath.Join(work, "skills", "secret-sauce", "SKILL.md"), []byte("---\nname: secret-sauce\ndescription: Private\n---\nprivate\n"), 0o644)
	sh(work, "git", "init", "-q", "-b", "main")
	sh(work, "git", "add", "-A")
	sh(work, "git", "commit", "-q", "-m", "init")
	sh(repos, "git", "clone", "-q", "--bare", work, "private.git")
	gitSrv := httptest.NewServer(&cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + repos, "GIT_HTTP_EXPORT_ALL=1"}})
	defer gitSrv.Close()

	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := &core.Core{Store: s, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	go c.Run(ctx)
	owner := func(method, path string, body any, out any) {
		t.Helper()
		api := testOwner(server.URL)
		if err := api.Do(ctx, method, path, body, out); err != nil {
			t.Fatal(method, path, err)
		}
	}
	var src storage.SkillSource
	owner("POST", "/skills/sources", core.SourceCreate{ID: "private", URL: gitSrv.URL + "/private.git", Ref: "main", Access: "node"}, &src)
	if src.Status != "waiting_for_node" {
		t.Fatalf("%+v", src)
	}
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir, cfg.Poll.WaitSeconds = 1, t.TempDir(), 2
	api := enrolledIn(t, s, storage.DefaultCompany, cfg.StateDir, server.URL)
	nodeCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{&fake.Driver{}}}})
	}()
	defer func() { stop(); <-done }()
	for {
		ns, _ := s.Nodes(ctx, storage.DefaultCompany)
		if len(ns) == 1 && ns[0].Online {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	owner("POST", "/skills/sources/private/sync", nil, &src)
	for src.Status != "ok" {
		if src.Status == "failed" {
			t.Fatal(src.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("not synced: %+v", src)
		case <-time.After(100 * time.Millisecond):
		}
		var list []storage.SkillSource
		owner("GET", "/skills/sources", nil, &list)
		for _, x := range list {
			if x.ID == "private" {
				src = x
			}
		}
	}
	var lib []json.RawMessage
	owner("GET", "/skills/library?source=private", nil, &lib)
	if src.Skills != 1 || len(lib) != 1 || src.Commit == "" {
		t.Fatalf("%+v %s", src, lib)
	}
}

// testOwner is the owner's API client of a test Core.
func testOwner(url string) worker.API { return worker.API{URL: url, Token: "test-token"} }
