package core

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"appfactory/internal/nodesig"
	"appfactory/internal/storage"
)

// Node enrollment routes (node-identity D3). Owners issue codes and confirm fingerprints on owner routes. A node
// holds no credential yet when it enrolls, so POST /nodes/enroll and its status route are served before the token
// gate, with a proof by the node's new key instead.

const (
	enrollPerAddressPerMinute = 10
	enrollPerMinute           = 120
)

// enrollLimiter limits the tokenless enroll route per address and overall. Forwarded addresses can be forged, so the
// overall limit is what holds; a code has 128 random bits, so this protects Core, not the codes.
type enrollLimiter struct {
	mu     sync.Mutex
	minute int64
	total  int
	byAddr map[string]int
}

func (l *enrollLimiter) allow(addr string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m := now.Unix() / 60; m != l.minute || l.byAddr == nil {
		l.minute, l.total, l.byAddr = m, 0, map[string]int{}
	}
	if l.total >= enrollPerMinute || l.byAddr[addr] >= enrollPerAddressPerMinute {
		return false
	}
	l.total++
	l.byAddr[addr]++
	return true
}

func clientAddress(r *http.Request) string {
	if f, _, ok := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); ok || f != "" {
		return strings.TrimSpace(f)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (c *Core) nodeKeyRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /node-enrollments", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.CreateNodeEnrollment(r.Context(), c.companyFrom(r), p.Name)
		reply(w, v, err)
	})
	m.HandleFunc("GET /node-keys", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.NodeKeys(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /node-keys/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Fingerprint string `json:"fingerprint"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.ApproveNodeKey(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Fingerprint)
		if errors.Is(err, storage.ErrFingerprint) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		reply(w, v, err)
	})
	m.HandleFunc("POST /node-keys/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.RejectNodeKey(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
}

// enrollmentGateway serves the routes of nodes without a credential; false leaves the request to the token gate.
func (c *Core) enrollmentGateway(w http.ResponseWriter, r *http.Request) bool {
	if c.Store == nil {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/nodes/enroll":
		c.enrollNode(w, r)
		return true
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "nodes" && parts[2] == "enrollment":
		c.nodeEnrollmentStatus(w, r, parts[1])
		return true
	}
	return false
}

// NodeEnrollmentAnswer is what a node learns about its keys.
type NodeEnrollmentAnswer struct {
	NodeID      string `json:"node_id"`
	Company     string `json:"company"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
}

func enrollmentAnswer(k storage.NodeKey) NodeEnrollmentAnswer {
	return NodeEnrollmentAnswer{NodeID: k.NodeID, Company: k.Company, Name: k.Name, Fingerprint: k.Fingerprint, Status: k.Status}
}

func (c *Core) enrollNode(w http.ResponseWriter, r *http.Request) {
	if !c.enrollLimit.allow(clientAddress(r), c.Store.Now()) {
		http.Error(w, "too many enrollment requests: try again in a minute", http.StatusTooManyRequests)
		return
	}
	var in storage.EnrollRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The proof shows the sender holds the private key of the keys it registers.
	sign, _, err := nodesig.DecodePublicKeys(in.SignPub, in.BoxPub)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	proof, err := hex.DecodeString(in.Proof)
	if err != nil || len(proof) != ed25519.SignatureSize || !ed25519.Verify(sign, nodesig.EnrollMessage(in.Code, in.NodeID, in.SignPub, in.BoxPub), proof) {
		http.Error(w, "proof of the signing key is not valid", http.StatusBadRequest)
		return
	}
	k, err := c.Store.EnrollNode(r.Context(), in)
	switch {
	case errors.Is(err, storage.ErrEnrollmentCode):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, storage.ErrNodeIDTaken):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		reply(w, enrollmentAnswer(k), err)
	}
}

// nodeEnrollmentStatus tells a node, by a request signed with its key, whether its keys are approved.
func (c *Core) nodeEnrollmentStatus(w http.ResponseWriter, r *http.Request, node string) {
	k, err := c.Store.NodeKeyOf(r.Context(), node)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	pub, err := hex.DecodeString(k.SignPub)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<10))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, err := nodesig.Verify(r, ed25519.PublicKey(pub), body, c.Store.Now())
	if err != nil || v.Node != node {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	write(w, enrollmentAnswer(k))
}
