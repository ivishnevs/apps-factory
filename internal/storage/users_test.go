package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"appfactory/internal/clock"
)

// TestSignInCreatesAccountAndCompany (user-accounts 3.1, D1, D4): the first sign-in creates the user, its
// account and one company it owns; the same identity signs in to the same user; a verified email links a new
// provider's identity, an unverified one is refused.
func TestSignInCreatesAccountAndCompany(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	google := Identity{Provider: "google", Subject: "g-1", Email: "Ann@Example.com", EmailVerified: true, Name: "Ann"}
	first, err := s.SignIn(ctx, google, NewCompany{ID: "ann-1a2b", Name: "Ann's company"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Company != "ann-1a2b" || first.User.Email != "ann@example.com" || first.User.Name != "Ann" || first.User.LastCompany != "ann-1a2b" {
		t.Fatalf("%+v", first)
	}
	if role, err := s.Role(ctx, first.User.ID, "ann-1a2b"); err != nil || role != RoleOwner {
		t.Fatal(role, err)
	}
	if _, err := s.Role(ctx, first.User.ID, DefaultCompany); !errors.Is(err, ErrNotFound) {
		t.Fatal("membership of another company", err)
	}
	again, err := s.SignIn(ctx, google, NewCompany{ID: "unused", Name: "x"})
	if err != nil || again.Created || again.User.ID != first.User.ID {
		t.Fatalf("%+v %v", again, err)
	}
	// Same person through GitHub with the same verified email: linked, no second company.
	github, err := s.SignIn(ctx, Identity{Provider: "github", Subject: "42", Email: "ann@example.com", EmailVerified: true}, NewCompany{ID: "unused2", Name: "x"})
	if err != nil || github.Created || github.User.ID != first.User.ID {
		t.Fatalf("%+v %v", github, err)
	}
	// Unverified email of an existing user: not linked, refused.
	if _, err = s.SignIn(ctx, Identity{Provider: "other", Subject: "x", Email: "ann@example.com"}, NewCompany{ID: "unused3", Name: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatal("unverified email linked", err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM companies WHERE id LIKE 'unused%'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	ms, err := s.Memberships(ctx, first.User.ID)
	if err != nil || len(ms) != 1 || ms[0] != (Membership{"ann-1a2b", "Ann's company", RoleOwner}) {
		t.Fatal(ms, err)
	}
	// Another company of the same account (multi-company), rename, last company.
	if err = s.CreateUserCompany(ctx, first.User.ID, NewCompany{ID: "ann-second", Name: "Second"}); err != nil {
		t.Fatal(err)
	}
	if err = s.RenameCompany(ctx, "ann-second", "  Games  "); err != nil {
		t.Fatal(err)
	}
	if name, _ := s.CompanyName(ctx, "ann-second"); name != "Games" {
		t.Fatal(name)
	}
	if err = s.SetLastCompany(ctx, first.User.ID, "ann-second"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetLastCompany(ctx, first.User.ID, DefaultCompany); !errors.Is(err, ErrNotFound) {
		t.Fatal("last company without membership", err)
	}
	if u, _ := s.User(ctx, first.User.ID); u.LastCompany != "ann-second" {
		t.Fatal(u)
	}
	if err = s.AddMember(ctx, "ann-second", first.User.ID, RoleAdmin); err != nil {
		t.Fatal("admin role", err)
	}
	if err = s.AddMember(ctx, "ann-second", first.User.ID, "operator"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

// TestSessions (user-accounts 3.1, D5): only the hash is stored; expiry slides to 30 days after the last use and
// never past 90 days after the start; sign-out revokes.
func TestSessions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	now := fake.Now()
	in, err := s.SignIn(ctx, Identity{Provider: "google", Subject: "s", Email: "s@example.com", EmailVerified: true}, NewCompany{ID: "s-co", Name: "S"})
	if err != nil {
		t.Fatal(err)
	}
	id, sess, err := s.CreateSession(ctx, in.User.ID)
	if err != nil || sess.ExpiresAt != now.Add(SessionSliding).Unix() {
		t.Fatal(sess, err)
	}
	var stored int
	if err = s.DB.QueryRow("SELECT count(*) FROM sessions WHERE id_hash=?", id).Scan(&stored); err != nil || stored != 0 {
		t.Fatal("session id stored in clear", stored, err)
	}
	// Used every 20 days: it lives until 90 days after its start.
	for range 4 {
		fake.Advance(20 * 24 * time.Hour)
		if got, err := s.AuthenticateSession(ctx, id); err != nil || got.User != in.User.ID {
			t.Fatal(fake.Now(), got, err)
		}
	}
	fake.Advance(11 * 24 * time.Hour) // day 91
	if _, err = s.AuthenticateSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("absolute expiry", err)
	}
	id2, _, _ := s.CreateSession(ctx, in.User.ID)
	fake.Advance(31 * 24 * time.Hour)
	if _, err = s.AuthenticateSession(ctx, id2); !errors.Is(err, ErrNotFound) {
		t.Fatal("sliding expiry", err)
	}
	id3, _, _ := s.CreateSession(ctx, in.User.ID)
	if err = s.RevokeSession(ctx, id3); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateSession(ctx, id3); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked session", err)
	}
	if _, err = s.AuthenticateSession(ctx, "fot_whatever"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
