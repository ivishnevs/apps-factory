package storage

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestSlowCompanyLockLogged: a deciding transaction that holds the company lock longer than SlowLockHold is logged
// with the function that took it; a snapshot read takes no lock and logs nothing.
func TestSlowCompanyLockLogged(t *testing.T) {
	s, _ := retryStore(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	defer func(d time.Duration) { SlowLockHold = d }(SlowLockHold)
	SlowLockHold = 0
	ctx := context.Background()
	tx, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx.Rollback() // deferred after a commit: logged once
	if out := buf.String(); strings.Count(out, "slow_company_lock") != 1 || !strings.Contains(out, "TestSlowCompanyLockLogged") {
		t.Fatalf("log %q", out)
	}
	buf.Reset()
	r, err := s.read(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	r.Rollback()
	if buf.Len() != 0 {
		t.Fatalf("a read logged %q", buf.String())
	}
}
