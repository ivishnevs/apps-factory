package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

// RunLog is one chunk of a run's log that its node uploaded (core-owned-project-data D6). Redacted says the
// node replaced the credential values it knows before the upload.
type RunLog struct {
	Seq       int64  `json:"seq"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Redacted  bool   `json:"redacted"`
	Final     bool   `json:"final"`
	CreatedAt int64  `json:"created_at"`
}

// PutRunLog stores chunk seq of a run's log, authenticated by the run's lease (also after the run ended: the
// final chunk follows the terminal event). A retried chunk with the same content is accepted again.
func (s *Store) PutRunLog(ctx context.Context, company, run, lease string, seq int64, final, redacted bool, r io.Reader) (RunLog, error) {
	out := RunLog{Seq: seq, Final: final, Redacted: redacted}
	if seq < 1 || seq > 100000 {
		return out, inputError("seq 1..100000 required")
	}
	if _, _, err := runLease(ctx, s.DB, company, run, lease); err != nil {
		return out, err
	}
	b, err := s.PutBlob(ctx, company, r, MaxBlob)
	if err != nil {
		return out, err
	}
	out.SHA256, out.Size, out.CreatedAt = b.SHA256, b.Size, s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, "INSERT INTO run_logs(company_id,run_id,seq,sha256,size,redacted,final,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT (company_id,run_id,seq) DO NOTHING",
		company, run, seq, out.SHA256, out.Size, redacted, final, out.CreatedAt)
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var have string
		if err = s.DB.QueryRowContext(ctx, "SELECT sha256 FROM run_logs WHERE company_id=? AND run_id=? AND seq=?", company, run, seq).Scan(&have); err != nil {
			return out, err
		}
		if have != out.SHA256 {
			return out, fmt.Errorf("%w: log chunk %d has other content", ErrConflict, seq)
		}
	}
	return out, nil
}

// RunLogs lists the uploaded chunks of a run.
func (s *Store) RunLogs(ctx context.Context, company, run string) ([]RunLog, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,sha256,size,redacted,final,created_at FROM run_logs WHERE company_id=? AND run_id=? ORDER BY seq", company, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunLog{}
	for rows.Next() {
		var l RunLog
		if err = rows.Scan(&l.Seq, &l.SHA256, &l.Size, &l.Redacted, &l.Final, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RunLogChunk opens chunk seq of a run's log.
func (s *Store) RunLogChunk(ctx context.Context, company, run string, seq int64) (io.ReadCloser, Blob, error) {
	var sha string
	err := s.DB.QueryRowContext(ctx, "SELECT sha256 FROM run_logs WHERE company_id=? AND run_id=? AND seq=?", company, run, seq).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Blob{}, ErrNotFound
	}
	if err != nil {
		return nil, Blob{}, err
	}
	return s.OpenBlob(ctx, company, sha)
}
