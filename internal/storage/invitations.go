package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// Invitations (team-invitations): an owner creates a single-use link for a role; whoever opens it signed in joins
// the company with that role. Links last InvitationTTL; owners list and revoke the open ones.

// InvitationTTL is how long an invitation link stays valid.
const InvitationTTL = 7 * 24 * time.Hour

// ErrInvitationClosed is an invitation that was used, revoked or has expired.
var ErrInvitationClosed = fmt.Errorf("%w: this invitation was already used, revoked or has expired", ErrConflict)

// Invitation is an open invitation link of a company.
type Invitation struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// InvitationPreview is what a link shows before it is accepted.
type InvitationPreview struct {
	Company     string `json:"company"`
	CompanyName string `json:"company_name"`
	Role        string `json:"role"`
	InvitedBy   string `json:"invited_by"`
	ExpiresAt   int64  `json:"expires_at"`
	// Open is false for a used, revoked or expired link.
	Open bool `json:"open"`
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateInvitation makes an invitation link for role (admin or member) and returns its token; only its hash is kept.
func (s *Store) CreateInvitation(ctx context.Context, company, role, by string) (Invitation, string, error) {
	if role != RoleAdmin && role != RoleMember {
		return Invitation{}, "", inputError("an invitation is for the admin or member role")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Invitation{}, "", err
	}
	token := hex.EncodeToString(raw)
	now := s.Now()
	inv := Invitation{ID: "inv" + protocol.ID()[:16], Role: role, CreatedBy: by, CreatedAt: now.Unix(), ExpiresAt: now.Add(InvitationTTL).Unix()}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO invitations(id,company_id,token_hash,role,created_by,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		inv.ID, company, tokenHash(token), role, by, inv.CreatedAt, inv.ExpiresAt)
	if err != nil {
		return Invitation{}, "", constraint(err)
	}
	return inv, token, nil
}

// Invitations lists company's open invitations, newest first.
func (s *Store) Invitations(ctx context.Context, company string) ([]Invitation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,role,created_by,created_at,expires_at FROM invitations
 WHERE company_id=? AND accepted_at=0 AND revoked_at=0 AND expires_at>? ORDER BY created_at DESC, id`, company, s.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err = rows.Scan(&i.ID, &i.Role, &i.CreatedBy, &i.CreatedAt, &i.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeInvitation closes an open invitation of company.
func (s *Store) RevokeInvitation(ctx context.Context, company, id string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE invitations SET revoked_at=? WHERE company_id=? AND id=? AND accepted_at=0 AND revoked_at=0", s.Now().Unix(), company, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// PreviewInvitation tells what a link invites to; ErrNotFound for a token that never existed.
func (s *Store) PreviewInvitation(ctx context.Context, token string) (InvitationPreview, error) {
	var p InvitationPreview
	var by string
	var accepted, revoked int64
	err := s.DB.QueryRowContext(ctx, `SELECT i.company_id,c.name,i.role,i.created_by,i.expires_at,i.accepted_at,i.revoked_at
 FROM invitations i JOIN companies c ON c.id=i.company_id WHERE i.token_hash=?`, tokenHash(token)).
		Scan(&p.Company, &p.CompanyName, &p.Role, &by, &p.ExpiresAt, &accepted, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Open = accepted == 0 && revoked == 0 && p.ExpiresAt > s.Now().Unix()
	p.InvitedBy = s.authorName(ctx, by)
	return p, nil
}

// authorName is the display name of a `user:<id>` author, else the author as written.
func (s *Store) authorName(ctx context.Context, by string) string {
	id, ok := strings.CutPrefix(by, "user:")
	if !ok {
		return by
	}
	if u, err := s.User(ctx, id); err == nil {
		if u.Name != "" {
			return u.Name
		}
		return u.Email
	}
	return ""
}

// AcceptInvitation joins user to the invitation's company with its role and uses the link up. A person who is
// already a member keeps their role and the link stays open for someone else. It returns the membership.
func (s *Store) AcceptInvitation(ctx context.Context, token, user string) (Membership, error) {
	var m Membership
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	t := &Tx{Tx: tx, store: s}
	var id, account string
	var expires, accepted, revoked int64
	err = t.QueryRowContext(ctx, `SELECT i.id,i.company_id,c.account_id,c.name,i.role,i.expires_at,i.accepted_at,i.revoked_at
 FROM invitations i JOIN companies c ON c.id=i.company_id WHERE i.token_hash=? FOR UPDATE OF i`, tokenHash(token)).
		Scan(&id, &m.Company, &account, &m.Name, &m.Role, &expires, &accepted, &revoked)
	if err != nil {
		return m, notFound(err)
	}
	var current string
	switch err = t.QueryRowContext(ctx, "SELECT role FROM memberships WHERE company_id=? AND user_id=?", m.Company, user).Scan(&current); {
	case err == nil:
		m.Role = current
		return m, nil // already in: nothing to use up
	case !errors.Is(err, sql.ErrNoRows):
		return m, err
	}
	now := s.Now().Unix()
	if accepted != 0 || revoked != 0 || expires <= now {
		return m, ErrInvitationClosed
	}
	if _, err = t.ExecContext(ctx, "INSERT INTO memberships(account_id,company_id,user_id,role,created_at) VALUES(?,?,?,?,?)", account, m.Company, user, m.Role, now); err != nil {
		return m, constraint(err)
	}
	if _, err = t.ExecContext(ctx, "UPDATE invitations SET accepted_by=?, accepted_at=? WHERE id=?", user, now, id); err != nil {
		return m, err
	}
	if _, err = t.ExecContext(ctx, "UPDATE users SET last_company_id=? WHERE id=?", m.Company, user); err != nil {
		return m, err
	}
	return m, tx.Commit()
}
