package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"appfactory/internal/storage"
)

// Structured catalog (structured-catalog): the configuration as entities, edited one by one through the API and the
// UI (team rename is its title), with a change log and undo; YAML stays a file format (export and import). Every
// edit is saved as a catalog version too, so runs, history and rollback keep working, and a company switches its
// source to the entities once they resolve the same agents.

func (c *Core) catalogEntityRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /catalog/changes", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.CatalogChanges(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("PUT /catalog/entities/{kind}/{name...}", c.putEntity)
	m.HandleFunc("POST /catalog/changes/{seq}/undo", c.undoCatalogChange)
	m.HandleFunc("POST /catalog/entities/switch", c.switchCatalog)
	m.HandleFunc("GET /catalog/export", func(w http.ResponseWriter, r *http.Request) {
		cur, err := c.Store.CurrentCatalog(r.Context(), c.companyFrom(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write([]byte(ExportYAML(cur.Docs)))
	})
	m.HandleFunc("POST /catalog/import", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			YAML string `json:"yaml"`
			Note string `json:"note"`
		}
		if !decodeMax(w, r, &p, 16<<20) {
			return
		}
		docs, err := ImportYAML(p.YAML)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		cur, err := c.Store.CurrentCatalog(r.Context(), c.companyFrom(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		v, err := c.ApplyCatalogChange(r.Context(), c.companyFrom(r), author(r), CatalogChange{Base: cur.Version, Put: docs, Note: firstNonEmpty(p.Note, "YAML import")})
		reply(w, v, err)
	})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// putEntity changes one entity: its whole document (content), or its title and/or spec (the document is
// regenerated); row_version guards concurrent edits.
func (c *Core) putEntity(w http.ResponseWriter, r *http.Request) {
	company := c.companyFrom(r)
	var p struct {
		RowVersion int64           `json:"row_version"`
		Title      *string         `json:"title,omitempty"`
		Spec       json.RawMessage `json:"spec,omitempty"`
		Content    *string         `json:"content,omitempty"`
		Note       string          `json:"note"`
	}
	if !decode(w, r, &p) {
		return
	}
	es, err := c.Store.CatalogEntities(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var e *storage.CatalogEntity
	for i := range es {
		if es[i].Kind == r.PathValue("kind") && es[i].Name == r.PathValue("name") {
			e = &es[i]
		}
	}
	if e == nil {
		http.Error(w, "no such entity", 404)
		return
	}
	if p.RowVersion != e.RowVersion {
		http.Error(w, fmt.Sprintf("conflict: the entity changed meanwhile (row_version %d)", e.RowVersion), 409)
		return
	}
	content := e.Content
	switch {
	case p.Content != nil:
		content = *p.Content
	case p.Title != nil || len(p.Spec) > 0:
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(e.Content), &doc); err != nil || doc == nil {
			http.Error(w, "this entity is not a YAML document: send its content", 400)
			return
		}
		meta, _ := doc["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		if p.Title != nil {
			if *p.Title == "" {
				delete(meta, "title")
			} else {
				meta["title"] = *p.Title
			}
		}
		doc["metadata"] = meta
		if len(p.Spec) > 0 {
			var spec any
			if err := json.Unmarshal(p.Spec, &spec); err != nil {
				http.Error(w, "spec must be JSON", 400)
				return
			}
			doc["spec"] = spec
		}
		b, err := yaml.Marshal(doc)
		if err != nil {
			reply(w, nil, err)
			return
		}
		content = string(b)
	}
	cur, err := c.Store.CurrentCatalog(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	v, err := c.ApplyCatalogChange(r.Context(), company, author(r), CatalogChange{Base: cur.Version, Put: map[string]string{e.Path: content}, Note: firstNonEmpty(p.Note, "change "+e.Kind+" "+e.Name)})
	reply(w, v, err)
}

// undoCatalogChange puts back what a logged change replaced (documents it added are deleted).
func (c *Core) undoCatalogChange(w http.ResponseWriter, r *http.Request) {
	company := c.companyFrom(r)
	seq, _ := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	list, err := c.Store.CatalogChanges(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var ch *storage.CatalogChange
	for i := range list {
		if list[i].Seq == seq {
			ch = &list[i]
		}
	}
	if ch == nil {
		http.Error(w, "no such change (only the last 100 can be undone)", 404)
		return
	}
	cur, err := c.Store.CurrentCatalog(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	undo := CatalogChange{Base: cur.Version, Put: map[string]string{}, Note: fmt.Sprintf("undo change %d (%s)", seq, ch.Note)}
	for _, x := range ch.Changes {
		if x["before"] == "" {
			if _, ok := cur.Docs[x["path"]]; ok {
				undo.Delete = append(undo.Delete, x["path"])
			}
		} else {
			undo.Put[x["path"]] = x["before"]
		}
	}
	v, err := c.ApplyCatalogChange(r.Context(), company, author(r), undo)
	reply(w, v, err)
}

// switchCatalog moves a company to its entities after checking they resolve the same agents (config hash per agent);
// {"source": "documents"} moves it back.
func (c *Core) switchCatalog(w http.ResponseWriter, r *http.Request) {
	company := c.companyFrom(r)
	var p struct {
		Source string `json:"source"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Source == "" {
		p.Source = "entities"
	}
	if p.Source == "entities" {
		if _, err := c.Store.ImportCatalogEntities(r.Context(), company, 0, author(r)); err != nil {
			reply(w, nil, err)
			return
		}
		es, err := c.Store.CatalogEntities(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		next, err := c.loadCatalog(company, storage.RenderEntities(es))
		if err != nil {
			http.Error(w, "the entities do not load: "+err.Error(), 409)
			return
		}
		if diff := agentHashDiff(c.cat(company).Agents, next.Agents); diff != "" {
			http.Error(w, "the entities resolve differently, not switched: "+diff, 409)
			return
		}
	}
	if err := c.Store.SetCatalogSource(r.Context(), company, p.Source); err != nil {
		reply(w, nil, err)
		return
	}
	write(w, map[string]string{"company": company, "source": p.Source})
}

func agentHashDiff[A any](a, b map[string]A) string {
	h := func(v any) [32]byte { x, _ := json.Marshal(v); return sha256.Sum256(x) }
	var diff []string
	for name, x := range a {
		if y, ok := b[name]; !ok || h(x) != h(y) {
			diff = append(diff, name)
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			diff = append(diff, name)
		}
	}
	sort.Strings(diff)
	return strings.Join(diff, ", ")
}

// ExportYAML renders documents as one multi-document YAML file, each preceded by "# path: P".
func ExportYAML(docs map[string]string) string {
	paths := make([]string, 0, len(docs))
	for p := range docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for i, p := range paths {
		if i > 0 {
			b.WriteString("---\n")
		}
		fmt.Fprintf(&b, "# path: %s\n", p)
		if strings.HasSuffix(p, ".md") {
			b.WriteString("# |\n")
			for _, l := range strings.Split(strings.TrimRight(docs[p], "\n"), "\n") {
				b.WriteString("#   " + l + "\n")
			}
			continue
		}
		b.WriteString(strings.TrimRight(docs[p], "\n") + "\n")
	}
	return b.String()
}

// ImportYAML parses ExportYAML's format back into documents.
func ImportYAML(text string) (map[string]string, error) {
	docs := map[string]string{}
	for i, part := range strings.Split("\n"+text, "\n---\n") {
		part = strings.TrimLeft(part, "\n")
		if strings.TrimSpace(part) == "" {
			continue
		}
		first, rest, _ := strings.Cut(part, "\n")
		p, ok := strings.CutPrefix(first, "# path: ")
		if !ok || strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("document %d: starts without a # path: line", i+1)
		}
		p = strings.TrimSpace(p)
		if body, ok := strings.CutPrefix(rest, "# |\n"); ok {
			var lines []string
			for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
				lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(l, "#   "), "#"))
			}
			docs[p] = strings.Join(lines, "\n") + "\n"
			continue
		}
		docs[p] = rest
	}
	return docs, nil
}
