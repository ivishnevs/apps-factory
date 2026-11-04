package skills

import (
	"appfactory/internal/configfile"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// BuiltinSource is the source of Factory's own skills, indexed by Core from the release (skill-registry D2).
const BuiltinSource = "factory"

// Skill is an installed skill of a company's catalog (skill-registry D5): its source and content hash, the commit
// it came from, an optional scope, and description and categories from the index unless set here.
type Skill struct {
	Source string `yaml:"source" json:"source"`
	// Hash is the skill's content (Hash); optional for the built-in source, whose current version is used then.
	Hash   string `yaml:"hash" json:"hash,omitempty"`
	Commit string `yaml:"commit" json:"commit,omitempty"`
	// Scope: "" (every agent), "team:<name>" or "agent:<name>" (D9).
	Scope       string   `yaml:"scope" json:"scope,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Categories  []string `yaml:"categories" json:"categories,omitempty"`
	// Dir is the skill's directory on disk when it was resolved from a release checkout (offline catalogs, tests);
	// Core otherwise reads contents from its blobs.
	Dir string `yaml:"-" json:"-"`
}

// Registry is a company's skills.yaml: the installed skills by name.
type Registry struct {
	Skills map[string]Skill `yaml:"skills"`
}

type Entry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
}

// File is the registry file name in a company catalog directory (configs/ or configs/<company>/).
const File = "skills.yaml"

var sourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Parse reads skills.yaml (found: data holds it; otherwise the company has no skills). Each skill names its source
// and, except for the built-in source, its hash; contents live in Core (skill-registry D2), not on disk.
func Parse(data []byte, found bool) (*Registry, error) {
	r := &Registry{Skills: map[string]Skill{}}
	if !found {
		return r, nil
	}
	if err := configfile.Decode(data, r); err != nil {
		return nil, fmt.Errorf("%s: %w (skills name their source and hash; path: entries are gone, skill-registry D2)", File, err)
	}
	if r.Skills == nil {
		r.Skills = map[string]Skill{}
	}
	for name, s := range r.Skills {
		switch {
		case !namePattern.MatchString(name):
			return nil, fmt.Errorf("invalid skill name %q", name)
		case !sourceID.MatchString(s.Source):
			return nil, fmt.Errorf("skill %s: source required (%s for Factory's skills, or a source of the Skills page)", name, BuiltinSource)
		case s.Hash == "" && s.Source != BuiltinSource:
			return nil, fmt.Errorf("skill %s: hash required for source %s", name, s.Source)
		case s.Hash != "" && !strings.HasPrefix(s.Hash, HashPrefix):
			return nil, fmt.Errorf("skill %s: hash must be %s<hex>", name, HashPrefix)
		case !validScope(s.Scope):
			return nil, fmt.Errorf("skill %s: scope must be team:NAME or agent:NAME", name)
		}
	}
	return r, nil
}

func validScope(s string) bool {
	if s == "" {
		return true
	}
	kind, name, ok := strings.Cut(s, ":")
	return ok && (kind == "team" || kind == "agent") && sourceID.MatchString(name)
}

// InScope reports whether an agent of team may use the skill (D9).
func (s Skill) InScope(team, agent string) bool {
	kind, name, _ := strings.Cut(s.Scope, ":")
	switch kind {
	case "team":
		return name == team
	case "agent":
		return name == agent
	}
	return true
}

func (r *Registry) Entries(names []string, query, category string) []Entry {
	out := []Entry{}
	for _, name := range names {
		s, ok := r.Skills[name]
		if !ok {
			continue
		}
		if category != "" && !has(s.Categories, category) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(name+" "+s.Description), strings.ToLower(query)) {
			continue
		}
		out = append(out, Entry{name, s.Description, s.Categories})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Available are the skills an agent of team gets: the ones it includes, and with discover those of its categories,
// within their scope.
func (r *Registry) Available(include []string, discover bool, categories []string, team, agent string) []string {
	set := map[string]bool{}
	for _, n := range include {
		set[n] = true
	}
	if discover {
		for n, s := range r.Skills {
			if !s.InScope(team, agent) {
				continue
			}
			for _, c := range categories {
				if has(s.Categories, c) {
					set[n] = true
				}
			}
		}
	}
	out := []string{}
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Pins are the content hashes of the named skills, as Core records them on a run (agent snapshot "skill_hashes").
func (r *Registry) Pins(names []string) map[string]string {
	if r == nil || len(names) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, n := range names {
		if s, ok := r.Skills[n]; ok && s.Hash != "" {
			out[n] = s.Hash
		}
	}
	return out
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ValidName reports whether name is a valid skill name (lowercase letters, digits and '-').
func ValidName(name string) bool { return namePattern.MatchString(name) }
