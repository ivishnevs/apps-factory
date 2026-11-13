package worker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/nodesig"
)

const skewNode = "0123456789abcdef0123456789abcdef"

// fakeCore verifies node signatures against its own clock, behind the node's by behind, and refuses a signature
// outside the window like Core: 401 signature_window with core_time_ms and a Date header.
func fakeCore(t *testing.T, pub ed25519.PublicKey, base time.Time, behind time.Duration, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		now := base.Add(-behind)
		if _, err := nodesig.Verify(r, pub, body, now); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Date", now.UTC().Format(http.TimeFormat))
			reason := "signature_invalid"
			if errors.Is(err, nodesig.ErrWindow) {
				reason = "signature_window"
			}
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "reason": reason, "core_time_ms": now.UnixMilli()})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClockCorrection (node-wake-recovery 4.2): with Core's clock 5 minutes behind, the node's first request is
// refused for the window, retried at once with the corrected time and accepted; later requests pass first time.
// Every request kind (JSON, raw post, get) corrects. Beyond MaxClockOffset the node only reports the offset.
func TestClockCorrection(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	base := time.Unix(1_800_000_000, 0)
	ctx := context.Background()
	var calls atomic.Int32
	srv := fakeCore(t, pub, base, 5*time.Minute, &calls)
	clk := new(Clock)
	api := API{URL: srv.URL, Node: skewNode, Key: key, Now: func() time.Time { return base }, Clock: clk}
	if err := api.Do(ctx, "POST", "/nodes/n1/heartbeat", map[string]int{"epoch": 1}, nil); err != nil {
		t.Fatal("not retried with the corrected time:", err)
	}
	if calls.Load() != 2 || clk.Offset() != -5*time.Minute || clk.Observed() != -5*time.Minute {
		t.Fatalf("calls %d, offset %s, observed %s", calls.Load(), clk.Offset(), clk.Observed())
	}
	if err := api.PostRaw(ctx, "/skills/sync-jobs/j/source", "application/x-tar", []byte("tar")); err != nil {
		t.Fatal(err)
	}
	body, err := api.Get(ctx, "/runs/r1/blobs/b")
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	if calls.Load() != 4 {
		t.Fatalf("%d calls: corrected requests were refused again", calls.Load())
	}

	// A copy of the API with a fresh clock learns on its raw post and get too.
	for _, call := range []func(API) error{
		func(a API) error {
			return a.PostRaw(ctx, "/skills/sync-jobs/j/source", "application/x-tar", []byte("tar"))
		},
		func(a API) error {
			b, err := a.Get(ctx, "/runs/r1/blobs/b")
			if err == nil {
				b.Close()
			}
			return err
		},
	} {
		calls.Store(0)
		fresh := api
		fresh.Clock = new(Clock)
		if err := call(fresh); err != nil || calls.Load() != 2 {
			t.Fatalf("calls %d, %v", calls.Load(), err)
		}
	}

	// Two hours apart: not corrected, only reported, and not retried.
	calls.Store(0)
	far := fakeCore(t, pub, base, 2*time.Hour, &calls)
	farClock := new(Clock)
	farAPI := API{URL: far.URL, Node: skewNode, Key: key, Now: func() time.Time { return base }, Clock: farClock}
	var h *HTTPError
	if err := farAPI.Do(ctx, "POST", "/nodes/n1/heartbeat", map[string]int{"epoch": 1}, nil); !errors.As(err, &h) || h.Status != 401 {
		t.Fatal("want the refusal:", err)
	}
	if calls.Load() != 1 || farClock.Offset() != 0 || farClock.Observed() != -2*time.Hour {
		t.Fatalf("calls %d, offset %s, observed %s", calls.Load(), farClock.Offset(), farClock.Observed())
	}

	// Without a Clock the node signs with its own time and does not retry.
	calls.Store(0)
	plain := API{URL: srv.URL, Node: skewNode, Key: key, Now: func() time.Time { return base }}
	if err := plain.Do(ctx, "POST", "/nodes/n1/heartbeat", map[string]int{"epoch": 1}, nil); err == nil || calls.Load() != 1 {
		t.Fatal("a node without a clock retried or passed", calls.Load(), err)
	}
}

// TestClockAccepted: once Core accepts a request signed with the current offset, a past observation beyond the bound
// is over and the heartbeat reports the offset in use.
func TestClockAccepted(t *testing.T) {
	c := new(Clock)
	base := time.Unix(1_800_000_000, 0)
	if c.learn(base.Add(-2*time.Hour), base) || c.Observed() != -2*time.Hour || c.Offset() != 0 {
		t.Fatal("an offset beyond the bound was applied", c.Offset(), c.Observed())
	}
	c.accepted()
	if c.Observed() != 0 {
		t.Fatal("observation kept after an accepted request", c.Observed())
	}
	if !c.learn(base.Add(3*time.Minute), base) || c.learn(base.Add(3*time.Minute), base) {
		t.Fatal("learning the same offset twice must change it once")
	}
	var none *Clock
	if none.Offset() != 0 || none.Observed() != 0 || none.learn(base, base.Add(time.Hour)) {
		t.Fatal("a nil clock corrects")
	}
}
