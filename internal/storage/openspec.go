package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/openspec"
	"appfactory/internal/tasks"
)

// A project's OpenSpec tree lives in spec_documents (current version per path plus history) with contents in
// the blob store (core-owned-project-data D1, D2); Core never reads a node's files.

func validOpenSpecSetting(o *tasks.ProjectOpenSpec) error {
	if o == nil {
		return nil
	}
	if o.Dir != "" && !openspec.ValidTreeDir(o.Dir) {
		return inputError("openspec.dir must be a relative directory outside .factory and .git")
	}
	if o.ArchiveAssignee != "" && !entityName.MatchString(o.ArchiveAssignee) {
		return inputError("invalid openspec.archive_assignee")
	}
	return nil
}

type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// specDocs are the current documents of project whose path starts with prefix ("" for all), by path.
func specDocs(ctx context.Context, q rowsQuerier, company, project, prefix string) ([]openspec.Doc, error) {
	rows, err := q.QueryContext(ctx, "SELECT path,sha256,size FROM spec_documents WHERE company_id=? AND project_id=? AND current=1 AND deleted=0 AND starts_with(path,?) ORDER BY path", company, project, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []openspec.Doc{}
	for rows.Next() {
		var d openspec.Doc
		if err = rows.Scan(&d.Path, &d.SHA256, &d.Size); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// specDoc is the current document at path; found is false when it never existed or was deleted. version is
// the path's latest version (0 when it never existed).
func specDoc(ctx context.Context, q rowQuerier, company, project, path string) (d openspec.Doc, version int64, found bool, err error) {
	var deleted bool
	err = q.QueryRowContext(ctx, "SELECT sha256,size,version,deleted FROM spec_documents WHERE company_id=? AND project_id=? AND path=? AND current=1", company, project, path).Scan(&d.SHA256, &d.Size, &version, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return d, 0, false, nil
	}
	d.Path = path
	if deleted {
		d.SHA256, d.Size = "", 0
	}
	return d, version, err == nil && !deleted, err
}

// openSpecProject is the project when its OpenSpec setting is enabled.
func openSpecProject(p tasks.Project, err error) (tasks.Project, error) {
	if err != nil {
		return p, err
	}
	if !p.OpenSpecEnabled() {
		return p, inputError("OpenSpec is disabled for project " + p.ID + " (openspec.enabled)")
	}
	return p, nil
}

// ListOpenSpec lists one directory of the project's tree.
func (s *Store) ListOpenSpec(ctx context.Context, company, project, dir, after string, limit int) (openspec.Page, error) {
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return openspec.Page{Items: []openspec.Entry{}}, err
	}
	d, err := openspec.ValidDir(dir)
	if err != nil {
		return openspec.Page{Items: []openspec.Entry{}}, err
	}
	prefix := d + "/"
	if d == "." {
		prefix = ""
	}
	docs, err := specDocs(ctx, s.DB, company, project, prefix)
	if err != nil {
		return openspec.Page{Items: []openspec.Entry{}}, err
	}
	return openspec.List(docs, d, after, limit)
}

// ReadOpenSpec pages the current version of a document; a continuation whose digest is no longer current
// conflicts.
func (s *Store) ReadOpenSpec(ctx context.Context, company, project, name string, offset, limit int, digest string) (openspec.Document, error) {
	out := openspec.Document{Path: name, Offset: offset}
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return out, err
	}
	if err := openspec.ValidRead(name, offset, limit, digest); err != nil {
		return out, err
	}
	d, _, found, err := specDoc(ctx, s.DB, company, project, name)
	if err != nil {
		return out, err
	}
	if !found {
		return out, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if digest != "" && digest != d.SHA256 {
		return out, openspec.ErrConflict
	}
	b, err := s.blobBytes(ctx, company, d.SHA256, openspec.MaxDocument)
	if err != nil {
		return out, err
	}
	return openspec.Read(name, b, offset, limit, digest)
}

// CheckOpenSpec checks an active change reference of the project.
func (s *Store) CheckOpenSpec(ctx context.Context, company, project, change string) (openspec.Check, error) {
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return openspec.Check{Change: change}, err
	}
	return s.checkChange(ctx, s.DB, company, project, change)
}

func (s *Store) checkChange(ctx context.Context, q rowQuerier, company, project, change string) (openspec.Check, error) {
	if !openspec.ValidChange(change) {
		return openspec.CheckChange(change, nil, false)
	}
	d, _, found, err := specDoc(ctx, q, company, project, openspec.ProposalPath(change))
	if err != nil {
		return openspec.Check{Change: change}, err
	}
	var b []byte
	if found {
		if b, err = s.blobBytes(ctx, company, d.SHA256, openspec.MaxDocument); err != nil {
			return openspec.Check{Change: change}, err
		}
	}
	return openspec.CheckChange(change, b, found)
}

// checkTaskChange validates a task's openspec_change against the project's tree (create, update, ready,
// dispatch).
func (s *Store) checkTaskChange(ctx context.Context, tx *Tx, t tasks.Task) error {
	if t.OpenSpecChange == "" {
		return nil
	}
	if _, err := openSpecProject(projectTx(ctx, tx, t.ProjectID)); err != nil {
		return err
	}
	if _, e := s.checkChange(ctx, tx, tx.company, t.ProjectID, t.OpenSpecChange); e != nil {
		return fmt.Errorf("%w: openspec: %v", ErrInput, e)
	}
	return nil
}

// runChangeCheck rechecks a run's change at claim; a disabled OpenSpec setting fails it too.
func (s *Store) runChangeCheck(ctx context.Context, tx *Tx, project, change string) error {
	if _, err := openSpecProject(projectTx(ctx, tx, project)); err != nil {
		return err
	}
	_, err := s.checkChange(ctx, tx, tx.company, project, change)
	return err
}

// OpenSpecManifest lists every current document of the project's tree (import, owner inspection).
func (s *Store) OpenSpecManifest(ctx context.Context, company, project string) ([]openspec.Doc, error) {
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return nil, err
	}
	return specDocs(ctx, s.DB, company, project, "")
}

// InitOpenSpec answers the tree's fixed directories. The tree lives in Core storage, so nothing is created:
// every node materializes the directories, and the official OpenSpec CLI adds documents there. The owner
// or an active run of the project may call it; the event records directory metadata only.
func (s *Store) InitOpenSpec(ctx context.Context, company, project, actor, runID, key string) (openspec.Initialized, error) {
	input := struct{ Project, Actor, RunID string }{project, actor, runID}
	return mutate(ctx, s, company, "openspec.init:"+project+":"+actor+":"+runID, key, input, func(tx *Tx) (openspec.Initialized, error) {
		out := openspec.Initialized{}
		if key == "" {
			return out, inputError("request_id required")
		}
		p, e := openSpecProject(projectTx(ctx, tx, project))
		if e != nil {
			return out, e
		}
		if runID != "" {
			if e = activeProjectRun(ctx, tx, s, project, actor, runID); e != nil {
				return out, e
			}
		} else if actor != "owner" {
			return out, inputError("owner or active run required")
		}
		out.ProjectID = project
		for _, d := range append([]string{""}, openspec.Directories...) {
			out.Directories = append(out.Directories, strings.TrimSuffix(p.OpenSpecDir()+"/"+d, "/"))
		}
		return out, entityEvent(ctx, tx, "openspec.initialized", project, project, out)
	})
}

// activeProjectRun: runID is a claimed or running run of agent in project with a live lease.
func activeProjectRun(ctx context.Context, tx *Tx, s *Store, project, agent, runID string) error {
	var valid int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE company_id=? AND id=? AND project=? AND agent=? AND status IN ('CLAIMED','RUNNING') AND lease_until>=?", tx.company, runID, project, agent, s.leaseCutoff()).Scan(&valid); err != nil {
		return err
	}
	if valid != 1 {
		return ErrConflict
	}
	return nil
}

// OpenSpecChange is a write, move or delete of the project's tree with compare-and-set on digests (D4).
// Base is the digest the caller's copy started from: "" means the document must not exist. Actor and RunID
// say who changed it (an active run of the project, or the owner).
type OpenSpecChange struct {
	Path    string
	From    string
	Base    string
	Content []byte
	Actor   string
	RunID   string
}

// WriteOpenSpec stores a document version when the current digest equals Base. A write of content that
// is already current succeeds unchanged, so a retried write never conflicts with itself.
func (s *Store) WriteOpenSpec(ctx context.Context, company, project string, in OpenSpecChange) (openspec.Written, error) {
	out := openspec.Written{Path: in.Path}
	if !openspec.ValidPath(in.Path) || in.Base != "" && len(in.Base) != 64 {
		return out, inputError("valid OpenSpec path and empty or 64-hex base_sha256 required")
	}
	if err := openspec.ValidContent(in.Content); err != nil {
		return out, inputError(err.Error())
	}
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return out, err
	}
	blob, err := s.PutBlob(ctx, company, bytes.NewReader(in.Content), openspec.MaxDocument)
	if err != nil {
		return out, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.changeAuthority(ctx, tx, project, in); err != nil {
		return out, err
	}
	cur, version, found, err := specDoc(ctx, tx, company, project, in.Path)
	if err != nil {
		return out, err
	}
	if found && cur.SHA256 == blob.SHA256 {
		return openspec.Written{Path: in.Path, SHA256: cur.SHA256, Size: cur.Size, Version: version, Unchanged: true}, nil
	}
	if cur.SHA256 != in.Base {
		return out, &openspec.ConflictError{Path: in.Path, Current: cur.SHA256}
	}
	if out, err = putSpecDoc(ctx, tx, project, in.Path, blob, version, in); err != nil {
		return out, err
	}
	if err = entityEvent(ctx, tx, "openspec.written", project, in.Path, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// DeleteOpenSpec deletes a document whose current digest equals Base; deleting a missing document succeeds
// unchanged.
func (s *Store) DeleteOpenSpec(ctx context.Context, company, project string, in OpenSpecChange) (openspec.Written, error) {
	out := openspec.Written{Path: in.Path, Deleted: true}
	if !openspec.ValidPath(in.Path) || len(in.Base) != 64 {
		return out, inputError("valid OpenSpec path and base_sha256 required")
	}
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return out, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.changeAuthority(ctx, tx, project, in); err != nil {
		return out, err
	}
	cur, version, found, err := specDoc(ctx, tx, company, project, in.Path)
	if err != nil {
		return out, err
	}
	if !found {
		out.Version, out.Unchanged = version, true
		return out, nil
	}
	if cur.SHA256 != in.Base {
		return out, &openspec.ConflictError{Path: in.Path, Current: cur.SHA256}
	}
	if out, err = putSpecDoc(ctx, tx, project, in.Path, Blob{}, version, in); err != nil {
		return out, err
	}
	if err = entityEvent(ctx, tx, "openspec.deleted", project, in.Path, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// MoveOpenSpec moves a document (Base is its current digest) or a whole directory such as an archived change
// (Base empty; every document below From moves) to a path that holds no documents yet.
func (s *Store) MoveOpenSpec(ctx context.Context, company, project string, in OpenSpecChange) ([]openspec.Written, error) {
	file := openspec.ValidPath(in.From)
	if in.From == in.Path || !file && !openspec.ValidSubdir(in.From) || file && (!openspec.ValidPath(in.Path) || len(in.Base) != 64) || !file && (in.Base != "" || !openspec.ValidSubdir(in.Path)) {
		return nil, inputError("move a document (to a valid path, with base_sha256) or a directory below changes/ or specs/ (without base_sha256)")
	}
	if _, err := openSpecProject(s.Project(ctx, company, project)); err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.changeAuthority(ctx, tx, project, in); err != nil {
		return nil, err
	}
	var moves []openspec.Doc // sources
	if file {
		cur, _, found, err := specDoc(ctx, tx, company, project, in.From)
		if err != nil {
			return nil, err
		}
		if cur.SHA256 != in.Base || !found {
			return nil, &openspec.ConflictError{Path: in.From, Current: cur.SHA256}
		}
		moves = []openspec.Doc{cur}
	} else if moves, err = specDocs(ctx, tx, company, project, in.From+"/"); err != nil {
		return nil, err
	}
	if len(moves) == 0 {
		return nil, fmt.Errorf("%w: nothing at %s", ErrNotFound, in.From)
	}
	var out []openspec.Written
	for _, d := range moves {
		to := in.Path + strings.TrimPrefix(d.Path, in.From)
		if !openspec.ValidPath(to) {
			return nil, inputError("invalid target " + to)
		}
		target, version, found, err := specDoc(ctx, tx, company, project, to)
		if err != nil {
			return nil, err
		}
		if found {
			return nil, &openspec.ConflictError{Path: to, Current: target.SHA256}
		}
		w, err := putSpecDoc(ctx, tx, project, to, Blob{SHA256: d.SHA256, Size: d.Size}, version, in)
		if err != nil {
			return nil, err
		}
		_, version, _, err = specDoc(ctx, tx, company, project, d.Path)
		if err != nil {
			return nil, err
		}
		if _, err = putSpecDoc(ctx, tx, project, d.Path, Blob{}, version, in); err != nil {
			return nil, err
		}
		w.From = d.Path
		out = append(out, w)
	}
	if err = entityEvent(ctx, tx, "openspec.moved", project, in.From, map[string]any{"from": in.From, "to": in.Path, "documents": out}); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// changeAuthority: the owner, or an active run of the project (whose credential the gateway checked).
func (s *Store) changeAuthority(ctx context.Context, tx *Tx, project string, in OpenSpecChange) error {
	if in.RunID == "" {
		if in.Actor != "owner" {
			return inputError("owner or active run required")
		}
		return nil
	}
	return activeProjectRun(ctx, tx, s, project, in.Actor, in.RunID)
}

// putSpecDoc adds version+1 of path (a deletion when blob has no digest) and makes it current.
func putSpecDoc(ctx context.Context, tx *Tx, project, path string, blob Blob, version int64, in OpenSpecChange) (openspec.Written, error) {
	out := openspec.Written{Path: path, SHA256: blob.SHA256, Size: blob.Size, Version: version + 1, Deleted: blob.SHA256 == ""}
	if _, err := tx.ExecContext(ctx, "UPDATE spec_documents SET current=0 WHERE company_id=? AND project_id=? AND path=? AND current=1", tx.company, project, path); err != nil {
		return out, err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO spec_documents(company_id,project_id,path,version,sha256,size,deleted,current,updated_by,run_id,updated_at) VALUES(?,?,?,?,?,?,?,1,?,?,?)",
		tx.company, project, path, out.Version, blob.SHA256, blob.Size, out.Deleted, in.Actor, in.RunID, tx.now().Unix())
	return out, err
}

func blockOpenSpecRun(ctx context.Context, tx *Tx, id, taskID, reason string) error {
	if _, e := tx.ExecContext(ctx, "UPDATE runs SET status='ENDED',outcome='stale',result=?,finished_at=? WHERE id=? AND status='QUEUED'", reason, tx.now().Unix(), id); e != nil {
		return e
	}
	if e := autoRuntimeReport(ctx, tx, id); e != nil {
		return e
	}
	if taskID == "" {
		return nil
	}
	t, e := taskTx(ctx, tx, taskID)
	if e != nil {
		return e
	}
	if t.CurrentRunID == id && t.Status == "IN_PROGRESS" {
		t.Status = "BLOCKED"
		t.BlockReason = "openspec: " + reason
		t.Submission = nil
		if e = saveTask(ctx, tx, &t, "task.openspec_waiting"); e != nil {
			return e
		}
		return addPark(ctx, tx, t, tasks.ParkBlock, "openspec", t.BlockReason, true)
	}
	return nil
}
