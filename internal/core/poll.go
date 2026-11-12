package core

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"sync"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/storage"
)

// HTTP node transport (http-node-transport D2–D6): a node's long poll answers offers of queued runs and the node
// actions Postgres holds as soon as there are any; run events arrive in ordered batches.

// MaxPollWait bounds a poll's wait, below the 60 s read timeout of common proxies (D2).
const MaxPollWait = 25 * time.Second

// pollRecheck is how often a waiting poll re-reads Postgres, for a missed wake-up or another Core process (D6).
var pollRecheck = 2 * time.Second

// merged is a and b in one map (b's entries under the same key win), for validating both free maps.
func merged(a, b map[string]int) map[string]int {
	out := make(map[string]int, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

// PollRequest is the body of POST /nodes/{node_id}/poll.
type PollRequest struct {
	Epoch int64 `json:"epoch"`
	// Free is the node's free slots per provider; offers never exceed them.
	Free map[string]int `json:"free"`
	// FreeConversation is the node's free conversation slots per provider (work-and-conversation-runs); absent
	// from older nodes.
	FreeConversation map[string]int `json:"free_conversation,omitempty"`
	// WaitSeconds is how long Core may wait for work (at most 25; 0 answers at once).
	WaitSeconds int `json:"wait_seconds"`
	// Skip lists runs the node refused to offer again for now (claims refused with retry lately).
	Skip []string `json:"skip,omitempty"`
	// Hooks and HooksHash: the NodeHooks document the node uses and the hash it applied (as in the heartbeat).
	Hooks     string `json:"hooks,omitempty"`
	HooksHash string `json:"hooks_hash,omitempty"`
	// ConfigVersion is the version of the settings from the web the node applied (node-packaging-updates B).
	ConfigVersion int64 `json:"config_version,omitempty"`
}

// PollAnswer is the poll's answer; every list is present, empty when there is nothing.
type PollAnswer struct {
	Offers []storage.Offer `json:"offers"`
	storage.PollWork
	// HooksChanged: the catalog's NodeHooks document differs from the node's; the node's next heartbeat gets it.
	HooksChanged bool `json:"hooks_changed,omitempty"`
	// ConfigVersion is the version of the node's settings from the web (0: none); a node that applied another one
	// fetches GET /nodes/{id}/config. A change answers a waiting poll at once.
	ConfigVersion int64 `json:"config_version,omitempty"`
	configChanged bool
}

func (a PollAnswer) empty() bool {
	return len(a.Offers) == 0 && a.PollWork.Empty() && !a.HooksChanged && !a.configChanged
}

// notifier wakes waiting polls: a commit that queued a run or changed what a node must do signals its company (D6).
type notifier struct {
	mu   sync.Mutex
	wait map[string]chan struct{}
}

// channel is closed at the company's next signal.
func (n *notifier) channel(company string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.wait == nil {
		n.wait = map[string]chan struct{}{}
	}
	ch, ok := n.wait[company]
	if !ok {
		ch = make(chan struct{})
		n.wait[company] = ch
	}
	return ch
}

func (n *notifier) signal(company string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.wait[company]; ok {
		close(ch)
		delete(n.wait, company)
	}
}

// polls keeps one outstanding poll per node and what the node's polls last delivered.
type polls struct {
	mu        sync.Mutex
	gen       map[string]uint64
	cancel    map[string]context.CancelFunc
	delivered map[string]string // node → epoch and key of the work and hooks last answered
}

// start registers a new poll of node, ending the older one; done must be called when it ends.
func (p *polls) start(ctx context.Context, node string) (context.Context, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen == nil {
		p.gen, p.cancel, p.delivered = map[string]uint64{}, map[string]context.CancelFunc{}, map[string]string{}
	}
	if cancel := p.cancel[node]; cancel != nil {
		cancel()
	}
	p.gen[node]++
	gen := p.gen[node]
	ctx, cancel := context.WithCancel(ctx)
	p.cancel[node] = cancel
	return ctx, func() {
		cancel()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.gen[node] == gen {
			delete(p.cancel, node)
		}
	}
}

func (p *polls) seen(node, key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.delivered[node] == key
}

func (p *polls) deliver(node, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delivered[node] = key
}

func (c *Core) pollRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /nodes/{id}/poll", c.poll)
	m.HandleFunc("POST /runs/{id}/events", c.runEvents)
}

// poll serves a node's long poll (D2): offers and work at once when there are any, else whatever arrives during
// the wait, else an empty answer at its end. Work the node's polls already got is not answered again (the
// heartbeat lists it until it is done); a newer poll of the node ends this one empty.
func (c *Core) poll(w http.ResponseWriter, r *http.Request) {
	var p PollRequest
	if !decode(w, r, &p) {
		return
	}
	company, node := c.companyFrom(r), r.PathValue("id")
	if p.Epoch < 1 || p.WaitSeconds < 0 {
		http.Error(w, "epoch and a wait of 0 to 25 seconds required", http.StatusBadRequest)
		return
	}
	for provider, n := range merged(p.Free, p.FreeConversation) {
		if n < 0 || n > 1024 || provider == "" {
			http.Error(w, "free slots per provider of 0 to 1024 required", http.StatusBadRequest)
			return
		}
	}
	if len(p.Skip) > 1024 {
		http.Error(w, "at most 1024 skipped runs", http.StatusBadRequest)
		return
	}
	wait := min(time.Duration(p.WaitSeconds)*time.Second, MaxPollWait)
	ctx, done := c.pollers.start(r.Context(), node)
	defer done()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	recheck := time.NewTicker(pollRecheck)
	defer recheck.Stop()
	empty := PollAnswer{Offers: []storage.Offer{}, PollWork: storage.PollWork{Stop: []storage.Stop{}, Reset: []string{}, Cleanup: []string{}, SyncJobs: []storage.SyncJob{}}}
	for {
		// Listen before reading, so a commit between the read and the wait still wakes this poll.
		woken := c.wakeups.channel(company)
		a, key, err := c.pollAnswer(ctx, company, node, p)
		if err != nil {
			if ctx.Err() != nil { // a newer poll of the node, or the node went away
				write(w, empty)
				return
			}
			nodeReply(w, nil, err)
			return
		}
		empty.ConfigVersion = a.ConfigVersion // every answer names the settings' version
		if len(a.Offers) > 0 || !a.empty() && !c.pollers.seen(node, key) {
			c.pollers.deliver(node, key)
			write(w, a)
			return
		}
		select {
		case <-ctx.Done():
			write(w, empty)
			return
		case <-deadline.C:
			write(w, empty)
			return
		case <-woken:
		case <-recheck.C:
			// A revoked node's waiting poll ends with the revocation (node-identity D7).
			if active, err := c.nodeKeyActive(ctx, node); err == nil && !active {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
	}
}

// pollAnswer reads what the node gets now and the key of its work and hooks (offers are never deduplicated: a run
// stays offered while it is queued and the node does not skip it).
func (c *Core) pollAnswer(ctx context.Context, company, node string, p PollRequest) (PollAnswer, string, error) {
	var a PollAnswer
	offers, err := c.Store.OffersFor(ctx, company, node, p.Epoch, p.Free, p.FreeConversation, p.Skip)
	if err != nil {
		return a, "", err
	}
	work, err := c.Store.NodeWork(ctx, company, node, p.Epoch)
	if err != nil {
		return a, "", err
	}
	a = PollAnswer{Offers: offers, PollWork: work}
	hooks := config.DeliverNodeHooks(c.cat(company).NodeHooks, p.Hooks)
	a.HooksChanged = hooks.Hash != p.HooksHash
	if a.ConfigVersion, err = c.Store.NodeConfigVersion(ctx, company, node); err != nil {
		return a, "", err
	}
	a.configChanged = a.ConfigVersion > 0 && a.ConfigVersion != p.ConfigVersion
	key, _ := json.Marshal(struct {
		Epoch  int64
		Work   string
		Hooks  string
		Config int64
	}{p.Epoch, work.Key(), map[bool]string{true: hooks.Hash}[a.HooksChanged], map[bool]int64{true: a.ConfigVersion}[a.configChanged]})
	return a, string(key), nil
}

func (c *Core) nodeKeyActive(ctx context.Context, node string) (bool, error) {
	key, err := c.Store.NodeKeyOf(ctx, node)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return err == nil && key.Status == storage.NodeKeyActive, err
}

// EventBatch is the body of POST /runs/{run}/events (D5): the run's lease and events in sequence order.
type EventBatch struct {
	Lease  string             `json:"lease"`
	Events []storage.SeqEvent `json:"events"`
}

// runEvents applies a batch of the run's events from its node (the gateway checked the run is the node's). A gap is
// 409 {"reason":"gap","expected_seq":n}; events of another lease are 409 {"reason":"lease"}, invalid ones 422: the
// node drops them.
func (c *Core) runEvents(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, storage.MaxEventBatchBytes)
	var b EventBatch
	if !decode(w, r, &b) {
		return
	}
	if b.Lease == "" || len(b.Events) == 0 || len(b.Events) > storage.MaxEventBatch {
		http.Error(w, "lease and 1 to 256 events required", http.StatusBadRequest)
		return
	}
	for _, e := range b.Events {
		var p struct {
			Lease string `json:"lease"`
		}
		if json.Unmarshal(e.Event.Payload, &p) != nil || p.Lease != b.Lease {
			jsonStatus(w, http.StatusConflict, map[string]string{"reason": "lease"})
			return
		}
	}
	applied, err := c.Store.ApplyEvents(r.Context(), c.companyFrom(r), r.PathValue("id"), b.Events)
	var gap *storage.GapError
	switch {
	case errors.As(err, &gap):
		jsonStatus(w, http.StatusConflict, map[string]any{"reason": "gap", "expected_seq": gap.Expected, "applied_seq": applied})
	case errors.Is(err, storage.ErrConflict):
		jsonStatus(w, http.StatusConflict, map[string]any{"reason": "lease", "applied_seq": applied})
	case errors.Is(err, storage.ErrInvalid):
		jsonStatus(w, http.StatusUnprocessableEntity, map[string]any{"reason": "invalid", "error": err.Error(), "applied_seq": applied})
	default:
		reply(w, map[string]int64{"applied_seq": applied}, err)
	}
}
