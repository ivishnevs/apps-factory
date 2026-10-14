package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"

	_ "modernc.org/sqlite"
)

var ErrInput = errors.New("invalid input")
var ErrInvalid = errors.New("invalid event")
var ErrConflict = errors.New("state or version conflict")
var ErrNotFound = errors.New("not found")

// DefaultCompany owns every row created before tenancy and all work of a single-company Core.
const DefaultCompany = "default"

// LeaseTTL is how long a run lease lives without a heartbeat.
const LeaseTTL = 45 * time.Second

type Store struct {
	DB       *sql.DB
	OpenSpec openspec.Access
	// Clock is the source of every timestamp Store writes or compares; nil means wall time.
	Clock clock.Clock
	// Continuity, when set by Core's tick, defers lease expiry until one TTL of continuous
	// ticking has passed, so a host sleep does not expire every lease at once.
	Continuity *clock.Continuity
}

func (s *Store) Now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// leaseCutoff is the lease_until below which a lease counts as expired: now, or 0 while the
// current continuity window is younger than one TTL (every lease still gets its full TTL).
func (s *Store) leaseCutoff() int64 {
	now := s.Now()
	if s.Continuity != nil {
		start := s.Continuity.Start()
		if start.IsZero() || !start.Add(LeaseTTL).Before(now) {
			return 0
		}
	}
	return now.Unix()
}

// LeaseLive reports whether a lease ending at until is still valid for Core.
func (s *Store) LeaseLive(until int64) bool { return until >= s.leaseCutoff() }

type Run struct {
	StartedAt  int64            `json:"started_at"`
	FinishedAt int64            `json:"finished_at"`
	Spec       protocol.RunSpec `json:"spec"`
	Status     string           `json:"status"`
	SessionID  string           `json:"session_id"`
	Result     string           `json:"result"`
	// Usage is the last cumulative usage the worker reported (runtime-drivers 2.5).
	Usage UsageList `json:"usage,omitempty"`
}

// UsageList is runs.usage: JSON of the last reported Event.usage, empty when none.
type UsageList []protocol.Usage

func (u *UsageList) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	case nil:
	default:
		return fmt.Errorf("usage column: %T", src)
	}
	*u = nil
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, u)
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
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err = s.checkIncarnation(context.Background(), path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Tx is a transaction bound to one company. Helpers write tx.company into every row they
// insert and scope every lookup by a caller-supplied ID with it.
type Tx struct {
	*sql.Tx
	company string
	store   *Store
}

func (tx *Tx) now() time.Time { return tx.store.Now() }

func (s *Store) begin(ctx context.Context, company string) (*Tx, error) {
	if company == "" {
		return nil, inputError("company required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, company: company, store: s}, nil
}

// CreateCompany registers a tenant. The default company exists in every database.
func (s *Store) CreateCompany(ctx context.Context, id, name string) error {
	if !entityName.MatchString(id) || strings.TrimSpace(name) == "" {
		return inputError("company requires valid id and name")
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO companies VALUES(?,?,?)", id, name, s.Now().Unix())
	return constraint(err)
}
func (s *Store) Create(ctx context.Context, company string, r protocol.RunSpec) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertRun(ctx, tx, &r); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Get(ctx context.Context, company, id string) (Run, error) {
	var r Run
	var b []byte
	err := s.DB.QueryRowContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at,usage FROM runs WHERE company_id=? AND id=?", company, id).Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt, &r.Usage)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r.Spec)
	return r, err
}
func (s *Store) List(ctx context.Context, company string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at,usage FROM runs WHERE company_id=? ORDER BY created_at,id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs := []Run{}
	for rows.Next() {
		var r Run
		var b []byte
		if err = rows.Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt, &r.Usage); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r.Spec); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, rows.Err()
}
func (s *Store) Claim(ctx context.Context, company, id string) (string, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, id)
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
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status='RUNNING',lease=?,lease_until=?,started_at=?,brief_from=coalesce((SELECT handled_seq FROM tasks WHERE id=runs.task_id),0),brief_to=coalesce((SELECT wake_seq FROM tasks WHERE id=runs.task_id),0) WHERE id=? AND status='QUEUED'", token, s.Now().Add(LeaseTTL).Unix(), s.Now().Unix(), id)
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

func (s *Store) Heartbeat(ctx context.Context, company, id, token string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, "UPDATE runs SET lease_until=? WHERE company_id=? AND id=? AND lease=? AND status IN ('RUNNING','CANCELLING') AND protocol=1 AND lease_until>=?", s.Now().Add(LeaseTTL).Unix(), company, id, token, s.leaseCutoff())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false, ErrConflict
	}
	r, err := s.Get(ctx, company, id)
	return r.Status == "CANCELLING", err
}

// Pending and Sent drive the outbox publisher, which serves every company.
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
	_, err := s.DB.ExecContext(ctx, "UPDATE outbox SET sent=1,sent_at=? WHERE id=?", s.Now().Unix(), id)
	return err
}
func (s *Store) Apply(ctx context.Context, company string, e protocol.Envelope) error {
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
	tx, err := s.begin(ctx, company)
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
	if err = tx.QueryRowContext(ctx, "SELECT lease,status FROM runs WHERE company_id=? AND id=?", company, p.RunID).Scan(&token, &status); err != nil {
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
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(id,run_id,body,company_id) VALUES(?,?,?,?)", e.ID, p.RunID, body, company); err != nil {
		return err
	}
	if p.Status != "" {
		// The node's inventory heartbeat drops an ended run once Core applied its terminal event.
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET end_applied=1 WHERE id=?", p.RunID); err != nil {
			return err
		}
	}
	if status == "RUNNING" || status == "CANCELLING" || status == "UNKNOWN" {
		// Late terminal evidence can resolve a lost lease; late progress never revives it.
		if p.Status != "" {
			if p.Status != "COMPLETED" && p.Status != "FAILED" && p.Status != "CANCELLED" && p.Status != "UNKNOWN" {
				return fmt.Errorf("invalid terminal status")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,result=?,finished_at=? WHERE id=?", p.Status, p.Result, tx.now().Unix(), p.RunID); err != nil {
				return err
			}
		}
	}
	if p.SessionID != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET session_id=? WHERE id=?", p.SessionID, p.RunID); err != nil {
			return err
		}
	}
	// Usage is cumulative: the last reported value replaces the stored one.
	if len(p.Usage) > 0 {
		b, _ := json.Marshal(p.Usage)
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET usage=? WHERE id=?", string(b), p.RunID); err != nil {
			return err
		}
	}
	if p.Quota != nil {
		if err = quotaPause(ctx, tx, p.RunID, *p.Quota); err != nil {
			return err
		}
	}
	if err = syncTaskRun(ctx, tx, p.RunID, true); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Events(ctx context.Context, company, id string) ([]protocol.Envelope, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT body FROM events WHERE company_id=? AND run_id=? ORDER BY seq", company, id)
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

func insertRun(ctx context.Context, tx *Tx, r *protocol.RunSpec) error {
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
	if _, err = tx.ExecContext(ctx, "INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,session_id,task_id,company_id,command_at) VALUES(?,?,?,?,?,'QUEUED',?,?,?,?,?)", r.ID, r.Project, r.Agent, r.Workspace, b, tx.now().Unix(), r.SessionID, nullable(r.TaskID), tx.company, tx.now().Unix()); err != nil {
		return fmt.Errorf("project already active or insert failed: %w", err)
	}
	e := protocol.Wrap("agent.run.requested", r.ID, "", r)
	b, _ = json.Marshal(e)
	if _, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", e.ID, "factory.commands.agent.run."+string(r.Provider), b, tx.company); err != nil {
		return err
	}
	return nil
}

// A session ID is opaque to Core but owned by the runtime that created it: Claude cannot
// resume a Codex thread. Explicit direct resumes are rejected; Core-scheduled task and
// conversation continuations start a fresh session after the agent's provider changed.
func checkSessionProvider(ctx context.Context, tx *Tx, r *protocol.RunSpec) error {
	if r.SessionID == "" {
		return nil
	}
	var b []byte
	err := tx.QueryRowContext(ctx, "SELECT spec FROM runs WHERE company_id=? AND session_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1", tx.company, r.SessionID).Scan(&b)
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
