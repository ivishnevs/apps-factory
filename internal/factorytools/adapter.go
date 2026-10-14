package factorytools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Adapter is the per-run Factory MCP adapter (docs/explicit-exits-contract.md): it forwards tool calls to
// Core with the run credential, tracks ack_seq (the highest trigger seq it has shown the runtime),
// intercepts factory_exit_* calls to add the node's git facts, push the branch and ensure its pull request,
// and records the exit state for the worker.
type Adapter struct {
	URL, Token string
	// Workdir is the run's working directory (git facts come from it).
	Workdir string
	// DefaultBranch of the project; empty asks origin/HEAD, then "main".
	DefaultBranch string
	// ScanCommand gets the revision range as its last argument and exits 1 on a finding (empty: no scan).
	ScanCommand []string
	// ExitFile receives the exit state (FACTORY_EXIT_FILE); empty skips it.
	ExitFile string
	// Exec runs a command in Workdir and returns stdout; nil uses os/exec. Tests replace it.
	Exec   func(ctx context.Context, name string, args ...string) (string, error)
	Client *http.Client
	// RetryBase is the first wait between deliveries of a queued exit (default 2 s, doubling up to 1 min).
	RetryBase time.Duration

	mu      sync.Mutex
	ack     int64
	tracked bool   // ack_seq is known (GET /agent/brief answered)
	pending int64  // seq shown by the response being written
	queued  bool   // an exit is being retried in the background
	head    string // HEAD of the exit being sent
	wg      sync.WaitGroup
}

// Delivered is called after a tool response reached the runtime: what it showed now counts as seen.
func (a *Adapter) Delivered() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending > a.ack {
		a.ack = a.pending
	}
	a.pending = 0
}

// Wait blocks until background exit deliveries finished (process shutdown).
func (a *Adapter) Wait() { a.wg.Wait() }

func (a *Adapter) client() *http.Client {
	if a.Client == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return a.Client
}

// do sends a request with the run credential and the current ack_seq; it returns status, body and the
// X-Factory-Seen-Seq header.
func (a *Adapter) do(ctx context.Context, method, path string, body any) (int, []byte, int64, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.URL, "/")+path, r)
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	a.mu.Lock()
	if a.tracked {
		req.Header.Set("X-Factory-Ack-Seq", strconv.FormatInt(a.ack, 10))
	}
	a.mu.Unlock()
	res, err := a.client().Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	seen, _ := strconv.ParseInt(res.Header.Get("X-Factory-Seen-Seq"), 10, 64)
	return res.StatusCode, b, seen, err
}

// brief reads the starting ack_seq once; an older Core without /agent/brief leaves sequences untracked.
func (a *Adapter) brief(ctx context.Context) {
	a.mu.Lock()
	known := a.tracked
	a.mu.Unlock()
	if known {
		return
	}
	status, b, _, err := a.do(ctx, "GET", "/agent/brief", nil)
	var v struct {
		AckSeq int64 `json:"ack_seq"`
	}
	if err != nil || status != 200 || json.Unmarshal(b, &v) != nil {
		return
	}
	a.mu.Lock()
	if !a.tracked {
		a.ack, a.tracked = v.AckSeq, true
	}
	a.mu.Unlock()
}

func (a *Adapter) show(seq int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if seq > a.pending {
		a.pending = seq
	}
}

// List forwards tools/list.
func (a *Adapter) List(ctx context.Context) (any, error) {
	status, b, _, err := a.do(ctx, "GET", "/agent/tools", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Core HTTP %d: %s", status, b)
	}
	var v any
	return v, json.Unmarshal(b, &v)
}

// Call forwards a tool call; exit tools go through Exit.
func (a *Adapter) Call(ctx context.Context, c Call) (any, error) {
	a.brief(ctx)
	if kind := ExitKind(c.Name); kind != "" {
		return a.Exit(ctx, kind, c.Arguments)
	}
	status, b, seen, err := a.do(ctx, "POST", "/agent/call", c)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("Core HTTP %d: %s", status, bytes.TrimSpace(b))
	}
	a.show(seen)
	var v any
	if err = json.Unmarshal(b, &v); err != nil {
		return string(b), nil
	}
	return v, nil
}

type exitRefusal struct {
	Reason string `json:"reason"`
	Unseen []struct {
		Seq int64 `json:"seq"`
	} `json:"unseen"`
	Hint string `json:"hint"`
}

// ExitState is what the adapter tells the worker through ExitFile (contract §5).
type ExitState struct {
	State string    `json:"state"` // accepted, pending, refused
	Kind  string    `json:"kind"`
	At    time.Time `json:"at"`
	// Head is the exit's commit (exit_sha) when the exit carried git facts; the node resets the worktree
	// to it after handoff, submit or done (task-worktrees 2.2).
	Head string `json:"head,omitempty"`
}

func (a *Adapter) record(state, kind string) {
	if a.ExitFile == "" {
		return
	}
	a.mu.Lock()
	head := a.head
	a.mu.Unlock()
	b, _ := json.Marshal(ExitState{State: state, Kind: kind, At: time.Now().UTC(), Head: head})
	tmp := a.ExitFile + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, a.ExitFile)
	}
}

// Exit runs one exit: facts, preflight, push and pull request for submit/handoff, then the exit itself.
// Core unreachable: the exit is queued and retried in the background with the same key (the run).
func (a *Adapter) Exit(ctx context.Context, kind string, args map[string]any) (any, error) {
	a.mu.Lock()
	queued := a.queued
	a.mu.Unlock()
	if queued {
		return nil, errors.New("an exit is already queued for delivery: stop working and finish the turn")
	}
	if args == nil {
		args = map[string]any{}
	}
	facts, err := a.Facts(ctx)
	if err != nil {
		return nil, err
	}
	code := kind == "submit" || kind == "handoff" || kind == "done"
	if code && facts != nil && facts.Dirty {
		return nil, errors.New("exit refused (dirty_tree): commit or discard your changes, or move notes to $FACTORY_SCRATCH, then exit again")
	}
	if facts != nil && facts.Scan != nil && !facts.Scan.OK {
		return nil, errors.New("exit refused (secret_found): the secret scan found credentials in your commits; remove them from the history:\n" + facts.Scan.Report)
	}
	if (kind == "submit" || kind == "handoff") && facts != nil && facts.Branch != "" && facts.Remote {
		// Preflight first: nothing is pushed for an exit Core would refuse.
		if v, err, done := a.send(ctx, "/agent/exit/preflight", kind, args, facts); done {
			return v, err
		}
		if facts, err = a.publish(ctx, facts); err != nil {
			return nil, err
		}
	}
	v, err, _ := a.send(ctx, "/agent/exit", kind, args, facts)
	return v, err
}

// send posts an exit; done reports a final answer (anything but a successful preflight).
func (a *Adapter) send(ctx context.Context, path, kind string, args map[string]any, facts *Facts) (any, error, bool) {
	a.mu.Lock()
	ack := a.ack
	a.mu.Unlock()
	body := map[string]any{"kind": kind, "arguments": args, "ack_seq": ack}
	if facts != nil {
		body["facts"] = facts.core()
		a.mu.Lock()
		a.head = facts.Head
		a.mu.Unlock()
	}
	status, b, _, err := a.do(ctx, "POST", path, body)
	preflight := strings.HasSuffix(path, "/preflight")
	switch {
	case err != nil || status >= 500:
		if preflight {
			return nil, fmt.Errorf("Core unreachable for the exit check (%v %s); try the exit again shortly", err, bytes.TrimSpace(b)), true
		}
		a.queue(body, kind)
		return map[string]string{"exit": kind, "status": "queued — stop working and finish the turn; the factory delivers the exit"}, nil, true
	case status == 200:
		if preflight {
			return nil, nil, false
		}
		a.record("accepted", kind)
		var v any
		_ = json.Unmarshal(b, &v)
		return map[string]any{"accepted": v, "next": "the exit is accepted: finish the turn now"}, nil, true
	case status == 410:
		return nil, errors.New("this run was stopped or replaced: stop working and finish the turn"), true
	}
	var r exitRefusal
	_ = json.Unmarshal(b, &r)
	if r.Reason == "unseen_input" {
		for _, u := range r.Unseen {
			a.show(u.Seq) // counts as seen once this answer reached the runtime
		}
		var v any
		_ = json.Unmarshal(b, &v)
		return map[string]any{"refused": v, "next": "new input arrived for this task: act on it, then call the exit again"}, nil, true
	}
	return nil, fmt.Errorf("exit refused (%s): %s", r.Reason, r.Hint), true
}

// queue delivers an exit in the background until Core answers; a 409 then means new input arrived.
func (a *Adapter) queue(body map[string]any, kind string) {
	a.mu.Lock()
	a.queued = true
	a.mu.Unlock()
	a.record("pending", kind)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		wait := a.RetryBase
		if wait <= 0 {
			wait = 2 * time.Second
		}
		for {
			time.Sleep(wait)
			wait = min(2*wait, time.Minute)
			status, _, _, err := a.do(context.Background(), "POST", "/agent/exit", body)
			switch {
			case err != nil || status >= 500:
				continue
			case status == 200:
				a.record("accepted", kind)
			default:
				a.record("refused", kind) // 409 (new input) or 410 (the run ended)
			}
			a.mu.Lock()
			a.queued = false
			a.mu.Unlock()
			return
		}
	}()
}

// Facts are the node's git facts; Remote reports an origin remote (push and PR possible).
type Facts struct {
	Head, Branch, DefaultBranch string
	Dirty, Pushed, Remote       bool
	AheadOfDefault              int
	PR                          *PRFacts
	Scan                        *ScanResult
}

type PRFacts struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	HeadSHA string `json:"head_sha"`
	URL     string `json:"url,omitempty"`
}

type ScanResult struct {
	OK     bool   `json:"ok"`
	Report string `json:"report,omitempty"`
}

func (f *Facts) core() map[string]any {
	return map[string]any{"head": f.Head, "dirty": f.Dirty, "branch": f.Branch, "default_branch": f.DefaultBranch,
		"ahead_of_default": f.AheadOfDefault, "pushed": f.Pushed, "pr": f.PR, "scan": f.Scan}
}

func (a *Adapter) git(ctx context.Context, args ...string) (string, error) {
	return a.run(ctx, "git", args...)
}

func (a *Adapter) run(ctx context.Context, name string, args ...string) (string, error) {
	if a.Exec != nil {
		return a.Exec(ctx, name, args...)
	}
	return runIn(ctx, a.Workdir, name, args...)
}

// Facts collects the git facts of Workdir; nil without a repository (scratch work).
func (a *Adapter) Facts(ctx context.Context) (*Facts, error) {
	if a.Workdir == "" {
		return nil, nil
	}
	// Only the workspace's own repository counts: a scratch workspace inside another checkout (for example
	// under a factory checkout's .cache) has no facts of its own.
	top, err := a.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil || !sameDir(top, a.Workdir) {
		return nil, nil
	}
	head, err := a.git(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, nil // no commit yet
	}
	f := &Facts{Head: head}
	status, err := a.git(ctx, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	f.Dirty = status != ""
	if b, err := a.git(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "HEAD" {
		f.Branch = b
	}
	if remotes, _ := a.git(ctx, "remote"); strings.Contains("\n"+remotes+"\n", "\norigin\n") {
		f.Remote = true
		_, _ = a.git(ctx, "fetch", "--quiet", "origin")
	}
	f.DefaultBranch = a.DefaultBranch
	if f.DefaultBranch == "" {
		if ref, err := a.git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
			f.DefaultBranch = strings.TrimPrefix(ref, "origin/")
		} else {
			f.DefaultBranch = "main"
		}
	}
	base := f.DefaultBranch
	if f.Remote {
		base = "origin/" + f.DefaultBranch
	}
	if n, err := a.git(ctx, "rev-list", "--count", base+"..HEAD"); err == nil {
		f.AheadOfDefault, _ = strconv.Atoi(n)
	}
	if f.Remote && f.Branch != "" {
		if remote, err := a.git(ctx, "rev-parse", "--verify", "--quiet", "origin/"+f.Branch); err == nil {
			f.Pushed = remote == head
		}
		f.PR = a.pr(ctx, f.Branch)
	}
	if len(a.ScanCommand) > 0 && f.AheadOfDefault > 0 {
		f.Scan = a.scan(ctx, base)
	}
	return f, nil
}

// pr reads the branch's pull request with gh; nil when there is none or gh is unavailable.
func (a *Adapter) pr(ctx context.Context, branch string) *PRFacts {
	out, err := a.run(ctx, "gh", "pr", "view", branch, "--json", "number,state,headRefOid,url")
	if err != nil {
		return nil
	}
	var v struct {
		Number     int    `json:"number"`
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"`
		URL        string `json:"url"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || v.Number == 0 {
		return nil
	}
	return &PRFacts{Number: v.Number, State: strings.ToLower(v.State), HeadSHA: v.HeadRefOid, URL: v.URL}
}

// scan runs the scanner over merge-base(base, HEAD)..HEAD (task-worktrees D6).
func (a *Adapter) scan(ctx context.Context, base string) *ScanResult {
	mb, err := a.git(ctx, "merge-base", base, "HEAD")
	if err != nil {
		return &ScanResult{OK: true, Report: "no merge base with " + base + "; not scanned"}
	}
	args := append(append([]string{}, a.ScanCommand[1:]...), mb+"..HEAD")
	out, err := a.run(ctx, a.ScanCommand[0], args...)
	if err == nil {
		return &ScanResult{OK: true}
	}
	var missing *notFound
	if errors.As(err, &missing) {
		return &ScanResult{OK: true, Report: a.ScanCommand[0] + " not installed; not scanned"}
	}
	if len(out) > 4096 {
		out = out[:4096]
	}
	return &ScanResult{OK: false, Report: strings.TrimSpace(out + "\n" + err.Error())}
}

// publish pushes the branch when the remote is behind and ensures a pull request (gh), then collects the
// facts again.
func (a *Adapter) publish(ctx context.Context, f *Facts) (*Facts, error) {
	if !f.Pushed {
		if _, err := a.git(ctx, "push", "--quiet", "-u", "origin", "HEAD:refs/heads/"+f.Branch); err != nil {
			return nil, fmt.Errorf("git push failed (fix it, then exit again): %w", err)
		}
	}
	if f.PR == nil && f.AheadOfDefault > 0 {
		if _, err := a.run(ctx, "gh", "pr", "create", "--head", f.Branch, "--base", f.DefaultBranch, "--fill"); err != nil {
			var missing *notFound
			if !errors.As(err, &missing) {
				return nil, fmt.Errorf("creating the pull request failed (fix it, then exit again): %w", err)
			}
		}
	}
	return a.Facts(ctx)
}

type notFound struct{ name string }

func (e *notFound) Error() string { return e.name + ": executable not found" }

// AdapterFromEnv builds the adapter of cmd/factory-mcp from the run environment.
func AdapterFromEnv(getenv func(string) string) (*Adapter, error) {
	a := &Adapter{URL: getenv("FACTORY_URL"), Token: getenv("FACTORY_RUN_TOKEN"), Workdir: getenv("FACTORY_WORKDIR"),
		DefaultBranch: getenv("FACTORY_DEFAULT_BRANCH"), ExitFile: getenv("FACTORY_EXIT_FILE")}
	if a.Token == "" || a.URL == "" {
		return nil, errors.New("FACTORY_RUN_TOKEN and FACTORY_URL required")
	}
	if v := getenv("FACTORY_SCAN_COMMAND"); v != "" {
		if err := json.Unmarshal([]byte(v), &a.ScanCommand); err != nil {
			return nil, fmt.Errorf("FACTORY_SCAN_COMMAND: JSON array expected: %w", err)
		}
	}
	if a.Workdir == "" {
		a.Workdir, _ = os.Getwd()
	}
	a.Workdir, _ = filepath.Abs(a.Workdir)
	return a, nil
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
