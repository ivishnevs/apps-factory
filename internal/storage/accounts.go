package storage

import (
	"context"
	"strings"
)

// DefaultAccount owns the default company (postgres-tenancy D2).
const DefaultAccount = "default"

// Membership roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Membership is a user's role in one company.
type Membership struct {
	Company string `json:"company"`
	Role    string `json:"role"`
}

// CreateAccount registers an account: it holds companies and users.
func (s *Store) CreateAccount(ctx context.Context, id, name string) error {
	if !entityName.MatchString(id) || strings.TrimSpace(name) == "" {
		return inputError("account requires valid id and name")
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO accounts(id,name,created_at) VALUES(?,?,?)", id, name, s.Now().Unix())
	return constraint(err)
}

// CreateCompany registers a tenant of the default account. The default company exists in every database.
func (s *Store) CreateCompany(ctx context.Context, id, name string) error {
	return s.CreateAccountCompany(ctx, DefaultAccount, id, name)
}

// CreateAccountCompany registers a company of account.
func (s *Store) CreateAccountCompany(ctx context.Context, account, id, name string) error {
	if !entityName.MatchString(id) || strings.TrimSpace(name) == "" {
		return inputError("company requires valid id and name")
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO companies(id,account_id,name,created_at) VALUES(?,?,?,?)", id, account, name, s.Now().Unix())
	return constraint(err)
}

// CreateUser registers a user of account.
func (s *Store) CreateUser(ctx context.Context, account, id, email string) error {
	if !entityName.MatchString(id) || !strings.Contains(email, "@") {
		return inputError("user requires valid id and email")
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO users(id,account_id,email,created_at) VALUES(?,?,?,?)", id, account, strings.ToLower(strings.TrimSpace(email)), s.Now().Unix())
	return constraint(err)
}

// AddMember makes user a member of company with role. The user and the company must belong to the same
// account (the composite foreign keys refuse anything else: ErrConflict).
func (s *Store) AddMember(ctx context.Context, company, user, role string) error {
	if role != RoleOwner && role != RoleMember {
		return inputError("role must be owner or member")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO memberships(account_id,company_id,user_id,role,created_at)
 SELECT u.account_id,?,u.id,?,? FROM users u WHERE u.id=?
 ON CONFLICT(company_id,user_id) DO UPDATE SET role=excluded.role`, company, role, s.Now().Unix(), user)
	if err != nil {
		return constraint(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// Memberships lists the companies user belongs to.
func (s *Store) Memberships(ctx context.Context, user string) ([]Membership, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT company_id,role FROM memberships WHERE user_id=? ORDER BY company_id", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Membership{}
	for rows.Next() {
		var m Membership
		if err = rows.Scan(&m.Company, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
