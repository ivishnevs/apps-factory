package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"appfactory/internal/blobstore"
	"appfactory/internal/clock"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
)

var ErrInput = errors.New("invalid input")
var ErrInvalid = errors.New("invalid event")
var ErrConflict = errors.New("state or version conflict")
var ErrNotFound = errors.New("not found")

// ErrForbidden: the actor may not do this (403).
var ErrForbidden = errors.New("forbidden")

// DefaultCompany owns every row created before tenancy and all work of a single-company Core.
const DefaultCompany = "default"

// LeaseTTL is how long a run lease lives without a heartbeat.
const LeaseTTL = 45 * time.Second

type Store struct {
	DB *DB
	// Blobs holds OpenSpec documents, artifact contents, attachments and run logs by digest (Options.Blobs).
	Blobs blobstore.Store
	// Clock is the source of every timestamp Store writes or compares; nil means wall time.
	Clock clock.Clock
	// Continuity, when set by Core's tick, defers lease expiry until one TTL of continuous
	// ticking has passed, so a host sleep does not expire every lease at once.
	Continuity *clock.Continuity
	// EscalationTarget routes an escalation of a task with this assignee (hierarchy: its manager); Core
	// sets it from the agent catalog, nil routes to the owner.
	EscalationTarget func(assignee string) string
	// ManagerOf is the agent's manager (reports_to, owner at the top); nil routes questions to the owner.
	ManagerOf func(agent string) string
	// SubtreeOf lists the agents under agent (hierarchy D6, manager briefs); nil means none.
	SubtreeOf func(agent string) []string
	// EscalateAfterFor is how long agent may leave a question of category unanswered before it moves up
	// (hierarchy D4, policy escalate_after); nil or 0 means EscalateAfter.
	EscalateAfterFor func(agent, category string) time.Duration
	// ExplicitExits: runs end their turn with factory_exit_* (docs/explicit-exits-contract.md); a succeeded
	// turn without an accepted exit is no_exit instead of an implicit submission.
	ExplicitExits bool
}

func (s *Store) Now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// leaseCutoff is the lease_until below which a lease counts as expired: now, or 0 while the
// current continuity window is younger than one TTL (every lease still gets its full TTL).
func (s *Store) leaseCutoff() int64 { return s.windowCutoff(LeaseTTL) }

// windowCutoff is now, or 0 while the current continuity window is younger than d: silence is judged only
// over d of continuous ticking, so a sleeping Core host judges nothing right after it wakes.
func (s *Store) windowCutoff(d time.Duration) int64 {
	now := s.Now()
	if s.Continuity != nil {
		start := s.Continuity.Start()
		if start.IsZero() || !start.Add(d).Before(now) {
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
	// Status is the run's phase (reliable-recovery D1): QUEUED, CLAIMED (a v2 run claimed but not
	// started), RUNNING, CANCELLING (a stop in flight), ENDED; Outcome says how an ENDED run ended.
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	// Usage is the last cumulative usage the worker reported (runtime-drivers 2.5).
	Usage UsageList `json:"usage,omitempty"`
	// Outcome is how the run ended (reliable-recovery D1); empty while it is live.
	Outcome string `json:"outcome,omitempty"`
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

// Options configure OpenWith.
type Options struct {
	// IncarnationFile keeps a copy of the database incarnation outside the database (in Core's data
	// directory), so a database restored from a backup is recognized and gets a new incarnation; empty skips
	// the check.
	IncarnationFile string
	// Blobs keeps project contents (core-owned-project-data D1); nil keeps them in memory (tests, tools), so
	// Core passes its blob volume (blobstore.LocalFS).
	Blobs blobstore.Store
}

// Open connects to the Postgres database at url (postgres://...) and applies the pending migrations.
func Open(url string) (*Store, error) { return OpenWith(url, Options{}) }

// OpenWith is Open with options.
func OpenWith(url string, o Options) (*Store, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	ctx := context.Background()
	if err = Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: &DB{db}, Blobs: o.Blobs}
	if s.Blobs == nil {
		s.Blobs = &blobstore.Memory{}
	}
	if err = s.checkIncarnation(ctx, o.IncarnationFile); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (tx *Tx) now() time.Time { return tx.store.Now() }

// begin starts a transaction of company holding the company's advisory lock (D4): transactions that read
// state and decide on it (dispatch, claim, expiry, approval expiry, message scheduling, every mutation) run
// one at a time per company, as they did on the single SQLite connection; other companies never wait.
func (s *Store) begin(ctx context.Context, company string) (*Tx, error) {
	if company == "" {
		return nil, inputError("company required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err = lockCompanyTx(ctx, tx, company); err != nil {
		tx.Rollback()
		return nil, err
	}
	return &Tx{Tx: tx, company: company, store: s}, nil
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
	err := s.DB.QueryRowContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at,usage,outcome FROM runs WHERE company_id=? AND id=?", company, id).Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt, &r.Usage, &r.Outcome)
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
	rows, err := s.DB.QueryContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at,usage,outcome FROM runs WHERE company_id=? ORDER BY created_at,id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs := []Run{}
	for rows.Next() {
		var r Run
		var b []byte
		if err = rows.Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt, &r.Usage, &r.Outcome); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r.Spec); err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	return rs, rows.Err()
}

// Heartbeat extends a v1 run's lease. It decides on the lease like expiry does, so it holds the company lock.
func (s *Store) Heartbeat(ctx context.Context, company, id, token string) (bool, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE runs SET lease_until=? WHERE company_id=? AND id=? AND lease=? AND status IN ('RUNNING','CANCELLING') AND protocol=1 AND lease_until>=?", s.Now().Add(LeaseTTL).Unix(), company, id, token, s.leaseCutoff())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false, ErrConflict
	}
	var status, stopReason string
	if err = tx.QueryRowContext(ctx, "SELECT status,stop_reason FROM runs WHERE company_id=? AND id=?", company, id).Scan(&status, &stopReason); err != nil {
		return false, err
	}
	return status == "CANCELLING" || stopReason != "", tx.Commit()
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
	if p.Status != "" && (p.Status != "ENDED" || !workerOutcomes[p.Outcome]) || p.Status == "" && p.Outcome != "" {
		return fmt.Errorf("%w: terminal status %q outcome %q", ErrInvalid, p.Status, p.Outcome)
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
	var token, status, stopReason string
	// lost: Core ended the started run lost without the worker's terminal evidence, which may still arrive.
	var lost bool
	if err = tx.QueryRowContext(ctx, "SELECT lease,status,stop_reason,status='ENDED' AND end_applied=0 AND outcome IN ('lost','timed_out') AND NOT (protocol=2 AND run_started=0) FROM runs WHERE company_id=? AND id=?", company, p.RunID).Scan(&token, &status, &stopReason, &lost); err != nil {
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
	if status == "CLAIMED" || status == "RUNNING" || status == "CANCELLING" || lost {
		// Late terminal evidence can resolve a lost lease; late progress never revives it.
		if p.Status != "" {
			if lost && (p.Outcome == "revoked" || p.Outcome == "lost") {
				// The node stopped a run Core already declared lost (stop: terminal) or ended it without
				// evidence: that confirms the runtime is gone, it is not an owner cancel.
				if err = releaseLost(ctx, tx, p.RunID); err != nil {
					return err
				}
				return tx.Commit()
			}
			result, outcome := endOutcome(status, stopReason, p)
			hasExit, err := runHasExit(ctx, tx, p.RunID)
			if err != nil {
				return err
			}
			var taskRun bool
			if err = tx.QueryRowContext(ctx, "SELECT task_id IS NOT NULL FROM runs WHERE id=?", p.RunID).Scan(&taskRun); err != nil {
				return err
			}
			switch {
			case hasExit:
				outcome = "succeeded" // an accepted exit fixes the outcome whatever happens afterwards
			case s.ExplicitExits && outcome == "succeeded" && !taskRun:
				// A run without a task (a message run) ends by replying; exits do not apply.
			case s.ExplicitExits && outcome == "succeeded" && p.ExitState == "pending":
				outcome = "lost"
				if result == "" {
					result = "lost: the exit was not delivered before the run ended"
				}
			case s.ExplicitExits && outcome == "succeeded":
				outcome = "no_exit"
			}
			// Late evidence on a lost run replaces its outcome: its runtime has ended now.
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET outcome=?,status='ENDED',result=?,finished_at=?,unreleased=0 WHERE id=?", outcome, result, tx.now().Unix(), p.RunID); err != nil {
				return err
			}
		} else if !lost {
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET last_activity_at=? WHERE id=?", tx.now().Unix(), p.RunID); err != nil {
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
	if err = syncTaskRun(ctx, tx, p.RunID); err != nil {
		return err
	}
	return tx.Commit()
}

// workerOutcomes are the outcomes a worker reports on its terminal event (status ENDED); no_exit, stale and
// unclaimed are Core's. lost: the run ended without terminal evidence (docs/runtime-protocol-v2.md §8).
var workerOutcomes = map[string]bool{"succeeded": true, "failed": true, "timed_out": true, "idle_timeout": true, "max_turns": true,
	"setup_failed": true, "paused_out": true, "revoked": true, "lost": true}

// endOutcome is the result and outcome a terminal event gives a live (or lost) run. A run Core stopped for
// inactivity (stop_reason idle_timeout) that the worker reports revoked, or ended without evidence, ended
// idle_timeout: Core knows the cause.
func endOutcome(current, stopReason string, p protocol.Event) (result, outcome string) {
	result, outcome = p.Result, p.Outcome
	if current == "RUNNING" && stopReason == "idle_timeout" && (outcome == "revoked" || outcome == "lost") {
		outcome = "idle_timeout"
		if result == "" {
			result = "idle_timeout: no runtime activity for " + IdleTimeout.String() + "; stopped by Core"
		}
	}
	return result, outcome
}

// RunEvent is a stored run event with its sequence, the cursor of the run event stream.
type RunEvent struct {
	Seq      int64
	Envelope protocol.Envelope
}

// RunEventsAfter reads up to limit events of a run after the sequence after, in order.
func (s *Store) RunEventsAfter(ctx context.Context, company, id string, after int64, limit int) ([]RunEvent, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,body FROM events WHERE company_id=? AND run_id=? AND seq>? ORDER BY seq LIMIT ?", company, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var b []byte
		var e RunEvent
		if err = rows.Scan(&e.Seq, &b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &e.Envelope); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunEventTail is the cursor before a run's last n events (0 when the run has n events or fewer).
func (s *Store) RunEventTail(ctx context.Context, company, id string, n int) (int64, error) {
	var seq int64
	err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(MIN(seq)-1,0) FROM (SELECT seq FROM events WHERE company_id=? AND run_id=? ORDER BY seq DESC LIMIT ?) t", company, id, n).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if seq < 0 {
		seq = 0
	}
	return seq, nil
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
	if _, err = tx.ExecContext(ctx, "INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,session_id,task_id,company_id,command_at) VALUES(?,?,?,?,?,'QUEUED',?,?,?,?,?)", r.ID, r.Project, r.Agent, writerSlot(*r, true), b, tx.now().Unix(), r.SessionID, nullable(r.TaskID), tx.company, tx.now().Unix()); err != nil {
		return fmt.Errorf("project already active or insert failed: %w", err)
	}
	e := protocol.Wrap("agent.run.requested", r.ID, "", r)
	b, _ = json.Marshal(e)
	if _, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", e.ID, protocol.CommandSubject(tx.company, r.Provider), b, tx.company); err != nil {
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
	err := tx.QueryRowContext(ctx, "SELECT spec FROM runs WHERE company_id=? AND session_id=? ORDER BY created_at DESC, seq DESC LIMIT 1", tx.company, r.SessionID).Scan(&b)
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

// writerSlot is the workspace a run writes in, for the workspace_writer index: a worktree or
// fresh_from_default task run has its own task worktree, so its slot is "<project workspace>#task/<task>";
// anything run in the project workspace (shared or scratch mode, message runs, or a node without worktrees)
// holds the project workspace path and runs one at a time.
func writerSlot(r protocol.RunSpec, worktrees bool) string {
	if r.Conversation && r.Workspace != "" {
		return r.Workspace + "#conversation/" + r.Agent // a conversation run talks; it does not write the project
	}
	if worktrees && (r.WorkspaceMode == "worktree" || r.WorkspaceMode == "fresh_from_default") && r.TaskID != "" && r.Workspace != "" {
		return r.Workspace + "#task/" + r.TaskID
	}
	return r.Workspace
}
