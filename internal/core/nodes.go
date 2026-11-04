package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/nodesig"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Runtime protocol v2 routes (docs/runtime-protocol-v2.md). Nodes sign every request (node-identity D4) and every
// request names its node_id. Refusals a node acts on are JSON.
func (c *Core) nodeRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /nodes/register", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			storage.NodeRegistration
			// Hooks names the catalog's NodeHooks document of the node (node.yaml hooks; empty is default).
			Hooks string `json:"hooks,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.RegisterNode(r.Context(), c.companyFrom(r), p.NodeRegistration)
		nodeReply(w, struct {
			storage.NodeRegistered
			Hooks protocol.NodeHooksDelivery `json:"hooks"`
		}{v, config.DeliverNodeHooks(c.cat(c.companyFrom(r)).NodeHooks, p.Hooks)}, err)
	})
	m.HandleFunc("POST /nodes/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			storage.NodeHeartbeat
			Hooks     string `json:"hooks,omitempty"`
			HooksHash string `json:"hooks_hash,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.NodeHeartbeat(r.Context(), c.companyFrom(r), r.PathValue("id"), p.NodeHeartbeat)
		// The NodeHooks document again only when the node applied another version (the catalog changed).
		var hooks *protocol.NodeHooksDelivery
		if d := config.DeliverNodeHooks(c.cat(c.companyFrom(r)).NodeHooks, p.Hooks); d.Hash != p.HooksHash {
			hooks = &d
		}
		nodeReply(w, struct {
			storage.HeartbeatAnswer
			Hooks *protocol.NodeHooksDelivery `json:"hooks,omitempty"`
		}{v, hooks}, err)
	})
	m.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		nodes, err := c.Store.Nodes(r.Context(), c.companyFrom(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		use, err := c.Store.NodesInUse(r.Context(), c.companyFrom(r))
		// Each node with in_use: the runs and resources its current epoch's unended runs hold (factory status).
		type nodeView struct {
			storage.Node
			InUse storage.NodeUse `json:"in_use"`
		}
		out := make([]nodeView, 0, len(nodes))
		for _, n := range nodes {
			u := use[n.NodeID]
			if u.Resources == nil {
				u.Resources = map[string]int{}
			}
			out = append(out, nodeView{n, u})
		}
		reply(w, out, err)
	})
	m.HandleFunc("POST /runs/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Lease string `json:"lease"`
		}
		if !decode(w, r, &p) {
			return
		}
		status, err := c.Store.StartRun(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Lease)
		reply(w, map[string]string{"status": status}, err)
	})
	m.HandleFunc("POST /runs/{id}/claim", c.claim)
	// Owner: removing a replaced machine.
	m.HandleFunc("DELETE /nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.RemoveNode(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
}

// maxSignedBody bounds what the node gateway reads to check a signature: the largest node request is a log chunk.
const maxSignedBody = 16 << 20

// nodeRoute reports whether a request is for a route only a node calls (node-identity D4). Bearer tokens never reach
// these routes.
func nodeRoute(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	post := r.Method == http.MethodPost
	switch {
	case post && len(parts) == 2 && parts[0] == "nodes" && parts[1] == "register":
	case post && len(parts) == 3 && parts[0] == "nodes" && (parts[2] == "heartbeat" || parts[2] == "keys" || parts[2] == "poll"):
	case post && len(parts) == 3 && parts[0] == "runs" && slices.Contains([]string{"claim", "start", "workspace", "credential", "project-setup", "events"}, parts[2]):
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "runs" && parts[2] == "blobs":
	case r.Method == http.MethodPut && len(parts) == 3 && parts[0] == "runs" && parts[2] == "logs":
	default:
		return false
	}
	return true
}

// nodeGateway serves a request signed by a node (node-identity D4): only with its active key, once per nonce, only
// the routes a node calls and only for that node, in the company of its key.
func (c *Core) nodeGateway(w http.ResponseWriter, r *http.Request, m *http.ServeMux) {
	unauthorized := func() { http.Error(w, "unauthorized", http.StatusUnauthorized) }
	id, err := nodesig.KeyID(r)
	if err != nil {
		unauthorized()
		return
	}
	key, err := c.Store.NodeKeyOf(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		slog.Warn("request signed by an unknown node key", "node_id", id)
		unauthorized()
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pub, err := hex.DecodeString(key.SignPub)
	if err != nil || key.Status != storage.NodeKeyActive {
		unauthorized()
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSignedBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	refuse := func(reason string) {
		if c.signatures.refusalDue(id, c.Store.Now()) {
			if err := c.Store.RecordNodeSignatureRefused(r.Context(), key.Company, id, reason); err != nil {
				slog.Error("record refused signature", "node_id", id, "error", err)
			}
		}
		unauthorized()
	}
	now := c.Store.Now()
	v, err := nodesig.Verify(r, ed25519.PublicKey(pub), body, now)
	switch {
	case err != nil:
		refuse(err.Error())
		return
	case v.Created.Before(c.signatures.started(now)):
		// Nonces live in memory, so what was signed before this Core started cannot be told from a replay.
		refuse("created before Core started")
		return
	case !c.signatures.firstUse(id, v.Nonce, now):
		refuse("replay")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, key.Company))
	forbid := func(why string) { http.Error(w, "node: "+why, http.StatusForbidden) }
	// bodyNode is the body's node_id.
	bodyNode := func() string {
		var p struct {
			NodeID string `json:"node_id"`
		}
		_ = json.Unmarshal(body, &p)
		return p.NodeID
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
	case !nodeRoute(r):
		forbid("route not open to nodes")
		return
	case parts[0] == "nodes" && parts[1] == "register":
		if bodyNode() != id {
			forbid("register your own node")
			return
		}
	case parts[0] == "nodes":
		if parts[1] != id {
			forbid("another node")
			return
		}
	case parts[2] == "claim":
		if bodyNode() != id {
			forbid("claim naming your own node required")
			return
		}
	default:
		// Project data of the node's runs (core-owned-project-data D3, D6); the run's lease is checked too.
		node, err := c.Store.RunNode(r.Context(), key.Company, parts[1])
		if err != nil {
			reply(w, nil, err)
			return
		}
		if node != id {
			forbid("run of another node")
			return
		}
	}
	m.ServeHTTP(w, r)
}

// signatureState is the node gateway's memory: nonces for the signature window on both sides of Core's clock, and
// when each node's refusal last reached the feed.
type signatureState struct {
	mu       sync.Mutex
	start    time.Time
	nonces   map[string]time.Time
	refusals map[string]time.Time
}

// started is the time from which Core accepts signatures; the first call sets it.
func (s *signatureState) started(now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.start.IsZero() {
		s.start = now.Truncate(time.Second)
	}
	return s.start
}

// firstUse records a nonce and says whether it is new. A nonce must outlive the whole ±window span.
func (s *signatureState) firstUse(node, nonce string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nonces == nil {
		s.nonces = map[string]time.Time{}
	}
	key := node + "/" + nonce
	if until, seen := s.nonces[key]; seen && now.Before(until) {
		return false
	}
	if len(s.nonces) >= 1<<16 {
		for k, until := range s.nonces {
			if !now.Before(until) {
				delete(s.nonces, k)
			}
		}
	}
	s.nonces[key] = now.Add(2 * nodesig.Window)
	return true
}

// refusalDue limits node.signature_refused to one event per node per minute.
func (s *signatureState) refusalDue(node string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refusals == nil {
		s.refusals = map[string]time.Time{}
	}
	if until, ok := s.refusals[node]; ok && now.Before(until) {
		return false
	}
	s.refusals[node] = now.Add(time.Minute)
	return true
}

// claim serves a node's {node_id, epoch, claim_id, slot};
// both get the claim answer (lease, RunSpec with the brief, brief window) or a JSON refusal.
func (c *Core) claim(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var p storage.ClaimRequest
	if !decode(w, r, &p) {
		return
	}
	v, err := c.Store.ClaimV2(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
	nodeReply(w, v, err)
}

// nodeReply answers a node: refusals and epoch conflicts as JSON so the node can act on the reason.
func nodeReply(w http.ResponseWriter, v any, err error) {
	var refusal *storage.Refusal
	switch {
	case errors.As(err, &refusal):
		jsonStatus(w, http.StatusConflict, refusal)
	case errors.Is(err, storage.ErrStaleEpoch):
		jsonStatus(w, http.StatusConflict, map[string]string{"reason": "stale_epoch"})
	case errors.Is(err, storage.ErrUnknownNode):
		jsonStatus(w, http.StatusNotFound, map[string]string{"reason": "unknown_node"})
	default:
		reply(w, v, err)
	}
}

func jsonStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
