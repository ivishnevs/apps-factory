package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// Pausing a node that holds sessions (node-readiness 4.1): the owner sees the session leases the node holds and the
// runs waiting for them, and either releases them now (the runs go to another node and start a fresh session from
// Core's context) or keeps them until the release timeout (a short pause). Released locks stay in session_locks with
// inactive_since 0 until the holder is active again or another node takes the session.

// Pause.Sessions values for a node pause.
const (
	PauseSessionsKeep    = "keep"
	PauseSessionsRelease = "release"
)

// HeldSession is a session a node holds for an open task.
type HeldSession struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id,omitempty"`
	Title     string `json:"title,omitempty"`
	// Released: the owner released it; a waiting run may go to another node.
	Released bool `json:"released,omitempty"`
}

// NodeSessions is what pausing the node holds back: its sessions of open tasks and the queued runs waiting for them.
type NodeSessions struct {
	Held    []HeldSession `json:"held"`
	Waiting []TiedRun     `json:"waiting"`
	// ReleaseSeconds is the company's release timeout: a kept session is released that long after the pause.
	ReleaseSeconds int64 `json:"release_seconds"`
}

type sessionQuerier interface {
	affinityQuerier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// NodeSessions lists the sessions the node holds for open tasks and the queued runs waiting for them.
func (s *Store) NodeSessions(ctx context.Context, company, node string) (NodeSessions, error) {
	out := NodeSessions{Held: []HeldSession{}, Waiting: []TiedRun{}}
	var exists int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&exists); err != nil {
		return out, err
	}
	if exists == 0 {
		return out, ErrNotFound
	}
	held, err := heldSessions(ctx, s.DB, company, node)
	if err != nil {
		return out, err
	}
	out.Held = held
	if out.Waiting, err = sessionWaits(ctx, s.DB, company, node); err != nil {
		return out, err
	}
	timeout, err := companyAffinity(ctx, s.DB, company)
	out.ReleaseSeconds = int64(timeout / time.Second)
	return out, err
}

// heldSessions: sessions whose latest run with a node ran on node, of tasks that are not closed.
func heldSessions(ctx context.Context, q sessionQuerier, company, node string) ([]HeldSession, error) {
	rows, err := q.QueryContext(ctx, `SELECT l.session_id,l.agent,coalesce(l.task_id,''),coalesce(t.body->>'title',''),EXISTS(SELECT 1 FROM session_locks k WHERE k.company_id=l.company_id AND k.session_id=l.session_id AND k.node_id=? AND k.inactive_since=0)
 FROM (SELECT DISTINCT ON (r.session_id) r.company_id,r.session_id,r.agent,r.task_id,r.node_id FROM runs r
  WHERE r.company_id=? AND r.session_id!='' AND r.node_id!='' AND r.session_id IN (SELECT session_id FROM runs WHERE company_id=? AND node_id=? AND session_id!='')
  ORDER BY r.session_id,r.created_at DESC,r.seq DESC) l
 LEFT JOIN tasks t ON t.company_id=l.company_id AND t.id=l.task_id
 WHERE l.node_id=? AND (l.task_id IS NULL OR t.status NOT IN ('DONE','CANCELLED')) ORDER BY l.agent,l.session_id`, node, company, company, node, node)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeldSession{}
	for rows.Next() {
		var h HeldSession
		if err = rows.Scan(&h.SessionID, &h.Agent, &h.TaskID, &h.Title, &h.Released); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// sessionWaits lists the queued runs that continue a session node holds.
func sessionWaits(ctx context.Context, q sessionQuerier, company, node string) ([]TiedRun, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,coalesce(task_id,''),agent,spec->>'session_id' FROM runs WHERE company_id=? AND status='QUEUED' AND coalesce(spec->>'session_id','')!='' ORDER BY created_at,seq LIMIT ?`, company, maxOfferScan)
	if err != nil {
		return nil, err
	}
	type queued struct {
		r       TiedRun
		session string
	}
	var all []queued
	for rows.Next() {
		var x queued
		if err = rows.Scan(&x.r.RunID, &x.r.TaskID, &x.r.Agent, &x.session); err != nil {
			rows.Close()
			return nil, err
		}
		x.r.Tie = "session"
		all = append(all, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []TiedRun{}
	for _, x := range all {
		holder, err := sessionHolder(ctx, q, company, x.session, x.r.RunID)
		if err != nil {
			return nil, err
		}
		if holder == node {
			out = append(out, x.r)
		}
	}
	return out, nil
}

// releaseSession releases holder's lock on session: the next node to look may take its waiting run (fresh session).
func releaseSession(ctx context.Context, tx *Tx, session, holder string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO session_locks(company_id,session_id,node_id,inactive_since) VALUES(?,?,?,0) ON CONFLICT(company_id,session_id) DO UPDATE SET node_id=excluded.node_id,inactive_since=0", tx.company, session, holder)
	return err
}

// ReleaseNodeSessions releases every session the node holds, which only an inactive (paused, offline) node may give
// up: an active node takes its waiting runs itself. It returns the waiting runs that may now go elsewhere.
func (s *Store) ReleaseNodeSessions(ctx context.Context, company, node, actor string) ([]TiedRun, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	waiting, err := releaseNodeSessions(ctx, tx, node, actor)
	if err != nil {
		return nil, err
	}
	return waiting, tx.Commit()
}

func releaseNodeSessions(ctx context.Context, tx *Tx, node, actor string) ([]TiedRun, error) {
	var paused, online bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pauses WHERE company_id=? AND scope='node' AND target=?),coalesce((SELECT online FROM nodes WHERE company_id=? AND id=?),0)", tx.company, node, tx.company, node).Scan(&paused, &online); err != nil {
		return nil, err
	}
	if !paused && online {
		return nil, fmt.Errorf("%w: node %s is active; pause it first, its sessions stay with it while it runs", ErrConflict, node)
	}
	held, err := heldSessions(ctx, tx, tx.company, node)
	if err != nil {
		return nil, err
	}
	for _, h := range held {
		if err = releaseSession(ctx, tx, h.SessionID, node); err != nil {
			return nil, err
		}
	}
	waiting, err := sessionWaits(ctx, tx, tx.company, node)
	if err != nil {
		return nil, err
	}
	tx.wakeNodes()
	if len(held) == 0 {
		return waiting, nil
	}
	return waiting, nodeFeed(ctx, tx, node, nodeName(ctx, tx, node), "node.sessions_released",
		fmt.Sprintf("%d sessions released by %s%s; their runs go to another node and start a fresh session", len(held), actorOr(actor), tasksNamed(waiting)), "", false)
}

// ReleaseRunSession releases the session lock that keeps a queued run waiting for an inactive holder ("Release now").
func (s *Store) ReleaseRunSession(ctx context.Context, company, run, actor string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=? AND status='QUEUED'", company, run)
	if err != nil {
		return err
	}
	a, err := sessionAffinity(ctx, tx, company, run, spec, "", tx.now())
	if err != nil {
		return err
	}
	if a == nil {
		return nil // nothing holds it back
	}
	if a.Inactive == "" {
		return fmt.Errorf("%w: node %s holds this session and is active; it takes the run when it has a free slot", ErrConflict, a.Holder)
	}
	if err = releaseSession(ctx, tx, spec.SessionID, a.Holder); err != nil {
		return err
	}
	tx.wakeNodes()
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "session.released", EntityID: run, ProjectID: spec.Project, CreatedAt: tx.now(), TaskID: spec.TaskID, RunID: run, Agent: spec.Agent, Actor: actorOr(actor),
		Summary: fmt.Sprintf("session of run %s released from node %s (%s) by %s; another node takes the run with a fresh session", run, a.Holder, a.Inactive, actorOr(actor))}); err != nil {
		return err
	}
	return tx.Commit()
}

// pauseSessionsNotice is the one notice of a node pause that holds sessions: the tasks whose runs wait for the node,
// until when, or that they were released.
func pauseSessionsNotice(ctx context.Context, tx *Tx, node, sessions, actor string) error {
	if sessions == PauseSessionsRelease {
		_, err := releaseNodeSessions(ctx, tx, node, actor)
		return err
	}
	waiting, err := sessionWaits(ctx, tx, tx.company, node)
	if err != nil || len(waiting) == 0 {
		return err
	}
	timeout, err := companyAffinity(ctx, tx, tx.company)
	if err != nil {
		return err
	}
	at, err := localClock(ctx, tx, tx.now().Add(timeout))
	if err != nil {
		return err
	}
	return nodeFeed(ctx, tx, node, nodeName(ctx, tx, node), "node.paused_sessions",
		fmt.Sprintf("paused while holding agent sessions%s; they wait for the node until %s unless released (Nodes page or the task: Release now)", tasksNamed(waiting), at), "", true)
}

func tasksNamed(rs []TiedRun) string {
	if len(rs) == 0 {
		return ""
	}
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		n := r.TaskID
		if n == "" {
			n = "run " + r.RunID
		}
		names = append(names, n+" ("+r.Agent+")")
	}
	return " — waiting: " + strings.Join(names, ", ")
}

func actorOr(actor string) string {
	if actor == "" {
		return "owner"
	}
	return actor
}

// localClock is t as HH:MM in the company's time zone (UTC when unset).
func localClock(ctx context.Context, tx *Tx, t time.Time) (string, error) {
	var zone string
	if err := tx.QueryRowContext(ctx, "SELECT time_zone FROM companies WHERE id=?", tx.company).Scan(&zone); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		loc = time.UTC
	}
	return t.In(loc).Format("15:04 MST"), nil
}
