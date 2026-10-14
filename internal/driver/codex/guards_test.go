package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
)

var guardRules = []policies.Rule{
	{Name: "host-ui-input", Type: "access", Commands: []string{"cliclick"}, Effect: "deny", Instruction: "Use UI tests."},
	{Name: "push", Type: "access", Commands: []string{"git push *"}, Effect: "approval", Instruction: "Ask before pushing."},
}

// TestFakeGuardServer sends command approval requests one at a time and ends the turn with the
// thread's approval policy, the decisions and the steered notes as the agent message.
func TestFakeGuardServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_CODEX") != "guards" {
		return
	}
	requests := []string{
		`{"threadId":"thread-1","turnId":"turn-1","itemId":"i1","startedAtMs":1,"command":"/bin/zsh -lc 'cliclick c:1,1'"}`,
		`{"threadId":"thread-1","turnId":"turn-1","itemId":"i2","startedAtMs":1,"command":"/bin/zsh -lc 'cd app && git push origin main'"}`,
		`{"threadId":"thread-1","turnId":"turn-1","itemId":"i3","startedAtMs":1,"command":"git push origin main"}`,
		`{"threadId":"thread-1","turnId":"turn-1","itemId":"i4","startedAtMs":1,"command":"/bin/zsh -lc 'git status'"}`,
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var policy string
	var out []string
	next := 0
	sendNext := func() {
		_ = enc.Encode(map[string]any{"id": "a" + string(rune('0'+next)), "method": "item/commandExecution/requestApproval", "params": json.RawMessage(requests[next])})
		next++
	}
	for sc.Scan() {
		var m rpc.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" { // a response to an approval request
			var r struct{ Decision string }
			_ = json.Unmarshal(m.Result, &r)
			out = append(out, r.Decision)
			if next < len(requests) {
				sendNext()
				continue
			}
			_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "text": policy + "|" + strings.Join(out, "|")}}})
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "turn-1", "status": "completed"}}})
			continue
		}
		if len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start":
			var p struct{ ApprovalPolicy string }
			_ = json.Unmarshal(m.Params, &p)
			policy = p.ApprovalPolicy
			result = map[string]any{"thread": map[string]string{"id": "thread-1"}}
		case "turn/start":
			result = map[string]any{"turn": map[string]string{"id": "turn-1"}}
		case "turn/steer":
			var p struct {
				ExpectedTurnID string `json:"expectedTurnId"`
				Input          []struct{ Text string }
			}
			_ = json.Unmarshal(m.Params, &p)
			out = append(out, "steer("+p.ExpectedTurnID+"):"+p.Input[0].Text)
			result = map[string]string{"turnId": "turn-1"}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		if m.Method == "turn/start" {
			sendNext()
		}
	}
	os.Exit(0)
}

// TestCommandGuards: a denied command, an approved-once command (a second request is refused) and an
// allowed command; Core sees the denied command and both push attempts, never the allowed one.
func TestCommandGuards(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	granted := false
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Argv []string }
		if r.URL.Path != shim.CommandPath || r.Header.Get("Authorization") != "Bearer run.tok" || json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		line := strings.Join(p.Argv, " ")
		seen = append(seen, line)
		switch d := policies.Command(guardRules, p.Argv); {
		case d.Effect == "deny":
			w.WriteHeader(403)
			_ = json.NewEncoder(w).Encode(shim.Check{Effect: "deny", Rule: d.Rule, Instruction: d.Instruction})
		case line == "git push origin main" && !granted:
			granted = true
			_ = json.NewEncoder(w).Encode(shim.Check{Effect: "allow", Rule: d.Rule, ApprovalID: "ap1"})
		default:
			w.WriteHeader(423)
			_ = json.NewEncoder(w).Encode(shim.Check{Effect: "approval", Rule: d.Rule})
		}
	}))
	defer core.Close()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := append(os.Environ(), "FACTORY_FAKE_CODEX=guards", "FACTORY_URL="+core.URL, "FACTORY_RUN_TOKEN=run.tok", shim.EnvDir+"="+dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeGuardServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work"}, Cwd: t.TempDir(), Env: env, Guards: guardRules})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	r := <-s.Done()
	got := strings.Split(r.Text, "|")
	if r.Status != "COMPLETED" || len(got) != 7 {
		t.Fatalf("%+v", r)
	}
	if got[0] != "untrusted" || got[1] != "decline" || !strings.HasPrefix(got[2], "steer(turn-1):Factory command policy: cliclick is denied by policy rule host-ui-input. Use UI tests.") ||
		got[3] != "accept" || got[4] != "decline" || !strings.Contains(got[5], `"argv":["git","push","origin","main"]`) || got[6] != "accept" {
		t.Fatalf("%q", got)
	}
	if want := []string{"cliclick c:1,1", "git push origin main", "git push origin main"}; !slices.Equal(seen, want) {
		t.Fatalf("Core saw %q", seen)
	}
	// The consumed grant is handed to the PATH shim for exactly one execution.
	if entries, _ := os.ReadDir(filepath.Join(dir, shim.GrantsDir)); len(entries) != 1 {
		t.Fatalf("grants %v", entries)
	}
}

// TestGuardsJudgeAllPartsBeforeGrants: a compound command with two approval-gated parts, or a gated
// part with unexpanded variables, is declined without asking Core, so no single-use grant is consumed;
// a single gated part with a grant is still accepted.
func TestGuardsJudgeAllPartsBeforeGrants(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Argv []string }
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		asked = append(asked, strings.Join(p.Argv, " "))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(shim.Check{Effect: "allow", Rule: "push", ApprovalID: "ap1"}) // every grant exists
	}))
	defer core.Close()
	dir := t.TempDir()
	g := newGuards(guardRules, []string{"FACTORY_URL=" + core.URL, "FACTORY_RUN_TOKEN=run.tok", shim.EnvDir + "=" + dir})
	ask := func(command string) (string, string) {
		params, _ := json.Marshal(map[string]string{"command": command})
		return g.decide(context.Background(), "item/commandExecution/requestApproval", params)
	}
	for _, c := range []struct{ command, note string }{
		{"/bin/zsh -lc 'git push origin main && git push origin dev'", "Run each of them as a separate command"},
		{"git push origin main; git push origin main", "Run each of them as a separate command"},
		{`/bin/zsh -lc 'git push origin "$BRANCH"'`, "literal arguments"},
		{"git push origin ${BRANCH}", "literal arguments"},
		{"git push origin $(git branch --show-current)", "literal arguments"},
		{"git push `echo origin` main", "literal arguments"},
	} {
		if d, note := ask(c.command); d != "decline" || !strings.Contains(note, c.note) {
			t.Errorf("%s: %s %q", c.command, d, note)
		}
	}
	if len(asked) != 0 {
		t.Fatalf("Core asked (grants consumed) for declined commands: %q", asked)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, shim.GrantsDir)); len(entries) != 0 {
		t.Fatalf("grants handed over for declined commands: %v", entries)
	}
	// Literal (single-quoted or escaped) dollars are exact arguments.
	if d, note := ask(`cd app && git push origin 'v$1' \$x`); d != "accept" || note != "" {
		t.Fatalf("%s %q", d, note)
	}
	if want := []string{"git push origin v$1 $x"}; !slices.Equal(asked, want) {
		t.Fatalf("Core saw %q", asked)
	}
}

func TestCommandLines(t *testing.T) {
	cases := map[string][][]string{
		`/bin/zsh -lc 'cd app && FOO=1 /usr/bin/git push origin "main branch"; ls|wc -l'`: {{"cd", "app"}, {"git", "push", "origin", "main branch"}, {"ls"}, {"wc", "-l"}},
		`bash -c "echo \"a b\" \\$x"`: {{"echo", "a b", "$x"}},
		`git status`:                  {{"git", "status"}},
		`(cliclick c:1)`:              {{"cliclick", "c:1"}},
	}
	for in, want := range cases {
		got := commandLines(in)
		if len(got) != len(want) {
			t.Fatalf("%s: %q", in, got)
		}
		for i := range got {
			if !slices.Equal(got[i], want[i]) {
				t.Fatalf("%s: %q", in, got)
			}
		}
	}
	for in, want := range map[string]bool{
		`echo $HOME`: true, `echo "${HOME}/x"`: true, "echo `id`": true, `echo $?`: true,
		`echo '$HOME'`: false, `echo \$HOME`: false, `echo "\$HOME"`: false, `echo 5$`: false, `bash -c "echo \"a b\" \\$x"`: false,
	} {
		if got := commands(in); len(got) != 1 || got[0].unexpanded != want {
			t.Errorf("%s: %+v, want unexpanded %v", in, got, want)
		}
	}
	if newGuards([]policies.Rule{{Name: "a", Type: "access", Commands: []string{"open"}, Effect: "allow"}}, nil).active() {
		t.Fatal("allow-only rules need no approval requests")
	}
}

// TestElicitationAnswer: Factory MCP tool calls are approved, anything else is declined.
func TestElicitationAnswer(t *testing.T) {
	for params, want := range map[string]string{
		`{"serverName":"factory","mode":"form","_meta":{"codex_approval_kind":"mcp_tool_call"},"message":"Allow?"}`: "accept",
		`{"serverName":"other","_meta":{"codex_approval_kind":"mcp_tool_call"}}`:                                    "decline",
		`{"serverName":"factory","mode":"form","message":"Your name?","requestedSchema":{}}`:                        "decline",
		`not json`: "decline",
	} {
		if got := elicitationAnswer(json.RawMessage(params))["action"]; got != want {
			t.Errorf("%s: %v, want %s", params, got, want)
		}
	}
}
