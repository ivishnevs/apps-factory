package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

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
	// Every part is judged before Core is asked about a grant: asking consumes an approved grant, so
	// a grant is used only when the whole command can then run.
	var gated []part
	for _, c := range commands(*p.Command) {
		d := policies.Command(g.rules, c.argv)
		switch d.Effect {
		case "deny":
			_, _, _ = shim.Ask(ctx, g.client, g.url, g.token, c.argv) // best effort: Core records the refusal
			return "decline", fmt.Sprintf("%s is denied by policy rule %s. %s", filepath.Base(c.argv[0]), d.Rule, d.Instruction)
		case "approval":
			gated = append(gated, c)
		}
	}
	for _, c := range gated {
		if c.unexpanded {
			d := policies.Command(g.rules, c.argv)
			return "decline", fmt.Sprintf("%s requires owner approval (rule %s) and contains shell variables or substitutions; an approval grants one exact argv. Run it with literal arguments, then request approval for exactly that command if needed.", strings.Join(c.argv, " "), d.Rule)
		}
	}
	if len(gated) > 1 {
		names := make([]string, len(gated))
		for i, c := range gated {
			names[i] = strings.Join(c.argv, " ")
		}
		return "decline", fmt.Sprintf("this command combines %d commands that each require owner approval (%s); no grant was used. Run each of them as a separate command.", len(gated), strings.Join(names, "; "))
	}
	for _, one := range gated { // at most one
		argv := one.argv
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

// part is one simple command of a shell command line; unexpanded means a word holds a $VAR, ${VAR},
// $(...) or `...` the shell would expand, so argv is not what would run.
type part struct {
	argv       []string
	unexpanded bool
}

// commandLines splits a shell command into the argv of each simple command, unwrapping
// `<shell> -c|-lc SCRIPT`. Leading VAR=value assignments are dropped and argv[0] is reduced to its
// basename. It is a guardrail lexer: substitutions and redirections are not interpreted.
func commandLines(command string) [][]string {
	var out [][]string
	for _, c := range commands(command) {
		out = append(out, c.argv)
	}
	return out
}

// commands is commandLines with each part's unexpanded flag; a `-c` script is judged by its own words.
func commands(command string) []part {
	var out []part
	for _, cmd := range lex(command) {
		w := cmd.words
		if len(w) == 3 && isShell(w[0]) && (w[1] == "-c" || w[1] == "-lc") {
			out = append(out, commands(w[2])...)
			continue
		}
		dyn := cmd.dynamic
		for len(w) > 0 && strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "=") {
			w, dyn = w[1:], dyn[1:]
		}
		if len(w) > 0 {
			w[0] = filepath.Base(w[0])
			out = append(out, part{argv: w, unexpanded: slices.Contains(dyn, true)})
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

// lexed is one simple command: its words and, per word, whether the shell would expand part of it.
type lexed struct {
	words   []string
	dynamic []bool
}

// expands reports whether r[i] starts a parameter expansion or command substitution: $ followed
// by a name, digit, special parameter, { or (, or a backquote.
func expands(r []rune, i int) bool {
	if r[i] == '`' {
		return true
	}
	if r[i] != '$' || i+1 >= len(r) {
		return false
	}
	n := r[i+1]
	return n == '_' || n == '{' || n == '(' || unicode.IsLetter(n) || unicode.IsDigit(n) || strings.ContainsRune("@*#?$!-", n)
}

// lex splits s into simple commands at ; & | newline and parentheses, honouring quotes and backslashes.
func lex(s string) []lexed {
	var cmds []lexed
	var cur lexed
	var word strings.Builder
	inWord, dynamic := false, false
	flushWord := func() {
		if inWord {
			cur.words = append(cur.words, word.String())
			cur.dynamic = append(cur.dynamic, dynamic)
			word.Reset()
			inWord, dynamic = false, false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(cur.words) > 0 {
			cmds = append(cmds, cur)
			cur = lexed{}
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
				} else if expands(r, i) {
					dynamic = true
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
			dynamic = dynamic || expands(r, i)
			word.WriteRune(c)
		}
	}
	flushCmd()
	return cmds
}
