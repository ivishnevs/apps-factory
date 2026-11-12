package core

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestAgentSecretsAPI (agent-secrets-ui 1.1, 2.1, 3.1): an agent's secret uses list its own, those inherited from its
// profile (with the profile) and their state (set, missing, wrong kind); its editor saves its own uses as a catalog
// change with a note; a file secret as a text variable (or the reverse) is refused with a hint, a use it already had
// stays; the Secrets page gives one secret to several agents in one change; GET /secrets names the agents; only
// owners and admins write.
func TestAgentSecretsAPI(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	co := storage.DefaultCompany
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	c.Handler()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	s.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{9}, secrets.KeySize), nil)
	for name, kind := range map[string]string{"GAME_SERVER_SSH_KEY": storage.SecretFile, "API_TOKEN": storage.SecretString} {
		if _, err = s.SetSecret(ctx, co, name, kind, []byte("value-of-"+name), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	// The developer profile (demo extends it) gives a secret nobody has set.
	cur, err := s.CurrentCatalog(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	docs := cloneDocs(cur.Docs)
	docs["configs/profiles/developer.yaml"] = strings.Replace(docs["configs/profiles/developer.yaml"], "spec:\n", "spec:\n  secrets:\n    - {name: SHARED_TOKEN, env: SHARED}\n", 1)
	if _, err = c.SaveCatalog(ctx, co, storage.CatalogSave{Base: cur.Version, Docs: docs, Author: "owner", Note: "profile secret"}); err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any) (int, string) {
		t.Helper()
		return call(t, c, method, path, c.Token, body)
	}
	uses := func() AgentSecrets {
		t.Helper()
		code, out := do("GET", "/catalog/agents/demo/secrets", nil)
		var v AgentSecrets
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatal(code, out)
		}
		return v
	}
	lastNote := func() string {
		h, _ := s.CatalogHistory(ctx, co)
		return h[0].Note
	}
	v := uses()
	if len(v.Uses) != 1 || v.Uses[0] != (AgentSecretUse{Name: "SHARED_TOKEN", Env: "SHARED", Kind: "string", Profile: "developer", State: "missing"}) || v.Base != c.catalogVersion(co) {
		t.Fatalf("%+v", v)
	}

	// A file secret as a text variable is refused with a hint; as a file (variable defaults to its name) it is saved.
	code, out := do("PUT", "/catalog/agents/demo/secrets", AgentSecretsPut{Base: v.Base, Secrets: []config.SecretUse{{Name: "GAME_SERVER_SSH_KEY", Env: "SSH_KEY"}}})
	if code != 400 || !strings.Contains(out, `"field":"secrets"`) || !strings.Contains(out, "is a file secret: give it as a file") {
		t.Fatal(code, out)
	}
	code, out = do("PUT", "/catalog/agents/demo/secrets", AgentSecretsPut{Base: v.Base, Secrets: []config.SecretUse{{Name: "API_TOKEN", Env: "API", File: true}}})
	if code != 400 || !strings.Contains(out, "is a text secret") {
		t.Fatal(code, out)
	}
	if code, out = do("PUT", "/catalog/agents/demo/secrets", AgentSecretsPut{Base: v.Base, Secrets: []config.SecretUse{{Name: "GAME_SERVER_SSH_KEY", File: true}}}); code != 200 {
		t.Fatal(code, out)
	}
	if n := lastNote(); n != "agent demo secrets: +GAME_SERVER_SSH_KEY as GAME_SERVER_SSH_KEY (file)" {
		t.Fatal(n)
	}
	if got := c.cat(co).Agents["demo"].Spec.Secrets; len(got) != 2 || got[1] != (config.SecretUse{Name: "GAME_SERVER_SSH_KEY", Env: "GAME_SERVER_SSH_KEY", File: true}) {
		t.Fatalf("%+v", got)
	}
	if code, out = do("PUT", "/catalog/agents/demo/secrets", AgentSecretsPut{Base: v.Base, Secrets: nil}); code != 409 {
		t.Fatal("stale base", code, out)
	}
	v = uses()
	if len(v.Uses) != 2 || v.Uses[0].Name != "GAME_SERVER_SSH_KEY" || v.Uses[0].State != "set" || v.Uses[0].Profile != "" || v.Uses[1].Profile != "developer" {
		t.Fatalf("%+v", v)
	}

	// The Secrets page gives one secret to two agents in one change; a secret that does not exist is 404.
	code, out = do("POST", "/secrets/API_TOKEN/agents", SecretGrant{Base: v.Base, Agents: []string{"manager", "demo"}, Env: "GAME_API"})
	if code != 200 {
		t.Fatal(code, out)
	}
	if n := lastNote(); n != "secret API_TOKEN given to demo, manager as GAME_API" {
		t.Fatal(n)
	}
	for _, a := range []string{"demo", "manager"} {
		found := false
		for _, u := range c.cat(co).Agents[a].Spec.Secrets {
			found = found || u == (config.SecretUse{Name: "API_TOKEN", Env: "GAME_API"})
		}
		if !found {
			t.Fatalf("%s: %+v", a, c.cat(co).Agents[a].Spec.Secrets)
		}
	}
	if code, out = do("POST", "/secrets/NOPE/agents", SecretGrant{Base: c.catalogVersion(co), Agents: []string{"demo"}}); code != 404 {
		t.Fatal(code, out)
	}
	if code, out = do("POST", "/secrets/API_TOKEN/agents", SecretGrant{Base: c.catalogVersion(co), Agents: []string{"nobody"}}); code != 404 {
		t.Fatal(code, out)
	}
	code, out = do("GET", "/secrets", nil)
	if code != 200 || !strings.Contains(out, `"name":"API_TOKEN"`) || !strings.Contains(out, `"used_by":["agent demo","agent manager"]`) || strings.Contains(out, "value-of-") {
		t.Fatal(code, out)
	}

	// The SSH key replaced by a text value: demo's use is marked wrong_kind; editing demo's other uses still works.
	if _, err = s.SetSecret(ctx, co, "GAME_SERVER_SSH_KEY", storage.SecretString, []byte("now-a-text-value"), "owner"); err != nil {
		t.Fatal(err)
	}
	v = uses()
	if v.Uses[0].Name != "GAME_SERVER_SSH_KEY" || v.Uses[0].State != "wrong_kind" || v.Uses[0].SecretKind != "string" {
		t.Fatalf("%+v", v)
	}
	if code, out = do("PUT", "/catalog/agents/demo/secrets", AgentSecretsPut{Base: v.Base, Secrets: []config.SecretUse{{Name: "GAME_SERVER_SSH_KEY", Env: "GAME_SERVER_SSH_KEY", File: true}}}); code != 200 {
		t.Fatal(code, out)
	}
	if n := lastNote(); n != "agent demo secrets: -API_TOKEN" {
		t.Fatal(n)
	}
	// The agent's full editor refuses a new wrong-kind use too.
	var doc AgentDoc
	if code, out = do("GET", "/catalog/agents/demo", nil); code != 200 || json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatal(code, out)
	}
	doc.Agent.Spec.Secrets = append(doc.Agent.Spec.Secrets, config.SecretUse{Name: "API_TOKEN", Env: "API_PATH", File: true})
	if code, out = do("PUT", "/catalog/agents/demo", doc); code != 400 || !strings.Contains(out, "is a text secret") {
		t.Fatal(code, out)
	}

	// Writes are the owner's and admins' (members and agents reach none of them).
	for _, r := range [][2]string{{"PUT", "/catalog/agents/demo/secrets"}, {"POST", "/secrets/API_TOKEN/agents"}} {
		if got := requiredRole(r[0], r[1]); got != storage.RoleAdmin {
			t.Fatal(r, got)
		}
	}
}
