package presets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/config"
)

// bareRoot is a factory root with the release's skills and nothing else: no repository prompts or catalogs, so a
// copied team must carry everything it needs as documents (team-builder D2).
func bareRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "skills"), os.DirFS("../../skills")); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestPresets (team-builder 3.1, 3.2, 6.1): the library loads; every team preset copied into a new company loads
// without repository prompts on disk, with its agents named, its lead reporting to the owner, prompts and
// Factory skills as documents and reviewers of its policies mapped to its agents; a second team leaves the first
// unchanged; an agent from a preset takes overrides; taken names are refused.
func TestPresets(t *testing.T) {
	lib, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Agents) < 8 || len(lib.Teams) < 5 || lib.Teams["game-dev"] == nil || lib.Teams["saas"] == nil {
		t.Fatal(len(lib.Agents), lib.TeamNames())
	}
	root := bareRoot(t)
	for _, name := range lib.TeamNames() {
		tp := lib.Teams[name]
		base, err := lib.Base("acme")
		if err != nil {
			t.Fatal(err)
		}
		keys := map[string]string{}
		for _, a := range tp.Spec.Agents {
			keys[a.Key] = a.Key
		}
		docs, err := lib.CopyTeam("acme", TeamCopy{Preset: name, Names: keys}, base)
		if err != nil {
			t.Fatal(name, err)
		}
		c, err := config.LoadStoredCatalog(root, "acme", docs)
		if err != nil {
			t.Fatal(name, err)
		}
		if len(c.Agents) != len(tp.Spec.Agents) || c.Agents[tp.Spec.Lead].Spec.ReportsTo != "owner" || len(c.Teams) != 1 {
			t.Fatal(name, config.Names(c.Agents))
		}
		for n, a := range c.Agents {
			if a.Spec.Team != tp.Spec.Team || !strings.HasPrefix(a.Metadata.Preset, "") || a.Metadata.Preset == "" || a.Instructions == "" || docs["prompts/"+n+".md"] == "" {
				t.Fatalf("%s/%s: %+v", name, n, a.Metadata)
			}
			for _, s := range a.Spec.Skills.Include {
				if c.Skills.Skills[s].Hash == "" {
					t.Fatalf("%s/%s: skill %s not installed", name, n, s)
				}
			}
		}
		if name == "full-team" {
			if req, reviewer := c.Agents["developer"].Review(); !req || reviewer != "qa" || c.Agents["developer"].ReviewPaths()[0].Reviewer != "product-owner" {
				t.Fatal("full-team review", req, reviewer)
			}
		}
		for p := range docs {
			if !strings.HasPrefix(p, "configs/acme/") && !strings.HasPrefix(p, "prompts/") {
				t.Fatal(name, "document outside the company:", p)
			}
		}
	}

	// A second team from another preset: agents prefixed with the team, the first team's documents unchanged.
	base, _ := lib.Base("acme")
	first, err := lib.CopyTeam("acme", TeamCopy{Preset: "full-team", Names: map[string]string{"product-owner": "product-owner", "designer": "designer", "developer": "developer", "qa": "qa"}}, base)
	if err != nil {
		t.Fatal(err)
	}
	both, err := lib.CopyTeam("acme", TeamCopy{Preset: "saas", Team: "web"}, first)
	if err != nil {
		t.Fatal(err)
	}
	for p, v := range first {
		if p != "configs/acme/skills.yaml" && both[p] != v {
			t.Fatal("the second team changed", p)
		}
	}
	c, err := config.LoadStoredCatalog(root, "acme", both)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Teams) != 2 || c.Agents["web-frontend"].Spec.Team != "web" || c.Agents["web-frontend"].Spec.ReportsTo != "web-product-owner" {
		t.Fatal(config.Names(c.Agents))
	}
	if _, err = lib.CopyTeam("acme", TeamCopy{Preset: "solo", Team: "web"}, both); !errors.Is(err, ErrInput) {
		t.Fatal("a second team web", err)
	}

	// One agent from a preset, with overrides; a taken name is refused.
	withIOS, n, err := lib.CopyAgent("acme", "product", "ios-developer", "", AgentOverrides{Provider: "claude", QuotaKey: "claude-subscription", Prompt: "Custom iOS prompt.",
		ContextWindow: config.ContextWindow{Work: "max", Conversation: "200k"}, MaxConcurrentRuns: 2, ConversationMaxRuns: 2}, both)
	if err != nil || n != "product-ios-developer" {
		t.Fatal(n, err)
	}
	c, err = config.LoadStoredCatalog(root, "acme", withIOS)
	if err != nil {
		t.Fatal(err)
	}
	ios := c.Agents["product-ios-developer"]
	if ios.Spec.Runtime.Provider != "claude" || ios.Spec.ReportsTo != "product-owner" || !strings.HasPrefix(ios.Instructions, "Custom iOS prompt.") || ios.Timeout.Minutes() != 90 || len(ios.Spec.Labels) != 1 {
		t.Fatalf("%+v", ios.Spec)
	}
	// work-and-conversation-runs: the overrides write the window per kind and the conversation cap.
	if ios.Spec.Runtime.ContextWindow.Tokens(true) != 200_000 || ios.Spec.Runtime.ContextWindow.Work != "max" || ios.Spec.Execution.Conversation.MaxConcurrentRuns != 2 || ios.Spec.Execution.MaxConcurrentRuns != 2 {
		t.Fatalf("run kinds %+v %+v", ios.Spec.Runtime.ContextWindow, ios.Spec.Execution.Conversation)
	}
	if _, _, err = lib.CopyAgent("acme", "product", "developer", "developer", AgentOverrides{}, withIOS); !errors.Is(err, ErrInput) {
		t.Fatal("taken name", err)
	}
	if _, _, err = lib.CopyAgent("acme", "nope", "developer", "", AgentOverrides{}, withIOS); !errors.Is(err, ErrInput) {
		t.Fatal("unknown team", err)
	}
}

// TestPresetJSONLists: the gallery's lists are never null.
func TestPresetJSONLists(t *testing.T) {
	lib, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range lib.Teams {
		if tp.Secrets == nil || tp.Skills == nil {
			t.Fatal(tp.Metadata.Name)
		}
	}
	for _, a := range lib.Agents {
		if a.Spec.Secrets == nil || a.Spec.Skills == nil {
			t.Fatal(a.Metadata.Name)
		}
	}
}

// TestPresetCapabilities (agent-capabilities 2.1): every agent preset says what it does, needs, produces and who
// reviews it.
func TestPresetCapabilities(t *testing.T) {
	lib, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range lib.Agents {
		caps, _ := a.Spec.Agent["capabilities"].([]any)
		have := map[string]bool{}
		for _, c := range caps {
			s, _ := c.(string)
			for _, k := range []string{"Does:", "Needs:", "Produces:", "Reviewed by:"} {
				if strings.HasPrefix(s, k) {
					have[k] = true
				}
			}
		}
		if len(have) != 4 {
			t.Errorf("preset %s capabilities %v miss does/needs/produces/reviewed by", name, caps)
		}
	}
}

// TestFactorySkills (default-agent-library 2.2): every Factory skill shipped in the release has a license, an eval
// (an example task and what good output contains) and stays within the size budget.
func TestFactorySkills(t *testing.T) {
	dirs, err := os.ReadDir("../../skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join("../../skills", d.Name())
		b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		fm := s
		if i := strings.Index(s[4:], "\n---"); strings.HasPrefix(s, "---\n") && i > 0 {
			fm = s[:i+4]
		}
		if !strings.Contains(fm, "\nlicense: ") || !strings.Contains(fm, "\n  eval: ") {
			t.Errorf("skill %s: license and metadata.eval required", d.Name())
		}
		if len(b) > 8<<10 {
			t.Errorf("skill %s: SKILL.md is %d bytes, budget 8 KiB", d.Name(), len(b))
		}
		var total int64
		_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, _ error) error {
			if fi != nil && !fi.IsDir() {
				total += fi.Size()
			}
			return nil
		})
		if total > 64<<10 {
			t.Errorf("skill %s: %d bytes, budget 64 KiB", d.Name(), total)
		}
	}
}
