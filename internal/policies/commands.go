package policies

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Builtins are the reserved mandatory policies: YAML cannot replace them and agents cannot drop them.
func Builtins() []Policy { return []Policy{System(), HostUI(), OwnerCLI()} }

// HostUI denies host-input tools (node-agent D6): agents test apps without driving the owner's screen.
func HostUI() Policy {
	return builtin("host-ui", Rule{Name: "host-ui-input", Type: "access", Commands: []string{"cliclick", "osascript", "osacompile", "xdotool"}, Effect: "deny", Instruction: "Never drive the host UI with clicks, keystrokes, AppleScript or JXA. Use debug launch arguments, deep links, or UI tests that find elements by accessibility identifier."})
}

// OwnerCLI denies the owner console to agents (explicit-exits-authority D9).
func OwnerCLI() Policy {
	return builtin("owner-cli", Rule{Name: "owner-cli", Type: "access", Commands: []string{"factory"}, Effect: "deny", Instruction: "The factory CLI is the owner's console. Use the Factory MCP tools (factory_*) for tasks, messages, approvals, reports and decisions."})
}
func builtin(name string, rules ...Rule) Policy {
	var p Policy
	p.APIVersion, p.Kind, p.Metadata.Name = "factory/v1", "Policy", name
	p.Spec.Mandatory, p.Spec.Rules = true, rules
	return p
}

var commandName = regexp.MustCompile(`^[A-Za-z0-9_+][A-Za-z0-9._+-]*$`)

// validCommand requires a literal command name as the first word: it names the shim.
func validCommand(c string) error {
	if c != strings.TrimSpace(c) || strings.ContainsAny(c, "[]\t\n") || strings.Contains(c, "  ") {
		return fmt.Errorf("invalid command pattern %q", c)
	}
	if !commandName.MatchString(CommandName(c)) {
		return fmt.Errorf("command pattern %q must start with a literal command name", c)
	}
	return nil
}

// CommandName is the command a pattern guards: its first word.
func CommandName(pattern string) string {
	name, _, _ := strings.Cut(pattern, " ")
	return name
}

// MatchCommand matches a pattern against the basename of argv[0] and against the command line (that
// basename and the arguments joined by single spaces). * matches any text, ? one character.
func MatchCommand(pattern string, argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	name := filepath.Base(argv[0])
	line := strings.Join(append([]string{name}, argv[1:]...), " ")
	return glob(pattern, name) || glob(pattern, line)
}
func glob(p, s string) bool {
	px, sx := []rune(p), []rune(s)
	star, mark := -1, 0
	i, j := 0, 0
	for j < len(sx) {
		switch {
		case i < len(px) && (px[i] == '?' || px[i] == sx[j]):
			i++
			j++
		case i < len(px) && px[i] == '*':
			star, mark = i, j
			i++
		case star >= 0:
			i, mark = star+1, mark+1
			j = mark
		default:
			return false
		}
	}
	for i < len(px) && px[i] == '*' {
		i++
	}
	return i == len(px)
}

type CommandDecision struct {
	Effect      string `json:"effect"`
	Rule        string `json:"rule,omitempty"`
	Instruction string `json:"instruction,omitempty"`
}

// Command decides argv with the access rules: deny > approval > allow; no matching rule allows.
// The first matching rule of the winning effect supplies the instruction.
func Command(rules []Rule, argv []string) CommandDecision {
	rank := map[string]int{"allow": 0, "approval": 1, "deny": 2}
	d := CommandDecision{Effect: "allow"}
	for _, r := range rules {
		if r.Type != "access" {
			continue
		}
		for _, c := range r.Commands {
			if MatchCommand(c, argv) {
				if d.Rule == "" || rank[r.Effect] > rank[d.Effect] {
					d = CommandDecision{Effect: r.Effect, Rule: r.Name, Instruction: r.Instruction}
				}
				break
			}
		}
	}
	return d
}

// Guarded lists the commands that need a shim: those named by deny and approval rules.
func Guarded(rules []Rule) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range rules {
		if r.Type != "access" || r.Effect == "allow" {
			continue
		}
		for _, c := range r.Commands {
			if n := CommandName(c); !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}
