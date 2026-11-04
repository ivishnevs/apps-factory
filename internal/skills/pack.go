package skills

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Skill contents in Core (skill-registry D2, D4): a skill directory travels as a deterministic tar of its regular
// files; its identity is Hash over the files, which the node verifies after unpacking.

// SkillFile is one file of a packed skill.
type SkillFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Pack tars the skill directory dir deterministically (sorted paths, fixed modes and times) and returns the tar,
// the skill's Hash and its files.
func Pack(dir string) ([]byte, string, []SkillFile, error) {
	files, err := list(dir)
	if err != nil {
		return nil, "", nil, err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var out []SkillFile
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return nil, "", nil, err
		}
		h := &tar.Header{Name: f, Mode: 0o644, Size: int64(len(b)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err = tw.WriteHeader(h); err != nil {
			return nil, "", nil, err
		}
		if _, err = tw.Write(b); err != nil {
			return nil, "", nil, err
		}
		out = append(out, SkillFile{Path: f, Size: int64(len(b))})
	}
	if err = tw.Close(); err != nil {
		return nil, "", nil, err
	}
	hash, err := Hash(dir)
	return buf.Bytes(), hash, out, err
}

// Unpack extracts a packed skill into dst (which must not exist) and returns the Hash of what it wrote; only
// regular files within the limits, no path leaving dst.
func Unpack(r io.Reader, dst string) (string, error) {
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("skill target %s exists", dst)
	}
	tr := tar.NewReader(r)
	var total int64
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name := path.Clean(h.Name)
		switch {
		case h.Typeflag == tar.TypeDir:
			continue
		case h.Typeflag != tar.TypeReg:
			return "", fmt.Errorf("skill archive entry %s: only regular files", h.Name)
		case name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name):
			return "", fmt.Errorf("skill archive entry %s escapes the skill", h.Name)
		case h.Size > maxSkillFile:
			return "", fmt.Errorf("skill file %s: larger than 1 MiB", name)
		}
		if total += h.Size; total > maxSkillTotal || n >= maxSkillFiles {
			return "", errors.New("skill larger than 16 MiB or 512 files")
		}
		n++
		p := filepath.Join(dst, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", err
		}
	}
	return Hash(dst)
}

// ReadFile reads one file of a packed skill.
func ReadFile(r io.Reader, name string) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no file %s in the skill", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == name && h.Typeflag == tar.TypeReg {
			if h.Size > maxSkillFile {
				return nil, errors.New("skill files are at most 1 MiB")
			}
			return io.ReadAll(io.LimitReader(tr, h.Size))
		}
	}
}

// Meta is the frontmatter of a SKILL.md (agentskills.io): name, description and the optional fields.
type Meta struct {
	Name          string            `yaml:"name" json:"name"`
	Description   string            `yaml:"description" json:"description"`
	License       string            `yaml:"license" json:"license,omitempty"`
	Compatibility string            `yaml:"compatibility" json:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata" json:"metadata,omitempty"`
	AllowedTools  any               `yaml:"allowed-tools" json:"allowed_tools,omitempty"`
}

// Categories are the discovery categories: metadata.categories (comma separated), else metadata.tags.
func (m Meta) Categories() []string {
	v := m.Metadata["categories"]
	if v == "" {
		v = m.Metadata["tags"]
	}
	var out []string
	for _, c := range strings.Split(v, ",") {
		if c = strings.TrimSpace(strings.ToLower(c)); c != "" && !has(out, c) {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

var agentSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ParseSkillMD reads and checks the frontmatter of a SKILL.md for the skill directory dirName: name equal to the
// directory, [a-z0-9-] at most 64; a description of at most 1024 characters.
func ParseSkillMD(data []byte, dirName string) (Meta, error) {
	var m Meta
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return m, errors.New("SKILL.md needs YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return m, errors.New("SKILL.md frontmatter is not closed")
	}
	raw := text[4 : 4+end]
	if err := decodeMetadata([]byte(raw), &m); err != nil {
		// metadata values that are not strings are kept out of Meta.Metadata
		var loose struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if decodeMetadata([]byte(raw), &loose) != nil {
			return m, fmt.Errorf("SKILL.md frontmatter: %v", err)
		}
		m = Meta{Name: loose.Name, Description: loose.Description}
	}
	switch {
	case !agentSkillName.MatchString(m.Name):
		return m, fmt.Errorf("skill name %q: lowercase letters, digits and '-', at most 64", m.Name)
	case m.Name != dirName:
		return m, fmt.Errorf("skill name %q differs from its directory %q", m.Name, dirName)
	case strings.TrimSpace(m.Description) == "":
		return m, errors.New("skill description required")
	case len(m.Description) > 1024:
		return m, errors.New("skill description longer than 1024 characters")
	}
	return m, nil
}

// Found is a skill directory discovered in a source tree.
type Found struct {
	Dir  string // absolute
	Name string
}

// Discover finds the skills of a source tree (D4), first layout that matches: a Claude plugin marketplace
// (.claude-plugin/marketplace.json: each plugin's skills directory, default skills/), else skills/*/SKILL.md,
// .agents/skills/*/SKILL.md and .claude/skills/*/SKILL.md (symlinked skill directories skipped), else a SKILL.md at the root (a single-skill source).
func Discover(root string) ([]Found, error) {
	if b, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "marketplace.json")); err == nil {
		var mk struct {
			Plugins []struct {
				Source any `json:"source"`
				Skills any `json:"skills"`
			} `json:"plugins"`
		}
		if err = json.Unmarshal(b, &mk); err != nil {
			return nil, fmt.Errorf("marketplace.json: %w", err)
		}
		var out []Found
		for _, p := range mk.Plugins {
			base := root
			if s, ok := p.Source.(string); ok && s != "" {
				if base, err = inside(root, s); err != nil {
					return nil, err
				}
			}
			dirs := []string{"skills"}
			switch v := p.Skills.(type) {
			case string:
				dirs = []string{v}
			case []any:
				dirs = nil
				for _, x := range v {
					if s, ok := x.(string); ok {
						dirs = append(dirs, s)
					}
				}
			}
			for _, d := range dirs {
				dir, err := inside(base, d)
				if err != nil {
					return nil, err
				}
				if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
					out = append(out, Found{Dir: dir, Name: filepath.Base(dir)})
					continue
				}
				out = append(out, glob(filepath.Join(dir, "*", "SKILL.md"))...)
			}
		}
		return sortFound(out), nil
	}
	var out []Found
	for _, pattern := range []string{"skills/*/SKILL.md", ".agents/skills/*/SKILL.md", ".claude/skills/*/SKILL.md"} {
		out = append(out, glob(filepath.Join(root, filepath.FromSlash(pattern)))...)
	}
	if len(out) > 0 {
		return sortFound(out), nil
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		return []Found{{Dir: root, Name: filepath.Base(root)}}, nil
	}
	return nil, nil
}

func inside(root, rel string) (string, error) {
	p := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s leaves the source", rel)
	}
	return p, nil
}

func glob(pattern string) []Found {
	matches, _ := filepath.Glob(pattern)
	var out []Found
	for _, m := range matches {
		dir := filepath.Dir(m)
		// A symlinked skill directory (.claude/skills/x -> ../../.agents/skills/x) is the other layout's skill.
		if d, err := os.Lstat(dir); err != nil || d.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() {
			out = append(out, Found{Dir: dir, Name: filepath.Base(dir)})
		}
	}
	return out
}

// sortFound orders by name and keeps the first of a name (two layouts holding one skill, e.g. .claude/skills
// symlinked to .agents/skills).
func sortFound(in []Found) []Found {
	sort.SliceStable(in, func(i, j int) bool { return in[i].Name < in[j].Name })
	var out []Found
	for _, f := range in {
		if len(out) > 0 && out[len(out)-1].Name == f.Name {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Indexed is one discovered skill after validation: its tar, hash and frontmatter, or why it was not indexed.
type Indexed struct {
	Name  string
	Meta  Meta
	Hash  string
	Tar   []byte
	Files []SkillFile
	Error string
}

// Index discovers, validates and packs the skills of a source tree (D4): deterministic for the same tree; an
// invalid skill gets its Error and does not stop the others.
func Index(root string) ([]Indexed, error) {
	found, err := Discover(root)
	if err != nil {
		return nil, err
	}
	var out []Indexed
	for _, f := range found {
		x := Indexed{Name: f.Name}
		data, err := os.ReadFile(filepath.Join(f.Dir, "SKILL.md"))
		if err == nil {
			x.Meta, err = ParseSkillMD(data, f.Name)
		}
		if err == nil {
			x.Tar, x.Hash, x.Files, err = Pack(f.Dir)
		}
		if err != nil {
			x.Error = err.Error()
			x.Tar, x.Files = nil, nil
		}
		out = append(out, x)
	}
	return out, nil
}
