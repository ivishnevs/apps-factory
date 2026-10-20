package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HashPrefix marks the content hash format of a skill directory (Hash).
const HashPrefix = "sha256:"

// Limits of a materialized skill directory.
const (
	maxSkillFile  = 1 << 20
	maxSkillTotal = 16 << 20
	maxSkillFiles = 512
)

// Dir is the directory of a registered skill: the directory of its SKILL.md.
func (r *Registry) Dir(name string) (string, error) {
	main, err := r.main(name)
	if err != nil {
		return "", err
	}
	return filepath.Dir(main), nil
}

// Hash is the content hash of a skill directory: "sha256:" and the SHA-256 over every regular file in
// slash-path order, each as "<path>\x00<size>\x00<content>". Only regular files and directories are
// allowed (no symlinks, devices or sockets); dot-files count like any other file. Core records this hash
// for a run's offered skills and the node verifies the materialized copy against it.
func Hash(dir string) (string, error) {
	files, err := list(dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(b))
		h.Write(b)
	}
	return HashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// list returns the slash paths of the regular files under dir, sorted, within the size limits.
func list(dir string) ([]string, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill file %s: only regular files are allowed", filepath.ToSlash(rel))
		}
		if info.Size() > maxSkillFile {
			return fmt.Errorf("skill file %s: larger than 1 MiB", filepath.ToSlash(rel))
		}
		if total += info.Size(); total > maxSkillTotal || len(files) >= maxSkillFiles {
			return errors.New("skill directory larger than 16 MiB or 512 files")
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !contains(files, "SKILL.md") {
		return nil, fmt.Errorf("skill directory %s has no SKILL.md", dir)
	}
	sort.Strings(files)
	return files, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Copy copies the skill directory src to dst (which must not exist) and returns the hash of the copy,
// so the hash describes exactly the files a runtime reads.
func Copy(src, dst string) (string, error) {
	files, err := list(src)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(dst); err == nil {
		return "", fmt.Errorf("skill copy target %s exists", dst)
	}
	for _, f := range files {
		if strings.HasPrefix(f, "../") {
			return "", fmt.Errorf("skill file %s escapes its directory", f)
		}
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		p := filepath.Join(dst, filepath.FromSlash(f))
		if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err = os.WriteFile(p, b, 0o644); err != nil {
			return "", err
		}
	}
	return Hash(dst)
}

// ValidName reports whether name is a valid skill name (lowercase letters, digits and '-').
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Pins are the content hashes (Hash) of the named skills in this registry, as Core records them on a run
// (agent snapshot "skill_hashes"); the node verifies its materialized copies against them.
func (r *Registry) Pins(names []string) (map[string]string, error) {
	if r == nil || len(names) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		dir, err := r.Dir(name)
		if err != nil {
			return nil, err
		}
		if out[name], err = Hash(dir); err != nil {
			return nil, fmt.Errorf("skill %s: %w", name, err)
		}
	}
	return out, nil
}
