package skills

import (
	"appfactory/internal/configfile"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type Skill struct {
	Path        string   `yaml:"path" json:"path"`
	Description string   `yaml:"description" json:"description"`
	Categories  []string `yaml:"categories" json:"categories"`
}
type Registry struct {
	Skills map[string]Skill `yaml:"skills"`
	Root   string           `yaml:"-"`
}
type Entry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
}

func Load(root string) (*Registry, error) {
	r := &Registry{Root: root, Skills: map[string]Skill{}}
	p := filepath.Join(root, "configs/skills.yaml")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return r, nil
	}
	if err := configfile.Read(p, r); err != nil {
		return nil, err
	}
	r.Root = root
	for name, s := range r.Skills {
		if !namePattern.MatchString(name) || strings.TrimSpace(s.Description) == "" || filepath.Base(s.Path) != "SKILL.md" {
			return nil, fmt.Errorf("invalid skill %q", name)
		}
		p, err := configfile.Path(root, s.Path)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", name, err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		if !strings.HasPrefix(text, "---\n") {
			return nil, fmt.Errorf("skill %s: YAML frontmatter required", name)
		}
		end := strings.Index(text[4:], "\n---\n")
		if end < 0 {
			return nil, fmt.Errorf("skill %s: invalid frontmatter", name)
		}
		var meta struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		} // Additional standard Agent Skills metadata is allowed.
		if err := decodeMetadata([]byte(text[4:4+end]), &meta); err != nil || meta.Name != name || meta.Description == "" {
			return nil, fmt.Errorf("skill %s: matching name and description required", name)
		}
	}
	return r, nil
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
func (r *Registry) Read(name, resource string) (string, error) {
	s, ok := r.Skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %s", name)
	}
	main, err := configfile.Path(r.Root, s.Path)
	if err != nil {
		return "", err
	}
	if resource == "" {
		resource = "SKILL.md"
	}
	p, err := configfile.Path(filepath.Dir(main), resource)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", fmt.Errorf("skill resource must be a regular file <= 1 MiB")
	}
	data, err := os.ReadFile(p)
	return string(data), err
}
func (r *Registry) Available(include []string, discover bool, categories []string) []string {
	set := map[string]bool{}
	for _, n := range include {
		set[n] = true
	}
	if discover {
		for n, s := range r.Skills {
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
func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
