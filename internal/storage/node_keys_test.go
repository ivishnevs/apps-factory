package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/nodesig"
)

func enrollInput(t *testing.T, code string) EnrollRequest {
	t.Helper()
	b := make([]byte, 16+32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	box := make([]byte, 32)
	if _, err := rand.Read(box); err != nil {
		t.Fatal(err)
	}
	return EnrollRequest{Code: code, NodeID: hex.EncodeToString(b[:16]), SignPub: hex.EncodeToString(b[16:]), BoxPub: hex.EncodeToString(box)}
}

// TestNodeEnrollmentStorage (node-identity 1.2): the code's life, the global node id and the 24-hour limit.
func TestNodeEnrollmentStorage(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if err := s.CreateCompany(ctx, "globex", "Globex"); err != nil {
		t.Fatal(err)
	}
	issue := func(company string) string {
		t.Helper()
		v, err := s.CreateNodeEnrollment(ctx, company, "mini")
		if err != nil || len(v.Code) != len(EnrollmentCodePrefix)+32 {
			t.Fatal(v, err)
		}
		return v.Code
	}

	// Only the hash is stored.
	code := issue(co)
	var stored int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM node_enrollments WHERE hash=?", code).Scan(&stored); err != nil || stored != 0 {
		t.Fatal("code stored in clear", stored, err)
	}

	// Expired.
	fake.Advance(EnrollmentCodeTTL + time.Second)
	if _, err := s.EnrollNode(ctx, enrollInput(t, code)); !errors.Is(err, ErrEnrollmentCode) {
		t.Fatal("expired code accepted", err)
	}

	// A refused request leaves the code usable.
	code = issue(co)
	bad := enrollInput(t, code)
	bad.NodeID = "n1"
	if _, err := s.EnrollNode(ctx, bad); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	in := enrollInput(t, code)
	k, err := s.EnrollNode(ctx, in)
	if err != nil || k.Status != NodeKeyPending || k.Company != co || k.Name != "mini" {
		t.Fatal(k, err)
	}
	if _, err = s.EnrollNode(ctx, enrollInput(t, code)); !errors.Is(err, ErrEnrollmentCode) {
		t.Fatal("used code accepted", err)
	}

	// The company of the code decides: an owner of another company can neither see nor approve these keys.
	if keys, err := s.NodeKeys(ctx, "globex"); err != nil || len(keys) != 0 {
		t.Fatal(keys, err)
	}
	if _, err = s.ApproveNodeKey(ctx, "globex", k.NodeID, k.Fingerprint); !errors.Is(err, ErrNotFound) {
		t.Fatal("approved by another company", err)
	}
	if err = s.RejectNodeKey(ctx, "globex", k.NodeID); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected by another company", err)
	}

	// A node id is global: another company's code cannot take it, nor can the same key.
	taken := enrollInput(t, issue("globex"))
	taken.NodeID = k.NodeID
	if _, err = s.EnrollNode(ctx, taken); !errors.Is(err, ErrNodeIDTaken) {
		t.Fatal("id of another company taken", err)
	}
	sameKey := enrollInput(t, issue("globex"))
	sameKey.SignPub = k.SignPub
	if _, err = s.EnrollNode(ctx, sameKey); !errors.Is(err, ErrNodeIDTaken) {
		t.Fatal("same signing key enrolled twice", err)
	}

	// Approval.
	if _, err = s.ApproveNodeKey(ctx, co, k.NodeID, "0000"); !errors.Is(err, ErrFingerprint) {
		t.Fatal(err)
	}
	a, err := s.ApproveNodeKey(ctx, co, k.NodeID, " "+k.Fingerprint+" ")
	if err != nil || a.Status != NodeKeyActive || a.ApprovedAt == 0 {
		t.Fatal(a, err)
	}
	if _, err = s.ApproveNodeKey(ctx, co, k.NodeID, k.Fingerprint); !errors.Is(err, ErrNotFound) {
		t.Fatal("approved twice", err)
	}
	if err = s.RejectNodeKey(ctx, co, k.NodeID); !errors.Is(err, ErrNotFound) {
		t.Fatal("active keys rejected", err)
	}

	// The fingerprint covers what was stored.
	sign, box, err := nodesig.DecodePublicKeys(k.SignPub, k.BoxPub)
	if err != nil || nodesig.Fingerprint(sign, box) != k.Fingerprint {
		t.Fatal(err, k)
	}

	// Pending keys past their limit are gone and free the id; active keys stay.
	p, err := s.EnrollNode(ctx, enrollInput(t, issue(co)))
	if err != nil {
		t.Fatal(err)
	}
	fake.Advance(PendingNodeKeyTTL + time.Second)
	if _, err = s.NodeKeyOf(ctx, p.NodeID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired pending keys live", err)
	}
	if _, err = s.ApproveNodeKey(ctx, co, p.NodeID, p.Fingerprint); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired pending keys approved", err)
	}
	if got, err := s.NodeKeyOf(ctx, k.NodeID); err != nil || got.Status != NodeKeyActive {
		t.Fatal(got, err)
	}
	re := enrollInput(t, issue(co))
	re.NodeID = p.NodeID
	if _, err = s.EnrollNode(ctx, re); err != nil {
		t.Fatal("expired pending keys hold the id", err)
	}

	// The feed names the fingerprint, never a code.
	var summaries string
	if err = s.DB.QueryRowContext(ctx, "SELECT coalesce(string_agg(summary, ' '),'') FROM feed_events WHERE kind='node.enrolled'").Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summaries, k.Fingerprint) || strings.Contains(summaries, EnrollmentCodePrefix) {
		t.Fatal(summaries)
	}
}
