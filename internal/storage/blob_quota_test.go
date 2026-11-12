package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestBlobQuota (security-review-2026-11 F6): a company stores content up to its quota and no further, whether the
// quota is already used or the new content would pass it; content it already has is accepted again; another
// company is not affected.
func TestBlobQuota(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	if err := s.CreateCompany(ctx, "other", "Other"); err != nil {
		t.Fatal(err)
	}
	s.BlobQuota = 10
	first, err := s.PutBlob(ctx, DefaultCompany, strings.NewReader("123456"), MaxBlob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("abcdefgh"), MaxBlob); !errors.Is(err, ErrQuota) || !errors.Is(err, ErrConflict) {
		t.Fatal("content past the quota stored", err)
	}
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("abcd"), MaxBlob); err != nil {
		t.Fatal("content within the quota refused", err)
	}
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("x"), MaxBlob); !errors.Is(err, ErrQuota) {
		t.Fatal("a full company stored more", err)
	}
	if _, err = s.PutBlob(ctx, "other", strings.NewReader("other's"), MaxBlob); err != nil {
		t.Fatal("another company refused", err)
	}
	if _, err = s.HasBlob(ctx, DefaultCompany, first.SHA256); err != nil {
		t.Fatal(err)
	}
	s.BlobQuota = 0
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("no quota"), MaxBlob); err != nil {
		t.Fatal(err)
	}
}
