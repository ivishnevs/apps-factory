package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// User accounts (user-accounts D1, D4, D5): people sign in through OIDC; a user is found by its identity
// (provider, subject); the first sign-in creates the user's account and one company the user owns. Web
// sessions are server-side: the cookie carries a random id, only its hash is stored.

// SessionPrefix starts every web session id, so Core tells a session cookie from a token at a glance.
const SessionPrefix = "fws_"

// Session lifetimes (D5): sliding 30 days, absolute 90 days.
const (
	SessionSliding  = 30 * 24 * time.Hour
	SessionAbsolute = 90 * 24 * time.Hour
)

// hashSecret is the stored form of a bearer secret (owner token, session id): its SHA-256 in hex.
func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// User is a person who signs in.
type User struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	LastCompany string `json:"last_company,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// Identity is what a verified ID token says about the person: the connector (provider) and its subject.
type Identity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// NewCompany names the company a first sign-in creates.
type NewCompany struct {
	ID   string
	Name string
}

// SignInResult is SignIn's answer; Company is set when the sign-in created the user and its company.
type SignInResult struct {
	User    User
	Created bool
	Company string
}

// SignIn finds the user of identity (D4) or creates one. A new identity is linked to an existing user only
// when its email is verified and equals that user's email; otherwise a new user is created with its own
// account and company (company names it, D1) and the user as its owner. An unverified email that belongs to
// another user is refused (ErrConflict): users.email stays unique and is never taken over.
func (s *Store) SignIn(ctx context.Context, in Identity, company NewCompany) (SignInResult, error) {
	var out SignInResult
	in.Provider, in.Subject = strings.TrimSpace(in.Provider), strings.TrimSpace(in.Subject)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Provider == "" || in.Subject == "" {
		return out, inputError("identity requires provider and subject")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	t := &Tx{Tx: tx, store: s}
	// Concurrent first sign-ins of one identity: one transaction at a time per provider subject.
	if _, err = t.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "identity:"+in.Provider+":"+in.Subject); err != nil {
		return out, err
	}
	var userID string
	err = t.QueryRowContext(ctx, "SELECT user_id FROM identities WHERE provider=? AND subject=?", in.Provider, in.Subject).Scan(&userID)
	switch {
	case err == nil:
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	default:
		if in.Email != "" {
			err = t.QueryRowContext(ctx, "SELECT id FROM users WHERE email=?", in.Email).Scan(&userID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			if err == nil && !in.EmailVerified {
				return out, conflictError("email " + in.Email + " belongs to another sign-in and is not verified by this provider")
			}
		}
		now := s.Now().Unix()
		if userID == "" {
			if in.Email == "" || !strings.Contains(in.Email, "@") {
				return out, inputError("the provider reported no email")
			}
			if !entityName.MatchString(company.ID) || strings.TrimSpace(company.Name) == "" {
				return out, inputError("company requires valid id and name")
			}
			userID = "u" + protocol.ID()[:20]
			name := strings.TrimSpace(in.Name)
			for _, q := range []struct {
				sql  string
				args []any
			}{
				{"INSERT INTO accounts(id,name,created_at) VALUES(?,?,?)", []any{userID, in.Email, now}},
				{"INSERT INTO users(id,account_id,email,name,created_at,last_company_id) VALUES(?,?,?,?,?,?)", []any{userID, userID, in.Email, name, now, company.ID}},
				{"INSERT INTO companies(id,account_id,name,created_at) VALUES(?,?,?,?)", []any{company.ID, userID, strings.TrimSpace(company.Name), now}},
				{"INSERT INTO memberships(account_id,company_id,user_id,role,created_at) VALUES(?,?,?,?,?)", []any{userID, company.ID, userID, RoleOwner, now}},
			} {
				if _, err = t.ExecContext(ctx, q.sql, q.args...); err != nil {
					return out, constraint(err)
				}
			}
			out.Created, out.Company = true, company.ID
		}
		if _, err = t.ExecContext(ctx, "INSERT INTO identities(provider,subject,user_id,email,created_at) VALUES(?,?,?,?,?)", in.Provider, in.Subject, userID, in.Email, now); err != nil {
			return out, constraint(err)
		}
	}
	if out.User, err = userBy(ctx, t, userID); err != nil {
		return out, err
	}
	if out.User.Name == "" && strings.TrimSpace(in.Name) != "" {
		out.User.Name = strings.TrimSpace(in.Name)
		if _, err = t.ExecContext(ctx, "UPDATE users SET name=? WHERE id=?", out.User.Name, userID); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func conflictError(message string) error { return fmt.Errorf("%w: %s", ErrConflict, message) }

func userBy(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (User, error) {
	var u User
	err := q.QueryRowContext(ctx, "SELECT id,account_id,email,name,last_company_id,created_at FROM users WHERE id=?", id).
		Scan(&u.ID, &u.Account, &u.Email, &u.Name, &u.LastCompany, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// User returns one user.
func (s *Store) User(ctx context.Context, id string) (User, error) { return userBy(ctx, s.DB, id) }

// Role is user's role in company, or ErrNotFound when the user is not its member (D6: no existence leak).
func (s *Store) Role(ctx context.Context, user, company string) (string, error) {
	var role string
	err := s.DB.QueryRowContext(ctx, "SELECT role FROM memberships WHERE user_id=? AND company_id=?", user, company).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// SetLastCompany remembers the company user worked in last (D6); only a company of a membership.
func (s *Store) SetLastCompany(ctx context.Context, user, company string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET last_company_id=? WHERE id=? AND last_company_id<>?
 AND EXISTS (SELECT 1 FROM memberships WHERE user_id=? AND company_id=?)`, company, user, company, user, company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err = s.Role(ctx, user, company); err != nil {
			return err
		}
	}
	return nil
}

// CreateUserCompany creates another company of user's account with user as its owner (D2; Core allows it only
// with the multi-company setting on).
func (s *Store) CreateUserCompany(ctx context.Context, user string, company NewCompany) error {
	if !entityName.MatchString(company.ID) || strings.TrimSpace(company.Name) == "" {
		return inputError("company requires valid id and name")
	}
	u, err := s.User(ctx, user)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := &Tx{Tx: tx, store: s}
	now := s.Now().Unix()
	if _, err = t.ExecContext(ctx, "INSERT INTO companies(id,account_id,name,created_at) VALUES(?,?,?,?)", company.ID, u.Account, strings.TrimSpace(company.Name), now); err != nil {
		return constraint(err)
	}
	if _, err = t.ExecContext(ctx, "INSERT INTO memberships(account_id,company_id,user_id,role,created_at) VALUES(?,?,?,?,?)", u.Account, company.ID, user, RoleOwner, now); err != nil {
		return constraint(err)
	}
	return tx.Commit()
}

// RenameCompany changes company's display name (its id, used in routes, stays).
func (s *Store) RenameCompany(ctx context.Context, company, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return inputError("company name requires 1..200 characters")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET name=? WHERE id=?", name, company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CompanyName is company's display name.
func (s *Store) CompanyName(ctx context.Context, company string) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, "SELECT name FROM companies WHERE id=?", company).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// Session is an authenticated web session.
type Session struct {
	User      string `json:"user"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// CreateSession starts a web session of user; the returned id goes into the cookie and is never stored.
func (s *Store) CreateSession(ctx context.Context, user string) (string, Session, error) {
	now := s.Now()
	out := Session{User: user, CreatedAt: now.Unix(), ExpiresAt: now.Add(SessionSliding).Unix()}
	id := SessionPrefix + protocol.ID() + protocol.ID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO sessions(id_hash,user_id,created_at,expires_at,last_seen_at) VALUES(?,?,?,?,?)", hashSecret(id), user, out.CreatedAt, out.ExpiresAt, out.CreatedAt)
	return id, out, constraint(err)
}

// AuthenticateSession returns the live session of id (ErrNotFound when unknown, expired or revoked) and slides
// its expiry: 30 days after the last use, never past 90 days after its start. Touches are written at most
// once a minute.
func (s *Store) AuthenticateSession(ctx context.Context, id string) (Session, error) {
	var out Session
	if !strings.HasPrefix(id, SessionPrefix) {
		return out, ErrNotFound
	}
	var lastSeen int64
	now := s.Now().Unix()
	err := s.DB.QueryRowContext(ctx, "SELECT user_id,created_at,expires_at,last_seen_at FROM sessions WHERE id_hash=? AND revoked_at=0 AND expires_at>?", hashSecret(id), now).
		Scan(&out.User, &out.CreatedAt, &out.ExpiresAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if now-lastSeen >= 60 {
		out.ExpiresAt = min(now+int64(SessionSliding/time.Second), out.CreatedAt+int64(SessionAbsolute/time.Second))
		if _, err = s.DB.ExecContext(ctx, "UPDATE sessions SET last_seen_at=?, expires_at=? WHERE id_hash=?", now, out.ExpiresAt, hashSecret(id)); err != nil {
			return out, err
		}
	}
	return out, nil
}

// RevokeSession ends the session of id (sign-out); an unknown id is not an error.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE id_hash=? AND revoked_at=0", s.Now().Unix(), hashSecret(id))
	return err
}
