package storage

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Structured catalog, step 1 (structured-catalog): the company's catalog as entities — one row per agent, profile,
// team, policy, node hooks, prompt and company file — with the parsed spec and the document it renders to, and a
// change log. Import fills them from the current catalog version; RenderEntities gives the documents back, so the
// loader is unchanged and a company switches over only once both give the same catalog.

// CatalogEntity is one configuration entity.
type CatalogEntity struct {
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Path       string          `json:"path"`
	Title      string          `json:"title,omitempty"`
	Spec       json.RawMessage `json:"spec"`
	Content    string          `json:"content"`
	RowVersion int64           `json:"row_version"`
	UpdatedAt  int64           `json:"updated_at"`
	UpdatedBy  string          `json:"updated_by"`
}

// entityOf parses a catalog document into its entity: YAML documents by their kind and metadata, prompts and
// company files by their path.
func entityOf(p, content string) CatalogEntity {
	e := CatalogEntity{Path: p, Content: content, Spec: json.RawMessage("{}")}
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	if strings.HasPrefix(p, "prompts/") {
		e.Kind, e.Name = "prompt", strings.TrimPrefix(p, "prompts/")
		return e
	}
	var doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name        string `yaml:"name"`
			Title       string `yaml:"title"`
			Description string `yaml:"description"`
		} `yaml:"metadata"`
		Spec any `yaml:"spec"`
	}
	if strings.HasSuffix(p, ".yaml") && yaml.Unmarshal([]byte(content), &doc) == nil && doc.Kind != "" {
		e.Kind, e.Name, e.Title = strings.ToLower(doc.Kind), doc.Metadata.Name, doc.Metadata.Title
		if e.Name == "" {
			e.Name = base
		}
		if b, err := json.Marshal(normalizeYAML(doc.Spec)); err == nil {
			e.Spec = b
		}
		return e
	}
	e.Kind, e.Name = "file", p
	return e
}

// normalizeYAML turns yaml.v3's map[string]any trees into JSON-encodable values.
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = normalizeYAML(val)
		}
		return x
	case map[any]any:
		m := map[string]any{}
		for k, val := range x {
			if s, ok := k.(string); ok {
				m[s] = normalizeYAML(val)
			}
		}
		return m
	case []any:
		for i := range x {
			x[i] = normalizeYAML(x[i])
		}
		return x
	}
	return v
}

// ImportCatalogEntities replaces the company's entities with its catalog version (0 the latest) and logs it.
func (s *Store) ImportCatalogEntities(ctx context.Context, company string, version int64, author string) (int, error) {
	cat, err := s.CatalogAt(ctx, company, version)
	if err != nil {
		return 0, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM catalog_entities WHERE company_id=?", company); err != nil {
		return 0, err
	}
	paths := make([]string, 0, len(cat.Docs))
	for p := range cat.Docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	now := tx.now().Unix()
	for _, p := range paths {
		e := entityOf(p, cat.Docs[p])
		if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_entities(company_id,kind,name,path,title,spec,content,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(company_id,kind,name) DO UPDATE SET path=excluded.path, content=excluded.content, spec=excluded.spec",
			company, e.Kind, e.Name, e.Path, e.Title, string(e.Spec), e.Content, now, author); err != nil {
			return 0, err
		}
	}
	note, _ := json.Marshal([]map[string]any{{"import": cat.Version, "documents": len(paths)}})
	if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_changes(company_id,at,author,note,changes) VALUES(?,?,?,?,?)", company, now, author, "import of catalog version "+itoa(cat.Version), string(note)); err != nil {
		return 0, err
	}
	return len(paths), tx.Commit()
}

// CatalogEntities lists the company's entities by path.
func (s *Store) CatalogEntities(ctx context.Context, company string) ([]CatalogEntity, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT kind,name,path,title,spec,content,row_version,updated_at,updated_by FROM catalog_entities WHERE company_id=? ORDER BY path", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogEntity
	for rows.Next() {
		var e CatalogEntity
		var spec []byte
		if err = rows.Scan(&e.Kind, &e.Name, &e.Path, &e.Title, &spec, &e.Content, &e.RowVersion, &e.UpdatedAt, &e.UpdatedBy); err != nil {
			return nil, err
		}
		e.Spec = spec
		out = append(out, e)
	}
	return out, rows.Err()
}

// RenderEntities is the catalog documents the entities render to (path → content).
func RenderEntities(es []CatalogEntity) map[string]string {
	docs := make(map[string]string, len(es))
	for _, e := range es {
		docs[e.Path] = e.Content
	}
	return docs
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
