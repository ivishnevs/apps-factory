package storage

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"appfactory/internal/blobstore"
)

// MaxBlob bounds one uploaded blob (an artifact version, a report attachment, a run log chunk).
const MaxBlob = 64 << 20

// Blob is stored content: its digest and size (core-owned-project-data D1).
type Blob struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var errBlobTooLarge = errors.New("blob too large")

// limited fails the read past max bytes, so the blob store never keeps an oversized blob.
type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, errBlobTooLarge
	}
	return n, err
}

// PutBlob stores r (at most max bytes) in the blob store and records it for company.
func (s *Store) PutBlob(ctx context.Context, company string, r io.Reader, max int64) (Blob, error) {
	if company == "" {
		return Blob{}, inputError("company required")
	}
	sha, n, err := s.Blobs.Put(ctx, company, &limited{r: r, left: max})
	if errors.Is(err, errBlobTooLarge) {
		return Blob{}, inputError(fmt.Sprintf("content exceeds %d bytes", max))
	}
	if err != nil {
		return Blob{}, err
	}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO blobs(company_id,sha256,size,created_at) VALUES(?,?,?,?) ON CONFLICT (company_id,sha256) DO NOTHING", company, sha, n, s.Now().Unix()); err != nil {
		return Blob{}, err
	}
	return Blob{SHA256: sha, Size: n}, nil
}

// HasBlob is the recorded blob of company with digest sha (ErrNotFound when the company never stored it).
func (s *Store) HasBlob(ctx context.Context, company, sha string) (Blob, error) {
	return blobRow(ctx, s.DB, company, sha)
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func blobRow(ctx context.Context, q rowQuerier, company, sha string) (Blob, error) {
	b := Blob{SHA256: sha}
	if !blobstore.ValidDigest(sha) {
		return b, inputError("sha256 must be 64 lowercase hex digits")
	}
	err := q.QueryRowContext(ctx, "SELECT size FROM blobs WHERE company_id=? AND sha256=?", company, sha).Scan(&b.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// OpenBlob reads a recorded blob of company.
func (s *Store) OpenBlob(ctx context.Context, company, sha string) (io.ReadCloser, Blob, error) {
	b, err := s.HasBlob(ctx, company, sha)
	if err != nil {
		return nil, b, err
	}
	r, err := s.Blobs.Get(ctx, company, sha)
	if errors.Is(err, blobstore.ErrNotFound) {
		return nil, b, fmt.Errorf("blob %s recorded but missing from the blob store", sha)
	}
	return r, b, err
}

// runLease checks lease against the run's (constant time); ErrForbidden when it differs or the run has none.
func runLease(ctx context.Context, q rowQuerier, company, run, lease string) (status, project string, err error) {
	var current string
	err = q.QueryRowContext(ctx, "SELECT status,lease,project FROM runs WHERE company_id=? AND id=?", company, run).Scan(&status, &current, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if current == "" || subtle.ConstantTimeCompare([]byte(current), []byte(lease)) != 1 {
		return "", "", fmt.Errorf("%w: the run's lease required", ErrForbidden)
	}
	return status, project, nil
}

// RunBlob opens a blob for a live run's materialization: the run's lease must match and the blob must belong
// to the run's project (an OpenSpec document version or an artifact version of it).
func (s *Store) RunBlob(ctx context.Context, company, run, lease, sha string) (io.ReadCloser, Blob, error) {
	status, project, err := runLease(ctx, s.DB, company, run, lease)
	if err != nil {
		return nil, Blob{}, err
	}
	if status == "ENDED" || status == "QUEUED" {
		return nil, Blob{}, fmt.Errorf("%w: run is %s", ErrConflict, status)
	}
	return s.ProjectBlob(ctx, company, project, sha)
}

// ProjectBlob opens a blob that belongs to project (an OpenSpec document version or an artifact version).
func (s *Store) ProjectBlob(ctx context.Context, company, project, sha string) (io.ReadCloser, Blob, error) {
	var refs int
	if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM spec_documents WHERE company_id=? AND project_id=? AND sha256=?)
 + (SELECT count(*) FROM artifact_versions WHERE company_id=? AND project_id=? AND sha256=?)`, company, project, sha, company, project, sha).Scan(&refs); err != nil {
		return nil, Blob{}, err
	}
	if refs == 0 {
		return nil, Blob{}, fmt.Errorf("%w: blob of another project or unknown", ErrNotFound)
	}
	return s.OpenBlob(ctx, company, sha)
}

// blobBytes reads a blob of at most max bytes.
func (s *Store) blobBytes(ctx context.Context, company, sha string, max int64) ([]byte, error) {
	r, err := s.Blobs.Get(ctx, company, sha)
	if errors.Is(err, blobstore.ErrNotFound) {
		return nil, fmt.Errorf("blob %s missing from the blob store", sha)
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err == nil && int64(len(b)) > max {
		err = fmt.Errorf("blob %s exceeds %d bytes", sha, max)
	}
	return b, err
}
