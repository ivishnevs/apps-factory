package storage

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"appfactory/internal/nodesig"
	"appfactory/internal/protocol"
)

// Connection requests (node-install D5): `factory-node login` registers its new keys without a credential; an owner
// or admin who is signed in approves the request for one of their companies, which makes the keys active there.
const (
	// NodeConnectTTL is how long a request can be approved.
	NodeConnectTTL = 15 * time.Minute

	NodeConnectPending  = "pending"
	NodeConnectApproved = "approved"
	NodeConnectRejected = "rejected"
	// NodeConnectExpired is reported, never stored.
	NodeConnectExpired = "expired"
)

// connectCodeAlphabet has no look-alike characters (Crockford base32 without 0, 1).
const connectCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// NodeConnectRequest is what a node sends; the keys are hex and Proof signs nodesig.ConnectMessage.
type NodeConnectRequest struct {
	NodeID  string `json:"node_id"`
	Name    string `json:"name"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	SignPub string `json:"sign_pub"`
	BoxPub  string `json:"box_pub"`
	Proof   string `json:"proof"`
}

// NodeConnect is a connection request as Core keeps it.
type NodeConnect struct {
	ID          string `json:"id"`
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Address     string `json:"address"`
	SignPub     string `json:"-"`
	Fingerprint string `json:"fingerprint"`
	Code        string `json:"code"`
	Status      string `json:"status"`
	Company     string `json:"company,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	boxPub      string
}

func connectCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, 9)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, connectCodeAlphabet[int(x)%len(connectCodeAlphabet)])
	}
	return string(out), nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// CreateNodeConnect stores a node's connection request from address. The node id and the key must not be in use.
func (s *Store) CreateNodeConnect(ctx context.Context, in NodeConnectRequest, address string) (NodeConnect, error) {
	var out NodeConnect
	if !enrollableNodeID.MatchString(in.NodeID) {
		return out, inputError("node_id must be 32 hex characters")
	}
	sign, box, err := nodesig.DecodePublicKeys(in.SignPub, in.BoxPub)
	if err != nil {
		return out, inputError(err.Error())
	}
	name := clip(in.Name, 128)
	if name == "" {
		return out, inputError("node name required")
	}
	code, err := connectCode()
	if err != nil {
		return out, err
	}
	now := s.Now()
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM node_connects WHERE expires_at<=?", now.Add(-24*time.Hour).Unix()); err != nil {
		return out, err
	}
	var taken int
	if err = s.DB.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM node_keys WHERE (node_id=? OR sign_pub=?) AND (status=? OR created_at>?)) + (SELECT count(*) FROM nodes WHERE id=?)",
		in.NodeID, in.SignPub, NodeKeyActive, now.Add(-PendingNodeKeyTTL).Unix(), in.NodeID).Scan(&taken); err != nil {
		return out, err
	}
	if taken > 0 {
		return out, ErrNodeIDTaken
	}
	out = NodeConnect{ID: protocol.ID(), NodeID: in.NodeID, Name: name, OS: clip(in.OS, 32), Arch: clip(in.Arch, 32), Address: clip(address, 64),
		SignPub: in.SignPub, boxPub: in.BoxPub, Fingerprint: nodesig.Fingerprint(sign, box), Code: code, Status: NodeConnectPending,
		CreatedAt: now.Unix(), ExpiresAt: now.Add(NodeConnectTTL).Unix()}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO node_connects(id,node_id,name,os,arch,address,sign_pub,box_pub,fingerprint,code,status,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
		out.ID, out.NodeID, out.Name, out.OS, out.Arch, out.Address, out.SignPub, out.boxPub, out.Fingerprint, out.Code, out.Status, out.CreatedAt, out.ExpiresAt)
	return out, err
}

const nodeConnectColumns = "id,node_id,name,os,arch,address,sign_pub,box_pub,fingerprint,code,status,company_id,created_at,expires_at"

func scanNodeConnect(row interface{ Scan(...any) error }) (NodeConnect, error) {
	var c NodeConnect
	err := row.Scan(&c.ID, &c.NodeID, &c.Name, &c.OS, &c.Arch, &c.Address, &c.SignPub, &c.boxPub, &c.Fingerprint, &c.Code, &c.Status, &c.Company, &c.CreatedAt, &c.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// NodeConnectOf reads a request; a pending one past its time reads as expired.
func (s *Store) NodeConnectOf(ctx context.Context, id string) (NodeConnect, error) {
	c, err := scanNodeConnect(s.DB.QueryRowContext(ctx, "SELECT "+nodeConnectColumns+" FROM node_connects WHERE id=?", id))
	if err == nil && c.Status == NodeConnectPending && c.ExpiresAt <= s.Now().Unix() {
		c.Status = NodeConnectExpired
	}
	return c, err
}

// pendingConnect locks a request inside tx; anything but a live pending request is ErrNotFound.
func pendingConnect(ctx context.Context, tx *Tx, id string) (NodeConnect, error) {
	c, err := scanNodeConnect(tx.QueryRowContext(ctx, "SELECT "+nodeConnectColumns+" FROM node_connects WHERE id=? FOR UPDATE", id))
	if err != nil {
		return c, err
	}
	if c.Status != NodeConnectPending || c.ExpiresAt <= tx.now().Unix() {
		return c, ErrNotFound
	}
	return c, nil
}

// ErrConnectCode is an approval whose code is not the one the machine's terminal shows.
var ErrConnectCode = fmt.Errorf("%w: the code is not the one the machine's terminal shows", ErrConflict)

// SameConnectCode compares a typed code with the request's, ignoring case, spaces and dashes.
func SameConnectCode(typed, code string) bool {
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '-' || unicode.IsSpace(r) {
				return -1
			}
			return unicode.ToUpper(r)
		}, s)
	}
	a, b := norm(typed), norm(code)
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ApproveNodeConnect makes the request's keys active in company, by actor (a person or token, for the feed). code
// is what the approver typed from the machine's terminal: the link alone connects nothing, so a link someone else
// sends cannot attach their machine to the company with one click (security-review-2026-11 F2).
func (s *Store) ApproveNodeConnect(ctx context.Context, company, id, code, actor string) (NodeKey, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return NodeKey{}, err
	}
	defer tx.Rollback()
	c, err := pendingConnect(ctx, tx, id)
	if err != nil {
		return NodeKey{}, err
	}
	if !SameConnectCode(code, c.Code) {
		return NodeKey{}, ErrConnectCode
	}
	// saas-billing: one more own node only within the account's plan.
	if err = checkNodeLimit(ctx, tx, company); err != nil {
		return NodeKey{}, err
	}
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, "DELETE FROM node_keys WHERE node_id=? AND status=? AND created_at<=?", c.NodeID, NodeKeyPending, tx.now().Add(-PendingNodeKeyTTL).Unix()); err != nil {
		return NodeKey{}, err
	}
	var taken int
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM node_keys WHERE node_id=? OR sign_pub=?) + (SELECT count(*) FROM nodes WHERE id=? AND company_id<>?)", c.NodeID, c.SignPub, c.NodeID, company).Scan(&taken); err != nil {
		return NodeKey{}, err
	}
	if taken > 0 {
		return NodeKey{}, ErrNodeIDTaken
	}
	key := NodeKey{NodeID: c.NodeID, Company: company, Name: c.Name, SignPub: c.SignPub, BoxPub: c.boxPub, Fingerprint: c.Fingerprint, Status: NodeKeyActive, CreatedAt: now, ApprovedAt: now}
	if _, err = tx.ExecContext(ctx, "INSERT INTO node_keys(node_id,company_id,name,sign_pub,box_pub,fingerprint,status,created_at,approved_at) VALUES(?,?,?,?,?,?,?,?,?)",
		key.NodeID, company, key.Name, key.SignPub, key.BoxPub, key.Fingerprint, key.Status, now, now); err != nil {
		if errors.Is(constraint(err), ErrConflict) {
			return NodeKey{}, ErrNodeIDTaken
		}
		return NodeKey{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE node_connects SET status=?,company_id=?,decided_at=? WHERE id=?", NodeConnectApproved, company, now, id); err != nil {
		return NodeKey{}, err
	}
	if actor == "" {
		actor = "owner"
	}
	item := FeedItem{EventID: protocol.ID(), Type: "node.approved", EntityID: "node:" + key.NodeID, CreatedAt: tx.now(), Actor: actor,
		Summary: fmt.Sprintf("node %s connected by sign-in (%s %s from %s), fingerprint %s", key.Name, c.OS, c.Arch, c.Address, key.Fingerprint)}
	if err = feed(ctx, tx, item); err != nil {
		return NodeKey{}, err
	}
	return key, tx.Commit()
}

// RejectNodeConnect ends a pending request.
func (s *Store) RejectNodeConnect(ctx context.Context, id string) error {
	now := s.Now().Unix()
	res, err := s.DB.ExecContext(ctx, "UPDATE node_connects SET status=?,decided_at=? WHERE id=? AND status=? AND expires_at>?", NodeConnectRejected, now, id, NodeConnectPending, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
