package factorytools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"appfactory/internal/openspec"
)

// OpenSpec write-back (core-owned-project-data D4): the node materialized the project's OpenSpec tree with
// .factory/manifest.json; before every exit (and on factory_openspec_sync) the adapter uploads the files the
// run changed, added or removed, each against the digest it started from. A conflict refuses the exit: Core's
// current version is saved beside the file (<file>.factory-current) and becomes the new base, so the agent
// merges and exits again.

// manifest mirrors protocol.Manifest (.factory/manifest.json; factorytools cannot import protocol).
type manifest struct {
	OpenSpecDir string         `json:"openspec_dir,omitempty"`
	Files       []manifestFile `json:"files"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

const manifestPath = ".factory/manifest.json"

// ConflictSuffix names the file holding Core's current version of a conflicting document.
const ConflictSuffix = ".factory-current"

// SyncResult is what a sync uploaded; Conflicts are the documents Core changed meanwhile.
type SyncResult struct {
	Written   []string       `json:"written"`
	Deleted   []string       `json:"deleted"`
	Conflicts []SyncConflict `json:"conflicts,omitempty"`
}

type SyncConflict struct {
	Path    string `json:"path"`
	Current string `json:"current_sha256"`
	// File holds Core's current version (relative to the worktree); empty when Core deleted the document.
	File string `json:"current_file,omitempty"`
}

// Sync uploads the worktree's OpenSpec edits; without a manifest (a run without materialization) it does
// nothing and returns nil.
func (a *Adapter) Sync(ctx context.Context) (*SyncResult, error) {
	if a.Workdir == "" {
		return nil, nil
	}
	mfile := filepath.Join(a.Workdir, filepath.FromSlash(manifestPath))
	b, err := os.ReadFile(mfile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if m.OpenSpecDir == "" || !openspec.ValidTreeDir(m.OpenSpecDir) {
		return nil, nil
	}
	prefix := m.OpenSpecDir + "/"
	base := map[string]manifestFile{} // tree path → manifest entry
	for _, f := range m.Files {
		if rel, ok := strings.CutPrefix(f.Path, prefix); ok {
			base[rel] = f
		}
	}
	type local struct {
		sha     string
		content []byte
	}
	files := map[string]local{}
	root := filepath.Join(a.Workdir, filepath.FromSlash(m.OpenSpecDir))
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == root {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !openspec.ValidPath(rel) {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err = openspec.ValidContent(content); err != nil {
			return fmt.Errorf("%s/%s: %w", m.OpenSpecDir, rel, err)
		}
		files[rel] = local{openspec.Digest(content), content}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := &SyncResult{Written: []string{}, Deleted: []string{}}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	for p := range base {
		if _, ok := files[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		f, exists := files[p]
		old := base[p].SHA256
		var call Call
		switch {
		case exists && f.sha == old:
			continue
		case exists:
			call = Call{Name: "factory_openspec_write", Arguments: map[string]any{"path": p, "base_sha256": old, "content": string(f.content)}}
		default:
			call = Call{Name: "factory_openspec_delete", Arguments: map[string]any{"path": p, "base_sha256": old}}
		}
		status, body, _, err := a.do(ctx, "POST", "/agent/call", call)
		if err != nil {
			return nil, err
		}
		switch {
		case status == 200 && exists:
			base[p] = manifestFile{Path: prefix + p, SHA256: f.sha, Size: int64(len(f.content))}
			out.Written = append(out.Written, p)
		case status == 200:
			delete(base, p)
			out.Deleted = append(out.Deleted, p)
		case status == 409:
			var c SyncConflict
			if json.Unmarshal(body, &c) != nil || c.Current != "" && len(c.Current) != 64 {
				return nil, fmt.Errorf("%s: Core HTTP 409: %s", p, strings.TrimSpace(string(body)))
			}
			c.Path = p
			if c.Current != "" {
				file := filepath.Join(root, filepath.FromSlash(p)) + ConflictSuffix
				if err = a.fetchBlob(ctx, c.Current, file); err != nil {
					return nil, fmt.Errorf("%s: reading Core's current version: %w", p, err)
				}
				c.File = prefix + p + ConflictSuffix
			}
			// The agent merges against Core's version: it is the base of the next sync.
			base[p] = manifestFile{Path: prefix + p, SHA256: c.Current}
			out.Conflicts = append(out.Conflicts, c)
		default:
			return nil, fmt.Errorf("%s: Core HTTP %d: %s", p, status, strings.TrimSpace(string(body)))
		}
	}
	m.Files = rebase(m.Files, prefix, base)
	nb, _ := json.MarshalIndent(m, "", "  ")
	tmp := mfile + ".tmp"
	if err = os.WriteFile(tmp, nb, 0o644); err == nil {
		err = os.Rename(tmp, mfile)
	}
	return out, err
}

// rebase rebuilds the manifest's files: entries outside the tree as they were and the tree's updated bases
// (a base without a digest is a document Core deleted: dropped).
func rebase(files []manifestFile, prefix string, base map[string]manifestFile) []manifestFile {
	out := []manifestFile{}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, prefix) {
			out = append(out, f)
		}
	}
	var tree []string
	for p, f := range base {
		if f.SHA256 != "" {
			tree = append(tree, p)
		}
	}
	sort.Strings(tree)
	for _, p := range tree {
		out = append(out, base[p])
	}
	return out
}

// fetchBlob saves a blob of the run's project to file.
func (a *Adapter) fetchBlob(ctx context.Context, sha, file string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(a.URL, "/")+"/agent/blobs/"+sha, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	res, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("Core HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, openspec.MaxDocument+1))
	if err != nil {
		return err
	}
	if openspec.Digest(b) != sha {
		return fmt.Errorf("content does not match sha256 %s", sha)
	}
	return os.WriteFile(file, b, 0o644)
}

// syncRefusal is the exit refusal of a sync with conflicts.
func syncRefusal(r *SyncResult) error {
	var b strings.Builder
	for _, c := range r.Conflicts {
		if c.File != "" {
			fmt.Fprintf(&b, "\n- %s: Core has another version (sha256 %s) in %s; merge it into your file and delete %s", c.Path, c.Current, c.File, c.File)
		} else {
			fmt.Fprintf(&b, "\n- %s: Core deleted it meanwhile; keep your file to restore it, or delete it", c.Path)
		}
	}
	return fmt.Errorf("exit refused (openspec_conflict): other runs changed these OpenSpec documents since your run started:%s\nThen exit again (your next exit uploads the merged files).", b.String())
}

// syncTool answers factory_openspec_sync.
func (a *Adapter) syncTool(ctx context.Context) (any, error) {
	r, err := a.Sync(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return map[string]any{"synced": false, "reason": "this run has no materialized OpenSpec tree"}, nil
	}
	if len(r.Conflicts) > 0 {
		return map[string]any{"result": r, "next": syncRefusal(r).Error()}, nil
	}
	return map[string]any{"result": r, "at": time.Now().UTC()}, nil
}
