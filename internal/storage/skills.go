package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/skills"
)

// Skill registry (skill-registry D2–D4): sources per scope (a company, or "factory" for the built-in skills), the
// skills indexed from them with their content in the scope's blobs, and node sync jobs of private sources.

// SkillSource is a source of skills.
type SkillSource struct {
	Scope    string `json:"-"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Subpath  string `json:"subpath,omitempty"`
	Access   string `json:"access"`
	Update   string `json:"update"`
	Commit   string `json:"commit,omitempty"`
	SyncedAt int64  `json:"synced_at"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Skills   int    `json:"skills"`
}

// IndexedSkill is one skill of a source's index (a version: its hash), or a discovered skill that failed (Error).
type IndexedSkill struct {
	Scope       string             `json:"-"`
	Source      string             `json:"source"`
	Name        string             `json:"name"`
	Hash        string             `json:"hash,omitempty"`
	Commit      string             `json:"commit,omitempty"`
	Blob        string             `json:"-"`
	Description string             `json:"description,omitempty"`
	Categories  []string           `json:"categories"`
	Files       []skills.SkillFile `json:"files,omitempty"`
	Frontmatter skills.Meta        `json:"-"`
	Error       string             `json:"error,omitempty"`
	Current     bool               `json:"current"`
}

// Skill source kinds and states.
const (
	SkillSourceBuiltin = "builtin"
	SkillSourceGit     = "git"
	SkillSourceUpload  = "upload"
)

// SaveSkillSource creates or updates a source (its settings; sync state is written by IndexSkills).
func (s *Store) SaveSkillSource(ctx context.Context, src SkillSource) error {
	if src.Access == "" {
		src.Access = "public"
	}
	if src.Update == "" {
		src.Update = "pinned"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO skill_sources(scope,id,kind,url,ref,subpath,access,update_mode,created_at) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT (scope,id) DO UPDATE SET kind=excluded.kind,url=excluded.url,ref=excluded.ref,subpath=excluded.subpath,access=excluded.access,update_mode=excluded.update_mode`,
		src.Scope, src.ID, src.Kind, src.URL, src.Ref, src.Subpath, src.Access, src.Update, s.Now().Unix())
	return err
}

// SetSkillSourceStatus records a source's sync state (syncing, waiting_for_node, failed with its error).
func (s *Store) SetSkillSourceStatus(ctx context.Context, scope, id, status, why string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE skill_sources SET status=?,error=? WHERE scope=? AND id=?", status, why, scope, id)
	return err
}

// SkillSources lists a scope's sources with their indexed skill counts.
func (s *Store) SkillSources(ctx context.Context, scope string) ([]SkillSource, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id,s.kind,s.url,s.ref,s.subpath,s.access,s.update_mode,s.commit_id,s.synced_at,s.status,s.error,
 (SELECT count(*) FROM skill_index i WHERE i.scope=s.scope AND i.source_id=s.id AND i.current AND i.error='')
 FROM skill_sources s WHERE s.scope=? ORDER BY s.id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillSource{}
	for rows.Next() {
		v := SkillSource{Scope: scope}
		if err = rows.Scan(&v.ID, &v.Kind, &v.URL, &v.Ref, &v.Subpath, &v.Access, &v.Update, &v.Commit, &v.SyncedAt, &v.Status, &v.Error, &v.Skills); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SkillSource reads one source.
func (s *Store) SkillSource(ctx context.Context, scope, id string) (SkillSource, error) {
	all, err := s.SkillSources(ctx, scope)
	if err != nil {
		return SkillSource{}, err
	}
	for _, v := range all {
		if v.ID == id {
			return v, nil
		}
	}
	return SkillSource{}, ErrNotFound
}

// DeleteSkillSource removes a source and its index.
func (s *Store) DeleteSkillSource(ctx context.Context, scope, id string) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM skill_sources WHERE scope=? AND id=?", scope, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// IndexSkills records a source's index at commit (D4): each valid skill's content goes to the scope's blobs; the
// rows become the source's current index (earlier versions stay, not current, so pinned hashes keep their content).
// It answers the hashes that changed per skill name (old → new) for update handling.
func (s *Store) IndexSkills(ctx context.Context, scope, source, commit string, idx []skills.Indexed) (map[string][2]string, error) {
	if s.Blobs == nil {
		return nil, errors.New("no blob store")
	}
	before, err := s.currentHashes(ctx, scope, source)
	if err != nil {
		return nil, err
	}
	blobs := map[string]string{}
	for _, x := range idx {
		if x.Error != "" {
			continue
		}
		sha, _, err := s.Blobs.Put(ctx, scope, bytes.NewReader(x.Tar))
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", x.Name, err)
		}
		blobs[x.Name] = sha
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.Now().Unix()
	if _, err = tx.ExecContext(ctx, "UPDATE skill_index SET current=false WHERE scope=$1 AND source_id=$2", scope, source); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM skill_index WHERE scope=$1 AND source_id=$2 AND hash=''", scope, source); err != nil {
		return nil, err
	}
	for _, x := range idx {
		cats, _ := json.Marshal(nonNil(x.Meta.Categories()))
		fm, _ := json.Marshal(x.Meta)
		files, _ := json.Marshal(x.Files)
		if files == nil || string(files) == "null" {
			files = []byte("[]")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO skill_index(scope,source_id,name,hash,commit_id,blob,description,categories,frontmatter,files,error,current,indexed_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,$12)
 ON CONFLICT (scope,source_id,name,hash) DO UPDATE SET commit_id=excluded.commit_id,blob=excluded.blob,description=excluded.description,categories=excluded.categories,
 frontmatter=excluded.frontmatter,files=excluded.files,error=excluded.error,current=true,indexed_at=excluded.indexed_at`,
			scope, source, x.Name, x.Hash, commit, blobs[x.Name], x.Meta.Description, string(cats), string(fm), string(files), x.Error, now); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE skill_sources SET commit_id=$1,synced_at=$2,status='ok',error='' WHERE scope=$3 AND id=$4", commit, now, scope, source); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	changed := map[string][2]string{}
	for _, x := range idx {
		if x.Error == "" && before[x.Name] != x.Hash {
			changed[x.Name] = [2]string{before[x.Name], x.Hash}
		}
	}
	return changed, nil
}

func (s *Store) currentHashes(ctx context.Context, scope, source string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT name,hash FROM skill_index WHERE scope=? AND source_id=? AND current AND hash!=''", scope, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, h string
		if err = rows.Scan(&n, &h); err != nil {
			return nil, err
		}
		out[n] = h
	}
	return out, rows.Err()
}

const skillColumns = "scope,source_id,name,hash,commit_id,blob,description,categories,frontmatter,files,error,current"

func scanSkill(sc interface{ Scan(...any) error }) (IndexedSkill, error) {
	var v IndexedSkill
	var cats, fm, files []byte
	err := sc.Scan(&v.Scope, &v.Source, &v.Name, &v.Hash, &v.Commit, &v.Blob, &v.Description, &cats, &fm, &files, &v.Error, &v.Current)
	if err != nil {
		return v, err
	}
	_ = json.Unmarshal(cats, &v.Categories)
	_ = json.Unmarshal(fm, &v.Frontmatter)
	_ = json.Unmarshal(files, &v.Files)
	if v.Categories == nil {
		v.Categories = []string{}
	}
	return v, nil
}

// SkillVersion finds a skill of scope's source: the version with hash, or the current one when hash is "".
func (s *Store) SkillVersion(ctx context.Context, scope, source, name, hash string) (IndexedSkill, error) {
	q := "SELECT " + skillColumns + " FROM skill_index WHERE scope=? AND source_id=? AND name=? AND error='' AND (hash=? OR ?='' AND current) ORDER BY current DESC LIMIT 1"
	v, err := scanSkill(s.DB.QueryRowContext(ctx, q, scope, source, name, hash, hash))
	if errors.Is(err, sql.ErrNoRows) {
		if hash == "" {
			return v, fmt.Errorf("%w: no skill %s in source %s", ErrNotFound, name, source)
		}
		return v, fmt.Errorf("%w: no skill %s with hash %s in source %s", ErrNotFound, name, hash, source)
	}
	return v, err
}

// SkillByHash finds the content of hash in any of scopes (a company's own, then the built-in).
func (s *Store) SkillByHash(ctx context.Context, scopes []string, hash string) (IndexedSkill, error) {
	for _, scope := range scopes {
		v, err := scanSkill(s.DB.QueryRowContext(ctx, "SELECT "+skillColumns+" FROM skill_index WHERE scope=? AND hash=? AND blob!='' LIMIT 1", scope, hash))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
	}
	return IndexedSkill{}, ErrNotFound
}

// SkillContent opens the packed content of an indexed skill.
func (s *Store) SkillContent(ctx context.Context, v IndexedSkill) (io.ReadCloser, error) {
	if v.Blob == "" {
		return nil, ErrNotFound
	}
	return s.Blobs.Get(ctx, v.Scope, v.Blob)
}

// SkillLibrary lists the current skills of scopes (filtered by source, a substring of name, description or
// categories, and a category), errors included.
func (s *Store) SkillLibrary(ctx context.Context, scopes []string, source, q, category string) ([]IndexedSkill, error) {
	out := []IndexedSkill{}
	for _, scope := range scopes {
		rows, err := s.DB.QueryContext(ctx, "SELECT "+skillColumns+" FROM skill_index WHERE scope=? AND current AND (?='' OR source_id=?) ORDER BY name,source_id", scope, source, source)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v, err := scanSkill(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			text := strings.ToLower(v.Name + " " + v.Description + " " + strings.Join(v.Categories, " "))
			if q != "" && !strings.Contains(text, strings.ToLower(q)) || category != "" && !has(v.Categories, category) {
				continue
			}
			out = append(out, v)
		}
		rows.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// SyncJob is a private source's sync a node of the company performs (D3): it fetches with its own git access and
// uploads the tree.
type SyncJob struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	URL    string `json:"url"`
	Ref    string `json:"ref,omitempty"`
	Node   string `json:"-"`
	Status string `json:"-"`
}

// CreateSyncJob asks an online node of company to sync source: ErrNotFound when none is online (the source waits).
func (s *Store) CreateSyncJob(ctx context.Context, company, source string) (SyncJob, error) {
	var job SyncJob
	var node string
	err := s.DB.QueryRowContext(ctx, "SELECT id FROM nodes WHERE company_id=? AND online=1 ORDER BY last_seen DESC LIMIT 1", company).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return job, ErrNotFound
	}
	if err != nil {
		return job, err
	}
	job = SyncJob{ID: protocol.ID(), Source: source, Node: node, Status: "pending"}
	now := s.Now().Unix()
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO skill_sync_jobs(company_id,id,source_id,node_id,status,created_at,updated_at) VALUES(?,?,?,?,'pending',?,?)", company, job.ID, source, node, now, now); err != nil {
		return job, err
	}
	if s.Wake != nil {
		s.Wake(company) // the node's waiting poll answers the job
	}
	return job, nil
}

// NodeSyncJobs are the pending sync jobs of node with their source's URL and ref.
func (s *Store) NodeSyncJobs(ctx context.Context, company, node string) ([]SyncJob, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT j.id,j.source_id,s.url,s.ref FROM skill_sync_jobs j JOIN skill_sources s ON s.scope=j.company_id AND s.id=j.source_id
 WHERE j.company_id=? AND j.node_id=? AND j.status='pending' ORDER BY j.created_at`, company, node)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncJob{}
	for rows.Next() {
		var j SyncJob
		if err = rows.Scan(&j.ID, &j.Source, &j.URL, &j.Ref); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SyncJobOf reads node's job; another node's or an unknown job is ErrNotFound.
func (s *Store) SyncJobOf(ctx context.Context, company, node, id string) (SyncJob, error) {
	var j SyncJob
	err := s.DB.QueryRowContext(ctx, "SELECT id,source_id,node_id,status FROM skill_sync_jobs WHERE company_id=? AND id=? AND node_id=?", company, id, node).Scan(&j.ID, &j.Source, &j.Node, &j.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// FinishSyncJob records a job's end.
func (s *Store) FinishSyncJob(ctx context.Context, company, id, status, why string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE skill_sync_jobs SET status=?,error=?,updated_at=? WHERE company_id=? AND id=?", status, why, s.Now().Unix(), company, id)
	return err
}
