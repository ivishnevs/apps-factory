package core

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestRunTokenIsBoundToCompany(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Token: "owner"}
	tools := func(token string) int {
		r := httptest.NewRequest("GET", "/agent/tools", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w.Code
	}
	for company, project := range map[string]string{storage.DefaultCompany: "d", "acme": "a"} {
		if _, err = s.CreateProject(ctx, company, tasks.ProjectCreate{ID: project, Name: project, Workspace: "/tmp/token-" + project}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: "mine", Project: "d", Agent: "demo", Workspace: "/tmp/token-d"}); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, storage.DefaultCompany, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Create(ctx, "acme", protocol.RunSpec{ID: "theirs", Project: "a", Agent: "demo", Workspace: "/tmp/token-a"}); err != nil {
		t.Fatal(err)
	}
	foreignLease, err := s.Claim(ctx, "acme", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	c.Agents = nil // the agent is unknown here, so a valid credential stops at 403, not 401
	if code := tools(c.runToken(storage.DefaultCompany, "mine", lease)); code != 403 {
		t.Fatal("own token rejected at authentication", code)
	}
	if code := tools(c.runToken("acme", "mine", lease)); code != 401 {
		t.Fatal("token minted for another company accepted", code)
	}
	if code := tools(c.runToken("acme", "theirs", foreignLease)); code != 401 {
		t.Fatal("another company's run authenticated", code)
	}
}
