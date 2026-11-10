package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Session affinity (session-affinity): a run that continues a session belongs to the node that holds the
// session — the node of the session's latest other run — while that node is active, however busy it is. An
// inactive holder (offline, paused, removed, or no longer able to run the agent) starts a release timer; when the
// company's timeout passes, any capable node may take the run, starts a fresh session, and the lock moves to it.
// A holder active again before that clears the timer.

// DefaultSessionAffinity is the release timeout of a company that set none (owner 2026-11-06: 90 minutes).
const DefaultSessionAffinity = 90 * time.Minute

// Affinity is why a node may not take a run now: the holder and, when it is inactive, when the lock is released.
type Affinity struct {
	Holder   string `json:"holder"`
	Inactive string `json:"inactive,omitempty"` // offline, paused, removed, unable: why the holder is inactive
	Release  int64  `json:"release_at,omitempty"`
}

func (a Affinity) detail() string {
	if a.Inactive == "" && a.Release > 0 {
		return fmt.Sprintf("node %s holds this conversation's session and has no free slot; another node starts it fresh at %s", a.Holder, time.Unix(a.Release, 0).UTC().Format(time.RFC3339))
	}
	if a.Inactive == "" {
		return fmt.Sprintf("node %s holds this agent's session and is active; the run waits for it", a.Holder)
	}
	return fmt.Sprintf("node %s holds this agent's session and is %s; the lock is released at %s", a.Holder, a.Inactive, time.Unix(a.Release, 0).UTC().Format(time.RFC3339))
}

type affinityQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sessionAffinity is the lock that keeps node from taking run id (spec): nil when the run has no session to
// continue, node holds it, the holder is active elsewhere... (non-nil) or the lock is released (nil). It keeps
// the inactivity timer in session_locks.
func sessionAffinity(ctx context.Context, q affinityQuerier, company, id string, spec protocol.RunSpec, node string, now time.Time) (*Affinity, error) {
	if spec.SessionID == "" {
		return nil, nil
	}
	holder, err := sessionHolder(ctx, q, company, spec.SessionID, id)
	if err != nil {
		return nil, err
	}
	if holder == "" {
		return nil, nil
	}
	if holder == node {
		_, err = q.ExecContext(ctx, "DELETE FROM session_locks WHERE company_id=? AND session_id=?", company, spec.SessionID)
		return nil, err
	}
	// A conversation waits for its session's node at most the company's conversation wait from when it was
	// queued, whether the holder is inactive or busy (work-and-conversation-runs); then any node starts it fresh.
	var deadline int64
	if spec.Conversation {
		if deadline, err = conversationDeadline(ctx, q, company, id); err != nil {
			return nil, err
		}
		if deadline > 0 && now.Unix() >= deadline {
			return nil, nil
		}
	}
	inactive, since, err := holderInactive(ctx, q, company, holder, spec)
	if err != nil {
		return nil, err
	}
	if inactive == "" {
		_, err = q.ExecContext(ctx, "DELETE FROM session_locks WHERE company_id=? AND session_id=?", company, spec.SessionID)
		return &Affinity{Holder: holder, Release: deadline}, err
	}
	var started int64
	err = q.QueryRowContext(ctx, "SELECT inactive_since FROM session_locks WHERE company_id=? AND session_id=? AND node_id=?", company, spec.SessionID, holder).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		started = now.Unix()
		if since > 0 && since < started {
			started = since
		}
		_, err = q.ExecContext(ctx, "INSERT INTO session_locks(company_id,session_id,node_id,inactive_since) VALUES(?,?,?,?) ON CONFLICT(company_id,session_id) DO UPDATE SET node_id=excluded.node_id, inactive_since=excluded.inactive_since", company, spec.SessionID, holder, started)
	}
	if err != nil {
		return nil, err
	}
	timeout, err := companyAffinity(ctx, q, company)
	if err != nil {
		return nil, err
	}
	release := started + int64(timeout/time.Second)
	if deadline > 0 && deadline < release {
		release = deadline
	}
	if now.Unix() >= release {
		// The released lock stays until the holder is active again or another node takes the session: deleting it
		// would restart the timer at the next look, which undid an owner's "Release now" (node-readiness 4.1).
		return nil, nil
	}
	return &Affinity{Holder: holder, Inactive: inactive, Release: release}, nil
}

// sessionHolder is the node of the session's latest run other than run ("" when no run of it has a node yet).
func sessionHolder(ctx context.Context, q affinityQuerier, company, session, run string) (string, error) {
	var holder string
	err := q.QueryRowContext(ctx, "SELECT node_id FROM runs WHERE company_id=? AND session_id=? AND node_id!='' AND id!=? ORDER BY created_at DESC, seq DESC LIMIT 1", company, session, run).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return holder, err
}

// holderInactive says why the holder cannot continue the session ("" when it is active) and, when known, since
// when (its last heartbeat for an offline node, the pause for a paused one).
func holderInactive(ctx context.Context, q affinityQuerier, company, holder string, spec protocol.RunSpec) (string, int64, error) {
	var online bool
	var lastSeen int64
	var drivers, labels []byte
	err := q.QueryRowContext(ctx, "SELECT online,last_seen,drivers,labels FROM nodes WHERE company_id=? AND id=?", company, holder).Scan(&online, &lastSeen, &drivers, &labels)
	if errors.Is(err, sql.ErrNoRows) {
		return "removed", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !online {
		return "offline", lastSeen, nil
	}
	var pausedAt sql.NullInt64
	if err = q.QueryRowContext(ctx, "SELECT min(created_at) FROM pauses WHERE company_id=? AND scope='node' AND target=?", company, holder).Scan(&pausedAt); err != nil {
		return "", 0, err
	}
	if pausedAt.Valid {
		return "paused", pausedAt.Int64, nil
	}
	var ds []NodeDriver
	var ls []string
	_ = json.Unmarshal(drivers, &ds)
	_ = json.Unmarshal(labels, &ls)
	if !slices.ContainsFunc(ds, func(d NodeDriver) bool { return d.Name == string(spec.Provider) && d.OK }) || len(tasks.MissingLabels(ls, AgentLabels(spec.ConfigSnapshot))) > 0 {
		return "unable to run the agent", 0, nil
	}
	// node-readiness: a holder not ready for the project cannot continue the session either.
	if spec.Project != "" {
		var since int64
		err = q.QueryRowContext(ctx, "SELECT since FROM node_readiness WHERE company_id=? AND node_id=? AND project_id=? AND faults!='[]'::jsonb", company, holder, spec.Project).Scan(&since)
		if err == nil {
			return "not ready for project " + spec.Project, since, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", 0, err
		}
	}
	return "", 0, nil
}

func companyAffinity(ctx context.Context, q affinityQuerier, company string) (time.Duration, error) {
	var minutes int64
	err := q.QueryRowContext(ctx, "SELECT session_affinity_minutes FROM companies WHERE id=?", company).Scan(&minutes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && minutes <= 0 {
		return DefaultSessionAffinity, nil
	}
	return time.Duration(minutes) * time.Minute, err
}

// DefaultConversationWait is how long a queued conversation run waits for the node holding its session in a company
// that set none (owner 2026-11-08: 10 minutes).
const DefaultConversationWait = 10 * time.Minute

func companyConversationWait(ctx context.Context, q affinityQuerier, company string) (time.Duration, error) {
	var minutes int64
	err := q.QueryRowContext(ctx, "SELECT conversation_wait_minutes FROM companies WHERE id=?", company).Scan(&minutes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && minutes <= 0 {
		return DefaultConversationWait, nil
	}
	return time.Duration(minutes) * time.Minute, err
}

// conversationDeadline is when queued conversation run id stops waiting for its session's node (0 when the run is
// not found).
func conversationDeadline(ctx context.Context, q affinityQuerier, company, id string) (int64, error) {
	var created int64
	err := q.QueryRowContext(ctx, "SELECT created_at FROM runs WHERE company_id=? AND id=?", company, id).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	wait, err := companyConversationWait(ctx, q, company)
	return created + int64(wait/time.Second), err
}

// ConversationWait is the company's conversation wait (session_affinity.conversation_wait).
func (s *Store) ConversationWait(ctx context.Context, company string) (time.Duration, error) {
	return companyConversationWait(ctx, s.DB, company)
}

// SetConversationWait sets the company's conversation wait (1 minute to 24 hours).
func (s *Store) SetConversationWait(ctx context.Context, company string, d time.Duration) error {
	if d < time.Minute || d > 24*time.Hour {
		return inputError("conversation wait must be between 1m and 24h")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET conversation_wait_minutes=? WHERE id=?", int64(d/time.Minute), company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// SessionAffinity is the company's release timeout.
func (s *Store) SessionAffinity(ctx context.Context, company string) (time.Duration, error) {
	return companyAffinity(ctx, s.DB, company)
}

// SetSessionAffinity sets the company's release timeout (10 minutes to 7 days).
func (s *Store) SetSessionAffinity(ctx context.Context, company string, d time.Duration) error {
	if d < 10*time.Minute || d > 7*24*time.Hour {
		return inputError("session affinity release must be between 10m and 168h")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET session_affinity_minutes=? WHERE id=?", int64(d/time.Minute), company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// RunAffinity is the affinity that keeps a QUEUED run from nodes other than its session's holder (nil when none),
// for the run's queued reason.
func (s *Store) RunAffinity(ctx context.Context, company, id string) (*Affinity, error) {
	spec, err := getDoc[protocol.RunSpec](ctx, s.DB, "SELECT spec FROM runs WHERE company_id=? AND id=? AND status='QUEUED'", company, id)
	if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sessionAffinity(ctx, s.DB, company, id, spec, "", s.Now())
}
