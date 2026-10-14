package factorytools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore records requests and answers from per-path handlers.
type fakeCore struct {
	mu       sync.Mutex
	acks     []string
	bodies   map[string][]map[string]any
	handlers map[string]func(n int) (int, string, string) // path → (status, body, seen header)
}

func newFakeCore(t *testing.T) (*fakeCore, *httptest.Server) {
	f := &fakeCore{bodies: map[string][]map[string]any{}, handlers: map[string]func(int) (int, string, string){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.acks = append(f.acks, r.Header.Get("X-Factory-Ack-Seq"))
		f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], body)
		n := len(f.bodies[r.URL.Path])
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer run.tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		status, out, seen := 200, `{}`, ""
		if r.URL.Path == "/agent/brief" {
			out = `{"ack_seq": 3, "task_id": "t1"}`
		}
		if h != nil {
			status, out, seen = h(n)
		}
		if seen != "" {
			w.Header().Set("X-Factory-Seen-Seq", seen)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeCore) last(path string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies[path]
	if len(b) == 0 {
		return nil
	}
	return b[len(b)-1]
}

func (f *fakeCore) lastAck() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acks[len(f.acks)-1]
}

// TestAdapterAckSeq: ack_seq starts at the brief end and rises only after a response that showed newer
// input reached the runtime; a 409 that never reached it raises nothing.
func TestAdapterAckSeq(t *testing.T) {
	f, srv := newFakeCore(t)
	f.handlers["/agent/call"] = func(int) (int, string, string) {
		return 200, `{"result": {"id": "p"}, "authority_input": [{"seq": 5}]}`, "5"
	}
	f.handlers["/agent/exit"] = func(int) (int, string, string) {
		return 409, `{"reason": "unseen_input", "unseen": [{"seq": 7, "text": "stop"}]}`, ""
	}
	a := &Adapter{URL: srv.URL, Token: "run.tok"}
	ctx := context.Background()
	if _, err := a.Call(ctx, Call{Name: "factory_project_get"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lastAck(); got != "3" {
		t.Fatalf("first call ack %q, want the brief end 3", got)
	}
	if _, err := a.Call(ctx, Call{Name: "factory_project_get"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lastAck(); got != "3" {
		t.Fatal("ack raised before the response reached the runtime:", got)
	}
	a.Delivered()
	v, err := a.Call(ctx, Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": "q"}})
	if err != nil || !strings.Contains(mustJSON(v), "unseen_input") || f.lastAck() != "5" {
		t.Fatal(v, err, f.lastAck())
	}
	// The 409 response was dropped (never delivered): the next exit still sends 5.
	a.mu.Lock()
	a.pending = 0
	a.mu.Unlock()
	_, _ = a.Call(ctx, Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": "q"}})
	if f.lastAck() != "5" {
		t.Fatal(f.lastAck())
	}
	a.Delivered()
	_, _ = a.Call(ctx, Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": "q"}})
	if f.lastAck() != "7" {
		t.Fatal("a delivered 409 did not count as seen", f.lastAck())
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a clone of a bare remote on main with a task branch af/t1 one commit ahead, and a fake gh whose
// pull request appears after `gh pr create`.
func repo(t *testing.T) (work string, ghLog string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	gc := filepath.Join(base, "gitconfig")
	_ = os.WriteFile(gc, []byte("[user]\n\tname = T\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote, work := filepath.Join(base, "remote.git"), filepath.Join(base, "work")
	gitT(t, base, "init", "--bare", "-q", remote)
	gitT(t, base, "clone", "-q", remote, work)
	_ = os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "init")
	gitT(t, work, "push", "-q", "origin", "main")
	gitT(t, work, "checkout", "-qb", "af/t1")
	_ = os.WriteFile(filepath.Join(work, "b.txt"), []byte("b\n"), 0o644)
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "feature")
	bin := filepath.Join(base, "bin")
	_ = os.MkdirAll(bin, 0o755)
	ghLog = filepath.Join(base, "gh.log")
	state := filepath.Join(base, "pr")
	script := `#!/bin/sh
echo "$@" >> ` + ghLog + `
if [ "$1 $2" = "pr view" ]; then
  [ -f ` + state + ` ] || { echo "no pull requests found" >&2; exit 1; }
  head=$(git rev-parse HEAD)
  echo "{\"number\": 42, \"state\": \"OPEN\", \"headRefOid\": \"$head\", \"url\": \"https://example.com/pull/42\"}"
  exit 0
fi
if [ "$1 $2" = "pr create" ]; then touch ` + state + `; echo https://example.com/pull/42; exit 0; fi
exit 1
`
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return work, ghLog
}

// TestAdapterSubmitPushesAndOpensPR: a clean branch passes preflight, is pushed, gets a pull request, and the
// exit carries the facts; the exit file says accepted. A dirty tree is refused before anything is sent.
func TestAdapterSubmitPushesAndOpensPR(t *testing.T) {
	work, ghLog := repo(t)
	f, srv := newFakeCore(t)
	f.handlers["/agent/exit"] = func(int) (int, string, string) {
		return 200, `{"exit": "submit", "task": {"id": "t1", "status": "IN_REVIEW"}}`, ""
	}
	exitFile := filepath.Join(t.TempDir(), "exit.json")
	a := &Adapter{URL: srv.URL, Token: "run.tok", Workdir: work, DefaultBranch: "main", ExitFile: exitFile}
	ctx := context.Background()

	_ = os.WriteFile(filepath.Join(work, "notes.txt"), []byte("wip\n"), 0o644)
	if _, err := a.Call(ctx, Call{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "s"}}); err == nil || !strings.Contains(err.Error(), "dirty_tree") {
		t.Fatal(err)
	}
	if f.last("/agent/exit") != nil || f.last("/agent/exit/preflight") != nil {
		t.Fatal("a dirty exit reached Core")
	}
	_ = os.Remove(filepath.Join(work, "notes.txt"))

	v, err := a.Call(ctx, Call{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "login", "reviewer": "calorie-manager"}})
	if err != nil || !strings.Contains(mustJSON(v), "accepted") {
		t.Fatal(v, err)
	}
	pre := f.last("/agent/exit/preflight")["facts"].(map[string]any)
	if pre["pushed"] != false || pre["pr"] != nil {
		t.Fatalf("preflight facts %v", pre)
	}
	head := gitT(t, work, "rev-parse", "HEAD")
	if remote := gitT(t, work, "ls-remote", "origin", "refs/heads/af/t1"); !strings.HasPrefix(remote, head) {
		t.Fatal("branch not pushed:", remote)
	}
	facts := f.last("/agent/exit")["facts"].(map[string]any)
	pr, _ := facts["pr"].(map[string]any)
	if facts["head"] != head || facts["pushed"] != true || facts["dirty"] != false || facts["ahead_of_default"] != float64(1) || pr["number"] != float64(42) || pr["state"] != "open" {
		t.Fatalf("exit facts %v", facts)
	}
	if b, _ := os.ReadFile(ghLog); !strings.Contains(string(b), "pr create --head af/t1 --base main --fill") {
		t.Fatalf("gh calls %s", b)
	}
	var st ExitState
	if b, _ := os.ReadFile(exitFile); json.Unmarshal(b, &st) != nil || st.State != "accepted" || st.Kind != "submit" {
		t.Fatalf("exit file %+v", st)
	}
}

// TestAdapterQueuesExitWhileCoreIsDown: the runtime is told to stop, the exit is retried with the same
// body until Core answers, and the exit file goes pending → accepted.
func TestAdapterQueuesExitWhileCoreIsDown(t *testing.T) {
	f, srv := newFakeCore(t)
	f.handlers["/agent/exit"] = func(n int) (int, string, string) {
		if n < 3 {
			return 503, "busy", ""
		}
		return 200, `{"exit": "done"}`, ""
	}
	exitFile := filepath.Join(t.TempDir(), "exit.json")
	a := &Adapter{URL: srv.URL, Token: "run.tok", ExitFile: exitFile, RetryBase: 10 * time.Millisecond}
	v, err := a.Call(context.Background(), Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "s"}})
	if err != nil || !strings.Contains(mustJSON(v), "queued") {
		t.Fatal(v, err)
	}
	var st ExitState
	_ = json.Unmarshal(must(os.ReadFile(exitFile)), &st)
	if st.State != "pending" {
		t.Fatal(st)
	}
	if _, err = a.Call(context.Background(), Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": "q"}}); err == nil {
		t.Fatal("a second exit while one is queued")
	}
	a.Wait()
	_ = json.Unmarshal(must(os.ReadFile(exitFile)), &st)
	if st.State != "accepted" {
		t.Fatal(st)
	}
	f.mu.Lock()
	bodies := f.bodies["/agent/exit"]
	f.mu.Unlock()
	if len(bodies) != 3 || mustJSON(bodies[0]) != mustJSON(bodies[2]) {
		t.Fatalf("retries changed the exit: %v", bodies)
	}
}

func must(b []byte, _ error) []byte { return b }

// TestAdapterFactsOutsideARepository: scratch work has no facts.
func TestAdapterFactsOutsideARepository(t *testing.T) {
	a := &Adapter{Workdir: t.TempDir()}
	if f, err := a.Facts(context.Background()); err != nil || f != nil {
		t.Fatal(f, err)
	}
}
