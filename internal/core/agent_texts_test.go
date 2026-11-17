package core

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"appfactory/internal/closedset"
	"appfactory/internal/factorytools"
	"appfactory/internal/ownermcp"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

var (
	sqlText  = regexp.MustCompile(`^\s*(SELECT|UPDATE|INSERT|DELETE|WITH)\b`)
	toolName = regexp.MustCompile(`\bfactory_[a-z_]+[a-z]\b`)
	// closedArgs are the closed-set arguments a text may name a value of, per tool (contracts-at-the-boundary §3).
	closedArgs = map[string]map[string]closedset.Set{
		"factory_report_create":             {"kind": closedset.ReportKinds},
		"factory_report_list":               {"kind": closedset.ReportListKinds},
		"factory_message_send":              {"message_type": closedset.MessageTypes},
		"factory_decision_list":             {"status": closedset.DecisionListStatuses},
		"factory_decision_record":           {"status": closedset.DecisionRecordStatuses},
		"factory_artifact_request_approval": {"audience": closedset.ArtifactAudiences},
		"factory_task_list":                 {"status": closedset.TaskStatuses},
	}
)

// agentTextProblems lists what an agent-facing text names wrongly: a factory_* tool that does not exist, or a value of a
// closed-set argument of a tool it names that the tool does not accept ("factory_report_create ... kind audit").
func agentTextProblems(text string, tools map[string]bool) []string {
	var out []string
	for _, at := range toolName.FindAllStringIndex(text, -1) {
		name := text[at[0]:at[1]]
		if !tools[name] && !strings.HasSuffix(name, "_") {
			out = append(out, "unknown tool "+name)
		}
		// The sentence that names the tool: its values are the tool's.
		near := text[at[1]:]
		if i := strings.IndexAny(near, ".\n"); i >= 0 {
			near = near[:i]
		}
		for arg, set := range closedArgs[name] {
			re := regexp.MustCompile(`\b` + arg + `\b[ :=]+(?:of )?["'` + "`" + `]?([A-Za-z_]+)`)
			for _, m := range re.FindAllStringSubmatch(near, -1) {
				if !set.Has(m[1]) {
					out = append(out, name+": "+set.Refusal(arg, m[1]))
				}
			}
		}
	}
	return out
}

func toolNames() map[string]bool {
	tools := map[string]bool{}
	for _, d := range append(factorytools.Definitions(), factorytools.ExitDefinitions()...) {
		tools[d.Name] = true
	}
	local := ownermcp.Local{
		NodeDoctor: func(context.Context) (string, bool, error) { return "", true, nil },
		NodeLog:    func(context.Context, int) (string, error) { return "", nil },
	}
	for _, d := range (&ownermcp.Server{Local: local}).Tools() { // the owner's tools the factory-setup skill names
		tools[d.Name] = true
	}
	return tools
}

// TestAgentTextsUseValidCalls (contracts-at-the-boundary §4): every agent-facing text Core and the node generate (the
// onboarding audit brief, review and conversation notes, hold hints, refusal hints, reminders, briefs: every string
// literal of the packages that write them) names only tools that exist and argument values those tools accept.
func TestAgentTextsUseValidCalls(t *testing.T) {
	tools := toolNames()
	// The checker itself: the 2026-11-15 brief's kind would have failed.
	if p := agentTextProblems("a report with options (factory_report_create, kind audit)", tools); len(p) != 1 {
		t.Fatal("the checker misses an invalid report kind:", p)
	}
	if p := agentTextProblems("factory_report_nonsense", tools); len(p) != 1 {
		t.Fatal("the checker misses an unknown tool:", p)
	}
	texts := []string{auditBrief(tasks.Project{ID: "p"}), worker.ReviewCopyNote}
	for _, dir := range []string{".", "../storage", "../worker", "../messaging", "../factorytools", "../tasks"} {
		texts = append(texts, stringLiterals(t, dir)...)
	}
	// The default library Core ships (prompts, presets, skills) is read by agents the same way.
	for _, pattern := range []string{"../../prompts/*.md", "../../presets/*/*.yaml", "../../skills/*/SKILL.md"} {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			texts = append(texts, string(b))
		}
	}
	for _, text := range texts {
		for _, p := range agentTextProblems(text, tools) {
			t.Errorf("%s in %q", p, shorten(text))
		}
	}
}

// TestAuditBriefAsksForADecision (project-creation, contracts-at-the-boundary §4): the onboarding audit asks for a
// decision proposal with the options, a stage_completed report and the branch rule, not a report with options.
func TestAuditBriefAsksForADecision(t *testing.T) {
	b := auditBrief(tasks.Project{ID: "shop"})
	for _, want := range []string{"factory_decision_propose", "alternatives", "rough size", "stage_completed", "task branch"} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q: %s", want, b)
		}
	}
	if strings.Contains(b, "needs_owner_action") {
		t.Error("the brief still asks for an owner-action report:", b)
	}
}

// stringLiterals are the string literals of the non-test Go files in dir.
func stringLiterals(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				// Prose only: identifiers (cookie names) have no space, queries are SQL.
				if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, "factory_") && strings.Contains(s, " ") && !sqlText.MatchString(s) {
					out = append(out, s)
				}
			}
			return true
		})
	}
	return out
}

func shorten(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
