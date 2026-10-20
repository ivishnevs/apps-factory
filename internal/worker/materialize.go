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
	"strings"
	"time"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
)

// Materialization (core-owned-project-data D3): before the runtime starts, the node places the run's
// manifest in the worktree: the project's current OpenSpec tree (stale documents removed) and the pinned
// approved artifact inputs. Blobs come from Core with the run's lease (GET /runs/{id}/blobs/{sha256}) and are
// cached per node by digest; Core never reads the node's disk.

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

// materialize writes spec.Materialize into cwd; an error ends the run setup_failed.
func (w *Worker) materialize(ctx context.Context, spec protocol.RunSpec, lease, cwd string) error {
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
	if m.OpenSpecDir != "" {
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
			if openspec.ValidPath(rel) && d.Type().IsRegular() && !want[m.OpenSpecDir+"/"+rel] {
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
	for _, f := range m.Files {
		if err = mkdirAll(root, path.Dir(f.Path)); err != nil {
			return err
		}
		if err = copyInto(root, f.Path, filepath.Join(cache, f.SHA256)); err != nil {
			return err
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err = mkdirAll(root, path.Dir(protocol.ManifestPath)); err != nil {
		return err
	}
	if err = writeRoot(root, protocol.ManifestPath, b); err != nil {
		return err
	}
	return excludeFromGit(ctx, cwd, m.OpenSpecDir)
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
	name := filepath.Join(cwd, filepath.FromSlash(dir), filepath.FromSlash(openspec.ProposalPath(spec.OpenSpecChange)))
	info, err := os.Lstat(name)
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
