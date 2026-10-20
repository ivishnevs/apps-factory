package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/redact"
)

// Run logs (core-owned-project-data D6): the node keeps the run's runtime event stream and uploads it to
// Core in chunks during the run and finally at its end (PUT /runs/{id}/logs with the run's lease). Before
// anything leaves the node, the credential values it knows (environment secrets, its own token, the run
// credential) and token shapes are replaced; Core never fetches anything from the node.

// LogChunk is the size at which the node uploads a log chunk during the run.
var LogChunk = 256 << 10

type runLog struct {
	api   API
	run   string
	lease string

	mu      sync.Mutex
	red     *redact.Redactor
	secrets []string
	buf     bytes.Buffer
	seq     int64
	final   bool
	uploads sync.WaitGroup // chunks uploading during the run
}

// newRunLog redacts the node's environment secrets and its Core token from the start.
func newRunLog(api API, run, lease string) *runLog {
	l := &runLog{api: api, run: run, lease: lease, secrets: append(redact.FromEnv(os.Environ()), api.Token)}
	l.red = redact.New(l.secrets...)
	return l
}

// secret adds a value learned during the run (the run credential).
func (l *runLog) secret(v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.secrets = append(l.secrets, v)
	l.red = redact.New(l.secrets...)
}

// values are the exact values redacted (handed to the run's MCP adapter for attachments).
func (l *runLog) values() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.red.Values()
}

// clean redacts an event before it is published or logged.
func (l *runLog) clean(p *protocol.Event) {
	l.mu.Lock()
	red := l.red
	l.mu.Unlock()
	if len(p.Data) > 0 {
		if b, changed := red.Bytes(p.Data); changed {
			// A replacement inside a JSON string keeps it valid; anything else is kept as a JSON string.
			if !json.Valid(b) {
				b, _ = json.Marshal(string(b))
			}
			p.Data = b
		}
	}
	if p.Result != "" {
		if b, changed := red.Bytes([]byte(p.Result)); changed {
			p.Result = string(b)
		}
	}
}

// add appends one (already redacted) event line and uploads a full chunk.
func (l *runLog) add(kind string, p protocol.Event) {
	line, _ := json.Marshal(map[string]any{"at": time.Now().UTC(), "kind": kind, "method": p.Method, "data": p.Data, "status": p.Status, "outcome": p.Outcome, "result": p.Result})
	l.mu.Lock()
	l.buf.Write(line)
	l.buf.WriteByte('\n')
	full := l.buf.Len() >= LogChunk
	l.mu.Unlock()
	if full {
		// Uploads during the run never hold up the run's events; the final flush waits for them.
		l.uploads.Add(1)
		go func() {
			defer l.uploads.Done()
			l.flush(false)
		}()
	}
}

// flush uploads the buffered lines as the next chunk; final marks the run's last chunk (sent once, even
// empty, after the chunks still uploading).
func (l *runLog) flush(final bool) {
	if final {
		l.uploads.Wait()
	}
	l.mu.Lock()
	if l.final || l.buf.Len() == 0 && !final {
		l.mu.Unlock()
		return
	}
	b := append([]byte(nil), l.buf.Bytes()...)
	l.buf.Reset()
	l.seq++
	seq := l.seq
	l.final = final
	l.mu.Unlock()
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		if err = l.api.putLog(ctx, l.run, l.lease, seq, final, b); err == nil {
			return
		}
		var h *HTTPError
		if errors.As(err, &h) && h.Status < 500 {
			break // refused (an older Core without logs, a replaced lease): retrying does not help
		}
	}
	slog.Error("run log upload", "run", l.run, "seq", seq, "error", err)
}

// putLog uploads one log chunk; every chunk went through the node's redaction (redacted=1).
func (a API) putLog(ctx context.Context, run, lease string, seq int64, final bool, b []byte) error {
	q := "?redacted=1&seq=" + strconv.FormatInt(seq, 10)
	if final {
		q += "&final=1"
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", strings.TrimRight(a.URL, "/")+"/runs/"+run+"/logs"+q, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("X-Factory-Lease", lease)
	req.Header.Set("Content-Type", "application/x-ndjson")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return &HTTPError{res.StatusCode, strings.TrimSpace(string(msg))}
	}
	return nil
}
