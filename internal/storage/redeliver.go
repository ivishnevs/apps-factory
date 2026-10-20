package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"appfactory/internal/protocol"
)

// redeliverAfter is the wait before resending the command of a still QUEUED run, by the number of
// commands sent so far. Duplicates are harmless (claim is atomic and workers drop a command for a run
// that is no longer QUEUED), so backoff only bounds the stream while no worker is listening.
func redeliverAfter(sent int) time.Duration {
	steps := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}
	if sent >= 1 && sent <= len(steps) {
		return steps[sent-1]
	}
	return 15 * time.Minute
}

// RedeliverQueued resends the run command of every QUEUED run whose last command is older than its
// backoff and already left the outbox, so a purged or restored stream never strands work. Each
// resend has message ID <run>/<n>, so JetStream deduplication does not swallow it.
func (s *Store) RedeliverQueued(ctx context.Context, company string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := tx.now()
	type queued struct {
		id, project string
		spec        []byte
		sent        int
		at          int64
	}
	// at = when the last command left the outbox (or was enqueued, for rows from before sent_at).
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.project,r.spec,r.command_count,
 GREATEST(r.command_at,coalesce((SELECT max(o.sent_at) FROM outbox o WHERE o.subject LIKE 'factory.commands.%' AND (o.body->>'correlation_id')=r.id),0))
 FROM runs r WHERE r.company_id=? AND r.status='QUEUED' AND r.command_at<=?
 AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.sent=0 AND o.subject LIKE 'factory.commands.%' AND (o.body->>'correlation_id')=r.id) ORDER BY r.seq`, company, now.Add(-time.Minute).Unix())
	if err != nil {
		return 0, err
	}
	var due []queued
	for rows.Next() {
		var q queued
		if err = rows.Scan(&q.id, &q.project, &q.spec, &q.sent, &q.at); err != nil {
			rows.Close()
			return 0, err
		}
		if now.Sub(time.Unix(q.at, 0)) >= redeliverAfter(q.sent) {
			due = append(due, q)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, q := range due {
		var spec protocol.RunSpec
		if err = json.Unmarshal(q.spec, &spec); err != nil {
			return 0, err
		}
		e := protocol.Wrap("agent.run.requested", spec.ID, "", spec)
		e.ID = fmt.Sprintf("%s/%d", spec.ID, q.sent+1)
		b, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", e.ID, "factory.commands.agent.run."+string(spec.Provider), b, company); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET command_count=?,command_at=? WHERE id=?", q.sent+1, now.Unix(), spec.ID); err != nil {
			return 0, err
		}
		item := FeedItem{EventID: protocol.ID(), Type: "run.command_redelivered", EntityID: spec.ID, ProjectID: q.project, CreatedAt: now, TaskID: spec.TaskID, RunID: spec.ID, Agent: spec.Agent, Status: "QUEUED", Summary: fmt.Sprintf("run command resent (%d) after %s unclaimed", q.sent+1, now.Sub(time.Unix(q.at, 0)).Round(time.Second))}
		if err = feed(ctx, tx, item); err != nil {
			return 0, err
		}
	}
	return len(due), tx.Commit()
}
