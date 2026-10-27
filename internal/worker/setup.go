package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/workspace"
)

// Project setup on the node (project-creation D2, D3): a run whose spec carries ProjectSetup first creates or
// onboards the project's repository, then reports it to Core (POST /runs/{id}/project-setup with the run's
// lease) and continues in the workspace Core answers, with the manifest of the (imported) OpenSpec tree.
//
//   - local: a git repository <projects_root>/<project> with an initial commit on main (README, .gitignore).
//   - github: the same, plus a private GitHub repository <gh user>/<project> created with the node's gh,
//     pushed and set as repo_url.
//   - existing: the workspace path as it is, or a clone of repo_url at <projects_root>/<project>. Its OpenSpec
//     tree is imported into Core; when git tracks it, a commit stops tracking it (git rm --cached, .gitignore)
//     and goes to the default branch, else to the first task's branch af/<task>, else stays local.
//
// Every step is idempotent: a retry after a crash reuses what an earlier attempt made (a repository is
// recognized as this project's by .git/factory-project).

// DefaultGH is the GitHub CLI the node runs for repo_mode github.
const DefaultGH = "gh"

// projectsRoot is where the node creates and clones project repositories (node.yaml projects_root).
func (w *Worker) projectsRoot() string {
	if w.ProjectsRoot != "" {
		return w.ProjectsRoot
	}
	root := w.StateDir
	if root == "" {
		root = filepath.Join(os.TempDir(), "factory-node")
	}
	return filepath.Join(root, "projects")
}

// setupProject performs the run's project setup and reports it; on failure the reason is reported too (the
// project's feed names it) and the run ends setup_failed.
func (w *Worker) setupProject(ctx context.Context, spec protocol.RunSpec, lease string) (protocol.ProjectSetupAnswer, error) {
	var answer protocol.ProjectSetupAnswer
	report, err := w.prepareRepository(ctx, spec)
	report.Lease, report.NodeID = lease, w.NodeID
	path := "/runs/" + spec.ID + "/project-setup"
	if err != nil {
		failed := protocol.ProjectSetupReport{Lease: lease, NodeID: w.NodeID, Error: err.Error()}
		if e := w.API.Do(ctx, "POST", path, failed, nil); e != nil {
			slog.Error("reporting a failed project setup", "run", spec.ID, "project", spec.Project, "error", e)
		}
		return answer, err
	}
	if err = w.API.Do(ctx, "POST", path, report, &answer); err != nil {
		return answer, fmt.Errorf("reporting the setup to Core: %w", err)
	}
	if answer.Workspace == "" {
		return answer, errors.New("Core answered no workspace")
	}
	return answer, nil
}

// prepareRepository creates or onboards the repository and says what to report.
func (w *Worker) prepareRepository(ctx context.Context, spec protocol.RunSpec) (protocol.ProjectSetupReport, error) {
	var rep protocol.ProjectSetupReport
	s := spec.ProjectSetup
	if spec.Project == "" || strings.ContainsAny(spec.Project, `/\`) || strings.HasPrefix(spec.Project, ".") {
		return rep, fmt.Errorf("invalid project id %q", spec.Project)
	}
	dir := filepath.Join(w.projectsRoot(), spec.Project)
	marker := w.company() + "/" + spec.Project
	switch s.Mode {
	case "local":
		if err := initRepository(ctx, dir, marker, s); err != nil {
			return rep, err
		}
		rep.Workspace = dir
	case "github":
		gh := cmpOr(w.GH, DefaultGH)
		login, err := ghLogin(ctx, gh, w.NodeID)
		if err != nil {
			return rep, err
		}
		if err = initRepository(ctx, dir, marker, s); err != nil {
			return rep, err
		}
		url, err := ghRepository(ctx, gh, login+"/"+spec.Project, s.Description)
		if err != nil {
			return rep, err
		}
		if err = setOrigin(ctx, dir, url); err != nil {
			return rep, err
		}
		if _, err = git(ctx, dir, "push", "--quiet", "-u", "origin", "main"); err != nil {
			return rep, fmt.Errorf("pushing to %s failed (does the node's git have GitHub credentials? gh auth setup-git): %w", url, err)
		}
		rep.Workspace, rep.RepoURL, rep.DefaultBranch = dir, url, "main"
	case "existing":
		if s.Workspace != "" {
			dir = s.Workspace
			top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
			if err != nil {
				return rep, fmt.Errorf("workspace %s on node %s is not a git repository: %w", dir, w.NodeID, err)
			}
			if !samePath(top, dir) {
				return rep, fmt.Errorf("workspace %s is inside the git repository %s: use the repository's top directory", dir, top)
			}
		} else if err := cloneRepository(ctx, dir, s.RepoURL, s.DefaultBranch); err != nil {
			return rep, err
		}
		rep.Workspace, rep.RepoURL, rep.DefaultBranch = dir, s.RepoURL, s.DefaultBranch
		if s.OpenSpecDir != "" {
			docs, untracked, err := onboardOpenSpec(ctx, dir, s.OpenSpecDir, s.DefaultBranch, spec.TaskID)
			if err != nil {
				return rep, err
			}
			rep.OpenSpec, rep.Untracked = docs, untracked
		}
	default:
		return rep, fmt.Errorf("unknown repo_mode %q", s.Mode)
	}
	return rep, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// git runs git in dir and answers its trimmed output; an error carries git's output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	return command(ctx, dir, "git", args...)
}

func command(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String() + " " + out.String())
		return "", fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// commit commits the index with a fallback identity when the node's git has none.
func commit(ctx context.Context, dir, message string) error {
	args := []string{"commit", "--quiet", "--no-verify", "-m", message}
	if name, _ := git(ctx, dir, "config", "user.email"); name == "" {
		args = append([]string{"-c", "user.name=App Factory", "-c", "user.email=factory@localhost"}, args...)
	}
	_, err := git(ctx, dir, args...)
	return err
}

// markerPath marks a repository the node created for a project (company/project).
func markerPath(dir string) string { return filepath.Join(dir, ".git", "factory-project") }

// initRepository creates a git repository with an initial commit on main at dir, or reuses the one an
// earlier attempt created there. Anything else at dir is refused: the owner's files are never adopted.
func initRepository(ctx context.Context, dir, marker string, s *protocol.ProjectSetup) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	case err != nil:
		return err
	case len(entries) > 0:
		b, e := os.ReadFile(markerPath(dir))
		if e != nil || strings.TrimSpace(string(b)) != marker {
			return fmt.Errorf("%s already exists and is not this project's repository: to use it, create the project with repo_mode existing and workspace %s", dir, dir)
		}
	}
	if _, err = os.Stat(filepath.Join(dir, ".git")); errors.Is(err, fs.ErrNotExist) {
		if _, err = git(ctx, dir, "init", "--quiet", "-b", "main"); err != nil {
			return err
		}
	}
	if err = os.WriteFile(markerPath(dir), []byte(marker+"\n"), 0o644); err != nil {
		return err
	}
	if _, err = git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return nil // the initial commit exists
	}
	readme := "# " + cmpOr(s.Name, filepath.Base(dir)) + "\n"
	if s.Description != "" {
		readme += "\n" + s.Description + "\n"
	}
	// OpenSpec lives in App Factory and is materialized into every run's worktree; it is not tracked.
	ignore := "/.factory/\n"
	if s.OpenSpecDir != "" {
		ignore += "/" + s.OpenSpecDir + "/\n"
	}
	for name, content := range map[string]string{"README.md": readme, ".gitignore": ignore} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	if _, err = git(ctx, dir, "add", "README.md", ".gitignore"); err != nil {
		return err
	}
	return commit(ctx, dir, "Initial commit")
}

// ghLogin checks the node's GitHub CLI and answers the authenticated user.
func ghLogin(ctx context.Context, gh, node string) (string, error) {
	path, err := exec.LookPath(gh)
	if err != nil {
		return "", fmt.Errorf("the GitHub CLI (gh) is not installed on node %s: install it and run gh auth login, then retry the task", node)
	}
	if _, err = command(ctx, "", path, "auth", "status"); err != nil {
		return "", fmt.Errorf("gh is not authenticated on node %s (run gh auth login, then retry the task): %w", node, err)
	}
	login, err := command(ctx, "", path, "api", "user", "--jq", ".login")
	if err != nil || login == "" {
		return "", fmt.Errorf("reading the GitHub user with gh on node %s: %w", node, err)
	}
	return login, nil
}

// ghRepository creates the private GitHub repository name (owner/repo) unless it exists, and answers its URL.
func ghRepository(ctx context.Context, gh, name, description string) (string, error) {
	view := func() (string, error) {
		return command(ctx, "", gh, "repo", "view", name, "--json", "url", "--jq", ".url")
	}
	if url, err := view(); err == nil && url != "" {
		return url, nil
	}
	args := []string{"repo", "create", name, "--private"}
	if description != "" {
		args = append(args, "--description", description)
	}
	if _, err := command(ctx, "", gh, args...); err != nil {
		return "", fmt.Errorf("creating the GitHub repository %s: %w", name, err)
	}
	url, err := view()
	if err != nil || url == "" {
		return "", fmt.Errorf("reading the new GitHub repository %s: %w", name, err)
	}
	return url, nil
}

// setOrigin points the repository's origin at url.
func setOrigin(ctx context.Context, dir, url string) error {
	cur, err := git(ctx, dir, "remote", "get-url", "origin")
	switch {
	case err != nil:
		_, err = git(ctx, dir, "remote", "add", "origin", url)
	case cur != url:
		_, err = git(ctx, dir, "remote", "set-url", "origin", url)
	}
	return err
}

// cloneRepository clones url to dir, or reuses a clone of url there.
func cloneRepository(ctx context.Context, dir, url, branch string) error {
	if _, err := os.Stat(dir); err == nil {
		if cur, e := git(ctx, dir, "remote", "get-url", "origin"); e == nil && cur == url {
			return nil
		}
		return fmt.Errorf("%s already exists and is not a clone of %s: give its path as workspace instead", dir, url)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"clone", "--quiet"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	tmp := dir + ".clone"
	_ = os.RemoveAll(tmp)
	if _, err := git(ctx, filepath.Dir(dir), append(args, url, tmp)...); err != nil {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	return os.Rename(tmp, dir)
}

// onboardOpenSpec reads the repository's OpenSpec tree (to import into Core) and, when git tracks it, stops
// tracking it in a commit, so that materializing Core's tree never conflicts with the repository's copy.
// The commit goes to the default branch when the repository's HEAD was exactly the remote default branch,
// else (a refused push) to the first task's branch af/<task>; otherwise it stays local.
func onboardOpenSpec(ctx context.Context, dir, tree, branch, task string) ([]protocol.SetupDocument, string, error) {
	base := filepath.Join(dir, filepath.FromSlash(tree))
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		return nil, "", nil
	}
	var docs []protocol.SetupDocument
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if !openspec.ValidPath(rel) {
			return nil // not an OpenSpec document (as factory openspec import skips it)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err = openspec.ValidContent(b); err != nil {
			return fmt.Errorf("%s/%s: %w", tree, rel, err)
		}
		docs = append(docs, protocol.SetupDocument{Path: rel, Content: string(b)})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	tracked, err := git(ctx, dir, "ls-files", "--", tree)
	if err != nil || tracked == "" {
		return docs, "", err
	}
	// Only the untracking goes into the commit: refuse when the owner has staged other changes.
	if _, err = git(ctx, dir, "diff", "--cached", "--quiet"); err != nil {
		return nil, "", fmt.Errorf("the repository %s has staged changes: commit or unstage them, then retry the task", dir)
	}
	if _, err = git(ctx, dir, "rm", "-r", "--cached", "--quiet", "--", tree); err != nil {
		return nil, "", err
	}
	if err = ignoreTree(dir, tree); err != nil {
		return nil, "", err
	}
	if _, err = git(ctx, dir, "add", "--", ".gitignore"); err != nil {
		return nil, "", err
	}
	if err = commit(ctx, dir, "Keep OpenSpec in App Factory: stop tracking "+tree+"/"); err != nil {
		return nil, "", err
	}
	return docs, pushUntrack(ctx, dir, branch, task), nil
}

// ignoreTree adds /<tree>/ to the repository's .gitignore.
func ignoreTree(dir, tree string) error {
	p := filepath.Join(dir, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l == "/"+tree+"/" || l == tree+"/" || l == "/"+tree {
			return nil
		}
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return os.WriteFile(p, append(b, "/"+tree+"/\n"...), 0o644)
}

// pushUntrack pushes the untracking commit: "pushed" to the default branch, "task_branch" to af/<task>, or
// "local". Nothing is pushed when the commit's parent is not the remote default branch (the checkout holds
// other unpushed work).
func pushUntrack(ctx context.Context, dir, branch, task string) string {
	if _, err := git(ctx, dir, "remote", "get-url", "origin"); err != nil {
		return "local"
	}
	_, _ = git(ctx, dir, "fetch", "--quiet", "origin")
	if branch == "" {
		head, err := git(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err != nil {
			if head, err = git(ctx, dir, "symbolic-ref", "--short", "HEAD"); err != nil {
				return "local"
			}
			head = "origin/" + head
		}
		branch = strings.TrimPrefix(head, "origin/")
	}
	parent, err1 := git(ctx, dir, "rev-parse", "HEAD^")
	remote, err2 := git(ctx, dir, "rev-parse", "refs/remotes/origin/"+branch)
	if err1 != nil || err2 != nil || parent != remote {
		return "local"
	}
	if _, err := git(ctx, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch); err == nil {
		return "pushed"
	} else {
		slog.Warn("pushing the OpenSpec untracking to the default branch", "dir", dir, "error", err)
	}
	if task != "" {
		if _, err := git(ctx, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+workspace.BranchName(task, "")); err == nil {
			return "task_branch"
		}
	}
	return "local"
}
