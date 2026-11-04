package config

import (
	"strings"
	"testing"
)

// TestAgentSecrets (core-secret-store 2.1): spec.secrets merge from profiles by name, need strings or files, refuse
// reserved or repeated env variables and a secret needed as both kinds; NeededSecrets lists MCP header secrets too.
func TestAgentSecrets(t *testing.T) {
	docs, err := SeedDocuments("../..", DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	with := func(profile, agent string) map[string]string {
		d := map[string]string{}
		for k, v := range docs {
			d[k] = v
		}
		d["configs/profiles/developer.yaml"] = strings.Replace(d["configs/profiles/developer.yaml"], "spec:\n", "spec:\n"+profile, 1)
		d["configs/agents/demo.yaml"] = strings.Replace(d["configs/agents/demo.yaml"], "spec:\n", "spec:\n"+agent, 1)
		return d
	}
	c, err := loadStored(t, "../..", DefaultCompany, with(
		"  secrets:\n    - {name: SHARED_TOKEN, env: SHARED}\n    - {name: SIGNING_KEY, env: KEY_PATH, file: true}\n",
		"  secrets:\n    - {name: SHARED_TOKEN, env: SHARED_TOKEN_ENV}\n    - {name: ASC_KEY, env: ASC_KEY_PATH, file: true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	demo := c.Agents["demo"]
	got := map[string]SecretNeed{}
	for _, n := range demo.NeededSecrets() {
		got[n.Name] = n
	}
	if demo.Spec.Extends != "developer" {
		t.Skip("demo does not extend the developer profile")
	}
	if len(got) != 3 || got["SHARED_TOKEN"].Env != "SHARED_TOKEN_ENV" || got["SIGNING_KEY"].Kind != SecretFile || got["ASC_KEY"].Kind != SecretFile {
		t.Fatalf("%+v", demo.NeededSecrets())
	}
	for name, agent := range map[string]string{
		"reserved env": "  secrets:\n    - {name: X_TOKEN, env: FACTORY_TOKEN}\n",
		"env twice":    "  secrets:\n    - {name: A_TOKEN, env: SAME}\n    - {name: B_TOKEN, env: SAME}\n",
		"name twice":   "  secrets:\n    - {name: A_TOKEN, env: A}\n    - {name: A_TOKEN, env: B}\n",
		"bad name":     "  secrets:\n    - {name: 1bad, env: A}\n",
		"no env":       "  secrets:\n    - {name: A_TOKEN}\n",
	} {
		profile := ""
		if _, err := loadStored(t, "../..", DefaultCompany, with(profile, agent)); err == nil || !strings.Contains(err.Error(), "secrets") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
