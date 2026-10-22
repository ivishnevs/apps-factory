package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
)

// Materialization (core-owned-project-data D3): before the runtime starts, the node places the run's
// manifest in the worktree: the project's current OpenSpec tree (stale documents removed) and the pinned
// approved artifact inputs. Blobs come from Core with the run's lease (GET /runs/{id}/blobs/{sha256}) and are
// cached per node by digest; Core never reads the node's disk.
//
// Unsynced edits survive: a run that edited OpenSpec documents and ended without an exit (or sync) left them
// in the worktree with the previous .factory/manifest.json. A document whose content differs from the digest
// that manifest recorded (edited, added or deleted locally) is kept as it is, and the new manifest keeps that
// digest as its base, so the next exit uploads it with compare-and-set and a document Core changed meanwhile
// takes the usual conflict path. Unmodified documents take Core's current version.

// validManifestPath: a path below the manifest's OpenSpec directory that OpenSpec accepts, or below
// .factory/inputs/; relative and without traversal.
func validManifestPath(m *protocol.Manifest, p string) bool {
	if p == "" || strings.Contains(p, "\\") || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || p == ".." {
		return false
	}
	if rest, ok := strings.CutPrefix(p, protocol.InputsDir+"/"); ok {
		return strings.Count(rest, "/") == 2
	}
	if m.OpenSpecDir == "" || !openspec.ValidTreeDir(m.OpenSpecDir) {
		return false
	}
	rest, ok := strings.CutPrefix(p, m.OpenSpecDir+"/")
	return ok && openspec.ValidPath(rest)
}

// Materialize writes spec.Materialize into cwd, keeping unsynced OpenSpec edits; an error ends the run
// setup_failed.
func (w *Worker) Materialize(ctx context.Context, spec protocol.RunSpec, lease, cwd string) error {
	m := spec.Materialize
	if m == nil {
		return nil
	}
	if m.OpenSpecDir != "" && !openspec.ValidTreeDir(m.OpenSpecDir) {
		return fmt.Errorf("invalid OpenSpec directory %q", m.OpenSpecDir)
	}
	want := map[string]bool{}
	for _, f := range m.Files {
		if !validManifestPath(m, f.Path) || len(f.SHA256) != 64 || f.Size < 0 {
			return fmt.Errorf("invalid manifest entry %q", f.Path)
		}
		want[f.Path] = true
	}
	cache := w.BlobCache
	if cache == "" {
		dir, err := os.MkdirTemp("", "factory-blobs-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		cache = dir
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	// Download everything first: a missing blob leaves the worktree as it was.
	for _, f := range m.Files {
		if err := w.cacheBlob(ctx, spec.ID, lease, cache, f); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return err
	}
	defer root.Close()
	// keep: tree paths whose local state the previous run left unsynced (the file as it is, or its absence),
	// with the base digest they started from ("" for a document added locally).
	keep := map[string]protocol.ManifestFile{}
	if m.OpenSpecDir != "" {
		if keep, err = unsynced(root, m); err != nil {
			return err
		}
		for _, d := range append([]string{""}, openspec.Directories...) {
			if err = mkdirAll(root, path.Join(m.OpenSpecDir, d)); err != nil {
				return err
			}
		}
		// Documents Core no longer has go away; other files of the directory stay.
		base := filepath.Join(cwd, filepath.FromSlash(m.OpenSpecDir))
		err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(base, p)
			rel = filepath.ToSlash(rel)
			_, kept := keep[m.OpenSpecDir+"/"+rel]
			if openspec.ValidPath(rel) && d.Type().IsRegular() && !want[m.OpenSpecDir+"/"+rel] && !kept {
				return root.Remove(filepath.FromSlash(m.OpenSpecDir + "/" + rel))
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err = os.RemoveAll(filepath.Join(cwd, filepath.FromSlash(protocol.InputsDir))); err != nil {
		return err
	}
	saved := protocol.Manifest{OpenSpecDir: m.OpenSpecDir, Files: []protocol.ManifestFile{}}
	for _, f := range m.Files {
		if old, ok := keep[f.Path]; ok {
			if old.SHA256 != "" {
				saved.Files = append(saved.Files, old)
			}
			continue
		}
		if err = mkdirAll(root, path.Dir(f.Path)); err != nil {
			return err
		}
		if err = copyInto(root, f.Path, filepath.Join(cache, f.SHA256)); err != nil {
			return err
		}
		saved.Files = append(saved.Files, f)
	}
	// Kept documents Core no longer has: an edit still needs its base (the sync then meets Core's deletion
	// as a conflict); a local deletion of a document Core deleted too is settled.
	for p, old := range keep {
		if !want[p] && old.SHA256 != "" {
			if _, exists, err := localDigest(root, p); err != nil {
				return err
			} else if exists {
				saved.Files = append(saved.Files, old)
			}
		}
	}
	sort.Slice(saved.Files, func(i, j int) bool { return saved.Files[i].Path < saved.Files[j].Path })
	b, _ := json.MarshalIndent(saved, "", "  ")
	if err = mkdirAll(root, path.Dir(protocol.ManifestPath)); err != nil {
		return err
	}
	if err = writeRoot(root, protocol.ManifestPath, b); err != nil {
		return err
	}
	return excludeFromGit(ctx, cwd, m.OpenSpecDir)
}

// unsynced compares the worktree's OpenSpec documents with the manifest the previous materialization (or sync)
// left: every tree path whose local content differs from its recorded digest (changed, deleted, or added
// without an entry), mapped to that entry (empty SHA256 for an added document). Without a previous manifest
// for the same directory nothing is kept: the worktree takes Core's tree.
func unsynced(root *os.Root, m *protocol.Manifest) (map[string]protocol.ManifestFile, error) {
	keep := map[string]protocol.ManifestFile{}
	b, err := fs.ReadFile(root.FS(), protocol.ManifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return keep, nil
	}
	if err != nil {
		return nil, err
	}
	var prev protocol.Manifest
	if json.Unmarshal(b, &prev) != nil || prev.OpenSpecDir != m.OpenSpecDir {
		return keep, nil
	}
	prefix := m.OpenSpecDir + "/"
	recorded := map[string]protocol.ManifestFile{}
	for _, f := range prev.Files {
		if rel, ok := strings.CutPrefix(f.Path, prefix); ok && openspec.ValidPath(rel) && len(f.SHA256) == 64 {
			recorded[f.Path] = f
		}
	}
	for p, f := range recorded {
		sum, exists, err := localDigest(root, p)
		if err != nil {
			return nil, err
		}
		// A file of another kind (sum "" while it exists) is not an edit: Core's version replaces it.
		if sum != f.SHA256 && (sum != "" || !exists) {
			keep[p] = f
		}
	}
	// Documents added locally (no recorded digest).
	err = fs.WalkDir(root.FS(), m.OpenSpecDir, func(name string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && name == m.OpenSpecDir {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel := strings.TrimPrefix(name, prefix)
		if _, ok := recorded[name]; ok || !openspec.ValidPath(rel) {
			return nil
		}
		keep[name] = protocol.ManifestFile{Path: name}
		return nil
	})
	return keep, err
}

// localDigest is the digest of a regular file in root ("" for another kind of file); exists is false when
// nothing is there.
func localDigest(root *os.Root, name string) (sum string, exists bool, err error) {
	info, err := root.Lstat(filepath.FromSlash(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", true, nil
	}
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return "", true, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", true, err
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// cacheBlob makes cache/<sha256> hold f's verified content, downloading it when absent or corrupt.
func (w *Worker) cacheBlob(ctx context.Context, run, lease, cache string, f protocol.ManifestFile) error {
	final := filepath.Join(cache, f.SHA256)
	if sum, n, err := digestFile(final); err == nil && sum == f.SHA256 && n == f.Size {
		return nil
	}
	tmp, err := os.CreateTemp(cache, ".get-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = w.API.Fetch(ctx, "/runs/"+run+"/blobs/"+f.SHA256, lease, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	sum, n, err := digestFile(tmp.Name())
	if err != nil {
		return err
	}
	if sum != f.SHA256 || n != f.Size {
		return fmt.Errorf("downloaded content does not match sha256 %s", f.SHA256)
	}
	return os.Rename(tmp.Name(), final)
}

func digestFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// Fetch streams GET path with the run's lease (X-Factory-Lease) into out.
func (a API) Fetch(ctx context.Context, path, lease string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(a.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("X-Factory-Lease", lease)
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return &HTTPError{res.StatusCode, string(b)}
	}
	_, err = io.Copy(out, res.Body)
	return err
}

// mkdirAll creates dir (slash-separated, relative) and its parents inside root.
func mkdirAll(root *os.Root, dir string) error {
	if dir == "." || dir == "" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, "/") {
		cur = path.Join(cur, part)
		if err := root.Mkdir(filepath.FromSlash(cur), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if info, err := root.Lstat(filepath.FromSlash(cur)); err != nil || !info.IsDir() {
			return fmt.Errorf("%s: directory required", cur)
		}
	}
	return nil
}

func copyInto(root *os.Root, name, from string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	_ = root.Remove(filepath.FromSlash(name)) // replaces a symlink instead of writing through it
	dst, err := root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

func writeRoot(root *os.Root, name string, b []byte) error {
	_ = root.Remove(filepath.FromSlash(name))
	f, err := root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// excludeFromGit adds the OpenSpec directory and .factory to the repository's info/exclude (shared by its
// worktrees), so materialized files never make the tree dirty or reach a commit. Outside a repository it
// does nothing.
func excludeFromGit(ctx context.Context, cwd, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil // not a repository
	}
	file := strings.TrimSpace(string(out))
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(old), "\n")
	var add []string
	for _, want := range []string{"/" + dir + "/", "/.factory/"} {
		if want == "//" {
			continue
		}
		found := false
		for _, l := range lines {
			found = found || strings.TrimSpace(l) == want
		}
		if !found {
			add = append(add, want)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(file, []byte(text+strings.Join(add, "\n")+"\n"), 0o644)
}

// preflightChange checks the run's linked change in the materialized tree before the runtime starts.
func preflightChange(cwd string, spec protocol.RunSpec) error {
	dir := openspec.DefaultDir
	if spec.Materialize != nil && spec.Materialize.OpenSpecDir != "" {
		dir = spec.Materialize.OpenSpecDir
	}
	tree := filepath.Join(cwd, filepath.FromSlash(dir))
	name := filepath.Join(tree, filepath.FromSlash(openspec.ProposalPath(spec.OpenSpecChange)))
	info, err := os.Lstat(name)
	if err != nil && openspec.ValidChange(spec.OpenSpecChange) {
		// The change may be archived by an earlier run of the task (Core accepted it at claim).
		found, _ := filepath.Glob(filepath.Join(tree, "changes", "archive", "*-"+spec.OpenSpecChange, "proposal.md"))
		paths := make([]string, 0, len(found))
		for _, f := range found {
			if rel, e := filepath.Rel(tree, f); e == nil {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
		if a := openspec.ArchivedProposals(spec.OpenSpecChange, paths); len(a) > 0 {
			name = filepath.Join(tree, filepath.FromSlash(a[0]))
			info, err = os.Lstat(name)
		}
	}
	if err != nil {
		_, err = openspec.CheckChange(spec.OpenSpecChange, nil, false)
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > openspec.MaxDocument {
		return fmt.Errorf("regular proposal <= 1 MiB required")
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	_, err = openspec.CheckChange(spec.OpenSpecChange, b, true)
	return err
}
