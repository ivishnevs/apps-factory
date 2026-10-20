package worker

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/redact"
	"appfactory/internal/storage"
)

// hostRecorder records the hosts of every outbound HTTP request of the process.
type hostRecorder struct {
	mu    sync.Mutex
	hosts map[string]int
	next  http.RoundTripper
}

func (h *hostRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.hosts[r.URL.Host]++
	h.mu.Unlock()
	return h.next.RoundTrip(r)
}

// TestJetStreamRunLogRedacted (core-owned-project-data 3.5): a command of a fake run prints a forge token the
// node holds; the node uploads the run's log in chunks through Core's API with the token replaced (and keeps
// it out of the published events and the result too). The only HTTP traffic is the node calling Core: Core
// requests nothing from the node.
func TestJetStreamRunLogRedacted(t *testing.T) {
	nats := os.Getenv("NATS_TEST_URL")
	if nats == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	const token = "forge-token-0123456789abcdef"
	t.Setenv("GH_TOKEN", token)
	rec := &hostRecorder{hosts: map[string]int{}, next: http.DefaultTransport}
	http.DefaultTransport = rec
	defer func() { http.DefaultTransport = rec.next }()
	old := LogChunk
	LogChunk = 64 // several chunks during the run
	defer func() { LogChunk = old }()
	var s *storage.Store
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "pushed with " + token, Activity: []fake.Activity{
		{Exec: []string{"sh", "-c", "echo pushing with " + token}},
		{Method: "fake/note", Data: []byte(`{"text":"plain progress"}`)},
	}}}
	r, events := runDriver(t, nats, Worker{Driver: d}, false, withStore(func(store *storage.Store) { s = store }))
	if r.Outcome != "succeeded" {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	for _, e := range events {
		if strings.Contains(string(e.Payload), token) {
			t.Fatal("token in a published event:", string(e.Payload))
		}
	}
	if strings.Contains(r.Result, token) || !strings.Contains(r.Result, redact.Marker) {
		t.Fatal(r.Result)
	}
	rec.mu.Lock()
	if len(rec.hosts) != 1 {
		t.Fatalf("HTTP traffic beyond the node calling Core: %v", rec.hosts)
	}
	rec.mu.Unlock()
	ctx := context.Background()
	chunks, err := s.RunLogs(ctx, storage.DefaultCompany, "fake-run")
	if err != nil || len(chunks) < 2 || !chunks[len(chunks)-1].Final {
		t.Fatalf("%+v %v", chunks, err)
	}
	var log strings.Builder
	for _, c := range chunks {
		if !c.Redacted {
			t.Fatalf("chunk %d not redacted", c.Seq)
		}
		rc, _, err := s.RunLogChunk(ctx, storage.DefaultCompany, "fake-run", c.Seq)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		log.Write(b)
	}
	if strings.Contains(log.String(), token) || !strings.Contains(log.String(), "pushing with "+redact.Marker) || !strings.Contains(log.String(), "plain progress") {
		t.Fatal(log.String())
	}
	if _, err = s.PutRunLog(ctx, storage.DefaultCompany, "fake-run", "not-the-lease", 99, false, true, strings.NewReader("x")); err == nil {
		t.Fatal("log chunk accepted without the run's lease")
	}
}
