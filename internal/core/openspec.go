package core

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"appfactory/internal/factorytools"
	"appfactory/internal/openspec"
	"appfactory/internal/storage"
)

func openSpecReply(w http.ResponseWriter, v any, e error) {
	if e == nil {
		write(w, v)
		return
	}
	status := 400
	if errors.Is(e, os.ErrNotExist) || errors.Is(e, storage.ErrNotFound) {
		status = 404
	}
	if errors.Is(e, os.ErrPermission) {
		status = 403
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
func (c *Core) openSpecRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /projects/{project}/openspec/init", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, e := c.Store.InitOpenSpec(r.Context(), r.PathValue("project"), "owner", "", p.RequestID)
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/list", func(w http.ResponseWriter, r *http.Request) {
		p, e := c.Store.Project(r.Context(), r.PathValue("project"))
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		limit, e := integerQuery(r, "limit", 20)
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		v, e := c.Store.OpenSpec.List(p.Workspace, r.URL.Query().Get("dir"), r.URL.Query().Get("after"), limit)
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/read", func(w http.ResponseWriter, r *http.Request) {
		p, e := c.Store.Project(r.Context(), r.PathValue("project"))
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
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
		v, e := c.Store.OpenSpec.Read(p.Workspace, r.URL.Query().Get("path"), offset, limit, r.URL.Query().Get("sha256"))
		openSpecReply(w, v, e)
	})
	m.HandleFunc("GET /projects/{project}/openspec/check/{change}", func(w http.ResponseWriter, r *http.Request) {
		p, e := c.Store.Project(r.Context(), r.PathValue("project"))
		if e != nil {
			openSpecReply(w, nil, e)
			return
		}
		v, e := c.Store.OpenSpec.CheckChange(p.Workspace, r.PathValue("change"))
		openSpecReply(w, v, e)
	})
}
func (c *Core) openSpecTool(w http.ResponseWriter, r *http.Request, call factorytools.Call, run storage.Run) {
	args := call.Arguments
	if call.Name == "factory_openspec_init" {
		v, e := c.Store.InitOpenSpec(r.Context(), run.Spec.Project, run.Spec.Agent, run.Spec.ID, stringArg(args, "request_id"))
		openSpecReply(w, v, e)
		return
	}
	// Resolve from the run snapshot, never from model-provided project/workspace arguments.
	workspace := run.Spec.Workspace
	switch call.Name {
	case "factory_openspec_list":
		limit := 10
		if x, ok := args["limit"].(float64); ok {
			if x < 1 || x > 20 {
				openSpecReply(w, nil, fmt.Errorf("limit must be 1..20"))
				return
			}
			limit = int(x)
		}
		v, e := c.Store.OpenSpec.List(workspace, stringArg(args, "dir"), stringArg(args, "after"), limit)
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
		v, e := c.Store.OpenSpec.Read(workspace, stringArg(args, "path"), offset, limit, stringArg(args, "sha256"))
		openSpecReply(w, v, e)
	case "factory_openspec_check":
		v, e := c.Store.OpenSpec.CheckChange(workspace, stringArg(args, "change"))
		openSpecReply(w, v, e)
	default:
		openSpecReply(w, nil, fmt.Errorf("unknown OpenSpec tool"))
	}
}
