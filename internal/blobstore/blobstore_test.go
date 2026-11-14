package blobstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoundTripAndCompanyIsolation (core-owned-project-data 1.1): put/get round-trips by digest, a repeated put
// is a no-op, and a company never reads another company's blob.
func TestRoundTripAndCompanyIsolation(t *testing.T) {
	ctx := context.Background()
	for name, s := range map[string]Store{"localfs": LocalFS{Root: t.TempDir()}, "memory": &Memory{}} {
		t.Run(name, func(t *testing.T) {
			sha, n, err := s.Put(ctx, "acme", strings.NewReader("hello spec\n"))
			if err != nil || n != 11 || sha != Digest([]byte("hello spec\n")) {
				t.Fatal(sha, n, err)
			}
			again, _, err := s.Put(ctx, "acme", strings.NewReader("hello spec\n"))
			if err != nil || again != sha {
				t.Fatal(again, err)
			}
			r, err := s.Get(ctx, "acme", sha)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(r)
			r.Close()
			if string(b) != "hello spec\n" {
				t.Fatal(string(b))
			}
			if ok, err := s.Exists(ctx, "acme", sha); !ok || err != nil {
				t.Fatal(ok, err)
			}
			if ok, _ := s.Exists(ctx, "other", sha); ok {
				t.Fatal("blob visible to another company")
			}
			if _, err = s.Get(ctx, "other", sha); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			for _, bad := range []string{"../acme", "", "a/b"} {
				if _, _, err = s.Put(ctx, bad, strings.NewReader("x")); err == nil {
					t.Fatal("company", bad)
				}
			}
			if _, err = s.Get(ctx, "acme", "../"+sha[3:]); err == nil {
				t.Fatal("invalid digest accepted")
			}
			// artifact-retention: Delete removes the blob; deleting it again is no error.
			if err = s.Delete(ctx, "acme", sha); err != nil {
				t.Fatal(err)
			}
			if ok, _ := s.Exists(ctx, "acme", sha); ok {
				t.Fatal("deleted blob still there")
			}
			if err = s.Delete(ctx, "acme", sha); err != nil {
				t.Fatal("second delete", err)
			}
			// saas-release-readiness 4.2: DeleteCompany removes one company's blobs only.
			a, _, _ := s.Put(ctx, "acme", strings.NewReader("a"))
			o, _, _ := s.Put(ctx, "other", strings.NewReader("o"))
			if err = s.DeleteCompany(ctx, "acme"); err != nil {
				t.Fatal(err)
			}
			if ok, _ := s.Exists(ctx, "acme", a); ok {
				t.Fatal("company blob kept")
			}
			if ok, _ := s.Exists(ctx, "other", o); !ok {
				t.Fatal("another company's blob deleted")
			}
			if err = s.DeleteCompany(ctx, "../acme"); err == nil {
				t.Fatal("invalid company accepted")
			}
		})
	}
}

// TestLocalFSLeavesNoPartialBlob: a failed read leaves nothing under the digest and no temporary file.
func TestLocalFSLeavesNoPartialBlob(t *testing.T) {
	root := t.TempDir()
	s := LocalFS{Root: root}
	if _, _, err := s.Put(context.Background(), "acme", io.MultiReader(strings.NewReader("part"), errReader{})); err == nil {
		t.Fatal("put of a failing reader succeeded")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "acme"))
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

// TestLocalFSRepeatedPutWritesNothing: storing small content the store already has writes nothing, so it succeeds
// on a volume that has no room left (here: a company directory that cannot be written); new content still fails.
func TestLocalFSRepeatedPutWritesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	s := LocalFS{Root: root}
	ctx := context.Background()
	sha, _, err := s.Put(ctx, "acme", strings.NewReader("skill body"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "acme")
	if err = os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	again, n, err := s.Put(ctx, "acme", strings.NewReader("skill body"))
	if err != nil || again != sha || n != int64(len("skill body")) {
		t.Fatal(again, n, err)
	}
	if _, _, err = s.Put(ctx, "acme", strings.NewReader("new body")); err == nil {
		t.Fatal("new content was stored in a directory that cannot be written")
	}
}
