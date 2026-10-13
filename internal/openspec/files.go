// Package openspec provides file-based integration, not an OpenSpec workflow engine.
package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

var ErrConflict = errors.New("document changed; restart the read")
var changeName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const MaxDocument = 1 << 20

type Access struct{ HostRoot, LocalRoot string }
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Bytes     int64  `json:"bytes,omitempty"`
}
type Page struct {
	Items     []Entry `json:"items"`
	NextAfter string  `json:"next_after"`
	HasMore   bool    `json:"has_more"`
}
type Document struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Content    string `json:"content"`
	Bytes      int    `json:"bytes"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	HasMore    bool   `json:"has_more"`
}
type Check struct {
	Change         string `json:"change"`
	Path           string `json:"path"`
	Valid          bool   `json:"valid"`
	Scope          string `json:"scope"`
	ProposalSHA256 string `json:"proposal_sha256"`
	Note           string `json:"note"`
}
type Initialized struct {
	ProjectID   string   `json:"project_id"`
	Directories []string `json:"directories"`
}

// Open the mapped root first so resolution cannot escape it, even through symlinks.
func (a Access) workspace(workspace string) (*os.Root, error) {
	if !filepath.IsAbs(workspace) {
		return nil, fmt.Errorf("absolute host workspace required")
	}
	if a.HostRoot == "" && a.LocalRoot == "" {
		return os.OpenRoot(workspace)
	}
	if !filepath.IsAbs(a.HostRoot) || !filepath.IsAbs(a.LocalRoot) {
		return nil, fmt.Errorf("both workspace roots must be absolute")
	}
	rel, e := filepath.Rel(filepath.Clean(a.HostRoot), filepath.Clean(workspace))
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace outside configured host root")
	}
	root, e := os.OpenRoot(a.LocalRoot)
	if e != nil {
		return nil, fmt.Errorf("workspace mount unavailable: %w", e)
	}
	defer root.Close()
	return root.OpenRoot(rel)
}
func directory(root *os.Root, name string) (*os.Root, error) {
	info, e := root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("regular directory required: %s", name)
	}
	return root.OpenRoot(name)
}
func (a Access) tree(workspace string) (*os.Root, error) {
	root, e := a.workspace(workspace)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	return directory(root, "openspec")
}
func mkdir(root *os.Root, name string) error {
	if e := root.Mkdir(name, 0755); e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	dir, e := directory(root, name)
	if e != nil {
		return e
	}
	return dir.Close()
}

// Init only bootstraps directories. No configuration, prompts or documents are overwritten.
func (a Access) Init(workspace string) (Initialized, error) {
	out := Initialized{Directories: []string{"openspec", "openspec/specs", "openspec/changes", "openspec/changes/archive"}}
	root, e := a.workspace(workspace)
	if e != nil {
		return out, e
	}
	defer root.Close()
	if e = mkdir(root, "openspec"); e != nil {
		return out, e
	}
	tree, e := directory(root, "openspec")
	if e != nil {
		return out, e
	}
	defer tree.Close()
	for _, name := range []string{"specs", "changes"} {
		if e = mkdir(tree, name); e != nil {
			return out, e
		}
	}
	changes, e := directory(tree, "changes")
	if e != nil {
		return out, e
	}
	defer changes.Close()
	return out, mkdir(changes, "archive")
}
func ValidChange(name string) bool {
	return len(name) <= 96 && changeName.MatchString(name) && name != "archive"
}
func validPath(name string) bool {
	if name == "" || len(name) > 512 || strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	bits := strings.Split(name, "/")
	for _, b := range bits {
		if b == "." || b == ".." || b == "" {
			return false
		}
	}
	if name == "config.yaml" {
		return true
	}
	if len(bits) < 2 || (bits[0] != "specs" && bits[0] != "changes") {
		return false
	}
	base := bits[len(bits)-1]
	return strings.HasSuffix(base, ".md") || base == ".openspec.yaml"
}
func read(root *os.Root, name string) ([]byte, error) {
	info, e := root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDocument {
		return nil, fmt.Errorf("regular document <= 1 MiB required")
	}
	// A nonblocking open avoids hanging if a trusted local edit races a file into a FIFO.
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("regular document required")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if e != nil {
		return nil, e
	}
	if len(b) > MaxDocument || !utf8.Valid(b) {
		return nil, fmt.Errorf("UTF-8 document <= 1 MiB required")
	}
	return b, nil
}
func (a Access) Read(workspace, name string, offset, limit int, digest string) (Document, error) {
	out := Document{Path: name, Offset: offset}
	if !validPath(name) || offset < 0 || limit < 4 || limit > 32768 || offset > 0 && len(digest) != 64 {
		return out, fmt.Errorf("valid OpenSpec path, offset >= 0, limit 4..32768 and SHA256 for continuation required")
	}
	root, e := a.tree(workspace)
	if e != nil {
		return out, e
	}
	defer root.Close()
	b, e := read(root, name)
	if e != nil {
		return out, e
	}
	sum := sha256.Sum256(b)
	out.SHA256 = hex.EncodeToString(sum[:])
	if digest != "" && digest != out.SHA256 {
		return out, ErrConflict
	}
	if offset > len(b) || offset < len(b) && !utf8.RuneStart(b[offset]) {
		return out, fmt.Errorf("offset must be a UTF-8 byte boundary within the document")
	}
	end := min(offset+limit, len(b))
	for end < len(b) && !utf8.RuneStart(b[end]) {
		end--
	}
	out.Bytes = len(b)
	out.NextOffset = end
	out.HasMore = end < len(b)
	out.Content = string(b[offset:end])
	return out, nil
}
func (a Access) CheckChange(workspace, name string) (Check, error) {
	out := Check{Change: name, Path: "openspec/changes/" + name, Scope: "reference", Note: "Checks an active change and nonempty proposal only; use official OpenSpec tools for schema/semantic validation."}
	if !ValidChange(name) {
		return out, fmt.Errorf("invalid active OpenSpec change name")
	}
	root, e := a.tree(workspace)
	if e != nil {
		return out, e
	}
	defer root.Close()
	change, e := directory(root, "changes/"+name)
	if e != nil {
		return out, e
	}
	defer change.Close()
	b, e := read(change, "proposal.md")
	if e != nil {
		return out, e
	}
	if strings.TrimSpace(string(b)) == "" {
		return out, fmt.Errorf("nonempty change proposal required")
	}
	sum := sha256.Sum256(b)
	out.ProposalSHA256 = hex.EncodeToString(sum[:])
	out.Valid = true
	return out, nil
}

// List one OpenSpec directory at a time; bounded scans avoid recursively loading project text.
func (a Access) List(workspace, dir, after string, limit int) (Page, error) {
	out := Page{Items: []Entry{}, NextAfter: after}
	if dir == "" {
		dir = "."
	}
	if dir != "." && (strings.HasPrefix(dir, "/") || path.Clean(dir) != dir || strings.Contains(dir, "\\") || len(dir) > 512 || !(dir == "changes" || dir == "specs" || strings.HasPrefix(dir, "changes/") || strings.HasPrefix(dir, "specs/"))) {
		return out, fmt.Errorf("valid OpenSpec directory required")
	}
	for _, b := range strings.Split(dir, "/") {
		if b == ".." {
			return out, fmt.Errorf("invalid directory")
		}
	}
	if limit < 1 || limit > 100 || len(after) > 255 {
		return out, fmt.Errorf("limit 1..100 and bounded after required")
	}
	root, e := a.tree(workspace)
	if e != nil {
		return out, e
	}
	defer root.Close()
	d, e := directory(root, dir)
	if e != nil {
		return out, e
	}
	defer d.Close()
	f, e := d.Open(".")
	if e != nil {
		return out, e
	}
	defer f.Close()
	entries, e := f.ReadDir(2001)
	if e != nil && !errors.Is(e, io.EOF) {
		return out, e
	}
	if len(entries) > 2000 {
		return out, fmt.Errorf("directory exceeds 2000 entries; use native OpenSpec tools")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if name <= after || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := path.Join(dir, name)
		if !entry.IsDir() && !validPath(p) {
			continue
		}
		info, e := d.Lstat(name)
		if e != nil {
			return out, e
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, Entry{Name: name, Path: p, Directory: info.IsDir(), Bytes: info.Size()})
		out.NextAfter = name
	}
	return out, nil
}
