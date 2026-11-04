package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"appfactory/internal/skills"
)

// SkillResolver completes an installed skill from Core's skill index (skill-registry D4, D5): the current hash of a
// built-in skill without one, and description and categories unless skills.yaml sets them; an unknown source or hash
// is an error. Core passes one to LoadStoredCatalogWith; without one (the offline CLI) skills.yaml is taken as written.
type SkillResolver func(company, name string, s skills.Skill) (skills.Skill, error)

// DiskSkills resolves the built-in skills from a release checkout at root (root/skills/<name>): their Hash,
// description and categories, as Core indexes the same files. Other sources are left as written.
func DiskSkills(root string) SkillResolver {
	return func(company, name string, s skills.Skill) (skills.Skill, error) {
		if s.Source != skills.BuiltinSource || root == "" {
			return s, nil
		}
		dir := filepath.Join(root, "skills", name)
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return s, fmt.Errorf("no built-in skill %s in %s", name, filepath.Join(root, "skills"))
		}
		meta, err := skills.ParseSkillMD(data, name)
		if err != nil {
			return s, err
		}
		hash, err := skills.Hash(dir)
		if err != nil {
			return s, err
		}
		if s.Hash != "" && s.Hash != hash {
			return s, fmt.Errorf("pinned hash %s differs from the release's %s", s.Hash, hash)
		}
		s.Hash, s.Dir = hash, dir
		if s.Description == "" {
			s.Description = meta.Description
		}
		if len(s.Categories) == 0 {
			s.Categories = meta.Categories()
		}
		return s, nil
	}
}

func resolveSkills(company string, r *skills.Registry, resolve SkillResolver) error {
	names := make([]string, 0, len(r.Skills))
	for n := range r.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := r.Skills[n]
		if resolve != nil {
			var err error
			if s, err = resolve(company, n, s); err != nil {
				return fmt.Errorf("skill %s: %w", n, err)
			}
		}
		if s.Description == "" {
			s.Description = n
		}
		r.Skills[n] = s
	}
	return nil
}
