package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"appfactory/internal/protocol"
)

// Company owner tokens (company-self-service 4.1): each authorizes the owner routes of one company. The
// operator's shared token (FACTORY_TOKEN) creates companies; a company's owner gets the first token then.

// OwnerTokenPrefix starts every company owner token, so Core tells it from the shared token without a lookup.
const OwnerTokenPrefix = "fot_"

type OwnerToken struct {
	ID         string `json:"id"`
	Company    string `json:"company"`
	Name       string `json:"name"`
	CreatedBy  string `json:"created_by,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	RevokedAt  int64  `json:"revoked_at,omitempty"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
}

// IssuedOwnerToken is CreateOwnerToken's answer; Token is shown only once.
type IssuedOwnerToken struct {
	OwnerToken
	Token string `json:"token"`
}

// CreateOwnerToken issues an owner token of company named name; author is who issued it (`user:<id>`,
// `operator`; user-accounts 5.3).
func (s *Store) CreateOwnerToken(ctx context.Context, company, name, author string) (IssuedOwnerToken, error) {
	out := IssuedOwnerToken{OwnerToken: OwnerToken{ID: protocol.ID(), Company: company, Name: strings.TrimSpace(name), CreatedBy: author, CreatedAt: s.Now().Unix()}}
	if len(out.Name) > 200 {
		return out, inputError("name exceeds 200 characters")
	}
	if _, err := s.CompanyTimeZone(ctx, company); err != nil {
		return out, err
	}
	out.Token = OwnerTokenPrefix + protocol.ID() + protocol.ID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO owner_tokens(company_id,id,name,hash,created_at,created_by) VALUES(?,?,?,?,?,?)", company, out.ID, out.Name, hashSecret(out.Token), out.CreatedAt, out.CreatedBy)
	return out, err
}

// OwnerTokens lists company's owner tokens (never the tokens themselves).
func (s *Store) OwnerTokens(ctx context.Context, company string) ([]OwnerToken, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,created_by,created_at,revoked_at,last_used_at FROM owner_tokens WHERE company_id=? ORDER BY created_at,id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OwnerToken{}
	for rows.Next() {
		t := OwnerToken{Company: company}
		if err = rows.Scan(&t.ID, &t.Name, &t.CreatedBy, &t.CreatedAt, &t.RevokedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeOwnerToken revokes one of company's owner tokens.
func (s *Store) RevokeOwnerToken(ctx context.Context, company, id string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE owner_tokens SET revoked_at=? WHERE company_id=? AND id=? AND revoked_at=0", s.Now().Unix(), company, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateOwnerToken returns the live owner token a request presents (with its company), or ErrNotFound.
func (s *Store) AuthenticateOwnerToken(ctx context.Context, token string) (OwnerToken, error) {
	var t OwnerToken
	if !strings.HasPrefix(token, OwnerTokenPrefix) {
		return t, ErrNotFound
	}
	err := s.DB.QueryRowContext(ctx, "SELECT company_id,id,name,created_at,last_used_at FROM owner_tokens WHERE hash=? AND revoked_at=0", hashSecret(token)).
		Scan(&t.Company, &t.ID, &t.Name, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if now := s.Now().Unix(); now-t.LastUsedAt >= 60 {
		if _, err = s.DB.ExecContext(ctx, "UPDATE owner_tokens SET last_used_at=? WHERE company_id=? AND id=?", now, t.Company, t.ID); err != nil {
			return t, err
		}
		t.LastUsedAt = now
	}
	return t, nil
}

// Company is a company's identity.
type Company struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// Companies lists every company.
func (s *Store) Companies(ctx context.Context) ([]Company, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,created_at FROM companies ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Company{}
	for rows.Next() {
		var c Company
		if err = rows.Scan(&c.ID, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
