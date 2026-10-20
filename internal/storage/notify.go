package storage

import (
	"context"
	"database/sql"
	"errors"
)

// Notifier state (waits-events-cost-cli D4): one JSON document per channel (telegram) and the updates the
// channel applied, so a repeated Telegram update is applied once.

// NotifyState returns the channel's state document ("" when none was saved).
func (s *Store) NotifyState(ctx context.Context, company, channel string) (string, error) {
	var state string
	err := s.DB.QueryRowContext(ctx, "SELECT state FROM notify_state WHERE company_id=? AND channel=?", company, channel).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

func (s *Store) SaveNotifyState(ctx context.Context, company, channel, state string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO notify_state(company_id,channel,state) VALUES(?,?,?) ON CONFLICT(company_id,channel) DO UPDATE SET state=excluded.state", company, channel, state)
	return err
}

// NotifyApplied tells whether the channel already applied the update.
func (s *Store) NotifyApplied(ctx context.Context, company, channel string, update int64) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM notify_updates WHERE company_id=? AND channel=? AND update_id=?", company, channel, update).Scan(&n)
	return n > 0, err
}

func (s *Store) MarkNotifyApplied(ctx context.Context, company, channel string, update int64) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO notify_updates(company_id,channel,update_id,applied_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", company, channel, update, s.Now().Unix())
	return err
}

// FeedTail is the company's latest feed sequence.
func (s *Store) FeedTail(ctx context.Context, company string) (int64, error) {
	var seq int64
	err := s.DB.QueryRowContext(ctx, "SELECT coalesce(max(seq),0) FROM feed_events WHERE company_id=?", company).Scan(&seq)
	return seq, err
}
