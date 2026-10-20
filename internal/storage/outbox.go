package storage

import (
	"context"
	"database/sql"
	"time"
)

// outboxBatch is how many messages one publisher transaction takes.
const outboxBatch = 100

// Pending lists the outbox messages due for publication (every company), oldest first.
func (s *Store) Pending(ctx context.Context) ([]Outbox, error) {
	return pending(ctx, s.DB, s.Now(), "")
}

func pending(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, now time.Time, lock string) ([]Outbox, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,subject,body FROM outbox WHERE sent=0 AND next_at<=? ORDER BY seq LIMIT ?"+lock, now.Unix(), outboxBatch)
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

// PublishOutbox publishes one batch of due outbox messages (D6). The batch is locked FOR UPDATE SKIP LOCKED
// in a short transaction, so several Core processes publish in parallel and each message is taken by one of
// them; publish sends a message with its outbox ID as the JetStream message ID, and the message is marked
// sent in the same transaction. A crash between publish and commit leaves it unsent: it is published again
// with the same ID (deduplicated by JetStream, idempotent at consumers). A failed publish is retried after a
// growing backoff (1 s doubling to 5 min) while the rest of the outbox moves on. It returns how many
// messages were published.
func (s *Store) PublishOutbox(ctx context.Context, publish func(context.Context, Outbox) error, failed func(Outbox, error)) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := &Tx{Tx: tx, store: s}
	out, err := pending(ctx, q, s.Now(), " FOR UPDATE SKIP LOCKED")
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, o := range out {
		if err = publish(ctx, o); err != nil {
			if failed != nil {
				failed(o, err)
			}
			if err = publishFailed(ctx, q, s.Now(), o.ID, err); err != nil {
				return 0, err
			}
			continue
		}
		if _, err = q.ExecContext(ctx, "UPDATE outbox SET sent=1,sent_at=? WHERE id=?", s.Now().Unix(), o.ID); err != nil {
			return 0, err
		}
		sent++
	}
	return sent, tx.Commit()
}

// PublishFailed records a failed publication of message id (see PublishOutbox).
func (s *Store) PublishFailed(ctx context.Context, id string, cause error) error {
	return publishFailed(ctx, s.DB, s.Now(), id, cause)
}

func publishFailed(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, now time.Time, id string, cause error) error {
	var attempts int
	if err := q.QueryRowContext(ctx, "SELECT attempts FROM outbox WHERE id=?", id).Scan(&attempts); err != nil {
		return err
	}
	wait := time.Second << min(attempts, 9)
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	_, err := q.ExecContext(ctx, "UPDATE outbox SET attempts=attempts+1,next_at=?,last_error=? WHERE id=?", now.Add(wait).Unix(), shortText(cause.Error(), 1024), id)
	return err
}

// Sent marks message id published.
func (s *Store) Sent(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE outbox SET sent=1,sent_at=? WHERE id=?", s.Now().Unix(), id)
	return err
}
