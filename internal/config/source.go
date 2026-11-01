package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"appfactory/internal/configfile"
)

// source is where a catalog is read from (company-self-service D1): fsys holds the factory root (slash paths
// relative to it: configs/..., prompts/...), root is the factory root on disk, which resolves skill contents
// (operator-only, never stored) and names files in errors. A disk catalog is os.Root's FS of root; a stored
// catalog is its documents over that disk view.
type source struct {
	fsys fs.FS
	root string
}

// name is p as errors show it: the disk path when there is a root.
func (s source) name(p string) string {
	if s.root == "" {
		return p
	}
	return filepath.Join(s.root, filepath.FromSlash(p))
}

func (s source) read(p string) ([]byte, error) { return fs.ReadFile(s.fsys, p) }

// exists reports whether p is a file of the source.
func (s source) exists(p string) bool {
	info, err := fs.Stat(s.fsys, p)
	return err == nil && !info.IsDir()
}

// glob lists dir/pattern, sorted.
func (s source) glob(dir, pattern string) ([]string, error) {
	return fs.Glob(s.fsys, path.Join(dir, pattern))
}

// decode reads p as exactly one strict YAML document into v.
func (s source) decode(p string, v any) error {
	b, err := s.read(p)
	if err != nil {
		return err
	}
	if err = configfile.Decode(b, v); err != nil {
		return fmt.Errorf("%s: %w", s.name(p), err)
	}
	return nil
}

// file is rel (a path relative to the factory root, as prompt files are named) as a path of the source.
func (s source) file(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", errors.New("relative path required")
	}
	p := path.Clean(filepath.ToSlash(rel))
	if p == ".." || strings.HasPrefix(p, "../") || !fs.ValidPath(p) {
		return "", errors.New("path escapes root")
	}
	return p, nil
}
