package storage

import (
	"context"
	"errors"
	"testing"
)

// TestCatalogVersions (company-self-service 3.2): versions are saved on top of the current one only, refused
// by validation without a trace, read back by version and listed newest first.
func TestCatalogVersions(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	if _, err := s.CurrentCatalog(ctx, DefaultCompany); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	v1 := map[string]string{"configs/agents/a.yaml": "a1", "prompts/a.md": "p1"}
	if _, err := s.SaveCatalog(ctx, DefaultCompany, CatalogSave{Docs: v1, Author: "seed"}); err != nil {
		t.Fatal(err)
	}
	refuse := func(map[string]string) error { return errors.New("agent a: unknown profile x") }
	if _, err := s.SaveCatalog(ctx, DefaultCompany, CatalogSave{Base: 1, Docs: map[string]string{"configs/agents/a.yaml": "bad"}, Author: "owner", Validate: refuse}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := s.SaveCatalog(ctx, DefaultCompany, CatalogSave{Base: 1, Docs: map[string]string{"configs/prices.yaml": "x"}, Author: "owner"}); !errors.Is(err, ErrInput) {
		t.Fatal("operator-only path accepted", err)
	}
	v2 := map[string]string{"configs/agents/a.yaml": "a2"}
	if _, err := s.SaveCatalog(ctx, DefaultCompany, CatalogSave{Base: 1, Docs: v2, Author: "owner", Note: "drop prompt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveCatalog(ctx, DefaultCompany, CatalogSave{Base: 1, Docs: v1, Author: "other"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale base accepted", err)
	}
	cur, err := s.CurrentCatalog(ctx, DefaultCompany)
	if err != nil || cur.Version != 2 || len(cur.Docs) != 1 || cur.Docs["configs/agents/a.yaml"] != "a2" || cur.Note != "drop prompt" {
		t.Fatalf("%+v %v", cur, err)
	}
	old, err := s.CatalogAt(ctx, DefaultCompany, 1)
	if err != nil || old.Docs["prompts/a.md"] != "p1" || old.Author != "seed" {
		t.Fatalf("%+v %v", old, err)
	}
	h, err := s.CatalogHistory(ctx, DefaultCompany)
	if err != nil || len(h) != 2 || h[0].Version != 2 || h[1].Documents != 2 {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestMatchPath(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"openspec/specs/**", "openspec/specs/a/spec.md", true},
		{"openspec/specs/**", "openspec/specs", true},
		{"openspec/specs/**", "openspec/changes/a/proposal.md", false},
		{"**/*.md", "docs/a/b.md", true},
		{"**/*.md", "README.md", true},
		{"src/*.go", "src/a/b.go", false},
		{"openspec/changes/*/proposal.md", "openspec/changes/x/proposal.md", true},
	} {
		if got := MatchPath(c.pattern, c.name); got != c.want {
			t.Errorf("%s %s: %v", c.pattern, c.name, got)
		}
	}
}
