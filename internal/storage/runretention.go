package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// Run data retention (run-data-retention): an ended run keeps its row, attempts, exits, costs and tool calls for
// good, but its event stream, its log chunks (whose stored contents go once nothing else uses them) and the large
// fields of its stored spec (prompt, context) are deleted RunRetentionDays after it ended. Without it these grow
// with every run and fill the database and the blob volume.

// DefaultRunRetentionDays is used when FACTORY_RUN_RETENTION_DAYS is unset.
const DefaultRunRetentionDays = 14

// runPruneBatch bounds the runs one transaction prunes, so a large backlog never needs one huge transaction (and
// its write-ahead log) on a nearly full database volume.
const runPruneBatch = 200

// runSpecKeep is the largest stored size of a spec field an old run keeps (ids, agent, project, mode).
const runSpecKeep = 512

// RunPrune is what one run-data retention pass removed.
type RunPrune struct {
	Runs       int   `json:"runs"`
	Events     int64 `json:"events"`
	Logs       int64 `json:"logs"`
	Deleted    int   `json:"deleted_contents"`
	BytesFreed int64 `json:"bytes_freed"`
}

// PruneRunData applies run data retention to the company's runs ended more than days ago (0: keep forever) and
// records one feed item when it removed anything.
func (s *Store) PruneRunData(ctx context.Context, company string, days int) (RunPrune, error) {
	var out RunPrune
	if days <= 0 {
		return out, nil
	}
	cutoff := s.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	for {
		n, shas, err := s.pruneRunBatch(ctx, company, cutoff, &out)
		if err != nil {
			return out, err
		}
		for _, sha := range shas {
			size, ok, err := s.deleteBlobIfUnused(ctx, company, sha)
			if err != nil {
				return out, err
			}
			if ok {
				out.Deleted++
				out.BytesFreed += size
			}
		}
		if n < runPruneBatch {
			break
		}
	}
	if out.Runs == 0 {
		return out, nil
	}
	return out, s.AddFeed(ctx, company, FeedItem{EventID: protocol.ID(), Type: "run.retention", EntityID: "run-retention", Actor: "system",
		Summary: fmt.Sprintf("run data retention (%d days): pruned %d run(s), %d event(s), %d log chunk(s); deleted %d stored content(s), freed %s", days, out.Runs, out.Events, out.Logs, out.Deleted, byteSize(out.BytesFreed))})
}

// pruneRunBatch prunes up to runPruneBatch due runs in one transaction and returns how many it took and the log
// contents they referenced.
func (s *Store) pruneRunBatch(ctx context.Context, company string, cutoff int64, out *RunPrune) (int, []string, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM runs r WHERE company_id=? AND status='ENDED' AND finished_at>0 AND finished_at<?
 AND (EXISTS (SELECT 1 FROM events e WHERE e.company_id=r.company_id AND e.run_id=r.id)
  OR EXISTS (SELECT 1 FROM run_logs l WHERE l.company_id=r.company_id AND l.run_id=r.id)
  OR length(r.spec::text)>?)
 ORDER BY finished_at LIMIT ?`, company, cutoff, 4*runSpecKeep, runPruneBatch)
	if err != nil {
		return 0, nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(ids) == 0 {
		return 0, nil, err
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{company}
	for _, id := range ids {
		args = append(args, id)
	}
	var shas []string
	lr, err := tx.QueryContext(ctx, "SELECT DISTINCT sha256 FROM run_logs WHERE company_id=? AND run_id IN ("+in+")", args...)
	if err != nil {
		return 0, nil, err
	}
	for lr.Next() {
		var sha string
		if err = lr.Scan(&sha); err != nil {
			lr.Close()
			return 0, nil, err
		}
		shas = append(shas, sha)
	}
	lr.Close()
	if err = lr.Err(); err != nil {
		return 0, nil, err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM events WHERE company_id=? AND run_id IN ("+in+")", args...)
	if err != nil {
		return 0, nil, err
	}
	n, _ := res.RowsAffected()
	out.Events += n
	if res, err = tx.ExecContext(ctx, "DELETE FROM run_logs WHERE company_id=? AND run_id IN ("+in+")", args...); err != nil {
		return 0, nil, err
	}
	n, _ = res.RowsAffected()
	out.Logs += n
	if _, err = tx.ExecContext(ctx, `UPDATE runs r SET spec=(SELECT coalesce(json_object_agg(k,v),'{}'::json) FROM json_each(r.spec) AS j(k,v) WHERE length(v::text)<=?)
 WHERE company_id=? AND id IN (`+in+`)`, append([]any{runSpecKeep}, args...)...); err != nil {
		return 0, nil, err
	}
	out.Runs += len(ids)
	return len(ids), shas, tx.Commit()
}
