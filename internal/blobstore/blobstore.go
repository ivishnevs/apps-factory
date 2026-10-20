// Package blobstore keeps project contents (OpenSpec documents, artifact versions, report attachments, run
// logs) addressed by their SHA-256, per company (core-owned-project-data D1). LocalFS keeps them in a Core
// volume; an S3-compatible store can implement the same interface later. Metadata (which blobs exist, who
// references them) lives in Postgres.
package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// ErrNotFound: the company has no blob with that digest.
var ErrNotFound = errors.New("blob not found")

// Store is a content-addressed store. Put returns the hex SHA-256 and size of what it read; storing the same
// content again is a no-op. Companies never see each other's blobs.
type Store interface {
	Put(ctx context.Context, company string, r io.Reader) (sha string, size int64, err error)
	Get(ctx context.Context, company, sha string) (io.ReadCloser, error)
	Exists(ctx context.Context, company, sha string) (bool, error)
}

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var company = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)

// ValidDigest reports a lowercase hex SHA-256.
func ValidDigest(s string) bool { return digest.MatchString(s) }

// Digest is the hex SHA-256 of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func check(c, sha string) error {
	if !company.MatchString(c) {
		return fmt.Errorf("invalid company %q", c)
	}
	if sha != "" && !ValidDigest(sha) {
		return fmt.Errorf("invalid digest")
	}
	return nil
}

// LocalFS keeps blobs as files Root/<company>/<sha[:2]>/<sha>, written to a temporary file and renamed into
// place, so a reader never sees a partial blob.
type LocalFS struct{ Root string }

func (l LocalFS) path(c, sha string) string { return filepath.Join(l.Root, c, sha[:2], sha) }

func (l LocalFS) Put(ctx context.Context, c string, r io.Reader) (string, int64, error) {
	if err := check(c, ""); err != nil {
		return "", 0, err
	}
	dir := filepath.Join(l.Root, c)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	final := l.path(c, sha)
	if _, err = os.Stat(final); err == nil {
		return sha, n, nil
	}
	if err = os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", 0, err
	}
	return sha, n, os.Rename(f.Name(), final)
}

func (l LocalFS) Get(ctx context.Context, c, sha string) (io.ReadCloser, error) {
	if err := check(c, sha); err != nil {
		return nil, err
	}
	f, err := os.Open(l.path(c, sha))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (l LocalFS) Exists(ctx context.Context, c, sha string) (bool, error) {
	if err := check(c, sha); err != nil {
		return false, err
	}
	_, err := os.Stat(l.path(c, sha))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Memory is an in-process Store for tests and tools; its contents vanish with the process.
type Memory struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (m *Memory) Put(ctx context.Context, c string, r io.Reader) (string, int64, error) {
	if err := check(c, ""); err != nil {
		return "", 0, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sha := Digest(b)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string][]byte{}
	}
	m.m[c+"/"+sha] = b
	return sha, int64(len(b)), nil
}

func (m *Memory) Get(ctx context.Context, c, sha string) (io.ReadCloser, error) {
	if err := check(c, sha); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.m[c+"/"+sha]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *Memory) Exists(ctx context.Context, c, sha string) (bool, error) {
	if err := check(c, sha); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.m[c+"/"+sha]
	return ok, nil
}
