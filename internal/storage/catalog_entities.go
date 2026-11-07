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

// ImportCatalogEntities syncs the company's entities with its catalog version (0 the latest): new and changed
// documents are upserted (a changed one gets the next row_version), removed ones deleted; the sync is logged with
// each entity's before and after. Every catalog save calls it, so entities and documents never drift.
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
	old := map[string]string{}
	rows, err := tx.QueryContext(ctx, "SELECT path,content FROM catalog_entities WHERE company_id=?", company)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var p, c string
		if err = rows.Scan(&p, &c); err != nil {
			rows.Close()
			return 0, err
		}
		old[p] = c
	}
	rows.Close()
	paths := make([]string, 0, len(cat.Docs))
	for p := range cat.Docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	now := tx.now().Unix()
	var changes []map[string]string
	for _, p := range paths {
		content := cat.Docs[p]
		if before, ok := old[p]; ok && before == content {
			delete(old, p)
			continue
		}
		e := entityOf(p, content)
		if _, err = tx.ExecContext(ctx, "DELETE FROM catalog_entities WHERE company_id=? AND path=? AND (kind<>? OR name<>?)", company, p, e.Kind, e.Name); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_entities(company_id,kind,name,path,title,spec,content,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(company_id,kind,name) DO UPDATE SET path=excluded.path, title=excluded.title, spec=excluded.spec, content=excluded.content,
 row_version=catalog_entities.row_version+1, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
			company, e.Kind, e.Name, e.Path, e.Title, string(e.Spec), e.Content, now, author); err != nil {
			return 0, err
		}
		changes = append(changes, map[string]string{"path": p, "before": old[p], "after": content})
		delete(old, p)
	}
	for p, before := range old {
		if _, err = tx.ExecContext(ctx, "DELETE FROM catalog_entities WHERE company_id=? AND path=?", company, p); err != nil {
			return 0, err
		}
		changes = append(changes, map[string]string{"path": p, "before": before, "after": ""})
	}
	if len(changes) > 0 {
		b, _ := json.Marshal(changes)
		if _, err = tx.ExecContext(ctx, "INSERT INTO catalog_changes(company_id,at,author,person,note,changes) VALUES(?,?,?,?,?,?)", company, now, author, personName(ctx), "catalog version "+itoa(cat.Version)+": "+cat.Note, string(b)); err != nil {
			return 0, err
		}
	}
	return len(paths), tx.Commit()
}

// CatalogChange is one logged change of the entities: what each entity's document was before and after.
type CatalogChange struct {
	Seq     int64               `json:"seq"`
	At      int64               `json:"at"`
	Author  string              `json:"author"`
	Person  string              `json:"person,omitempty"`
	Note    string              `json:"note,omitempty"`
	Changes []map[string]string `json:"changes"`
}

// CatalogChanges lists the company's entity changes, newest first (at most 100).
func (s *Store) CatalogChanges(ctx context.Context, company string) ([]CatalogChange, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,at,author,person,note,changes FROM catalog_changes WHERE company_id=? ORDER BY seq DESC LIMIT 100", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogChange{}
	for rows.Next() {
		var c CatalogChange
		var b []byte
		if err = rows.Scan(&c.Seq, &c.At, &c.Author, &c.Person, &c.Note, &b); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(b, &c.Changes)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CatalogSource is where Core reads the company's catalog from: "documents" or "entities".
func (s *Store) CatalogSource(ctx context.Context, company string) (string, error) {
	var src string
	err := s.DB.QueryRowContext(ctx, "SELECT catalog_source FROM companies WHERE id=?", company).Scan(&src)
	return src, err
}

// SetCatalogSource switches the company's catalog source.
func (s *Store) SetCatalogSource(ctx context.Context, company, source string) error {
	if source != "documents" && source != "entities" {
		return inputError("catalog source must be documents or entities")
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE companies SET catalog_source=? WHERE id=?", source, company)
	return err
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
