package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"appfactory/internal/storage"
)

// Runtime protocol v2 routes (docs/runtime-protocol-v2.md). Nodes authenticate with the worker token
// like v1 workers; every request names its node_id. Refusals a node acts on are JSON with a reason.
func (c *Core) nodeRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /nodes/register", func(w http.ResponseWriter, r *http.Request) {
		var p storage.NodeRegistration
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.RegisterNode(r.Context(), c.companyFrom(r), p)
		nodeReply(w, v, err)
	})
	m.HandleFunc("POST /nodes/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var p storage.NodeHeartbeat
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.NodeHeartbeat(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		nodeReply(w, v, err)
	})
	m.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Nodes(r.Context(), c.companyFrom(r))
		reply(w, v, err)
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
