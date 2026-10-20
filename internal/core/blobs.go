package core

import (
	"io"
	"net/http"
	"strconv"

	"appfactory/internal/storage"
)

// Project data travels as blobs (core-owned-project-data): nodes download a run's materialization and upload
// run logs with the run's lease; the run's MCP adapter uploads artifact contents and attachments with the run
// credential (/agent/blobs); the owner reads everything. Core only answers: it never calls a node.

func (c *Core) blobRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /runs/{id}/blobs/{sha256}", func(w http.ResponseWriter, r *http.Request) {
		rc, b, err := c.Store.RunBlob(r.Context(), c.companyFrom(r), r.PathValue("id"), r.Header.Get("X-Factory-Lease"), r.PathValue("sha256"))
		sendBlob(w, rc, b, err)
	})
	m.HandleFunc("GET /blobs/{sha256}", func(w http.ResponseWriter, r *http.Request) {
		rc, b, err := c.Store.OpenBlob(r.Context(), c.companyFrom(r), r.PathValue("sha256"))
		sendBlob(w, rc, b, err)
	})
	m.HandleFunc("GET /projects/{project}/artifacts/{artifact}/{version}/content", func(w http.ResponseWriter, r *http.Request) {
		a, err := c.Store.Artifact(r.Context(), c.companyFrom(r), r.PathValue("project"), r.PathValue("artifact"), r.PathValue("version"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		if a.SHA256 == "" {
			http.Error(w, "artifact version is an external reference without content in Factory: "+a.URI, 404)
			return
		}
		rc, b, err := c.Store.OpenBlob(r.Context(), c.companyFrom(r), a.SHA256)
		sendBlob(w, rc, b, err)
	})
}

func sendBlob(w http.ResponseWriter, rc io.ReadCloser, b storage.Blob, err error) {
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(b.Size, 10))
	w.Header().Set("X-Factory-SHA256", b.SHA256)
	_, _ = io.Copy(w, rc)
}

// agentBlob serves /agent/blobs for a run credential: PUT stores a blob (at most storage.MaxBlob) and answers
// its digest; GET /agent/blobs/{sha256} reads a blob of the run's project (a conflicting document's current
// version during sync).
func (c *Core) agentBlob(w http.ResponseWriter, r *http.Request, run storage.Run) {
	switch {
	case r.Method == http.MethodPut && r.URL.Path == "/agent/blobs":
		b, err := c.Store.PutBlob(r.Context(), c.companyFrom(r), http.MaxBytesReader(w, r.Body, storage.MaxBlob+1), storage.MaxBlob)
		reply(w, b, err)
	case r.Method == http.MethodGet && len(r.URL.Path) == len("/agent/blobs/")+64:
		rc, b, err := c.Store.ProjectBlob(r.Context(), c.companyFrom(r), run.Spec.Project, r.URL.Path[len("/agent/blobs/"):])
		sendBlob(w, rc, b, err)
	default:
		http.Error(w, "not found", 404)
	}
}
