package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"appfactory/internal/factorytools"
	"appfactory/internal/openspec"
	"appfactory/internal/storage"
)

// maxDocumentBody bounds a request carrying one OpenSpec document (1 MiB of UTF-8, JSON-escaped).
const maxDocumentBody = 8 << 20

func openSpecReply(w http.ResponseWriter, v any, e error) {
	if e == nil {
		write(w, v)
		return
	}
	// A compare-and-set conflict names the document's current digest, so the caller can re-read and merge.
	var c *openspec.ConflictError
	if errors.As(e, &c) {
		jsonStatus(w, 409, map[string]string{"reason": "conflict", "path": c.Path, "current_sha256": c.Current, "error": c.Error()})
		return
	}
	status := 400
	if errors.Is(e, storage.ErrNotFound) {
		status = 404
	}
	if errors.Is(e, openspec.ErrConflict) || errors.Is(e, storage.ErrConflict) {
		status = 409
	}
	http.Error(w, e.Error(), status)
}
func integerQuery(r *http.Request, key string, def int) (int, error) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

// documentChange is the body of a write, move or delete; BaseSHA256 empty means the target must not exist.
type documentChange struct {
	Path       string `json:"path"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	BaseSHA256 string `json:"base_sha256"`
	Content    string `json:"content,omitempty"`
}

func (c *Core) openSpecRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /projects/{project}/openspec/init", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, e := c.Store.InitOpenSpec(r.Context(), c.companyFrom(r), r.PathValue("project"), "owner", "", p.RequestID)
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/list", func(w http.ResponseWriter, r *http.Request) {
		limit, e := integerQuery(r, "limit", 20)
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		v, e := c.Store.ListOpenSpec(r.Context(), c.companyFrom(r), r.PathValue("project"), r.URL.Query().Get("dir"), r.URL.Query().Get("after"), limit)
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/read", func(w http.ResponseWriter, r *http.Request) {
		offset, e := integerQuery(r, "offset", 0)
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		limit, e := integerQuery(r, "limit", 8192)
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		v, e := c.Store.ReadOpenSpec(r.Context(), c.companyFrom(r), r.PathValue("project"), r.URL.Query().Get("path"), offset, limit, r.URL.Query().Get("sha256"))
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/check/{change}", func(w http.ResponseWriter, r *http.Request) {
		v, e := c.Store.CheckOpenSpec(r.Context(), c.companyFrom(r), r.PathValue("project"), r.PathValue("change"))
		openSpecReply(w, v, e)
	})
	// The whole current tree by digest (factory openspec import compares against it).
	m.HandleFunc("GET /projects/{project}/openspec/manifest", func(w http.ResponseWriter, r *http.Request) {
		v, e := c.Store.OpenSpecManifest(r.Context(), c.companyFrom(r), r.PathValue("project"))
		openSpecReply(w, v, e)
	})
	for _, op := range []string{"write", "move", "delete"} {
		m.HandleFunc("POST /projects/{project}/openspec/"+op, func(w http.ResponseWriter, r *http.Request) {
			var p documentChange
			if !decodeMax(w, r, &p, maxDocumentBody) {
				return
			}
			v, e := c.changeOpenSpec(r, op, r.PathValue("project"), "owner", "", p)
			openSpecReply(w, v, e)
		})
	}
}

func (c *Core) changeOpenSpec(r *http.Request, op, project, actor, run string, p documentChange) (any, error) {
	in := storage.OpenSpecChange{Path: p.Path, Base: p.BaseSHA256, Content: []byte(p.Content), Actor: actor, RunID: run}
	switch op {
	case "write":
		return c.Store.WriteOpenSpec(r.Context(), c.companyFrom(r), project, in)
	case "delete":
		return c.Store.DeleteOpenSpec(r.Context(), c.companyFrom(r), project, in)
	default:
		in.From, in.Path = p.From, p.To
		return c.Store.MoveOpenSpec(r.Context(), c.companyFrom(r), project, in)
	}
}

func (c *Core) openSpecTool(w http.ResponseWriter, r *http.Request, call factorytools.Call, run storage.Run) {
	args := call.Arguments
	// Resolve from the run snapshot, never from model-provided project/workspace arguments.
	project := run.Spec.Project
	switch call.Name {
	case "factory_openspec_init":
		v, e := c.Store.InitOpenSpec(r.Context(), c.companyFrom(r), project, run.Spec.Agent, run.Spec.ID, stringArg(args, "request_id"))
		openSpecReply(w, v, e)
	case "factory_openspec_list":
		limit := 10
		if x, ok := args["limit"].(float64); ok {
			if x < 1 || x > 20 {
				openSpecReply(w, nil, fmt.Errorf("limit must be 1..20"))
				return
			}
			limit = int(x)
		}
		v, e := c.Store.ListOpenSpec(r.Context(), c.companyFrom(r), project, stringArg(args, "dir"), stringArg(args, "after"), limit)
		openSpecReply(w, v, e)
	case "factory_openspec_read":
		offset, limit := 0, 8192
		if x, ok := args["offset"].(float64); ok {
			if x < 0 || x > openspec.MaxDocument {
				openSpecReply(w, nil, fmt.Errorf("invalid offset"))
				return
			}
			offset = int(x)
		}
		if x, ok := args["limit"].(float64); ok {
			if x < 4 || x > 8192 {
				openSpecReply(w, nil, fmt.Errorf("limit must be 4..8192"))
				return
			}
			limit = int(x)
		}
		v, e := c.Store.ReadOpenSpec(r.Context(), c.companyFrom(r), project, stringArg(args, "path"), offset, limit, stringArg(args, "sha256"))
		openSpecReply(w, v, e)
	case "factory_openspec_check":
		v, e := c.Store.CheckOpenSpec(r.Context(), c.companyFrom(r), project, stringArg(args, "change"))
		openSpecReply(w, v, e)
	case "factory_openspec_write", "factory_openspec_move", "factory_openspec_delete":
		p := documentChange{Path: stringArg(args, "path"), From: stringArg(args, "from"), To: stringArg(args, "to"), BaseSHA256: stringArg(args, "base_sha256"), Content: stringArg(args, "content")}
		v, e := c.changeOpenSpec(r, call.Name[len("factory_openspec_"):], project, run.Spec.Agent, run.Spec.ID, p)
		openSpecReply(w, v, e)
	case "factory_openspec_sync":
		openSpecReply(w, nil, fmt.Errorf("factory_openspec_sync runs in the node's Factory MCP adapter, not in Core"))
	default:
		openSpecReply(w, nil, fmt.Errorf("unknown OpenSpec tool"))
	}
}
