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
	// Run logs: the node uploads redacted chunks with the run's lease; the owner lists and reads them.
	m.HandleFunc("PUT /runs/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq, err := strconv.ParseInt(q.Get("seq"), 10, 64)
		if err != nil {
			http.Error(w, "seq required", 400)
			return
		}
		v, err := c.Store.PutRunLog(r.Context(), c.companyFrom(r), r.PathValue("id"), r.Header.Get("X-Factory-Lease"), seq, q.Get("final") == "1", q.Get("redacted") == "1", http.MaxBytesReader(w, r.Body, storage.MaxBlob+1))
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.RunLogs(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/{id}/logs/{seq}", func(w http.ResponseWriter, r *http.Request) {
		seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
		if err != nil {
			http.Error(w, "seq required", 400)
			return
		}
		rc, b, err := c.Store.RunLogChunk(r.Context(), c.companyFrom(r), r.PathValue("id"), seq)
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
