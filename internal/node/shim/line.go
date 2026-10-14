package shim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"appfactory/internal/policies"
)

// Guard decides whole shell command lines inside a runtime (runtime-drivers D2): Codex approval requests
// and the Claude PreToolUse hook (through `factory-shim --decide`). Each simple command is decided with the
// same rules as the PATH shim: deny → refuse with the rule's instruction, approval → allow only with Core's
// single-use exact grant (handed to the PATH shim), allow → allow. The PATH shims stay in place.
type Guard struct {
	Rules      []policies.Rule
	URL, Token string
	Dir        string // FACTORY_SHIM_DIR; "" hands no grant over
	Client     *http.Client
}

// NewGuard keeps the command access rules and reads FACTORY_URL, FACTORY_RUN_TOKEN and EnvDir from env.
func NewGuard(rules []policies.Rule, env []string) *Guard {
	g := &Guard{Client: &http.Client{Timeout: 10 * time.Second}}
	for _, r := range rules {
		if r.Type == "access" && len(r.Commands) > 0 {
			g.Rules = append(g.Rules, r)
		}
	}
	for _, v := range env {
		k, val, _ := strings.Cut(v, "=")
		switch k {
		case "FACTORY_URL":
			g.URL = val
		case "FACTORY_RUN_TOKEN":
			g.Token = val
		case EnvDir:
			g.Dir = val
		}
	}
	return g
}

// Active means some rule denies or gates a command.
func (g *Guard) Active() bool { return len(policies.Guarded(g.Rules)) > 0 }

// Line decides a shell command line; when it refuses, note tells the agent why.
func (g *Guard) Line(ctx context.Context, command string) (ok bool, note string) {
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Every part is judged before Core is asked about a grant: asking consumes an approved grant, so
	// a grant is used only when the whole command can then run.
	var gated []Part
	for _, c := range Parts(command) {
		d := policies.Command(g.Rules, c.Argv)
		switch d.Effect {
		case "deny":
			_, _, _ = Ask(ctx, client, g.URL, g.Token, c.Argv) // best effort: Core records the refusal
			return false, fmt.Sprintf("%s is denied by policy rule %s. %s", filepath.Base(c.Argv[0]), d.Rule, d.Instruction)
		case "approval":
			gated = append(gated, c)
		}
	}
	for _, c := range gated {
		if c.Unexpanded {
			d := policies.Command(g.Rules, c.Argv)
			return false, fmt.Sprintf("%s requires owner approval (rule %s) and contains shell variables or substitutions; an approval grants one exact argv. Run it with literal arguments, then request approval for exactly that command if needed.", strings.Join(c.Argv, " "), d.Rule)
		}
	}
	if len(gated) > 1 {
		names := make([]string, len(gated))
		for i, c := range gated {
			names[i] = strings.Join(c.Argv, " ")
		}
		return false, fmt.Sprintf("this command combines %d commands that each require owner approval (%s); no grant was used. Run each of them as a separate command.", len(gated), strings.Join(names, "; "))
	}
	for _, one := range gated { // at most one
		argv := one.Argv
		d := policies.Command(g.Rules, argv)
		c, status, err := Ask(ctx, client, g.URL, g.Token, argv)
		if err != nil {
			return false, fmt.Sprintf("%s requires owner approval (rule %s) and the approval check failed: %v", argv[0], d.Rule, err)
		}
		if status != http.StatusOK || c.Effect != "allow" {
			hint := c.Instruction
			if hint == "" || c.Effect == "approval" {
				hint = ApprovalHint(argv, d.Instruction)
			}
			return false, fmt.Sprintf("%s refused (%s, rule %s). %s", strings.Join(argv, " "), c.Effect, d.Rule, hint)
		}
		if g.Dir != "" {
			if err = Grant(g.Dir, argv); err != nil {
				return false, fmt.Sprintf("%s was approved but the grant could not be handed to the command shim: %v", argv[0], err)
			}
		}
	}
	return true, ""
}

// Binary is the name factory-shim answers to directly; any other name is a shimmed command.
const Binary = "factory-shim"

// DecideFlag selects the line mode of factory-shim: `factory-shim --decide` reads a DecideRequest on
// stdin and writes one Decision on stdout (exit 0), deciding with $FACTORY_SHIM_DIR/rules.json, the
// run token and the grants directory exactly like the PATH shim.
const DecideFlag = "--decide"

type DecideRequest struct {
	Command string `json:"command"`
}

// Decision answers DecideRequest: "allow" or "deny" with the reason for the agent.
type Decision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// Decide runs the line mode; any failure is a deny (fail closed).
func Decide(ctx context.Context, getenv func(string) string, client *http.Client, in io.Reader, out io.Writer) int {
	write := func(d Decision) int {
		if err := json.NewEncoder(out).Encode(d); err != nil {
			return Refused
		}
		return 0
	}
	deny := func(err error) int {
		return write(Decision{Decision: "deny", Reason: "Factory command guard: " + err.Error()})
	}
	var req DecideRequest
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&req); err != nil {
		return deny(fmt.Errorf("unreadable request: %w", err))
	}
	dir := getenv(EnvDir)
	if dir == "" {
		return deny(errors.New(EnvDir + " is unset"))
	}
	rules, err := readRules(dir)
	if err != nil {
		return deny(fmt.Errorf("command rules unreadable: %w", err))
	}
	g := &Guard{Rules: rules, URL: getenv("FACTORY_URL"), Token: getenv("FACTORY_RUN_TOKEN"), Dir: dir, Client: client}
	if ok, note := g.Line(ctx, req.Command); !ok {
		return write(Decision{Decision: "deny", Reason: "Factory command policy: " + note})
	}
	return write(Decision{Decision: "allow"})
}

func readRules(dir string) ([]policies.Rule, error) {
	var rules []policies.Rule
	b, err := os.ReadFile(filepath.Join(dir, RulesFile))
	if err == nil {
		err = json.Unmarshal(b, &rules)
	}
	return rules, err
}

// Executable is the factory-shim binary behind a run's shim directory (the target of its command links);
// "" when nothing in dir is guarded.
func Executable(dir string, rules []policies.Rule) (string, error) {
	names := policies.Guarded(rules)
	if len(names) == 0 {
		return "", nil
	}
	target, err := os.Readlink(filepath.Join(dir, names[0]))
	if err != nil {
		return "", fmt.Errorf("factory-shim link for %s: %w", names[0], err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	return target, nil
}

// Part is one simple command of a shell command line; Unexpanded means a word holds a $VAR, ${VAR},
// $(...) or `...` the shell would expand, or the command is ambiguous to the lexer (a heredoc or process
// substitution in the line before it or in it, a redirection without a target, a {var} file descriptor), so
// Argv is not known to be what would run.
type Part struct {
	Argv       []string
	Unexpanded bool
}

// CommandLines splits a shell command into the argv of each simple command, unwrapping
// `<shell> -c|-lc SCRIPT`. Leading VAR=value assignments are dropped and argv[0] is reduced to its
// basename. Redirections ([n]>file, [n]>>file, [n]<file, &>file, &>>file, [n]>&m, [n]<&m, >|file, <>file,
// <<<word) and their targets are not arguments. It is a guardrail lexer: substitutions are not interpreted.
func CommandLines(command string) [][]string {
	var out [][]string
	for _, c := range Parts(command) {
		out = append(out, c.Argv)
	}
	return out
}

// Parts is CommandLines with each part's Unexpanded flag; a `-c` script is judged by its own words.
func Parts(command string) []Part {
	var out []Part
	for _, cmd := range lex(command) {
		w := cmd.words
		if len(w) == 3 && isShell(w[0]) && (w[1] == "-c" || w[1] == "-lc") {
			inner := Parts(w[2])
			for i := range inner {
				inner[i].Unexpanded = inner[i].Unexpanded || cmd.ambiguous
			}
			out = append(out, inner...)
			continue
		}
		dyn := cmd.dynamic
		for len(w) > 0 && strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "=") {
			w, dyn = w[1:], dyn[1:]
		}
		if len(w) > 0 {
			w[0] = filepath.Base(w[0])
			out = append(out, Part{Argv: w, Unexpanded: cmd.ambiguous || slices.Contains(dyn, true)})
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

// lexed is one simple command: its words and, per word, whether the shell would expand part of it;
// ambiguous means the lexer cannot tell its argv for sure.
type lexed struct {
	words     []string
	dynamic   []bool
	ambiguous bool
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

// at reports whether r[i] is c.
func at(r []rune, i int, c rune) bool { return i < len(r) && r[i] == c }

// lex splits s into simple commands at ; & | newline and parentheses, honouring quotes and backslashes.
// Redirection operators and their target words are dropped; `&` in `&>` and `>&` is not a separator. A
// heredoc (<<, <<-) or a process substitution (<(...), >(...)) makes its command and every later one in s
// ambiguous: the lexer does not know where a heredoc's body ends or what a substitution runs.
func lex(s string) []lexed {
	var cmds []lexed
	var cur lexed
	var word strings.Builder
	inWord, dynamic, quoted := false, false, false
	target := false  // the next word is a redirection target, not an argument
	tainted := false // a heredoc or process substitution appeared: the rest of s is ambiguous
	resetWord := func() {
		word.Reset()
		inWord, dynamic, quoted = false, false, false
	}
	flushWord := func() {
		if !inWord {
			return
		}
		if target {
			target = false
		} else {
			cur.words = append(cur.words, word.String())
			cur.dynamic = append(cur.dynamic, dynamic)
		}
		resetWord()
	}
	flushCmd := func() {
		flushWord()
		if target { // a redirection without its target
			cur.ambiguous, target = true, false
		}
		cur.ambiguous = cur.ambiguous || tainted
		if len(cur.words) > 0 {
			cmds = append(cmds, cur)
		}
		cur = lexed{}
	}
	// operator starts a redirection at r[i]: a word of digits right before it is its file descriptor
	// (not an argument), a {name} word a variable descriptor (ambiguous).
	operator := func() {
		if inWord && !quoted && !dynamic {
			w := word.String()
			if strings.Trim(w, "0123456789") == "" {
				resetWord()
			} else if strings.HasPrefix(w, "{") && strings.HasSuffix(w, "}") {
				resetWord()
				cur.ambiguous = true
			}
		}
		flushWord()
		if target { // an operator where its predecessor's target belongs
			cur.ambiguous, target = true, false
		}
	}
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			inWord, quoted = true, true
			for i++; i < len(r) && r[i] != '\''; i++ {
				word.WriteRune(r[i])
			}
		case c == '"':
			inWord, quoted = true, true
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
				inWord, quoted = true, true
				word.WriteRune(r[i])
			}
		case c == '<' || c == '>':
			operator()
			if at(r, i+1, '(') { // process substitution; the ( then splits like a subshell
				cur.ambiguous, tainted = true, true
				continue
			}
			switch {
			case c == '<' && at(r, i+1, '<') && at(r, i+2, '<'): // <<< here-string
				i += 2
			case c == '<' && at(r, i+1, '<'): // << or <<- heredoc: the delimiter is the target
				i++
				if at(r, i+1, '-') {
					i++
				}
				cur.ambiguous, tainted = true, true
			case c == '<' && (at(r, i+1, '&') || at(r, i+1, '>')): // <& <>
				i++
			case c == '>' && (at(r, i+1, '>') || at(r, i+1, '&') || at(r, i+1, '|')): // >> >& >|
				i++
			}
			target = true
		case c == '&' && at(r, i+1, '>'): // &> and &>>
			operator()
			i++
			if at(r, i+1, '>') {
				i++
			}
			target = true
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
