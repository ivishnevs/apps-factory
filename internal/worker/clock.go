package worker

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"appfactory/internal/delivery"
)

// MaxClockOffset bounds the offset a node signs with (node-wake-recovery, owner decision 1): a clock further apart
// is not corrected, only reported, and the owner fixes the clock.
const MaxClockOffset = time.Hour

// Clock is a node's offset to Core's clock, learned from Core's signature_window refusals (node-wake-recovery). After
// the host of Core or of the node wakes up, their clocks can be minutes apart until one catches up; Core refuses
// signatures created more than nodesig.Window from its own time and answers its time, so the node signs later
// requests with the corrected time instead of losing its heartbeats. Replay protection is unchanged: nonces and the
// window around the corrected time. One Clock is shared by every copy of a node's API; the zero value signs with
// the node's own time.
type Clock struct {
	mu sync.Mutex
	// offset is added to the node's time when it signs; observed is Core's clock minus the node's as last learned,
	// which may exceed MaxClockOffset (then offset keeps its last value).
	offset, observed time.Duration
}

// Offset is what the node adds to its time when it signs.
func (c *Clock) Offset() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// Observed is Core's clock minus the node's as last learned; the heartbeat reports it (health.clock_offset_ms).
func (c *Clock) Observed() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed
}

// learn records Core's time core from a signature_window refusal received at the node's time local and reports
// whether the signing offset changed, so a retry signed now can pass.
func (c *Clock) learn(core, local time.Time) bool {
	if c == nil || core.IsZero() {
		return false
	}
	off := core.Sub(local).Round(time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observed = off
	if off.Abs() > MaxClockOffset {
		slog.Error("Core's clock is too far from this node's to correct; fix the clocks of Core's host and this machine", "offset", off, "max", MaxClockOffset)
		return false
	}
	if off == c.offset {
		return false
	}
	slog.Warn("signing with Core's time: the clocks are apart", "offset", off, "was", c.offset)
	c.offset = off
	return true
}

// accepted records that Core accepted a request signed with the current offset: the clocks agree within the window
// around it, so a past observation beyond the bound is over.
func (c *Clock) accepted() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observed = c.offset
}

// coreTime reads Core's time from a signature_window refusal: core_time_ms in the body, else the Date header. It
// returns the zero time for any other answer.
func coreTime(res *http.Response, body []byte) time.Time {
	if res.StatusCode != http.StatusUnauthorized || delivery.Reason(string(body)) != "signature_window" {
		return time.Time{}
	}
	var v struct {
		CoreTimeMS int64 `json:"core_time_ms"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(string(body))), &v) == nil && v.CoreTimeMS > 0 {
		return time.UnixMilli(v.CoreTimeMS)
	}
	if t, err := http.ParseTime(res.Header.Get("Date")); err == nil {
		return t
	}
	return time.Time{}
}

// send sends a request built by build, signed with the node's corrected time. When Core refuses the signature for
// the time window and the node learns a new offset from the answer, it retries at once, once. An answer of 300 or
// more is returned as *HTTPError.
func (a API) send(build func() (*http.Request, error), body []byte, client *http.Client) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		if err = a.authorize(req, body); err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode < 300 {
			if a.signs() {
				a.Clock.accepted()
			}
			return res, nil
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if attempt == 0 && a.signs() && a.Clock.learn(coreTime(res, b), a.now()) && req.Context().Err() == nil {
			continue
		}
		return nil, &HTTPError{res.StatusCode, string(b)}
	}
}

// signs reports whether the client signs its requests (a node), not only presents a token (the owner CLI).
func (a API) signs() bool { return a.Key != nil || a.CurrentKey != nil }

func (a API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
