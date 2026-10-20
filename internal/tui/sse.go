package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// sseMessage is one server-sent event.
type sseMessage struct {
	ID    int64
	Event string
	Data  []byte
}

// readSSE reads one connection of an event stream: open is called once the stream answered 200, each event
// goes to on. It returns when the stream or ctx ends. after > 0 is sent as Last-Event-ID.
func readSSE(ctx context.Context, client *http.Client, url, token string, after int64, open func(), on func(sseMessage)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	if after > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return &worker.HTTPError{Status: res.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	open()
	return parseSSE(res.Body, on)
}

// parseSSE splits an event stream into events (id, event and data lines; comments ignored).
func parseSSE(r io.Reader, on func(sseMessage)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	var cur sseMessage
	var data []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if cur.Event != "" || len(data) > 0 {
				if cur.Event == "" {
					cur.Event = "message"
				}
				cur.Data = []byte(strings.Join(data, "\n"))
				on(cur)
			}
			cur, data = sseMessage{}, nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			cur.ID, _ = strconv.ParseInt(value, 10, 64)
		case "event":
			cur.Event = value
		case "data":
			data = append(data, value)
		}
	}
	return sc.Err()
}

func decodeFeed(b []byte) (storage.FeedItem, error) {
	var item storage.FeedItem
	err := json.Unmarshal(b, &item)
	return item, err
}

func decodeEnvelope(b []byte) (protocol.Envelope, error) {
	var e protocol.Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("run event: %w", err)
	}
	return e, nil
}

func endOutcome(b []byte) string {
	var v struct {
		Outcome string `json:"outcome"`
	}
	_ = json.Unmarshal(b, &v)
	return v.Outcome
}
