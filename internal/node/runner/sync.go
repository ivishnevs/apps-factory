package runner

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"appfactory/internal/skills"
)

// Private skill sources (skill-registry D3): Core asks an online node of the company to fetch a source with the
// node's own git access (the owner's machine, as it clones project repositories); the node packs the tree at the
// resolved commit and uploads it with a signed request. Core keeps no git credentials.

type syncJob struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
}

// syncTimeout bounds one private source fetch.
var syncTimeout = 5 * time.Minute

var syncing sync.Map // job ID → true while it runs or was done by this process

// startSyncJob runs job once per process (the poll lists it until Core records its end).
func (s *session) startSyncJob(ctx context.Context, job syncJob) {
	if _, loaded := syncing.LoadOrStore(job.ID, true); loaded {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, syncTimeout)
		defer cancel()
		commit, tree, err := fetchPrivate(ctx, job)
		if tree != "" {
			defer os.RemoveAll(filepath.Dir(tree))
		}
		path := "/skills/sync-jobs/" + url.PathEscape(job.ID)
		if err == nil {
			var body []byte
			if body, err = skills.PackTree(tree); err == nil {
				err = s.api.PostRaw(ctx, path+"?commit="+url.QueryEscape(commit), "application/x-tar", body)
			}
		}
		if err != nil {
			slog.Warn("private skill source sync failed", "source", job.Source, "error", err)
			msg := err.Error()
			if len(msg) > 512 {
				msg = msg[:512]
			}
			if e := s.api.PostRaw(ctx, path+"?error="+url.QueryEscape(msg), "application/x-tar", nil); e != nil {
				slog.Error("reporting a skill source sync failure", "source", job.Source, "error", e)
			}
			return
		}
		slog.Info("private skill source synced", "source", job.Source, "commit", commit)
	}()
}

// fetchPrivate shallow-clones the source with the node's git (no hooks, no prompts, no LFS) and returns its commit.
func fetchPrivate(ctx context.Context, job syncJob) (string, string, error) {
	dir, err := os.MkdirTemp("", "factory-skill-sync-")
	if err != nil {
		return "", "", err
	}
	tree := filepath.Join(dir, "tree")
	args := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.file.allow=never", "clone", "--depth", "1", "--no-tags", "--single-branch"}
	if job.Ref != "" {
		args = append(args, "--branch", job.Ref)
	}
	args = append(args, "--", job.URL, tree)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1", "GIT_ASKPASS=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", tree, &gitError{strings.TrimSpace(string(out)), err}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", tree, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", tree, err
	}
	return strings.TrimSpace(string(out)), tree, nil
}

type gitError struct {
	out string
	err error
}

func (e *gitError) Error() string { return "git clone: " + e.err.Error() + ": " + e.out }
