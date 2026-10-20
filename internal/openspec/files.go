// Package openspec holds the OpenSpec path rules and document views over a project's tree in Core storage
// (core-owned-project-data D2); it is not an OpenSpec workflow engine. Storage supplies the documents, nodes
// materialize them as files and the official OpenSpec CLI works on those files.
package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var ErrConflict = errors.New("document changed; restart the read")
var changeName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const MaxDocument = 1 << 20

// DefaultDir is the tree's directory in a worktree when the project does not set openspec.dir.
const DefaultDir = "openspec"

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

// Doc is one current document of a project's tree: its path relative to the tree, digest and size.
type Doc struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Written is the answer of a write, move or delete: the document's new current digest ("" once deleted).
type Written struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Version int64  `json:"version"`
	Deleted bool   `json:"deleted,omitempty"`
	// From is the source of a move.
	From string `json:"from,omitempty"`
	// Unchanged: the document already had this content (a retried write), no version was added.
	Unchanged bool `json:"unchanged,omitempty"`
}

// ConflictError is a compare-and-set failure (HTTP 409): the document's current digest is not the caller's
// base ("" when the document does not exist).
type ConflictError struct {
	Path    string `json:"path"`
	Current string `json:"current_sha256"`
}

func (e *ConflictError) Error() string {
	if e.Current == "" {
		return "openspec: " + e.Path + " does not exist (base_sha256 must be empty)"
	}
	return "openspec: " + e.Path + " changed; current sha256 " + e.Current
}
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// Directories are the tree's fixed directories: every initialized tree lists them, even when empty.
var Directories = []string{"specs", "changes", "changes/archive"}

func ValidChange(name string) bool {
	return len(name) <= 96 && changeName.MatchString(name) && name != "archive"
}

// ValidPath: config.yaml, or Markdown and .openspec.yaml under specs/ and changes/ (archive included),
// relative, without traversal.
func ValidPath(name string) bool {
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

// ValidDir is a directory of the tree: "." (the root), specs, changes or below them.
func ValidDir(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	if dir != "." && (strings.HasPrefix(dir, "/") || path.Clean(dir) != dir || strings.Contains(dir, "\\") || len(dir) > 512 || !(dir == "changes" || dir == "specs" || strings.HasPrefix(dir, "changes/") || strings.HasPrefix(dir, "specs/"))) {
		return dir, fmt.Errorf("valid OpenSpec directory required")
	}
	for _, b := range strings.Split(dir, "/") {
		if b == ".." {
			return dir, fmt.Errorf("invalid directory")
		}
	}
	return dir, nil
}

// ValidSubdir is a directory below specs/ or changes/ (a moved change or capability).
func ValidSubdir(dir string) bool {
	d, err := ValidDir(dir)
	return err == nil && d != "." && d != "specs" && d != "changes" && !ValidPath(d)
}

// ValidTreeDir is a project's openspec.dir: a relative directory of the worktree outside .factory and .git.
func ValidTreeDir(dir string) bool {
	if dir == "" || len(dir) > 128 || strings.Contains(dir, "\\") || path.Clean(dir) != dir || strings.HasPrefix(dir, "/") {
		return false
	}
	for _, b := range strings.Split(dir, "/") {
		if b == "." || b == ".." || b == "" {
			return false
		}
	}
	first := strings.Split(dir, "/")[0]
	return first != ".factory" && first != ".git"
}

// ValidContent: a UTF-8 document of at most 1 MiB.
func ValidContent(b []byte) error {
	if len(b) > MaxDocument || !utf8.Valid(b) {
		return fmt.Errorf("UTF-8 document <= 1 MiB required")
	}
	return nil
}

func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ValidRead checks read arguments before the document is loaded.
func ValidRead(name string, offset, limit int, digest string) error {
	if !ValidPath(name) || offset < 0 || limit < 4 || limit > 32768 || offset > 0 && len(digest) != 64 {
		return fmt.Errorf("valid OpenSpec path, offset >= 0, limit 4..32768 and SHA256 for continuation required")
	}
	return nil
}

// Read pages a document: up to limit bytes from offset on UTF-8 boundaries. A continuation (digest set)
// fails with ErrConflict once the document changed.
func Read(name string, b []byte, offset, limit int, digest string) (Document, error) {
	out := Document{Path: name, Offset: offset}
	if err := ValidRead(name, offset, limit, digest); err != nil {
		return out, err
	}
	if err := ValidContent(b); err != nil {
		return out, err
	}
	out.SHA256 = Digest(b)
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

// ProposalPath is the proposal of an active change, relative to the tree.
func ProposalPath(change string) string { return "changes/" + change + "/proposal.md" }

// CheckChange validates an active change reference from its proposal's content (found reports whether the
// proposal exists).
func CheckChange(name string, proposal []byte, found bool) (Check, error) {
	out := Check{Change: name, Path: "openspec/changes/" + name, Scope: "reference", Note: "Checks an active change and nonempty proposal only; use official OpenSpec tools for schema/semantic validation."}
	if !ValidChange(name) {
		return out, fmt.Errorf("invalid active OpenSpec change name")
	}
	if !found {
		return out, fmt.Errorf("change %s has no proposal (%s)", name, ProposalPath(name))
	}
	if err := ValidContent(proposal); err != nil {
		return out, err
	}
	if strings.TrimSpace(string(proposal)) == "" {
		return out, fmt.Errorf("nonempty change proposal required")
	}
	out.ProposalSHA256 = Digest(proposal)
	out.Valid = true
	return out, nil
}

// List one directory of the tree from its documents (all current documents under dir): the documents directly
// in it and the subdirectories that hold documents, plus the fixed directories. Names are sorted; after is the
// previous page's next_after.
func List(docs []Doc, dir, after string, limit int) (Page, error) {
	out := Page{Items: []Entry{}, NextAfter: after}
	dir, err := ValidDir(dir)
	if err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 || len(after) > 255 {
		return out, fmt.Errorf("limit 1..100 and bounded after required")
	}
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	entries := map[string]Entry{}
	for _, d := range Directories {
		if rest, ok := strings.CutPrefix(d, prefix); ok && rest != "" && !strings.Contains(rest, "/") {
			entries[rest] = Entry{Name: rest, Path: d, Directory: true}
		}
	}
	for _, d := range docs {
		rest, ok := strings.CutPrefix(d.Path, prefix)
		if !ok || rest == "" {
			continue
		}
		name, sub, nested := strings.Cut(rest, "/")
		if nested && sub != "" {
			entries[name] = Entry{Name: name, Path: path.Join(dir, name), Directory: true}
		} else if !nested {
			entries[name] = Entry{Name: name, Path: d.Path, Bytes: d.Size}
		}
	}
	if len(entries) > 2000 {
		return out, fmt.Errorf("directory exceeds 2000 entries; use native OpenSpec tools")
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if name <= after {
			continue
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, entries[name])
		out.NextAfter = name
	}
	return out, nil
}

// Changes are the active changes named by documents (changes/<name>/..., archive excluded).
func Changes(docs []Doc) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range docs {
		bits := strings.Split(d.Path, "/")
		if len(bits) >= 3 && bits[0] == "changes" && ValidChange(bits[1]) && !seen[bits[1]] {
			seen[bits[1]] = true
			out = append(out, bits[1])
		}
	}
	sort.Strings(out)
	return out
}
