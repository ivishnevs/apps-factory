package factorytools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/workspace"
)

// TestPlantedSecretBlocksPushAndExit (task-worktrees 3.2): in a node worktree with the default gitleaks
// scanner, a clean branch is submitted (scanned, pushed, PR opened); after a commit with a planted test
// secret, the worktree's pre-push hook refuses `git push` and the adapter refuses the exit with
// secret_found and the scanner's redacted report: nothing reaches the remote, gh or Core.
func TestPlantedSecretBlocksPushAndExit(t *testing.T) {
	if _, err := exec.LookPath(workspace.DefaultScanCommand[0]); err != nil {
		t.Skip("gitleaks not on PATH")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	gc := filepath.Join(base, "gitconfig")
	_ = os.WriteFile(gc, []byte("[user]\n\tname = T\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote, seed := filepath.Join(base, "remote.git"), filepath.Join(base, "seed")
	gitT(t, base, "init", "--bare", "-q", remote)
	gitT(t, base, "clone", "-q", remote, seed)
	_ = os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a\n"), 0o644)
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-qm", "init")
	gitT(t, seed, "push", "-q", "origin", "main")

	// Fake gh: the pull request exists after `gh pr create`.
	bin, ghLog, pr := filepath.Join(base, "bin"), filepath.Join(base, "gh.log"), filepath.Join(base, "pr")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
echo "$@" >> `+ghLog+`
if [ "$1 $2" = "pr view" ]; then
  [ -f `+pr+` ] || { echo "no pull requests found" >&2; exit 1; }
  echo "{\"number\": 7, \"state\": \"OPEN\", \"headRefOid\": \"$(git rev-parse HEAD)\", \"url\": \"https://example.com/pull/7\"}"
  exit 0
fi
if [ "$1 $2" = "pr create" ]; then touch `+pr+`; echo https://example.com/pull/7; exit 0; fi
exit 1
`), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The worktree and scanner settings as a node gives them to the run (worker: FACTORY_SCAN_COMMAND).
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: filepath.Join(base, "root")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wt, err := trees.Ensure(ctx, workspace.Request{Company: "acme", Project: "app", RepoURL: remote, DefaultBranch: "main", Task: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	f, srv := newFakeCore(t)
	f.handlers["/agent/exit"] = func(int) (int, string, string) {
		return 200, `{"exit": "submit", "task": {"id": "t1", "status": "IN_REVIEW"}}`, ""
	}
	scan, _ := json.Marshal(trees.ScanCommand())
	exitFile := filepath.Join(base, "exit.json")
	env := map[string]string{"FACTORY_URL": srv.URL, "FACTORY_RUN_TOKEN": "run.tok", "FACTORY_WORKDIR": wt.Path,
		"FACTORY_DEFAULT_BRANCH": "main", "FACTORY_EXIT_FILE": exitFile, "FACTORY_SCAN_COMMAND": string(scan)}
	a, err := AdapterFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	// A clean branch: scanned, pushed with the pre-push hook passing, PR opened, exit accepted.
	_ = os.WriteFile(filepath.Join(wt.Path, "ok.txt"), []byte("fine\n"), 0o644)
	gitT(t, wt.Path, "add", "-A")
	gitT(t, wt.Path, "commit", "-qm", "ok")
	if v, err := a.Call(ctx, Call{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "ok"}}); err != nil || !strings.Contains(mustJSON(v), "accepted") {
		t.Fatal(v, err)
	}
	if scanned, _ := f.last("/agent/exit")["facts"].(map[string]any)["scan"].(map[string]any); scanned["ok"] != true {
		t.Fatalf("clean exit scan %v", f.last("/agent/exit")["facts"])
	}
	pushed := gitT(t, remote, "rev-parse", wt.Branch)
	if pushed != gitT(t, wt.Path, "rev-parse", "HEAD") {
		t.Fatal("clean branch not pushed")
	}

	// The planted secret.
	key := "AKIA" + "QYLPMN5HHHFPZAM2"
	_ = os.WriteFile(filepath.Join(wt.Path, "config.env"), []byte("aws_access_key_id = "+key+"\n"), 0o644)
	gitT(t, wt.Path, "add", "-A")
	gitT(t, wt.Path, "commit", "-qm", "leak")
	push := exec.Command("git", "push", "origin", "HEAD")
	push.Dir = wt.Path
	out, err := push.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "secret scan") || strings.Contains(string(out), key) {
		t.Fatalf("git push with the secret: %v\n%s", err, out)
	}
	if got := gitT(t, remote, "rev-parse", wt.Branch); got != pushed {
		t.Fatal("git push: the secret reached the remote")
	}

	ghBefore, _ := os.ReadFile(ghLog)
	f.mu.Lock()
	exits, preflights := len(f.bodies["/agent/exit"]), len(f.bodies["/agent/exit/preflight"])
	f.mu.Unlock()
	for _, exit := range []string{"factory_exit_submit", "factory_exit_handoff", "factory_exit_done"} {
		v, err := a.Call(ctx, Call{Name: exit, Arguments: map[string]any{"summary": "leak"}})
		if err == nil || !strings.Contains(err.Error(), "secret_found") || !strings.Contains(err.Error(), "config.env") || !strings.Contains(err.Error(), "aws-access-token") || strings.Contains(err.Error(), key) {
			t.Fatalf("%s: %v %v", exit, v, err)
		}
	}
	if got := gitT(t, remote, "rev-parse", wt.Branch); got != pushed {
		t.Fatal("exit: the secret reached the remote")
	}
	f.mu.Lock()
	reached := len(f.bodies["/agent/exit"]) != exits || len(f.bodies["/agent/exit/preflight"]) != preflights
	f.mu.Unlock()
	if reached {
		t.Fatal("a refused exit reached Core")
	}
	if ghAfter, _ := os.ReadFile(ghLog); strings.Contains(strings.TrimPrefix(string(ghAfter), string(ghBefore)), "pr create") {
		t.Fatal("a refused exit opened a pull request")
	}
	var st ExitState
	if b, _ := os.ReadFile(exitFile); json.Unmarshal(b, &st) != nil || st.ExitSHA != pushed {
		t.Fatalf("exit file changed by a refused exit: %+v", st)
	}
}
