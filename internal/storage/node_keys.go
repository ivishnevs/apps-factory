package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"appfactory/internal/nodesig"
	"appfactory/internal/protocol"
)

// Node enrollment (node-identity D2, D3): a single-use code issued by an owner lets a node register its public
// keys, which stay pending until an owner confirms their fingerprint.
const (
	// EnrollmentCodePrefix starts every enrollment code.
	EnrollmentCodePrefix = "fne_"
	// EnrollmentCodeTTL is how long a code can be used.
	EnrollmentCodeTTL = 15 * time.Minute
	// PendingNodeKeyTTL is how long keys wait for approval before they are dropped.
	PendingNodeKeyTTL = 24 * time.Hour

	NodeKeyPending = "pending"
	NodeKeyActive  = "active"
)

// enrollableNodeID is the form factory-node generates (internal/node/identity.go).
var enrollableNodeID = regexp.MustCompile(`^[0-9a-f]{32}$`)

var (
	// ErrEnrollmentCode: the code is unknown, used or expired; the three are not told apart.
	ErrEnrollmentCode = errors.New("enrollment code is not valid: issue a new one")
	// ErrNodeIDTaken: the node id already has keys or belongs to another company.
	ErrNodeIDTaken = errors.New("node id is already in use")
	// ErrFingerprint: the confirmed fingerprint is not the one of the pending keys.
	ErrFingerprint = errors.New("fingerprint does not match the node's keys")
)

// NodeEnrollment is an issued code without the code.
type NodeEnrollment struct {
	ID        string `json:"id"`
	Company   string `json:"company"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// IssuedNodeEnrollment is CreateNodeEnrollment's answer; Code is shown only once.
type IssuedNodeEnrollment struct {
	NodeEnrollment
	Code string `json:"code"`
}

// NodeKey is a node's public keys as Core knows them.
type NodeKey struct {
	NodeID      string `json:"node_id"`
	Company     string `json:"company"`
	Name        string `json:"name"`
	SignPub     string `json:"sign_pub"`
	BoxPub      string `json:"box_pub"`
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	ApprovedAt  int64  `json:"approved_at,omitempty"`
}

// EnrollRequest is what a node sends with its code; the keys are hex.
type EnrollRequest struct {
	Code    string `json:"code"`
	NodeID  string `json:"node_id"`
	SignPub string `json:"sign_pub"`
	BoxPub  string `json:"box_pub"`
	// Proof is the signature of nodesig.EnrollMessage by the signing key.
	Proof string `json:"proof"`
}

func hashEnrollmentCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// CreateNodeEnrollment issues a code for a node of company named name.
func (s *Store) CreateNodeEnrollment(ctx context.Context, company, name string) (IssuedNodeEnrollment, error) {
	var out IssuedNodeEnrollment
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return out, inputError("node name required (at most 128 characters)")
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return out, err
	}
	now := s.Now()
	out.NodeEnrollment = NodeEnrollment{ID: protocol.ID()[:16], Company: company, Name: name, CreatedAt: now.Unix(), ExpiresAt: now.Add(EnrollmentCodeTTL).Unix()}
	out.Code = EnrollmentCodePrefix + hex.EncodeToString(secret)
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO node_enrollments(company_id,id,name,hash,created_at,expires_at) VALUES(?,?,?,?,?,?)",
		company, out.ID, name, hashEnrollmentCode(out.Code), out.CreatedAt, out.ExpiresAt); err != nil {
		return out, constraint(err)
	}
	return out, tx.Commit()
}

// EnrollNode burns the code and stores the node's keys as pending. A refused request leaves the code usable,
// except that a used code is refused like any other invalid one.
func (s *Store) EnrollNode(ctx context.Context, in EnrollRequest) (NodeKey, error) {
	var key NodeKey
	if !enrollableNodeID.MatchString(in.NodeID) {
		return key, inputError("node_id must be 32 hex characters")
	}
	signPub, boxPub, err := nodesig.DecodePublicKeys(in.SignPub, in.BoxPub)
	if err != nil {
		return key, inputError(err.Error())
	}
	if !strings.HasPrefix(in.Code, EnrollmentCodePrefix) {
		return key, ErrEnrollmentCode
	}
	hash := hashEnrollmentCode(in.Code)
	var company, name, id string
	err = s.DB.QueryRowContext(ctx, "SELECT company_id,id,name FROM node_enrollments WHERE hash=?", hash).Scan(&company, &id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return key, ErrEnrollmentCode
	}
	if err != nil {
		return key, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return key, err
	}
	defer tx.Rollback()
	now := tx.now().Unix()
	result, err := tx.ExecContext(ctx, "UPDATE node_enrollments SET used_at=? WHERE company_id=? AND id=? AND used_at=0 AND expires_at>?", now, company, id, now)
	if err != nil {
		return key, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return key, ErrEnrollmentCode
	}
	// Keys left pending too long no longer hold the id.
	if _, err = tx.ExecContext(ctx, "DELETE FROM node_keys WHERE node_id=? AND status=? AND created_at<=?", in.NodeID, NodeKeyPending, tx.now().Add(-PendingNodeKeyTTL).Unix()); err != nil {
		return key, err
	}
	var taken int
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM node_keys WHERE node_id=? OR sign_pub=?) + (SELECT count(*) FROM nodes WHERE id=? AND company_id<>?)", in.NodeID, in.SignPub, in.NodeID, company).Scan(&taken); err != nil {
		return key, err
	}
	if taken > 0 {
		return key, ErrNodeIDTaken
	}
	key = NodeKey{NodeID: in.NodeID, Company: company, Name: name, SignPub: in.SignPub, BoxPub: in.BoxPub, Fingerprint: nodesig.Fingerprint(signPub, boxPub), Status: NodeKeyPending, CreatedAt: now}
	if _, err = tx.ExecContext(ctx, "INSERT INTO node_keys(node_id,company_id,name,sign_pub,box_pub,fingerprint,status,created_at) VALUES(?,?,?,?,?,?,?,?)",
		key.NodeID, company, name, key.SignPub, key.BoxPub, key.Fingerprint, key.Status, now); err != nil {
		if errors.Is(constraint(err), ErrConflict) {
			return NodeKey{}, ErrNodeIDTaken
		}
		return NodeKey{}, err
	}
	item := FeedItem{EventID: protocol.ID(), Type: "node.enrolled", EntityID: "node:" + key.NodeID, CreatedAt: tx.now(), Actor: "node:" + key.NodeID, Attention: true,
		Summary: fmt.Sprintf("node %s waits for approval, fingerprint %s", name, key.Fingerprint)}
	if err = feed(ctx, tx, item); err != nil {
		return NodeKey{}, err
	}
	return key, tx.Commit()
}

const nodeKeyColumns = "node_id,company_id,name,sign_pub,box_pub,fingerprint,status,created_at,approved_at"

func scanNodeKey(row interface{ Scan(...any) error }) (NodeKey, error) {
	var k NodeKey
	err := row.Scan(&k.NodeID, &k.Company, &k.Name, &k.SignPub, &k.BoxPub, &k.Fingerprint, &k.Status, &k.CreatedAt, &k.ApprovedAt)
	return k, err
}

func (s *Store) nodeKeyLive(k NodeKey) bool {
	return k.Status != NodeKeyPending || k.CreatedAt > s.Now().Add(-PendingNodeKeyTTL).Unix()
}

// NodeKeyOf returns the live keys of a node in any company; node ids are global. Pending keys past their limit
// are ErrNotFound.
func (s *Store) NodeKeyOf(ctx context.Context, node string) (NodeKey, error) {
	k, err := scanNodeKey(s.DB.QueryRowContext(ctx, "SELECT "+nodeKeyColumns+" FROM node_keys WHERE node_id=?", node))
	if errors.Is(err, sql.ErrNoRows) {
		return NodeKey{}, ErrNotFound
	}
	if err != nil {
		return NodeKey{}, err
	}
	if !s.nodeKeyLive(k) {
		return NodeKey{}, ErrNotFound
	}
	return k, nil
}

// NodeKeys lists the company's live node keys, oldest first.
func (s *Store) NodeKeys(ctx context.Context, company string) ([]NodeKey, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+nodeKeyColumns+" FROM node_keys WHERE company_id=? ORDER BY created_at,node_id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeKey{}
	for rows.Next() {
		k, err := scanNodeKey(rows)
		if err != nil {
			return nil, err
		}
		if s.nodeKeyLive(k) {
			out = append(out, k)
		}
	}
	return out, rows.Err()
}

// pendingNodeKey reads the company's live pending keys of node inside tx; anything else is ErrNotFound.
func pendingNodeKey(ctx context.Context, tx *Tx, company, node string) (NodeKey, error) {
	k, err := scanNodeKey(tx.QueryRowContext(ctx, "SELECT "+nodeKeyColumns+" FROM node_keys WHERE company_id=? AND node_id=?", company, node))
	if errors.Is(err, sql.ErrNoRows) {
		return NodeKey{}, ErrNotFound
	}
	if err != nil {
		return NodeKey{}, err
	}
	if k.Status != NodeKeyPending || k.CreatedAt <= tx.now().Add(-PendingNodeKeyTTL).Unix() {
		return NodeKey{}, ErrNotFound
	}
	return k, nil
}

// ApproveNodeKey activates the company's pending keys of node when fingerprint is theirs.
func (s *Store) ApproveNodeKey(ctx context.Context, company, node, fingerprint string) (NodeKey, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return NodeKey{}, err
	}
	defer tx.Rollback()
	k, err := pendingNodeKey(ctx, tx, company, node)
	if err != nil {
		return NodeKey{}, err
	}
	if strings.TrimSpace(fingerprint) != k.Fingerprint {
		return NodeKey{}, ErrFingerprint
	}
	k.Status, k.ApprovedAt = NodeKeyActive, tx.now().Unix()
	if _, err = tx.ExecContext(ctx, "UPDATE node_keys SET status=?,approved_at=? WHERE node_id=?", k.Status, k.ApprovedAt, node); err != nil {
		return NodeKey{}, err
	}
	item := FeedItem{EventID: protocol.ID(), Type: "node.approved", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: "owner", Summary: "node " + k.Name + " approved"}
	if err = feed(ctx, tx, item); err != nil {
		return NodeKey{}, err
	}
	return k, tx.Commit()
}

// RejectNodeKey deletes the company's pending keys of node; active keys go through revocation.
func (s *Store) RejectNodeKey(ctx context.Context, company, node string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	k, err := pendingNodeKey(ctx, tx, company, node)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM node_keys WHERE node_id=?", node); err != nil {
		return err
	}
	item := FeedItem{EventID: protocol.ID(), Type: "node.enroll_rejected", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: "owner", Summary: "node " + k.Name + " rejected, fingerprint " + k.Fingerprint}
	if err = feed(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit()
}
