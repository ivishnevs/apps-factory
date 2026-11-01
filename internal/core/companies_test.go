package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestCompanyProvisioning (company-self-service 4.1, 4.2): the operator creates a company from a template; its
// owner token sees only that company (catalog, agents, tasks), cannot manage companies, can issue and revoke
// owner tokens; Core schedules the new company too.
func TestCompanyProvisioning(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	c := &Core{Store: s, Root: "../..", Token: "operator"}
	h := c.Handler()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(token, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("operator", "POST", "/companies", CompanyCreate{ID: "acme", Name: "Acme", Template: "nonexistent"}); w.Code != 400 || !strings.Contains(w.Body.String(), "product-team") {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call("operator", "POST", "/companies", CompanyCreate{ID: "acme", Name: "Acme", Template: "product-team"})
	var created CompanyCreated
	if json.Unmarshal(w.Body.Bytes(), &created); w.Code != 200 || created.CatalogVersion != 1 || !strings.HasPrefix(created.Owner.Token, storage.OwnerTokenPrefix) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("operator", "POST", "/companies", CompanyCreate{ID: "acme", Name: "Acme", Template: "solo"}); w.Code != 409 {
		t.Fatal("duplicate company", w.Code)
	}
	owner := created.Owner.Token
	var agents []string
	if w = call(owner, "GET", "/agents", nil); json.Unmarshal(w.Body.Bytes(), &agents) != nil || strings.Join(agents, ",") != "developer,product-owner,reviewer" {
		t.Fatal(w.Code, w.Body.String())
	}
	var view CatalogView
	if w = call(owner, "GET", "/catalog", nil); json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Company != "acme" || view.Author != "operator" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(owner, "POST", "/companies", CompanyCreate{ID: "evil", Template: "solo"}); w.Code != 403 {
		t.Fatal("owner token created a company", w.Code)
	}
	if w = call(owner, "GET", "/companies", nil); w.Code != 403 {
		t.Fatal("owner token listed companies", w.Code)
	}
	// Tasks of the default company stay invisible to acme's owner.
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "secret", Name: "secret", Workspace: "/tmp/secret"}); err != nil {
		t.Fatal(err)
	}
	if w = call(owner, "GET", "/projects/secret", nil); w.Code != 404 {
		t.Fatal("another company's project visible", w.Code, w.Body.String())
	}
	if w = call(owner, "POST", "/projects", map[string]any{"id": "app", "name": "App", "workspace": "/tmp/acme-app"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = s.Project(ctx, "acme", "app"); err != nil {
		t.Fatal("project not in acme", err)
	}
	// Core runs acme's loops.
	if got := strings.Join(c.companies(), ","); got != "acme,default" {
		t.Fatal(got)
	}
	// Owner tokens: issue, list, revoke.
	var second storage.IssuedOwnerToken
	if w = call(owner, "POST", "/owner-tokens", map[string]string{"name": "laptop"}); json.Unmarshal(w.Body.Bytes(), &second) != nil || second.Company != "acme" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(owner, "POST", "/owner-tokens/"+second.ID+"/revoke", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(second.Token, "GET", "/agents", nil); w.Code != 401 {
		t.Fatal("revoked token works", w.Code)
	}
	var list []map[string]any
	if w = call("operator", "GET", "/companies", nil); json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
}
