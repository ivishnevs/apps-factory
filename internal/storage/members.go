package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Members of a company (saas-web-ui 2.3): who belongs to it with which role. Owners change roles and remove
// members; a company always keeps one owner.

// Member is a person of the company.
type Member struct {
	User      string `json:"user"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

// ErrLastOwner refuses to leave a company without an owner.
var ErrLastOwner = fmt.Errorf("%w: a company needs an owner: make someone else owner first", ErrConflict)

// Members lists the company's people, owners first.
func (s *Store) Members(ctx context.Context, company string) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id,u.email,u.name,m.role,m.created_at FROM memberships m JOIN users u ON u.id=m.user_id
 WHERE m.company_id=? ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, u.email`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err = rows.Scan(&m.User, &m.Email, &m.Name, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// otherOwners counts the company's owners other than user inside tx.
func otherOwners(ctx context.Context, tx *Tx, company, user string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM memberships WHERE company_id=? AND role='owner' AND user_id<>?", company, user).Scan(&n)
	return n, err
}

// SetMemberRole changes a member's role; the last owner keeps theirs.
func (s *Store) SetMemberRole(ctx context.Context, company, user, role string) error {
	if !ValidRole(role) {
		return inputError("role must be owner, admin or member")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT role FROM memberships WHERE company_id=? AND user_id=? FOR UPDATE", company, user).Scan(&current); err != nil {
		return notFound(err)
	}
	if current == RoleOwner && role != RoleOwner {
		if n, err := otherOwners(ctx, tx, company, user); err != nil {
			return err
		} else if n == 0 {
			return ErrLastOwner
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE memberships SET role=? WHERE company_id=? AND user_id=?", role, company, user); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveMember takes a person out of the company; the last owner stays.
func (s *Store) RemoveMember(ctx context.Context, company, user string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT role FROM memberships WHERE company_id=? AND user_id=? FOR UPDATE", company, user).Scan(&current); err != nil {
		return notFound(err)
	}
	if current == RoleOwner {
		if n, err := otherOwners(ctx, tx, company, user); err != nil {
			return err
		} else if n == 0 {
			return ErrLastOwner
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM memberships WHERE company_id=? AND user_id=?", company, user); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET last_company_id='' WHERE id=? AND last_company_id=?", user, company); err != nil {
		return err
	}
	return tx.Commit()
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
