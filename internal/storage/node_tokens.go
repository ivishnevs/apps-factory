package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"appfactory/internal/protocol"
)

// Node tokens (node-agent 1.1): one per machine, company-scoped. Core keeps only the SHA-256 of the
// secret. A token is bound to one node_id, at issue or by its first register; it authenticates only the
// node routes, and only for that node. The shared FACTORY_TOKEN keeps working during the transition.

// NodeTokenPrefix starts every node token, so Core tells it from the shared token without a lookup.
const NodeTokenPrefix = "fnt_"

// ErrNodeTokenBound: the token is bound to another node_id.
var ErrNodeTokenBound = errors.New("node token is bound to another node")

type NodeToken struct {
	ID         string `json:"id"`
	Company    string `json:"company,omitempty"`
	Name       string `json:"name"`
	NodeID     string `json:"node_id"`
	CreatedAt  int64  `json:"created_at"`
	RevokedAt  int64  `json:"revoked_at,omitempty"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
}

// IssuedNodeToken is CreateNodeToken's answer; Token is shown only once.
type IssuedNodeToken struct {
	NodeToken
	Token string `json:"token"`
}

func hashNodeToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateNodeToken issues a node token named name, bound to nodeID when given.
func (s *Store) CreateNodeToken(ctx context.Context, company, name, node string) (IssuedNodeToken, error) {
	var out IssuedNodeToken
	name = strings.TrimSpace(name)
	switch {
	case name == "" || len(name) > 128:
		return out, inputError("token name required (at most 128 characters)")
	case node != "" && !nodeID.MatchString(node):
		return out, inputError("valid node_id required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.NodeToken = NodeToken{ID: protocol.ID()[:16], Name: name, NodeID: node, CreatedAt: tx.now().Unix()}
	out.Token = NodeTokenPrefix + protocol.ID() + protocol.ID()
	if _, err = tx.ExecContext(ctx, "INSERT INTO node_tokens(company_id,id,name,node_id,hash,created_at) VALUES(?,?,?,?,?,?)",
		company, out.ID, out.Name, out.NodeID, hashNodeToken(out.Token), out.CreatedAt); err != nil {
		return out, constraint(err)
	}
	return out, tx.Commit()
}

// NodeTokens lists the company's node tokens (never their secrets).
func (s *Store) NodeTokens(ctx context.Context, company string) ([]NodeToken, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,node_id,created_at,revoked_at,last_used_at FROM node_tokens WHERE company_id=? ORDER BY created_at,rowid", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeToken{}
	for rows.Next() {
		var t NodeToken
		if err = rows.Scan(&t.ID, &t.Name, &t.NodeID, &t.CreatedAt, &t.RevokedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeNodeToken revokes a token at once; revoking twice is a no-op.
func (s *Store) RevokeNodeToken(ctx context.Context, company, id string) (NodeToken, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return NodeToken{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE node_tokens SET revoked_at=? WHERE company_id=? AND id=? AND revoked_at=0", tx.now().Unix(), company, id); err != nil {
		return NodeToken{}, err
	}
	var t NodeToken
	err = tx.QueryRowContext(ctx, "SELECT id,name,node_id,created_at,revoked_at,last_used_at FROM node_tokens WHERE company_id=? AND id=?", company, id).
		Scan(&t.ID, &t.Name, &t.NodeID, &t.CreatedAt, &t.RevokedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// AuthenticateNodeToken returns the live token a request presents (with its company), or ErrNotFound.
// last_used_at is refreshed at most once a minute.
func (s *Store) AuthenticateNodeToken(ctx context.Context, token string) (NodeToken, error) {
	var t NodeToken
	if !strings.HasPrefix(token, NodeTokenPrefix) {
		return t, ErrNotFound
	}
	err := s.DB.QueryRowContext(ctx, "SELECT company_id,id,name,node_id,created_at,last_used_at FROM node_tokens WHERE hash=? AND revoked_at=0", hashNodeToken(token)).
		Scan(&t.Company, &t.ID, &t.Name, &t.NodeID, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if now := s.Now().Unix(); now-t.LastUsedAt >= 60 {
		if _, err = s.DB.ExecContext(ctx, "UPDATE node_tokens SET last_used_at=? WHERE company_id=? AND id=?", now, t.Company, t.ID); err != nil {
			return t, err
		}
		t.LastUsedAt = now
	}
	return t, nil
}

// BindNodeToken binds an unbound token to node (its first register); a token bound to another node is
// ErrNodeTokenBound.
func (s *Store) BindNodeToken(ctx context.Context, company, id, node string) error {
	if !nodeID.MatchString(node) {
		return inputError("valid node_id required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bound string
	err = tx.QueryRowContext(ctx, "SELECT node_id FROM node_tokens WHERE company_id=? AND id=? AND revoked_at=0", company, id).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch bound {
	case node:
		return nil
	case "":
		if _, err = tx.ExecContext(ctx, "UPDATE node_tokens SET node_id=? WHERE company_id=? AND id=? AND node_id=''", node, company, id); err != nil {
			return err
		}
		return tx.Commit()
	}
	return ErrNodeTokenBound
}

// RunNode returns the node_id a v2 run was claimed by ("" for v1 runs and unclaimed runs).
func (s *Store) RunNode(ctx context.Context, company, run string) (string, error) {
	var node string
	err := s.DB.QueryRowContext(ctx, "SELECT node_id FROM runs WHERE company_id=? AND id=?", company, run).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return node, err
}
