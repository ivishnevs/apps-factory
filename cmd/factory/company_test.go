package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigValidateCompany (postgres-tenancy 2.4): factory config validate --company X checks the catalog in
// configs/X/; without the flag it checks the default company's configs/ tree.
func TestConfigValidateCompany(t *testing.T) {
	root := t.TempDir()
	write := func(path, body string) {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	agent := func(name string) string {
		return "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: " + name + "\nspec:\n  role: developer\n  runtime:\n    provider: codex\n  prompt:\n    file: prompts/agent.md\n  execution:\n    timeout: 1m\n    max_concurrent_runs: 1\n"
	}
	write("prompts/agent.md", "prompt")
	write("configs/agents/builder.yaml", agent("builder"))
	write("configs/acme/agents/designer.yaml", agent("designer"))
	t.Setenv("FACTORY_ROOT", root)
	validate := func(args ...string) (string, error) {
		saved, stdout := os.Args, os.Stdout
		defer func() { os.Args, os.Stdout = saved, stdout }()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Args, os.Stdout = append([]string{"factory"}, args...), w
		err = run()
		w.Close()
		out, _ := io.ReadAll(r)
		return string(out), err
	}
	for args, want := range map[string]string{"config validate": "default builder", "config validate --company acme": "acme designer", "--company acme config validate": "acme designer"} {
		out, err := validate(strings.Fields(args)...)
		var got struct {
			Company string   `json:"company"`
			Agents  []string `json:"agents"`
		}
		if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Company+" "+strings.Join(got.Agents, ",") != want {
			t.Errorf("%s: %v %s", args, err, out)
		}
	}
	if _, err := validate("config", "validate", "--company", "globex"); err == nil || !strings.Contains(err.Error(), "no agents in") {
		t.Fatal(err)
	}
}
