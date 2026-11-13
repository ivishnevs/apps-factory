package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"appfactory/internal/delivery"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Events is the node's run event spool (http-node-transport D5): every event of a run is appended to
// <dir>/<run>/events.jsonl before it is sent, sent to POST /runs/{run}/events in sequence order, and the spool is
// removed once Core applied the run's terminal event. While Core is unreachable runs go on and events wait; Run
// retries them and resumes the spools a previous node process left.
type Events struct {
	API API
	// Dir holds the spools (<state_dir>/runs); empty keeps them in memory only (tests, a node without state).
	Dir string
	// SendTimeout bounds one send; default 10 s.
	SendTimeout time.Duration

	mu   sync.Mutex
	runs map[string]*runEvents
}

type runEvents struct {
	run, lease string
	path       string

	mu       sync.Mutex // the spool's state
	seq      int64      // last sequence assigned
	pending  []storage.SeqEvent
	terminal bool // the terminal event was appended
	retryAt  time.Time
	backoff  time.Duration
	// failingSince is when the current streak of refused or failed sends began (zero after a delivery); past
	// delivery.Horizon the node reports it in its health instead of dropping the events (node-wake-recovery).
	failingSince time.Time
	lastErr      string

	send sync.Mutex // one send of the run at a time, in order
}

// spoolHeader is the first line of a spool file.
type spoolHeader struct {
	Run   string `json:"run"`
	Lease string `json:"lease"`
}

var spoolRun = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const maxBatchBytes = 900 << 10 // below storage.MaxEventBatchBytes with the batch's own JSON

func (e *Events) timeout() time.Duration {
	if e.SendTimeout > 0 {
		return e.SendTimeout
	}
	return 10 * time.Second
}

func (e *Events) open(run, lease string) (*runEvents, error) {
	if !spoolRun.MatchString(run) {
		return nil, fmt.Errorf("invalid run id %q", run)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runs == nil {
		e.runs = map[string]*runEvents{}
	}
	if r, ok := e.runs[run]; ok {
		if r.lease != lease {
			return nil, fmt.Errorf("run %s spooled with another lease", run)
		}
		return r, nil
	}
	r := &runEvents{run: run, lease: lease}
	if e.Dir != "" {
		r.path = filepath.Join(e.Dir, run, "events.jsonl")
		if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
			return nil, err
		}
		h, _ := json.Marshal(spoolHeader{run, lease})
		if err := os.WriteFile(r.path, append(h, '\n'), 0o600); err != nil {
			return nil, err
		}
	}
	e.runs[run] = r
	return r, nil
}

// Append spools one event of run (built with the run's lease) and sends what the run has pending unless Core was
// unreachable lately (then Run retries). It fails only when the event cannot be spooled.
func (e *Events) Append(ctx context.Context, run, lease string, ev protocol.Envelope, terminal bool) error {
	r, err := e.open(run, lease)
	if err != nil {
		return err
	}
	ev.Payload = fitPayload(ev.Payload)
	r.mu.Lock()
	r.seq++
	se := storage.SeqEvent{Seq: r.seq, Event: ev}
	if r.path != "" {
		line, err := json.Marshal(se)
		if err == nil {
			err = appendLine(r.path, line, terminal)
		}
		if err != nil {
			r.seq--
			r.mu.Unlock()
			return fmt.Errorf("spool event of run %s: %w", run, err)
		}
	}
	r.pending = append(r.pending, se)
	r.terminal = r.terminal || terminal
	failing := time.Now().Before(r.retryAt)
	r.mu.Unlock()
	if !failing {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.timeout())
		defer cancel()
		if err := e.flush(sendCtx, r); err != nil {
			slog.Warn("run events spooled; Core unreachable, retrying", "run", run, "error", err)
		}
	}
	return nil
}

// maxEventPayload bounds one event so it always fits a batch Core accepts (storage.MaxEventBatchBytes): a runtime
// item such as a generated image carried as base64 is larger, and a refused batch drops the run's whole spool,
// its terminal event included, which loses the run.
const maxEventPayload = 256 << 10

// maxEventString is the longest string kept in an oversized payload; longer ones are replaced by a note.
const maxEventString = 16 << 10

// fitPayload returns payload, or, when it is larger than maxEventPayload, the same JSON with its long strings
// replaced by "[omitted: N bytes]"; if that is still too large, only the event's identity fields are kept.
func fitPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) <= maxEventPayload {
		return payload
	}
	var v any
	if json.Unmarshal(payload, &v) == nil {
		if b, err := json.Marshal(shorten(v)); err == nil && len(b) <= maxEventPayload {
			return b
		}
	}
	keep := map[string]any{"omitted_bytes": len(payload)}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"run_id", "lease", "session_id", "method", "status", "outcome"} {
			if x, ok := m[k].(string); ok && len(x) <= maxEventString {
				keep[k] = x
			}
		}
	}
	b, _ := json.Marshal(keep)
	return b
}

func shorten(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxEventString {
			return fmt.Sprintf("[omitted: %d bytes]", len(x))
		}
	case map[string]any:
		for k, e := range x {
			x[k] = shorten(e)
		}
	case []any:
		for i, e := range x {
			x[i] = shorten(e)
		}
	}
	return v
}

func appendLine(path string, line []byte, sync bool) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(line, '\n')); err == nil && sync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// flush sends the run's pending events in batches until none is left or a send fails.
func (e *Events) flush(ctx context.Context, r *runEvents) error {
	r.send.Lock()
	defer r.send.Unlock()
	for {
		r.mu.Lock()
		batch := batchOf(r.pending)
		r.mu.Unlock()
		if len(batch) == 0 {
			return e.finish(r)
		}
		var answer struct {
			AppliedSeq int64 `json:"applied_seq"`
		}
		err := e.API.Do(ctx, "POST", "/runs/"+r.run+"/events", struct {
			Lease  string             `json:"lease"`
			Events []storage.SeqEvent `json:"events"`
		}{r.lease, batch}, &answer)
		var h *HTTPError
		switch {
		case err == nil:
			r.delivered(answer.AppliedSeq)
		case errors.As(err, &h) && h.Status == 409:
			var refusal struct {
				Reason      string `json:"reason"`
				ExpectedSeq int64  `json:"expected_seq"`
			}
			_ = json.Unmarshal([]byte(h.Message), &refusal)
			if refusal.Reason != "gap" || refusal.ExpectedSeq < 1 {
				return e.drop(r, "refused: "+h.Message) // another lease: the run is no longer this node's
			}
			if refusal.ExpectedSeq < batch[0].Seq { // Core lacks events this spool no longer has
				return e.drop(r, fmt.Sprintf("Core expects seq %d, the spool starts at %d", refusal.ExpectedSeq, batch[0].Seq))
			}
			r.delivered(refusal.ExpectedSeq - 1)
		case errors.As(err, &h) && delivery.Classify(h.Status, delivery.Reason(h.Message)) == delivery.Final:
			// Another node's or an unknown run, invalid events, a permanent auth refusal: they never apply.
			return e.drop(r, "refused: "+err.Error())
		default:
			// Core unreachable or busy, or a temporary refusal (a signature outside the time window right after a
			// wake): the spool stays and is resent; a fresh request is signed anew.
			r.mu.Lock()
			r.backoff = min(max(2*r.backoff, time.Second), 30*time.Second)
			r.retryAt = time.Now().Add(r.backoff)
			if r.failingSince.IsZero() {
				r.failingSince = time.Now()
			}
			r.lastErr = err.Error()
			r.mu.Unlock()
			return err
		}
	}
}

// batchOf is the first events of pending that fit one request.
func batchOf(pending []storage.SeqEvent) []storage.SeqEvent {
	size := 0
	for i, e := range pending {
		size += len(e.Event.Payload) + 256
		if i == storage.MaxEventBatch || i > 0 && size > maxBatchBytes {
			return append([]storage.SeqEvent(nil), pending[:i]...)
		}
	}
	return append([]storage.SeqEvent(nil), pending...)
}

// delivered forgets the events up to seq.
func (r *runEvents) delivered(seq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := 0
	for i < len(r.pending) && r.pending[i].Seq <= seq {
		i++
	}
	r.pending = r.pending[i:]
	r.retryAt, r.backoff = time.Time{}, 0
	r.failingSince, r.lastErr = time.Time{}, ""
}

// finish removes the spool of a run whose terminal event Core applied.
func (e *Events) finish(r *runEvents) error {
	r.mu.Lock()
	done := r.terminal && len(r.pending) == 0
	r.mu.Unlock()
	if !done {
		return nil
	}
	return e.forget(r)
}

func (e *Events) drop(r *runEvents, why string) error {
	slog.Error("run events dropped", "run", r.run, "reason", why)
	return e.forget(r)
}

func (e *Events) forget(r *runEvents) error {
	e.mu.Lock()
	if e.runs[r.run] == r {
		delete(e.runs, r.run)
	}
	e.mu.Unlock()
	if r.path != "" {
		// Only the spool: the run's directory may hold other files of the run (file secrets) its owner removes.
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(filepath.Dir(r.path)) // the directory goes when nothing else is in it
	}
	return nil
}

// OverdueCheck is the node self-check that fails while a run's events stay undelivered past delivery.Horizon.
const OverdueCheck = "delivery.events"

// Overdue reports the runs whose events Core has refused or not answered for longer than delivery.Horizon, as a
// failing self-check (node.check_failed on Core) naming them; nil when none is. The events stay spooled.
func (e *Events) Overdue() []node.CheckResult {
	e.mu.Lock()
	runs := make([]*runEvents, 0, len(e.runs))
	for _, r := range e.runs {
		runs = append(runs, r)
	}
	e.mu.Unlock()
	var late []string
	for _, r := range runs {
		r.mu.Lock()
		if !r.failingSince.IsZero() && time.Since(r.failingSince) > delivery.Horizon {
			late = append(late, r.run+" ("+r.lastErr+")")
		}
		r.mu.Unlock()
	}
	if len(late) == 0 {
		return nil
	}
	sort.Strings(late)
	return []node.CheckResult{{Check: OverdueCheck, Status: node.Fail,
		Detail: fmt.Sprintf("%d run(s) with events undelivered for over %s: %s", len(late), delivery.Horizon, strings.Join(late, "; ")),
		Hint:   "Core keeps refusing or not answering; check the node's connection, clock and key"}}
}

// Pending reports how many runs have spooled events Core has not applied yet.
func (e *Events) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.runs)
}

// Run resumes the spools a previous node process left in Dir and sends pending events every interval until ctx
// ends (a run whose last send failed waits for its backoff).
func (e *Events) Run(ctx context.Context, interval time.Duration) {
	if err := e.Resume(); err != nil {
		slog.Error("resuming run event spools", "error", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		e.mu.Lock()
		runs := make([]*runEvents, 0, len(e.runs))
		for _, r := range e.runs {
			runs = append(runs, r)
		}
		e.mu.Unlock()
		for _, r := range runs {
			r.mu.Lock()
			due := len(r.pending) > 0 && !time.Now().Before(r.retryAt)
			r.mu.Unlock()
			if !due {
				continue
			}
			sendCtx, cancel := context.WithTimeout(ctx, e.timeout())
			if err := e.flush(sendCtx, r); err != nil && ctx.Err() == nil {
				slog.Warn("run events still pending", "run", r.run, "error", err)
			}
			cancel()
		}
	}
}

// Resume loads the spool files in Dir that this process does not hold yet.
func (e *Events) Resume() error {
	if e.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(e.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range entries {
		if !d.IsDir() || !spoolRun.MatchString(d.Name()) {
			continue
		}
		path := filepath.Join(e.Dir, d.Name(), "events.jsonl")
		r, err := readSpool(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || r.run != d.Name() {
			slog.Error("unreadable run event spool removed", "path", path, "error", err)
			_ = os.Remove(path)
			continue
		}
		e.mu.Lock()
		if e.runs == nil {
			e.runs = map[string]*runEvents{}
		}
		if _, held := e.runs[r.run]; !held {
			e.runs[r.run] = r
			slog.Info("resuming run event spool", "run", r.run, "events", len(r.pending))
		}
		e.mu.Unlock()
	}
	return nil
}

func readSpool(path string) (*runEvents, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	if !sc.Scan() {
		return nil, errors.New("empty spool")
	}
	var h spoolHeader
	if err = json.Unmarshal(sc.Bytes(), &h); err != nil || h.Run == "" || h.Lease == "" {
		return nil, fmt.Errorf("spool header: %v", err)
	}
	r := &runEvents{run: h.Run, lease: h.Lease, path: path}
	for sc.Scan() {
		var se storage.SeqEvent
		if err = json.Unmarshal(sc.Bytes(), &se); err != nil {
			break // a line cut by a crash ends the spool
		}
		r.pending = append(r.pending, se)
		r.seq = se.Seq
		var p protocol.Event
		if json.Unmarshal(se.Event.Payload, &p) == nil && p.Status != "" {
			r.terminal = true
		}
	}
	return r, nil
}
