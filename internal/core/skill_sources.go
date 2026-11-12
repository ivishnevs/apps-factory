package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"appfactory/internal/config"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
)

// Skill sources, library and installed skills of a company (skill-registry D3–D7, D10): owners and admins add
// sources once per company; Core fetches public ones, a node of the company fetches private ones; installing a
// skill saves a catalog version.

// SourceCreate is the body of POST /skills/sources.
type SourceCreate struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Ref     string `json:"ref"`
	Subpath string `json:"subpath"`
	Access  string `json:"access"`
	Update  string `json:"update"`
}

// SkillInstall is the body of POST /skills/install and /skills/uninstall.
type SkillInstall struct {
	Base   int64  `json:"base"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Scope  string `json:"scope"`
}

// LibrarySkill is a skill of the library with whether it is installed (and at which hash).
type LibrarySkill struct {
	storage.IndexedSkill
	Installed     bool   `json:"installed"`
	InstalledHash string `json:"installed_hash,omitempty"`
}

// InstalledSkill is an installed skill with its available update (a newer indexed version of a pinned source).
type InstalledSkill struct {
	Name        string       `json:"name"`
	Source      string       `json:"source"`
	Hash        string       `json:"hash"`
	Commit      string       `json:"commit,omitempty"`
	Scope       string       `json:"scope,omitempty"`
	Description string       `json:"description"`
	Categories  []string     `json:"categories"`
	Update      *SkillUpdate `json:"update,omitempty"`
}

// SkillUpdate is a newer version of an installed skill and what changed per file.
type SkillUpdate struct {
	Hash   string       `json:"hash"`
	Commit string       `json:"commit,omitempty"`
	Files  []FileChange `json:"files"`
}

// FileChange is one file of an update: added, removed or changed.
type FileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

var sourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// maxUpload bounds an uploaded archive or folder.
const maxUpload = 64 << 20

func (c *Core) skillRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /skills/sources", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.SkillSources(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /skills/sources", func(w http.ResponseWriter, r *http.Request) {
		var p SourceCreate
		if !decode(w, r, &p) {
			return
		}
		src, err := c.addSource(r.Context(), c.companyFrom(r), p)
		reply(w, src, err)
	})
	m.HandleFunc("POST /skills/sources/upload", func(w http.ResponseWriter, r *http.Request) {
		src, err := c.uploadSource(r.Context(), c.companyFrom(r), w, r)
		reply(w, src, err)
	})
	m.HandleFunc("POST /skills/sources/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		src, err := c.Store.SkillSource(r.Context(), company, r.PathValue("id"))
		if err == nil {
			err = c.syncSource(r.Context(), company, src)
		}
		if err == nil {
			src, err = c.Store.SkillSource(r.Context(), company, src.ID)
		}
		reply(w, src, err)
	})
	m.HandleFunc("DELETE /skills/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		company, id := c.companyFrom(r), r.PathValue("id")
		for name, s := range c.cat(company).Skills.Skills {
			if s.Source == id {
				reply(w, nil, fmt.Errorf("%w: skill %s of source %s is installed: uninstall it first", storage.ErrConflict, name, id))
				return
			}
		}
		reply(w, map[string]bool{"ok": true}, c.Store.DeleteSkillSource(r.Context(), company, id))
	})
	m.HandleFunc("GET /skills/library", func(w http.ResponseWriter, r *http.Request) {
		company, q := c.companyFrom(r), r.URL.Query()
		list, err := c.Store.SkillLibrary(r.Context(), []string{company, skills.BuiltinSource}, q.Get("source"), q.Get("q"), q.Get("category"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		installed := c.cat(company).Skills.Skills
		out := make([]LibrarySkill, 0, len(list))
		for _, v := range list {
			x := LibrarySkill{IndexedSkill: v}
			if s, ok := installed[v.Name]; ok && s.Source == v.Source {
				x.Installed, x.InstalledHash = true, s.Hash
			}
			x.Files = nil
			out = append(out, x)
		}
		reply(w, out, nil)
	})
	m.HandleFunc("GET /skills/library/{source}/{name}/files", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		v, err := c.Store.SkillVersion(r.Context(), skillScope(company, r.PathValue("source")), r.PathValue("source"), r.PathValue("name"), r.URL.Query().Get("hash"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, v.Files, nil)
	})
	m.HandleFunc("GET /skills/library/{source}/{name}/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		v, err := c.Store.SkillVersion(r.Context(), skillScope(company, r.PathValue("source")), r.PathValue("source"), r.PathValue("name"), r.URL.Query().Get("hash"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		rc, err := c.Store.SkillContent(r.Context(), v)
		if err != nil {
			reply(w, nil, err)
			return
		}
		defer rc.Close()
		b, err := skills.ReadFile(rc, path.Clean(r.PathValue("path")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	})
	m.HandleFunc("GET /skills/installed", func(w http.ResponseWriter, r *http.Request) {
		out, err := c.installedSkills(r.Context(), c.companyFrom(r), r.URL.Query().Get("team"))
		reply(w, out, err)
	})
	m.HandleFunc("POST /skills/install", func(w http.ResponseWriter, r *http.Request) {
		var p SkillInstall
		if !decode(w, r, &p) {
			return
		}
		v, err := c.installSkill(r.Context(), c.companyFrom(r), p, author(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /skills/uninstall", func(w http.ResponseWriter, r *http.Request) {
		var p SkillInstall
		if !decode(w, r, &p) {
			return
		}
		v, err := c.uninstallSkill(r.Context(), c.companyFrom(r), p, author(r))
		reply(w, v, err)
	})
	// A node's upload of a private source's tree (D3), signed; the job must be the node's.
	m.HandleFunc("POST /skills/sync-jobs/{id}", c.syncJobResult)
}

func defaultSourceID(raw string) string {
	base := strings.TrimSuffix(path.Base(strings.TrimRight(raw, "/")), ".git")
	base = strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9-]+`).ReplaceAllString(base, "-"))
	return strings.Trim(base, "-")
}

// addSource records a git source and syncs it at once.
func (c *Core) addSource(ctx context.Context, company string, p SourceCreate) (storage.SkillSource, error) {
	if p.ID == "" {
		p.ID = defaultSourceID(p.URL)
	}
	switch {
	case !sourceID.MatchString(p.ID) || p.ID == skills.BuiltinSource:
		return storage.SkillSource{}, fmt.Errorf("%w: source id: lowercase letters, digits and '-' (not %s)", storage.ErrInput, skills.BuiltinSource)
	case p.Access != "" && p.Access != "public" && p.Access != "node":
		return storage.SkillSource{}, fmt.Errorf("%w: access public or node", storage.ErrInput)
	case p.Update != "" && p.Update != "pinned" && p.Update != "track":
		return storage.SkillSource{}, fmt.Errorf("%w: update pinned or track", storage.ErrInput)
	case strings.ContainsAny(p.Ref, " \n\t") || strings.HasPrefix(p.Ref, "-"):
		return storage.SkillSource{}, fmt.Errorf("%w: invalid ref", storage.ErrInput)
	}
	if p.Access != "node" {
		if _, err := skills.CheckSourceURL(p.URL); err != nil {
			return storage.SkillSource{}, fmt.Errorf("%w: %v", storage.ErrInput, err)
		}
	} else if p.URL == "" || strings.HasPrefix(p.URL, "-") || strings.ContainsAny(p.URL, " \n\t") {
		return storage.SkillSource{}, fmt.Errorf("%w: a git URL the node can clone", storage.ErrInput)
	}
	if _, err := skills.SourceRoot("/x", p.Subpath); err != nil {
		return storage.SkillSource{}, fmt.Errorf("%w: %v", storage.ErrInput, err)
	}
	src := storage.SkillSource{Scope: company, ID: p.ID, Kind: storage.SkillSourceGit, URL: p.URL, Ref: p.Ref, Subpath: p.Subpath, Access: p.Access, Update: p.Update}
	if err := c.Store.SaveSkillSource(ctx, src); err != nil {
		return src, err
	}
	saved, err := c.Store.SkillSource(ctx, company, p.ID)
	if err != nil {
		return saved, err
	}
	if err = c.syncSource(ctx, company, saved); err != nil {
		slog.Warn("skill source sync", "company", company, "source", p.ID, "error", err)
	}
	return c.Store.SkillSource(ctx, company, p.ID)
}

// syncSource fetches a git source: Core itself for a public one, a node of the company for a private one (the
// source waits for one when none is online). The sync's failure is recorded on the source.
func (c *Core) syncSource(ctx context.Context, company string, src storage.SkillSource) error {
	switch {
	case src.Kind != storage.SkillSourceGit:
		return fmt.Errorf("%w: only git sources sync; upload again to update an uploaded one", storage.ErrInput)
	case src.Access == "node":
		if _, err := c.Store.CreateSyncJob(ctx, company, src.ID); errors.Is(err, storage.ErrNotFound) {
			return c.Store.SetSkillSourceStatus(ctx, company, src.ID, "waiting_for_node", "no online node of the company to fetch it")
		} else if err != nil {
			return err
		}
		return c.Store.SetSkillSourceStatus(ctx, company, src.ID, "syncing", "")
	}
	if err := c.Store.SetSkillSourceStatus(ctx, company, src.ID, "syncing", ""); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "factory-skill-source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	commit, err := skills.FetchPublic(ctx, src.URL, src.Ref, dir+"/tree")
	if err == nil {
		err = c.indexSource(ctx, company, src, dir+"/tree", commit)
	}
	if err != nil {
		_ = c.Store.SetSkillSourceStatus(ctx, company, src.ID, "failed", err.Error())
		return err
	}
	return nil
}

// indexSource indexes a fetched or uploaded tree as src's current index, then applies a track source's updates.
func (c *Core) indexSource(ctx context.Context, company string, src storage.SkillSource, tree, commit string) error {
	root, err := skills.SourceRoot(tree, src.Subpath)
	if err != nil {
		return err
	}
	idx, err := skills.Index(root)
	if err != nil {
		return err
	}
	if len(idx) == 0 {
		return errors.New("no skills found (skills/*/SKILL.md, .claude/skills, .agents/skills, a plugin marketplace or a SKILL.md at the root)")
	}
	changed, err := c.Store.IndexSkills(ctx, company, src.ID, commit, idx)
	if err != nil {
		return err
	}
	if src.Update == "track" && len(changed) > 0 {
		return c.trackUpdates(ctx, company, src.ID, changed)
	}
	return nil
}

// trackUpdates installs the new versions of a track source's installed skills (D7: a version authored sync).
func (c *Core) trackUpdates(ctx context.Context, company, source string, changed map[string][2]string) error {
	return c.editSkills(ctx, company, 0, "sync", "skills of source "+source+" updated", func(reg map[string]skills.Skill) (bool, error) {
		edited := false
		for name, s := range reg {
			if ch, ok := changed[name]; ok && s.Source == source && s.Hash != ch[1] {
				v, err := c.Store.SkillVersion(ctx, company, source, name, ch[1])
				if err != nil {
					return false, err
				}
				s.Hash, s.Commit = v.Hash, v.Commit
				reg[name] = s
				edited = true
			}
		}
		return edited, nil
	})
}

// uploadSource takes an archive (zip, tar.gz) or the files of a folder as an upload source (D3), re-uploads update it.
func (c *Core) uploadSource(ctx context.Context, company string, w http.ResponseWriter, r *http.Request) (storage.SkillSource, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return storage.SkillSource{}, fmt.Errorf("%w: multipart form with archive or files[] required: %v", storage.ErrInput, err)
	}
	id := r.FormValue("name")
	if !sourceID.MatchString(id) || id == skills.BuiltinSource {
		return storage.SkillSource{}, fmt.Errorf("%w: name: lowercase letters, digits and '-'", storage.ErrInput)
	}
	if old, err := c.Store.SkillSource(ctx, company, id); err == nil && old.Kind != storage.SkillSourceUpload {
		return old, fmt.Errorf("%w: source %s is a %s source", storage.ErrConflict, id, old.Kind)
	}
	dir, err := os.MkdirTemp("", "factory-skill-upload-")
	if err != nil {
		return storage.SkillSource{}, err
	}
	defer os.RemoveAll(dir)
	h := sha256.New()
	if f, _, err := r.FormFile("archive"); err == nil {
		data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
		f.Close()
		if err != nil || len(data) > maxUpload {
			return storage.SkillSource{}, fmt.Errorf("%w: archives are at most 64 MiB", storage.ErrInput)
		}
		h.Write(data)
		if err = skills.UnpackUpload(data, dir); err != nil {
			return storage.SkillSource{}, fmt.Errorf("%w: %v", storage.ErrInput, err)
		}
	} else {
		var total int64
		n := 0
		files := r.MultipartForm.File["files[]"]
		if len(files) == 0 {
			return storage.SkillSource{}, fmt.Errorf("%w: an archive or files[] required", storage.ErrInput)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Filename < files[j].Filename })
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				return storage.SkillSource{}, err
			}
			data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
			f.Close()
			if err != nil {
				return storage.SkillSource{}, err
			}
			fmt.Fprintf(h, "%s\x00%d\x00", fh.Filename, len(data))
			h.Write(data)
			if err = skills.WriteUploadedFile(dir, fh.Filename, data, &total, &n); err != nil {
				return storage.SkillSource{}, fmt.Errorf("%w: %v", storage.ErrInput, err)
			}
		}
	}
	src := storage.SkillSource{Scope: company, ID: id, Kind: storage.SkillSourceUpload, Access: "public", Update: "pinned"}
	if err = c.Store.SaveSkillSource(ctx, src); err != nil {
		return src, err
	}
	commit := "upload:" + hex.EncodeToString(h.Sum(nil))[:16]
	if err = c.indexSource(ctx, company, src, dir, commit); err != nil {
		_ = c.Store.SetSkillSourceStatus(ctx, company, id, "failed", err.Error())
		return src, fmt.Errorf("%w: %v", storage.ErrInput, err)
	}
	return c.Store.SkillSource(ctx, company, id)
}

// syncJobResult takes a node's upload of a private source's tree (raw tar, ?commit=) or its failure (?error=).
func (c *Core) syncJobResult(w http.ResponseWriter, r *http.Request) {
	company := c.companyFrom(r)
	node, _ := nodeOf(r)
	job, err := c.Store.SyncJobOf(r.Context(), company, node, r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	// A job is answered once: a finished job taking a new tree would change a tracked source's skills without
	// anyone asking (security-review-2026-11 F7).
	if job.Status != "pending" {
		jsonStatus(w, http.StatusConflict, map[string]string{"error": "this sync job is finished (" + job.Status + ")", "reason": "job_finished"})
		return
	}
	src, err := c.Store.SkillSource(r.Context(), company, job.Source)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if why := r.URL.Query().Get("error"); why != "" {
		_ = c.Store.FinishSyncJob(r.Context(), company, job.ID, "failed", why)
		reply(w, map[string]bool{"ok": true}, c.Store.SetSkillSourceStatus(r.Context(), company, src.ID, "failed", "node "+node+": "+why))
		return
	}
	dir, err := os.MkdirTemp("", "factory-skill-sync-")
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer os.RemoveAll(dir)
	commit := r.URL.Query().Get("commit")
	err = skills.UnpackTree(http.MaxBytesReader(w, r.Body, skills.MaxSourceSize+(1<<20)), dir)
	if err == nil {
		err = c.indexSource(r.Context(), company, src, dir, commit)
	}
	if err != nil {
		_ = c.Store.FinishSyncJob(r.Context(), company, job.ID, "failed", err.Error())
		_ = c.Store.SetSkillSourceStatus(r.Context(), company, src.ID, "failed", err.Error())
		reply(w, nil, fmt.Errorf("%w: %v", storage.ErrInput, err))
		return
	}
	reply(w, map[string]bool{"ok": true}, c.Store.FinishSyncJob(r.Context(), company, job.ID, "done", ""))
}

// installedSkills lists the company's installed skills (only those an agent of team may use when team is set) and
// the update of a pinned source's skill whose source has a newer version.
func (c *Core) installedSkills(ctx context.Context, company, team string) ([]InstalledSkill, error) {
	reg := c.cat(company).Skills
	sources := map[string]storage.SkillSource{}
	if list, err := c.Store.SkillSources(ctx, company); err == nil {
		for _, s := range list {
			sources[s.ID] = s
		}
	}
	out := []InstalledSkill{}
	names := make([]string, 0, len(reg.Skills))
	for n := range reg.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := reg.Skills[name]
		if team != "" && !strings.HasPrefix(s.Scope, "agent:") && !s.InScope(team, "") {
			continue
		}
		x := InstalledSkill{Name: name, Source: s.Source, Hash: s.Hash, Commit: s.Commit, Scope: s.Scope, Description: s.Description, Categories: s.Categories}
		if x.Categories == nil {
			x.Categories = []string{}
		}
		if src := sources[s.Source]; s.Source == skills.BuiltinSource || src.Update != "track" {
			cur, err := c.Store.SkillVersion(ctx, skillScope(company, s.Source), s.Source, name, "")
			if err == nil && cur.Hash != s.Hash {
				x.Update = &SkillUpdate{Hash: cur.Hash, Commit: cur.Commit, Files: c.skillDiff(ctx, company, s, cur)}
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// skillDiff compares the installed version's files with a newer version's.
func (c *Core) skillDiff(ctx context.Context, company string, s skills.Skill, newer storage.IndexedSkill) []FileChange {
	old, err := c.Store.SkillByHash(ctx, []string{skillScope(company, s.Source)}, s.Hash)
	files := func(v storage.IndexedSkill) map[string]string {
		out := map[string]string{}
		rc, err := c.Store.SkillContent(ctx, v)
		if err != nil {
			return out
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		for _, f := range v.Files {
			b, _ := skills.ReadFile(bytes.NewReader(data), f.Path)
			sum := sha256.Sum256(b)
			out[f.Path] = hex.EncodeToString(sum[:])
		}
		return out
	}
	before := map[string]string{}
	if err == nil {
		before = files(old)
	}
	after := files(newer)
	out := []FileChange{}
	for p, h := range after {
		switch b, ok := before[p]; {
		case !ok:
			out = append(out, FileChange{p, "added"})
		case b != h:
			out = append(out, FileChange{p, "changed"})
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, FileChange{p, "removed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// installSkill installs (or updates to its current version) a library skill (D5, D6): a catalog version on base.
func (c *Core) installSkill(ctx context.Context, company string, p SkillInstall, who string) (storage.CatalogVersion, error) {
	scope := skillScope(company, p.Source)
	v, err := c.Store.SkillVersion(ctx, scope, p.Source, p.Name, "")
	if err != nil {
		return storage.CatalogVersion{}, err
	}
	var out storage.CatalogVersion
	err = c.editSkills(ctx, company, p.Base, who, "install skill "+p.Name+" from "+p.Source, func(reg map[string]skills.Skill) (bool, error) {
		if old, ok := reg[p.Name]; ok && old.Source != p.Source {
			return false, fmt.Errorf("%w: skill %s is installed from source %s; uninstall it first (names are unique per company)", storage.ErrConflict, p.Name, old.Source)
		}
		reg[p.Name] = skills.Skill{Source: p.Source, Hash: v.Hash, Commit: v.Commit, Scope: p.Scope}
		return true, nil
	}, &out)
	return out, err
}

// uninstallSkill removes an installed skill no agent includes.
func (c *Core) uninstallSkill(ctx context.Context, company string, p SkillInstall, who string) (storage.CatalogVersion, error) {
	for name, a := range c.cat(company).Agents {
		for _, n := range a.Spec.Skills.Include {
			if n == p.Name {
				return storage.CatalogVersion{}, fmt.Errorf("%w: agent %s includes skill %s", storage.ErrConflict, name, p.Name)
			}
		}
	}
	var out storage.CatalogVersion
	err := c.editSkills(ctx, company, p.Base, who, "uninstall skill "+p.Name, func(reg map[string]skills.Skill) (bool, error) {
		if _, ok := reg[p.Name]; !ok {
			return false, fmt.Errorf("%w: skill %s is not installed", storage.ErrNotFound, p.Name)
		}
		delete(reg, p.Name)
		return true, nil
	}, &out)
	return out, err
}

// editSkills changes company's skills.yaml in a new catalog version on base (0: the current one, for sync).
func (c *Core) editSkills(ctx context.Context, company string, base int64, who, note string, edit func(map[string]skills.Skill) (bool, error), saved ...*storage.CatalogVersion) error {
	cur, err := c.Store.CurrentCatalog(ctx, company)
	if err != nil {
		return err
	}
	if base == 0 {
		base = cur.Version
	}
	dir, err := config.CompanyDir(company)
	if err != nil {
		return err
	}
	docPath := path.Join(strings.ReplaceAll(dir, "\\", "/"), skills.File)
	var doc struct {
		Skills map[string]skills.Skill `yaml:"skills"`
	}
	if text, ok := cur.Docs[docPath]; ok {
		if err = yaml.Unmarshal([]byte(text), &doc); err != nil {
			return err
		}
	}
	if doc.Skills == nil {
		doc.Skills = map[string]skills.Skill{}
	}
	changed, err := edit(doc.Skills)
	if err != nil || !changed {
		return err
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	docs := map[string]string{}
	for k, v := range cur.Docs {
		docs[k] = v
	}
	docs[docPath] = "# Installed skills (skill-registry D5); edited on the Skills page.\n" + string(body)
	v, err := c.SaveCatalog(ctx, company, storage.CatalogSave{Base: base, Docs: docs, Author: who, Note: note})
	if err == nil && len(saved) > 0 {
		*saved[0] = v
	}
	return err
}

// syncDueSources syncs the company's git sources not synced for a day (D7, periodic sync).
func (c *Core) syncDueSources(ctx context.Context) {
	for _, company := range c.companies() {
		list, err := c.Store.SkillSources(ctx, company)
		if err != nil {
			continue
		}
		for _, src := range list {
			if src.Kind != storage.SkillSourceGit || src.Status == "syncing" || time.Since(time.Unix(src.SyncedAt, 0)) < 24*time.Hour {
				continue
			}
			if err := c.syncSource(ctx, company, src); err != nil {
				slog.Warn("periodic skill source sync", "company", company, "source", src.ID, "error", err)
			}
		}
	}
}
