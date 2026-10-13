package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed migration2.sql
var migration2 string

//go:embed migration3.sql
var migration3 string

//go:embed migration4.sql
var migration4 string

//go:embed migration5.sql
var migration5 string

//go:embed migration6.sql
var migration6 string

//go:embed migration7.sql
var migration7 string
var ErrInput = errors.New("invalid input")
var ErrInvalid = errors.New("invalid event")
var ErrConflict = errors.New("state or version conflict")
var ErrNotFound = errors.New("not found")

type Store struct {
	DB       *sql.DB
	OpenSpec openspec.Access
}
type Run struct {
	StartedAt  int64            `json:"started_at"`
	FinishedAt int64            `json:"finished_at"`
	Spec       protocol.RunSpec `json:"spec"`
	Status     string           `json:"status"`
	SessionID  string           `json:"session_id"`
	Result     string           `json:"result"`
}
type Outbox struct {
	ID, Subject string
	Body        []byte
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 7 {
		db.Close()
		return nil, fmt.Errorf("unsupported database schema %d", version)
	}
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if version == 0 {
		_, err = tx.Exec(schema)
	}
	if err == nil && version < 2 {
		_, err = tx.Exec(migration2)
	}
	if err == nil && version < 3 {
		_, err = tx.Exec(migration3)
	}
	if err == nil && version < 4 {
		_, err = tx.Exec(migration4)
	}
	if err == nil && version < 5 {
		_, err = tx.Exec(migration5)
	}
	if err == nil && version < 6 {
		_, err = tx.Exec(migration6)
	}
	if err == nil && version < 7 {
		_, err = tx.Exec(migration7)
	}
	if err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
func (s *Store) Create(ctx context.Context, r protocol.RunSpec) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertRun(ctx, tx, &r); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	var r Run
	var b []byte
	err := s.DB.QueryRowContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at FROM runs WHERE id=?", id).Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r.Spec)
	return r, err
}
func (s *Store) List(ctx context.Context) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at FROM runs ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs := []Run{}
	for rows.Next() {
		var r Run
		var b []byte
		if err = rows.Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r.Spec); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, rows.Err()
}
func (s *Store) Claim(ctx context.Context, id string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", id)
	if err != nil {
		return "", err
	}
	if spec.OpenSpecChange != "" {
		var status string
		if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", id).Scan(&status); err != nil {
			return "", err
		}
		if status != "QUEUED" {
			return "", ErrConflict
		}
		if _, e := s.OpenSpec.CheckChange(spec.Workspace, spec.OpenSpecChange); e != nil {
			if err = blockOpenSpecRun(ctx, tx, id, spec.TaskID, e.Error()); err != nil {
				return "", err
			}
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return "", ErrConflict
		}
	}
	valid, err := approvedRunArtifacts(ctx, tx, spec)
	if err != nil {
		return "", err
	}
	if !valid {
		if err = reconcileArtifactTasks(ctx, tx); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "", ErrConflict
	}
	token := protocol.ID()
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status='RUNNING',lease=?,lease_until=?,started_at=strftime('%s','now') WHERE id=? AND status='QUEUED'", token, time.Now().Add(45*time.Second).Unix(), id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return "", ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) Heartbeat(ctx context.Context, id, token string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, "UPDATE runs SET lease_until=? WHERE id=? AND lease=? AND status IN ('RUNNING','CANCELLING') AND lease_until>=?", time.Now().Add(45*time.Second).Unix(), id, token, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false, ErrConflict
	}
	r, err := s.Get(ctx, id)
	return r.Status == "CANCELLING", err
}
func (s *Store) Pending(ctx context.Context) ([]Outbox, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,subject,body FROM outbox WHERE sent=0 ORDER BY rowid LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		var o Outbox
		if err = rows.Scan(&o.ID, &o.Subject, &o.Body); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) Sent(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE outbox SET sent=1 WHERE id=?", id)
	return err
}
func (s *Store) Apply(ctx context.Context, e protocol.Envelope) error {
	if e.Version != 1 || e.ID == "" {
		return fmt.Errorf("%w: unsupported envelope", ErrInvalid)
	}
	var p protocol.Event
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if p.RunID == "" || p.RunID != e.CorrelationID {
		return fmt.Errorf("%w: correlation mismatch", ErrInvalid)
	}
	if p.Status != "" && p.Status != "COMPLETED" && p.Status != "FAILED" && p.Status != "CANCELLED" && p.Status != "UNKNOWN" {
		return fmt.Errorf("%w: terminal status", ErrInvalid)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE id=?", e.ID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	var token, status string
	if err = tx.QueryRowContext(ctx, "SELECT lease,status FROM runs WHERE id=?", p.RunID).Scan(&token, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: unknown run", ErrInvalid)
		}
		return err
	}
	if token == "" || token != p.Lease {
		return ErrConflict
	}
	p.Data = openspec.AuditData(p.Data)
	e.Payload, err = json.Marshal(p)
	if err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(id,run_id,body) VALUES(?,?,?)", e.ID, p.RunID, body); err != nil {
		return err
	}
	if status == "RUNNING" || status == "CANCELLING" || status == "UNKNOWN" {
		// Late terminal evidence can resolve a lost lease; late progress never revives it.
		if p.Status != "" {
			if p.Status != "COMPLETED" && p.Status != "FAILED" && p.Status != "CANCELLED" && p.Status != "UNKNOWN" {
				return fmt.Errorf("invalid terminal status")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,result=?,finished_at=? WHERE id=?", p.Status, p.Result, time.Now().Unix(), p.RunID); err != nil {
				return err
			}
		}
	}
	if p.SessionID != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET session_id=? WHERE id=?", p.SessionID, p.RunID); err != nil {
			return err
		}
	}
	if err = syncTaskRun(ctx, tx, p.RunID, true); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Events(ctx context.Context, id string) ([]protocol.Envelope, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT body FROM events WHERE run_id=? ORDER BY seq", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Envelope{}
	for rows.Next() {
		var b []byte
		var e protocol.Envelope
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func insertRun(ctx context.Context, tx *sql.Tx, r *protocol.RunSpec) error {
	if err := enrichRunDecisions(ctx, tx, r); err != nil {
		return err
	}
	if err := checkSessionProvider(ctx, tx, r); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > 900*1024 {
		return inputError("run context exceeds broker message budget")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,session_id,task_id) VALUES(?,?,?,?,?,'QUEUED',?,?,?)", r.ID, r.Project, r.Agent, r.Workspace, b, time.Now().Unix(), r.SessionID, nullable(r.TaskID)); err != nil {
		return fmt.Errorf("project already active or insert failed: %w", err)
	}
	e := protocol.Wrap("agent.run.requested", r.ID, "", r)
	b, _ = json.Marshal(e)
	if _, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body) VALUES(?,?,?)", e.ID, "factory.commands.agent.run."+string(r.Provider), b); err != nil {
		return err
	}
	return nil
}

// A session ID is opaque to Core but owned by the runtime that created it: Claude cannot
// resume a Codex thread. Explicit direct resumes are rejected; Core-scheduled task and
// conversation continuations start a fresh session after the agent's provider changed.
func checkSessionProvider(ctx context.Context, tx *sql.Tx, r *protocol.RunSpec) error {
	if r.SessionID == "" {
		return nil
	}
	var b []byte
	err := tx.QueryRowContext(ctx, "SELECT spec FROM runs WHERE session_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1", r.SessionID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var prior protocol.RunSpec
	if err = json.Unmarshal(b, &prior); err != nil || prior.Provider == r.Provider {
		return err
	}
	if r.TaskID == "" && r.MessageID == "" {
		return inputError("session belongs to " + string(prior.Provider) + " runtime, agent uses " + string(r.Provider))
	}
	r.SessionID = ""
	return nil
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
