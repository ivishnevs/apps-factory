package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"appfactory/internal/skills"
	"appfactory/internal/storage"
)

// Skill contents in Core (skill-registry D2, D8): Core indexes the release's skills as the built-in source, completes
// installed skills from the index, reads skills for agents from blobs, and serves a run's pinned skills to its node.

// IndexBuiltinSkills indexes the release's default skills (<Root>/skills/<name>/SKILL.md) as source factory of the
// built-in scope; a skill whose content changed gets a new version, older versions keep their content.
func (c *Core) IndexBuiltinSkills(ctx context.Context) error {
	if c.Root == "" || c.Store == nil || c.Store.Blobs == nil {
		return nil
	}
	dir := filepath.Join(c.Root, "skills")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var idx []skills.Indexed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		one, err := skills.Index(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		idx = append(idx, one...)
	}
	if err = c.Store.SaveSkillSource(ctx, storage.SkillSource{Scope: skills.BuiltinSource, ID: skills.BuiltinSource, Kind: storage.SkillSourceBuiltin}); err != nil {
		return err
	}
	if _, err = c.Store.IndexSkills(ctx, skills.BuiltinSource, skills.BuiltinSource, "release", idx); err != nil {
		return err
	}
	for _, x := range idx {
		if x.Error != "" {
			slog.Warn("built-in skill not indexed", "skill", x.Name, "error", x.Error)
		}
	}
	return nil
}

// skillScope is the scope holding a source's skills: the built-in scope for source factory, the company otherwise.
func skillScope(company, source string) string {
	if source == skills.BuiltinSource {
		return skills.BuiltinSource
	}
	return company
}

// resolveSkill completes an installed skill from the index (config.SkillResolver): a built-in skill without a hash
// gets its current one; description and categories come from the index unless skills.yaml sets them.
func (c *Core) resolveSkill(company, name string, s skills.Skill) (skills.Skill, error) {
	v, err := c.Store.SkillVersion(context.Background(), skillScope(company, s.Source), s.Source, name, s.Hash)
	if err != nil {
		return s, err
	}
	s.Hash = v.Hash
	if s.Commit == "" {
		s.Commit = v.Commit
	}
	if s.Description == "" {
		s.Description = v.Description
	}
	if len(s.Categories) == 0 {
		s.Categories = v.Categories
	}
	return s, nil
}

// skillFile reads a file of an installed skill of company from its blob (SKILL.md when resource is "").
func (c *Core) skillFile(ctx context.Context, company, name, resource string) (string, error) {
	s, ok := c.cat(company).Skills.Skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %s", name)
	}
	if resource == "" {
		resource = "SKILL.md"
	}
	resource = path.Clean(strings.TrimPrefix(resource, "./"))
	if strings.HasPrefix(resource, "../") || path.IsAbs(resource) || resource == "." {
		return "", errors.New("skill resources are files inside the skill")
	}
	if c.Store == nil || s.Dir != "" {
		// A catalog resolved from a release checkout reads the checkout's files.
		if s.Dir == "" {
			return "", fmt.Errorf("skill %s: content not found", name)
		}
		tar, _, _, err := skills.Pack(s.Dir)
		if err != nil {
			return "", err
		}
		b, err := skills.ReadFile(bytes.NewReader(tar), resource)
		return string(b), err
	}
	v, err := c.Store.SkillByHash(ctx, []string{skillScope(company, s.Source)}, s.Hash)
	if err != nil {
		return "", fmt.Errorf("skill %s: content not found", name)
	}
	rc, err := c.Store.SkillContent(ctx, v)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := skills.ReadFile(rc, resource)
	return string(b), err
}

// runSkill serves GET /runs/{id}/skills/{hash} to the run's node (D8): only a hash pinned on that run.
func (c *Core) runSkill(w http.ResponseWriter, r *http.Request) {
	company, run, hash := c.companyFrom(r), r.PathValue("id"), r.PathValue("hash")
	got, err := c.Store.Get(r.Context(), company, run)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var snap struct {
		SkillHashes map[string]string `json:"skill_hashes"`
	}
	_ = json.Unmarshal(got.Spec.ConfigSnapshot, &snap)
	pinned := false
	for _, h := range snap.SkillHashes {
		pinned = pinned || h == hash
	}
	if !pinned {
		http.Error(w, "skill not pinned on this run", http.StatusNotFound)
		return
	}
	v, err := c.Store.SkillByHash(r.Context(), []string{company, skills.BuiltinSource}, hash)
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
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("X-Skill-Name", v.Name)
	_, _ = io.Copy(w, rc)
}
