package core

import (
	"net/http"

	"appfactory/internal/storage"
)

// uploadRoutes: a person uploads an image before naming it in a message or a comment (people-attachments). The
// body is the file; ?name= is its file name. The answer is the attachment to name ({path, sha256, size, preview}).
func (c *Core) uploadRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /uploads", func(w http.ResponseWriter, r *http.Request) {
		blobTransfer(w)
		v, err := c.Store.PutUpload(r.Context(), c.companyFrom(r), "", r.URL.Query().Get("name"), http.MaxBytesReader(w, r.Body, storage.MaxPersonUpload+1))
		reply(w, v, err)
	})
}
