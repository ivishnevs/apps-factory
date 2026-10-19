package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Runtime protocol v2 routes (docs/runtime-protocol-v2.md). Nodes authenticate with the worker token
// like v1 workers; every request names its node_id. Refusals a node acts on are JSON with a reason.
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
		}{v, config.DeliverNodeHooks(c.NodeHooks, p.Hooks)}, err)
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
		if d := config.DeliverNodeHooks(c.NodeHooks, p.Hooks); d.Hash != p.HooksHash {
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
	// Owner: per-machine node tokens (node-agent 1.1) and removing a replaced machine.
	m.HandleFunc("POST /node-tokens", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name   string `json:"name"`
			NodeID string `json:"node_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.CreateNodeToken(r.Context(), c.companyFrom(r), p.Name, p.NodeID)
		reply(w, v, err)
	})
	m.HandleFunc("GET /node-tokens", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.NodeTokens(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /node-tokens/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.RevokeNodeToken(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("DELETE /nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.RemoveNode(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
}

// nodeGateway serves a request authenticated by a node token: only the routes a node calls, only for the
// node the token is bound to (register binds an unbound token to its node_id), in the token's company.
func (c *Core) nodeGateway(w http.ResponseWriter, r *http.Request, m *http.ServeMux, token string) {
	t, err := c.Store.AuthenticateNodeToken(r.Context(), token)
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, t.Company))
	forbid := func(why string) { http.Error(w, "node token: "+why, 403) }
	// bodyNode reads the body's node_id and puts the body back for the route.
	bodyNode := func() (string, bool) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return "", false
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var p struct {
			NodeID string `json:"node_id"`
		}
		_ = json.Unmarshal(body, &p)
		return p.NodeID, true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	post := r.Method == http.MethodPost
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
	case post && len(parts) == 2 && parts[0] == "nodes" && parts[1] == "register":
		node, ok := bodyNode()
		if !ok {
			return
		}
		switch {
		case node == "":
			forbid("node_id required")
			return
		case t.NodeID == "":
			if err = c.Store.BindNodeToken(r.Context(), t.Company, t.ID, node); errors.Is(err, storage.ErrNodeTokenBound) {
				forbid("bound to another node")
				return
			} else if err != nil {
				reply(w, nil, err)
				return
			}
		case t.NodeID != node:
			forbid("bound to another node")
			return
		}
	case t.NodeID == "":
		forbid("not bound to a node yet: register first")
		return
	case post && len(parts) == 3 && parts[0] == "nodes" && parts[2] == "heartbeat":
		if parts[1] != t.NodeID {
			forbid("bound to another node")
			return
		}
	case post && len(parts) == 3 && parts[0] == "runs" && parts[2] == "claim":
		node, ok := bodyNode()
		if !ok {
			return
		}
		if node != t.NodeID {
			forbid("v2 claim of the token's node required")
			return
		}
	case post && len(parts) == 3 && parts[0] == "runs" && slices.Contains([]string{"start", "workspace", "credential", "heartbeat"}, parts[2]):
		node, err := c.Store.RunNode(r.Context(), t.Company, parts[1])
		if err != nil {
			reply(w, nil, err)
			return
		}
		if node != t.NodeID {
			forbid("run of another node")
			return
		}
	default:
		forbid("route not open to nodes")
		return
	}
	m.ServeHTTP(w, r)
}

// claim serves both protocols: a v1 worker sends no body and gets {lease}; a v2 node sends
// {node_id, epoch, claim_id, slot} and gets the claim answer or a JSON refusal.
func (c *Core) claim(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		token, err := c.Store.Claim(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, map[string]string{"lease": token}, err)
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
