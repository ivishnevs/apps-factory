package storage

import (
	"context"
	"testing"
	"time"
)

// TestCompanyLockSerializesDecisions (postgres-tenancy D4, 3.1): while a transaction of a company is open,
// the company's deciding operations (expiry, approval expiry, claim) wait for it; another company's do not.
func TestCompanyLockSerializesDecisions(t *testing.T) {
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	held, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	decide := func(company string) chan error {
		done := make(chan error, 1)
		go func() {
			if err := s.Expire(ctx, company); err != nil {
				done <- err
				return
			}
			if err := s.ExpireApprovals(ctx, company); err != nil {
				done <- err
				return
			}
			_, err := s.ExpireQueued(ctx, company)
			done <- err
		}()
		return done
	}
	other := decide("acme")
	select {
	case err = <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another company waited for the lock")
	}
	same := decide(DefaultCompany)
	select {
	case err = <-same:
		t.Fatal("the company's expiry ran while its lock was held", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err = held.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-same:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the company's expiry did not resume after the lock was released")
	}
}
