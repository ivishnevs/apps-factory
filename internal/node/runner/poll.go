package runner

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/worker"
)

// The node's long poll (http-node-transport D2, D3): free slots go to Core, offers come back and go to idle slots,
// stops, resets and cleanups are handled as from the heartbeat answer, a hooks change asks for a heartbeat now.

type pollRequest struct {
	Epoch       int64          `json:"epoch"`
	Free        map[string]int `json:"free"`
	WaitSeconds int            `json:"wait_seconds"`
	Skip        []string       `json:"skip,omitempty"`
	Hooks       string         `json:"hooks,omitempty"`
	HooksHash   string         `json:"hooks_hash,omitempty"`
}

type pollAnswer struct {
	Offers []struct {
		RunID    string `json:"run_id"`
		Provider string `json:"provider"`
	} `json:"offers"`
	Stop         []stop    `json:"stop"`
	Reset        []string  `json:"reset"`
	Cleanup      []string  `json:"cleanup"`
	HooksChanged bool      `json:"hooks_changed"`
	SyncJobs     []syncJob `json:"sync_jobs"`
}

// skips are runs whose claim Core refused for now; the poll asks Core not to offer them until their time passed.
type skips struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (k *skips) add(run string, d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.until == nil {
		k.until = map[string]time.Time{}
	}
	k.until[run] = time.Now().Add(d)
}

// soonest is how long until the first skip ends (0 when none).
func (k *skips) soonest() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	var d time.Duration
	now := time.Now()
	for _, t := range k.until {
		if left := t.Sub(now); left > 0 && (d == 0 || left < d) {
			d = left
		}
	}
	return d
}

func (k *skips) list() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	var out []string
	for run, t := range k.until {
		if now.Before(t) {
			out = append(out, run)
		} else {
			delete(k.until, run)
		}
	}
	return out
}

// pollBackoff is the longest pause between polls after errors.
var pollBackoff = 30 * time.Second

// poll runs the node's long poll until ctx ends.
func (s *session) poll(ctx context.Context, pool *Pool) {
	var skipped skips
	pool.Refused = func(run string) { skipped.add(run, worker.RetryDelay) }
	var backoff time.Duration
	pause := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	for ctx.Err() == nil {
		if s.switching.Load() { // re-registering: the new epoch polls
			if !pause(200 * time.Millisecond) {
				return
			}
			continue
		}
		free := map[string]int{}
		if s.claimable() {
			free = pool.Free()
		}
		wait := s.cfg.Poll.WaitSeconds
		if wait <= 0 {
			wait = node.MaxPollWait
		}
		// Runs the slots hold are skipped too: a run whose claim is still in flight is queued at Core and would be
		// offered again to another slot, whose claim Core then drops as a duplicate.
		skip := skipped.list()
		for _, sl := range pool.Status() {
			if sl.Busy && sl.Run != "" {
				skip = append(skip, sl.Run)
			}
		}
		if left := skipped.soonest(); left > 0 && len(free) > 0 {
			// A skipped run is offered again once its skip ends: do not wait past that.
			wait = max(1, min(wait, int((left+time.Second-1)/time.Second)))
		}
		req := pollRequest{Epoch: s.inv.Epoch(), Free: free, WaitSeconds: wait, Skip: skip, Hooks: s.cfg.Hooks, HooksHash: s.hooksHash()}
		reqCtx, cancel := context.WithTimeout(ctx, time.Duration(req.WaitSeconds)*time.Second+15*time.Second)
		// A slot freed while this poll waits makes it stale (it told Core fewer free slots): poll again at once.
		var redo atomic.Bool
		stop := make(chan struct{})
		go func() {
			select {
			case <-pool.Freed():
				redo.Store(true)
				cancel()
			case <-stop:
			}
		}()
		var a pollAnswer
		err := s.api.Do(reqCtx, "POST", "/nodes/"+s.inv.NodeID+"/poll", req, &a)
		close(stop)
		cancel()
		if err != nil && redo.Load() {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			backoff = min(max(2*backoff, time.Second), pollBackoff)
			var h *worker.HTTPError
			if !errors.As(err, &h) || h.Status != 404 { // 404: the heartbeat registers the node again
				slog.Warn("node poll failed", "error", err, "retry_in", backoff.String())
			}
			if !pause(backoff + rand.N(backoff/2+1)) {
				return
			}
			continue
		}
		backoff = 0
		s.stopRuns(a.Stop, "poll")
		s.queueResets(a.Reset)
		s.queueCleanups(a.Cleanup)
		if a.HooksChanged {
			s.driversChanged() // the heartbeat delivers the NodeHooks document
		}
		for _, j := range a.SyncJobs {
			s.startSyncJob(ctx, j)
		}
		taken := 0
		for _, o := range a.Offers {
			if pool.Offer(Offer{RunID: o.RunID, Provider: o.Provider}) {
				taken++
			}
		}
		if len(a.Offers) > 0 && taken == 0 && !pause(200*time.Millisecond) { // the slots filled meanwhile
			return
		}
	}
}
