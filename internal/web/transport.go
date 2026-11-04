package web

import (
	"io"
	"net/http"
	"sync"
)

// HandlerTransport serves the web API's requests to Core with Core's own handler in memory (saas-web D1): no
// network hop and no second service. Responses stream through a pipe, so event streams flow as they are written.
type HandlerTransport struct{ H http.Handler }

func (t HandlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := &pipeWriter{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	in := r.Clone(r.Context())
	in.RequestURI = in.URL.RequestURI()
	if in.Body == nil {
		in.Body = http.NoBody
	}
	go func() {
		defer w.finish()
		t.H.ServeHTTP(w, in)
	}()
	<-w.ready
	return &http.Response{StatusCode: w.status, Status: http.StatusText(w.status), Header: w.header, Body: pr, Request: r,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, ContentLength: -1}, nil
}

type pipeWriter struct {
	header http.Header
	pw     *io.PipeWriter
	status int
	once   sync.Once
	ready  chan struct{}
}

func (w *pipeWriter) Header() http.Header { return w.header }

func (w *pipeWriter) WriteHeader(status int) {
	w.once.Do(func() {
		w.status = status
		w.header = w.header.Clone() // the response owns this copy; later changes do not race with the reader
		close(w.ready)
	})
}

func (w *pipeWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

func (w *pipeWriter) Flush() {}

func (w *pipeWriter) finish() {
	w.WriteHeader(http.StatusOK)
	_ = w.pw.Close()
}
