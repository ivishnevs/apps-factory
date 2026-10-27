package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Project setup (project-creation D2): a project created without a ready repository (repo_mode local, github
// or existing) is set up by the node that claims the project's first task run, before that run starts. The run
// holds the project's setup slot (runs.workspace = "project-setup/<project>"), so the workspace_writer index
// keeps a second setup and every other run of the project out until it ends; tasks of the project other than
// the one whose run performs the setup wait with reason project_setup. The node reports the repository (or
// the reason it failed) with the run's lease; Core then records the workspace, imports an existing
// repository's OpenSpec tree and hands the run its workspace and manifest.

// setupSlot is the writer slot of the run that sets project's repository up.
func setupSlot(project string) string { return "project-setup/" + project }

// setupSpec is what the node sets up for p.
func setupSpec(p tasks.Project) *protocol.ProjectSetup {
	s := &protocol.ProjectSetup{Mode: p.RepoMode, Name: p.Name, Description: p.Description, Workspace: p.Workspace, RepoURL: p.RepoURL, DefaultBranch: p.DefaultBranch}
	if p.OpenSpecEnabled() {
		s.OpenSpecDir = p.OpenSpecDir()
	}
	return s
}

// saveProjectBody stores p's body as it is (no version change: setup state is Core's, not an owner edit).
func saveProjectBody(ctx context.Context, tx *Tx, p tasks.Project) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE projects SET workspace=?,body=? WHERE company_id=? AND id=?", p.Workspace, b, tx.company, p.ID)
	return err
}

// setupWait says why task waits for p's setup: another open task's run performs (or retries) it.
func setupWait(ctx context.Context, tx *Tx, p tasks.Project, task string) (string, error) {
	if p.Ready() || p.Setup.TaskID == "" || p.Setup.TaskID == task {
		return "", nil
	}
	var status string
	err := tx.QueryRowContext(ctx, "SELECT status FROM tasks WHERE company_id=? AND id=?", tx.company, p.Setup.TaskID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || status == "DONE" || status == "CANCELLED" {
		return "", nil // the setup's task is gone: this task's run sets the project up
	}
	if err != nil {
		return "", err
	}
	if p.Setup.State == tasks.SetupFailed {
		return fmt.Sprintf("the setup of project %s's repository failed (%s); task %s retries it", p.ID, p.Setup.Reason, p.Setup.TaskID), nil
	}
	return fmt.Sprintf("project %s's repository is being set up by task %s's run", p.ID, p.Setup.TaskID), nil
}

// CheckProjectRepo checks a new project's repository fields (as CreateProject does) without creating it.
func CheckProjectRepo(in tasks.ProjectCreate) error {
	var p tasks.Project
	return projectRepo(&p, in)
}

// SetProjectAuditTask records the project's onboarding audit task (idempotent).
func (s *Store) SetProjectAuditTask(ctx context.Context, company, project, task string) (tasks.Project, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return tasks.Project{}, err
	}
	defer tx.Rollback()
	p, err := projectTx(ctx, tx, project)
	if err != nil || p.Setup == nil || p.Setup.AuditTaskID == task {
		return p, err
	}
	p.Setup.AuditTaskID = task
	if err = saveProjectBody(ctx, tx, p); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// CompleteProjectSetup records the node's report of the setup a run performed, authenticated by the run's
// lease. A failure is recorded with its reason (the run then ends setup_failed and its task retries). A
// success sets the project's workspace (and repo_url, default_branch), imports the reported OpenSpec documents
// (those that differ from Core's current version; nothing is deleted), marks the setup ready, and answers the
// run's workspace and manifest. A repeated report of a ready setup by the same run answers the same.
func (s *Store) CompleteProjectSetup(ctx context.Context, company, runID string, in protocol.ProjectSetupReport) (protocol.ProjectSetupAnswer, error) {
	var out protocol.ProjectSetupAnswer
	type doc struct {
		path string
		blob Blob
	}
	var docs []doc
	if in.Error == "" {
		for _, d := range in.OpenSpec {
			content := []byte(d.Content)
			if !openspec.ValidPath(d.Path) {
				return out, inputError("invalid OpenSpec path " + d.Path)
			}
			if err := openspec.ValidContent(content); err != nil {
				return out, inputError(d.Path + ": " + err.Error())
			}
			blob, err := s.PutBlob(ctx, company, bytes.NewReader(content), openspec.MaxDocument)
			if err != nil {
				return out, err
			}
			docs = append(docs, doc{d.Path, blob})
		}
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var lease, status string
	var b []byte
	err = tx.QueryRowContext(ctx, "SELECT lease,status,spec FROM runs WHERE company_id=? AND id=?", company, runID).Scan(&lease, &status, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if lease == "" || lease != in.Lease || status == "ENDED" {
		return out, ErrConflict
	}
	var spec protocol.RunSpec
	if err = json.Unmarshal(b, &spec); err != nil {
		return out, err
	}
	if spec.ProjectSetup == nil {
		return out, inputError("run " + runID + " does not set up a project")
	}
	p, err := projectTx(ctx, tx, spec.Project)
	if err != nil {
		return out, err
	}
	if p.Setup == nil || p.Setup.RunID != runID {
		return out, ErrConflict // another run took the setup over
	}
	answer := func() (protocol.ProjectSetupAnswer, error) {
		a := protocol.ProjectSetupAnswer{Workspace: spec.Workspace, RepoURL: spec.RepoURL, DefaultBranch: spec.DefaultBranch, WorkspaceMode: spec.WorkspaceMode}
		m, err := runManifest(ctx, tx, spec)
		a.Materialize = m
		return a, err
	}
	if p.Setup.State == tasks.SetupReady {
		return answer()
	}
	now := tx.now().Unix()
	p.Setup.NodeID, p.Setup.UpdatedAt = in.NodeID, now
	if in.Error != "" {
		reason, _ := excerpt(in.Error, 2048)
		p.Setup.State, p.Setup.Reason = tasks.SetupFailed, reason
		if err = saveProjectBody(ctx, tx, p); err != nil {
			return out, err
		}
		note := "setting up project " + p.ID + "'s repository failed on node " + in.NodeID + ": " + reason
		if err = entityEventNote(ctx, tx, "project.setup_failed", p.ID, p.ID, p, note); err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if !filepath.IsAbs(in.Workspace) || filepath.Clean(in.Workspace) != in.Workspace {
		return out, inputError("workspace must be a clean absolute path")
	}
	if err = validRepo(in.RepoURL, in.DefaultBranch); err != nil {
		return out, err
	}
	var other string
	err = tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE company_id=? AND workspace=? AND id!=?", company, in.Workspace, p.ID).Scan(&other)
	if err == nil {
		return out, inputError("workspace " + in.Workspace + " belongs to project " + other)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	imported := 0
	for _, d := range docs {
		cur, version, found, err := specDoc(ctx, tx, company, p.ID, d.path)
		if err != nil {
			return out, err
		}
		if found && cur.SHA256 == d.blob.SHA256 {
			continue
		}
		if _, err = putSpecDoc(ctx, tx, p.ID, d.path, d.blob, version, OpenSpecChange{Actor: "project-setup", RunID: runID}); err != nil {
			return out, err
		}
		imported++
	}
	p.Workspace, p.RepoURL = in.Workspace, in.RepoURL
	if in.DefaultBranch != "" {
		p.DefaultBranch = in.DefaultBranch
	}
	p.Setup.State, p.Setup.Reason, p.Setup.OpenSpecImported, p.Setup.OpenSpecUntracked = tasks.SetupReady, "", imported, in.Untracked
	p.Version++
	p.UpdatedAt = now
	if err = saveProjectBody(ctx, tx, p); err != nil {
		return out, constraint(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE projects SET version=? WHERE company_id=? AND id=?", p.Version, company, p.ID); err != nil {
		return out, err
	}
	if err = entityEventNote(ctx, tx, "project.provisioned", p.ID, p.ID, p, provisionedNote(p, in.NodeID)); err != nil {
		return out, err
	}
	// The run continues in the repository: its spec as if it had been dispatched now.
	spec.Workspace, spec.RepoURL, spec.DefaultBranch, spec.WorkspaceMode = p.Workspace, p.RepoURL, p.DefaultBranch, ""
	if p.RepoURL != "" {
		spec.WorkspaceMode = WorkspaceMode(spec.ConfigSnapshot)
	}
	if b, err = json.Marshal(spec); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET spec=? WHERE company_id=? AND id=?", b, company, runID); err != nil {
		return out, err
	}
	if out, err = answer(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// provisionedNote is the feed summary of a finished setup.
func provisionedNote(p tasks.Project, node string) string {
	var what string
	switch p.RepoMode {
	case tasks.RepoLocal:
		what = "local git repository " + p.Workspace
	case tasks.RepoGitHub:
		what = "private GitHub repository " + p.RepoURL + " cloned to " + p.Workspace
	default:
		what = "existing repository " + p.Workspace
		if p.RepoURL != "" {
			what += " (" + p.RepoURL + ")"
		}
	}
	note := "project " + p.ID + " ready on node " + node + ": " + what
	var parts []string
	if n := p.Setup.OpenSpecImported; n > 0 {
		parts = append(parts, fmt.Sprintf("%d OpenSpec documents imported into Factory", n))
	}
	switch p.Setup.OpenSpecUntracked {
	case "pushed":
		parts = append(parts, "the repository no longer tracks the OpenSpec tree (commit pushed to the default branch)")
	case "task_branch":
		parts = append(parts, "the repository no longer tracks the OpenSpec tree (commit on the first task's branch af/"+p.Setup.TaskID+"; it reaches the default branch with that task's pull request)")
	case "local":
		parts = append(parts, "the repository no longer tracks the OpenSpec tree (local commit, not pushed)")
	}
	if len(parts) > 0 {
		note += "; " + strings.Join(parts, "; ")
	}
	return note
}
