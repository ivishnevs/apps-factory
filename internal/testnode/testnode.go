// Package testnode gives tests an enrolled, approved node, so they reach Core's node routes the way a node does.
package testnode

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"testing"

	"appfactory/internal/nodesig"
	"appfactory/internal/storage"
)

// Node is an enrolled node.
type Node struct {
	ID  string
	Key ed25519.PrivateKey
	Box *ecdh.PrivateKey
}

// Enroll creates an approved node of company.
func Enroll(t testing.TB, s *storage.Store, company string) Node {
	t.Helper()
	ctx := context.Background()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	box, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	code, err := s.CreateNodeEnrollment(ctx, company, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.EnrollNode(ctx, storage.EnrollRequest{Code: code.Code, NodeID: hex.EncodeToString(id), SignPub: hex.EncodeToString(pub), BoxPub: hex.EncodeToString(box.PublicKey().Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveNodeKey(ctx, company, k.NodeID, k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return Node{ID: k.NodeID, Key: key, Box: box}
}

// Sign signs r with the node's key at the store's time.
func (n Node) Sign(t testing.TB, s *storage.Store, r *http.Request, body []byte) {
	t.Helper()
	if err := nodesig.Sign(r, n.ID, n.Key, body, s.Now()); err != nil {
		t.Fatal(err)
	}
}

// OwnRun makes run this node's, as a claim does, for tests that claim through the store.
func (n Node) OwnRun(t testing.TB, s *storage.Store, company, run string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), "UPDATE runs SET node_id=$1 WHERE company_id=$2 AND id=$3", n.ID, company, run); err != nil {
		t.Fatal(err)
	}
}
