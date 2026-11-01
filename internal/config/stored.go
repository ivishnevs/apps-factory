package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"
	"unicode/utf8"
)

// Stored catalogs (company-self-service D1): a company's catalog is a set of documents, each a path relative
// to the factory root (as in the file layout) and its UTF-8 content. Inside the company's catalog directory
// only the documents count; elsewhere (shared prompts, skill contents) the factory root on disk is underneath.

// MaxCatalogDocument bounds one document of a stored catalog; MaxCatalogDocuments bounds a version.
const (
	MaxCatalogDocument  = 256 << 10
	MaxCatalogDocuments = 1000
)

// catalogDirs are the catalog directory's subdirectories of YAML documents; catalogFiles its single files.
var (
	catalogDirs  = []string{"agents", "profiles", "policies", "node-hooks"}
	catalogFiles = []string{"skills.yaml", "company.yaml", "quota_keys.yaml", "mcp_servers.yaml"}
)

// ValidCatalogPath checks that p may be a document of company's stored catalog: <dir>/{agents,profiles,
// policies,node-hooks}/<name>.yaml, <dir>/{skills,company,quota_keys,mcp_servers}.yaml, or a Markdown prompt
// below prompts/.
func ValidCatalogPath(company, p string) error {
	dir, err := CompanyDir(company)
	if err != nil {
		return err
	}
	dir = filepath.ToSlash(dir)
	if !fs.ValidPath(p) || path.Clean(p) != p {
		return fmt.Errorf("invalid document path %q", p)
	}
	if rest, ok := strings.CutPrefix(p, "prompts/"); ok && rest != "" && strings.HasSuffix(p, ".md") {
		return nil
	}
	rest, ok := strings.CutPrefix(p, dir+"/")
	if ok && slices.Contains(catalogFiles, rest) {
		return nil
	}
	if sub, file, found := strings.Cut(rest, "/"); ok && found && slices.Contains(catalogDirs, sub) && !strings.Contains(file, "/") && strings.HasSuffix(file, ".yaml") && file != ".yaml" {
		return nil
	}
	return fmt.Errorf("document path %q is not part of company %s's catalog (%s/{%s}/NAME.yaml, %s/{%s}, prompts/*.md)", p, company, dir, strings.Join(catalogDirs, ","), dir, strings.Join(catalogFiles, ","))
}

// ValidCatalogDocuments checks paths, sizes and encoding of a stored catalog version.
func ValidCatalogDocuments(company string, docs map[string]string) error {
	if len(docs) > MaxCatalogDocuments {
		return fmt.Errorf("at most %d documents per catalog", MaxCatalogDocuments)
	}
	for _, p := range sortedKeys(docs) {
		if err := ValidCatalogPath(company, p); err != nil {
			return err
		}
		if c := docs[p]; len(c) > MaxCatalogDocument || !utf8.ValidString(c) {
			return fmt.Errorf("%s: UTF-8 of at most %d KiB required", p, MaxCatalogDocument>>10)
		}
	}
	return nil
}

// CatalogFS is company's view of the factory root for a stored catalog: docs, and outside the company's
// catalog sections the disk underneath (shared prompts, skill contents).
func CatalogFS(disk fs.FS, company string, docs map[string]string) (fs.FS, error) {
	dir, err := CompanyDir(company)
	if err != nil {
		return nil, err
	}
	mem := fstest.MapFS{}
	now := time.Unix(0, 0)
	for p, c := range docs {
		mem[p] = &fstest.MapFile{Data: []byte(c), Mode: 0o644, ModTime: now}
	}
	return overlay{mem: mem, disk: disk, dir: filepath.ToSlash(dir)}, nil
}

type overlay struct {
	mem  fstest.MapFS
	disk fs.FS
	dir  string
}

// owned reports whether name belongs to the catalog's own sections, where only documents count.
func (o overlay) owned(name string) bool {
	rest, ok := strings.CutPrefix(name, o.dir+"/")
	if !ok {
		return false
	}
	sub, _, _ := strings.Cut(rest, "/")
	return slices.Contains(catalogDirs, sub) || slices.Contains(catalogFiles, rest)
}

func (o overlay) Open(name string) (fs.File, error) {
	if f, err := o.mem.Open(name); err == nil {
		if info, e := f.Stat(); e == nil && !info.IsDir() {
			return f, nil
		}
		f.Close()
	}
	if o.owned(name) {
		if slices.Contains(catalogDirs, strings.TrimPrefix(name, o.dir+"/")) {
			return o.mem.Open(name) // a section directory: its documents (or none)
		}
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return o.disk.Open(name)
}

func (o overlay) ReadDir(name string) ([]fs.DirEntry, error) {
	if o.owned(name) {
		entries, err := fs.ReadDir(o.mem, name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return entries, err
	}
	return fs.ReadDir(o.disk, name)
}

// SeedDocuments are the documents of company's catalog on disk (company-self-service D1, seed): every file
// of its catalog sections and the prompt files its agents and profiles name. The disk catalog must load.
func SeedDocuments(root, company string) (map[string]string, error) {
	c, err := LoadCompanyCatalog(root, company)
	if err != nil {
		return nil, err
	}
	dir, _ := CompanyDir(company)
	dir = filepath.ToSlash(dir)
	docs := map[string]string{}
	add := func(p string) error {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		docs[p] = string(b)
		return nil
	}
	for _, sub := range catalogDirs {
		paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), sub, "*.yaml"))
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			if err = add(path.Join(dir, sub, filepath.Base(p))); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range catalogFiles {
		if p := path.Join(dir, f); fileExists(filepath.Join(root, filepath.FromSlash(p))) {
			if err = add(p); err != nil {
				return nil, err
			}
		}
	}
	for _, as := range []map[string]Agent{c.Profiles, c.Agents} {
		for _, a := range as {
			if f := a.Spec.Prompt.File; f != "" {
				if err = add(path.Clean(filepath.ToSlash(f))); err != nil {
					return nil, err
				}
			}
		}
	}
	return docs, ValidCatalogDocuments(company, docs)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
