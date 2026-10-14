package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
)

// guards maps app-server approval requests onto the agent's command rules (runtime-drivers D2):
// deny → decline with the rule's instruction, approval → accept only with Core's single-use exact grant,
// allow → accept. The PATH shims stay in place; a grant consumed here is handed to the shim.
type guards struct {
	rules      []policies.Rule
	url, token string
	dir        string // FACTORY_SHIM_DIR
	client     *http.Client
}

func newGuards(rules []policies.Rule, env []string) *guards {
	g := &guards{client: &http.Client{Timeout: 10 * time.Second}}
	for _, r := range rules {
		if r.Type == "access" && len(r.Commands) > 0 {
			g.rules = append(g.rules, r)
		}
	}
	for _, v := range env {
		k, val, _ := strings.Cut(v, "=")
		switch k {
		case "FACTORY_URL":
			g.url = val
		case "FACTORY_RUN_TOKEN":
			g.token = val
		case shim.EnvDir:
			g.dir = val
		}
	}
	return g
}

// active means the thread asks for approvals: some rule denies or gates a command.
func (g *guards) active() bool { return len(policies.Guarded(g.rules)) > 0 }

// decide answers an approval request; note, when set, tells the agent why it was declined.
func (g *guards) decide(ctx context.Context, method string, params json.RawMessage) (decision, note string) {
	if method != "item/commandExecution/requestApproval" {
		return "accept", "" // file changes: command rules do not cover them
	}
	var p struct {
		Command *string `json:"command"`
		Kind    string  `json:"kind"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "decline", "Factory could not read the command approval request."
	}
	if p.Kind == "writeStdin" || p.Command == nil {
		return "accept", "" // input to a terminal whose command was already decided
	}
	var gated [][]string
	for _, argv := range commandLines(*p.Command) {
		d := policies.Command(g.rules, argv)
		switch d.Effect {
		case "deny":
			_, _, _ = shim.Ask(ctx, g.client, g.url, g.token, argv) // best effort: Core records the refusal
			return "decline", fmt.Sprintf("%s is denied by policy rule %s. %s", filepath.Base(argv[0]), d.Rule, d.Instruction)
		case "approval":
			gated = append(gated, argv)
		}
	}
	for _, argv := range gated {
		d := policies.Command(g.rules, argv)
		c, status, err := shim.Ask(ctx, g.client, g.url, g.token, argv)
		if err != nil {
			return "decline", fmt.Sprintf("%s requires owner approval (rule %s) and the approval check failed: %v", argv[0], d.Rule, err)
		}
		if status != http.StatusOK || c.Effect != "allow" {
			hint := c.Instruction
			if hint == "" || c.Effect == "approval" {
				hint = shim.ApprovalHint(argv, d.Instruction)
			}
			return "decline", fmt.Sprintf("%s refused (%s, rule %s). %s", strings.Join(argv, " "), c.Effect, d.Rule, hint)
		}
		if g.dir != "" {
			if err = shim.Grant(g.dir, argv); err != nil {
				return "decline", fmt.Sprintf("%s was approved but the grant could not be handed to the command shim: %v", argv[0], err)
			}
		}
	}
	return "accept", ""
}

// commandLines splits a shell command into the argv of each simple command, unwrapping
// `<shell> -c|-lc SCRIPT`. Leading VAR=value assignments are dropped and argv[0] is reduced to its
// basename. It is a guardrail lexer: substitutions and redirections are not interpreted.
func commandLines(command string) [][]string {
	words := lex(command)
	var out [][]string
	for _, w := range words {
		if len(w) == 3 && isShell(w[0]) && (w[1] == "-c" || w[1] == "-lc") {
			out = append(out, commandLines(w[2])...)
			continue
		}
		for len(w) > 0 && strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "=") {
			w = w[1:]
		}
		if len(w) > 0 {
			w[0] = filepath.Base(w[0])
			out = append(out, w)
		}
	}
	return out
}
func isShell(s string) bool {
	switch filepath.Base(s) {
	case "sh", "bash", "zsh", "dash":
		return true
	}
	return false
}

// lex splits s into simple commands at ; & | newline and parentheses, honouring quotes and backslashes.
func lex(s string) [][]string {
	var cmds [][]string
	var cur []string
	var word strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			cur = append(cur, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(cur) > 0 {
			cmds = append(cmds, cur)
			cur = nil
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			inWord = true
			for i++; i < len(r) && r[i] != '\''; i++ {
				word.WriteRune(r[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) && strings.ContainsRune(`"\$`+"`", r[i+1]) {
					i++
				}
				word.WriteRune(r[i])
			}
		case c == '\\' && i+1 < len(r):
			i++
			if r[i] != '\n' {
				inWord = true
				word.WriteRune(r[i])
			}
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')':
			flushCmd()
		case c == ' ' || c == '\t':
			flushWord()
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	flushCmd()
	return cmds
}
