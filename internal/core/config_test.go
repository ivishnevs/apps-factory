package core

import (
	"appfactory/internal/config"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillAccessAndLazyContext(t *testing.T) {
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Agents: catalog.Agents, Skills: catalog.Skills, Token: "test"}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	for path, status := range map[string]int{"/agents/demo/skills": 200, "/agents/demo/skills?q=spec": 200, "/agents/demo/skills/openspec": 200, "/agents/demo/skills/missing": 403, "/agents/missing/skills": 404, "/agents/demo/skills/openspec?resource=../../README.md": 400} {
		w := request(path)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	a := c.Agents["demo"]
	if strings.Contains(a.Instructions, "Mark tasks complete only") {
		t.Fatal("eager skill content")
	}
	a.Spec.Tools.Deny = []string{"factory.skills.read"}
	c.Agents["demo"] = a
	if w := request("/agents/demo/skills/openspec"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	a.Spec.Tools.Deny = nil
	a.Spec.Approvals.Mode = "always"
	c.Agents["demo"] = a
	w := request("/agents/demo/skills/openspec")
	if w.Code != 423 {
		t.Fatal(w.Code)
	}
	var d map[string]any
	if json.Unmarshal(w.Body.Bytes(), &d) != nil || d["effect"] != "approval" {
		t.Fatal(w.Body.String())
	}
}
