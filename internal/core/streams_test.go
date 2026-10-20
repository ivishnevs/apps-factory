package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// sseFrames parses a recorded event stream into its events: id, event name and data.
type sseFrame struct{ id, event, data string }

func sseFrames(t *testing.T, body string) []sseFrame {
	t.Helper()
	out := []sseFrame{}
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var f sseFrame
		for _, line := range strings.Split(block, "\n") {
			k, v, _ := strings.Cut(line, ": ")
			switch k {
			case "id":
				f.id = v
			case "event":
				f.event = v
			case "data":
				f.data = v
			}
		}
		if f.event != "" {
			out = append(out, f)
		}
	}
	return out
}

// TestRunEventStream: a run's events stream in order with their sequence as id; an ended run's stream ends
// with `event: end`; Last-Event-ID resumes after a sequence, tail starts at the last events; unknown runs 404.
func TestRunEventStream(t *testing.T) {
	h := newExitHarness(t)
	h.create("s", "notes", "demo")
	r := h.start("s")
	for i, method := range []string{"claude/assistant", "claude/user", "factory/command_refused"} {
		e := protocol.Wrap("agent.progress", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Method: method, Data: json.RawMessage(`{"n":` + string(rune('0'+i)) + `}`)})
		if err := h.s.Apply(context.Background(), storage.DefaultCompany, e); err != nil {
			t.Fatal(err)
		}
	}
	h.end(r, "succeeded", "none")

	w := h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream", nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatal(w.Code, w.Body.String())
	}
	frames := sseFrames(t, w.Body.String())
	if len(frames) != 5 || frames[4].event != "end" || frames[4].id != "" || !strings.Contains(frames[4].data, `"status":"ENDED"`) || strings.Contains(w.Body.String(), r.lease) {
		t.Fatalf("%+v", frames)
	}
	methods := []string{}
	for _, f := range frames[:4] {
		var e protocol.Envelope
		var p protocol.Event
		if f.event != "run" || f.id == "" || json.Unmarshal([]byte(f.data), &e) != nil || json.Unmarshal(e.Payload, &p) != nil {
			t.Fatalf("%+v", f)
		}
		methods = append(methods, p.Method+p.Status)
	}
	if strings.Join(methods, ",") != "claude/assistant,claude/user,factory/command_refused,ENDED" {
		t.Fatal(methods)
	}

	// Reconnect after the second event: the rest once, then the end.
	w = h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream", nil, map[string]string{"Last-Event-ID": frames[1].id})
	if got := sseFrames(t, w.Body.String()); len(got) != 3 || got[0].id != frames[2].id || got[2].event != "end" {
		t.Fatalf("%+v", got)
	}
	// tail=1: only the terminal event.
	w = h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream?tail=1", nil, nil)
	if got := sseFrames(t, w.Body.String()); len(got) != 2 || got[0].id != frames[3].id {
		t.Fatalf("%+v", got)
	}
	if w = h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream?tail=0", nil, nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream?after=x", nil, nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = h.do(h.c.Token, "GET", "/runs/nope/events/stream", nil, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	// Owner token only.
	if w = h.do(r.token, "GET", "/runs/"+r.id+"/events/stream", nil, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

// TestRunEventStreamLive: a live run's stream delivers events as they arrive and ends once the run ends.
func TestRunEventStreamLive(t *testing.T) {
	h := newExitHarness(t)
	h.create("l", "notes", "demo")
	r := h.start("l")
	done := make(chan string)
	go func() {
		w := h.do(h.c.Token, "GET", "/runs/"+r.id+"/events/stream", nil, nil)
		done <- w.Body.String()
	}()
	e := protocol.Wrap("agent.progress", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Method: "claude/assistant", Data: json.RawMessage(`{}`)})
	if err := h.s.Apply(context.Background(), storage.DefaultCompany, e); err != nil {
		t.Fatal(err)
	}
	h.end(r, "failed", "none")
	frames := sseFrames(t, <-done)
	if len(frames) != 3 || frames[2].event != "end" || !strings.Contains(frames[2].data, `"outcome":"failed"`) {
		t.Fatalf("%+v", frames)
	}
}

// TestMessageFeedSummary: a message's feed row names its route and body.
func TestMessageFeedSummary(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	m, err := h.c.ownerMessage(ctx, storage.DefaultCompany, "notes", "@manager ship the notes app", "", "say-1")
	if err != nil {
		t.Fatal(err)
	}
	page, err := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{Project: "notes"}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Type == "message.created" && item.EntityID == m.ID {
			if item.Summary != "owner → manager (question): @manager ship the notes app" || item.Actor != "owner" {
				t.Fatalf("%+v", item)
			}
			return
		}
	}
	t.Fatalf("no message.created row: %+v", page.Items)
}
