package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// Runtime protocol v2, executor side (docs/runtime-protocol-v2.md §4–§6).

// RetryDelay is how long a node does not take an offer again after Core refused its claim with retry (capacity,
// busy agent or project).
var RetryDelay = 5 * time.Second

// ClaimRequest is the body of POST /runs/{id}/claim in v2.
type ClaimRequest struct {
	NodeID  string `json:"node_id"`
	Epoch   int64  `json:"epoch"`
	ClaimID string `json:"claim_id"`
	Slot    int    `json:"slot"`
}

// Claim is the 200 answer: the run is CLAIMED and must be started by StartBy.
type Claim struct {
	Lease   string           `json:"lease"`
	StartBy time.Time        `json:"start_by"`
	RunSpec protocol.RunSpec `json:"run_spec"`
	Brief   json.RawMessage  `json:"brief,omitempty"`
	// Secrets are the run's company secrets sealed to this node (core-secret-store D4).
	Secrets []protocol.SealedSecret `json:"secrets,omitempty"`
}

// Refusal is a 409 answer: Retry keeps the run QUEUED (nak), Drop ends the command (ack).
type Refusal struct {
	Retry  bool   `json:"retry"`
	Drop   bool   `json:"drop"`
	Reason string `json:"reason"`
	// Detail says what the node lacks for a capacity refusal (driver, labels, slots, a resource class).
	Detail string `json:"detail,omitempty"`
}

// ClaimV2 claims run for slot. Transient failures are retried with the same claim_id; a refusal is
// returned as *Refusal.
func (w *Worker) ClaimV2(ctx context.Context, run string, slot int) (int64, Claim, error) {
	epoch, id := w.Inventory.Claim(slot)
	req := ClaimRequest{NodeID: w.Inventory.NodeID, Epoch: epoch, ClaimID: id, Slot: slot}
	var c Claim
	err := w.API.Retry(ctx, 6, "/runs/"+run+"/claim", req, &c)
	var h *HTTPError
	if errors.As(err, &h) && h.Status == 409 {
		var r Refusal
		if json.Unmarshal([]byte(h.Message), &r) == nil && (r.Retry || r.Drop) {
			return epoch, c, &r
		}
	}
	if err == nil && (c.Lease == "" || c.RunSpec.ID != run) {
		err = errors.New("claim answer without lease or with another run")
	}
	return epoch, c, err
}

func (r *Refusal) Error() string {
	if r.Drop {
		return "claim dropped: " + r.Reason
	}
	if r.Detail != "" {
		return "claim refused, retry: " + r.Reason + " (" + r.Detail + ")"
	}
	return "claim refused, retry: " + r.Reason
}

// ProcessRun claims an offered run for the context's slot and executes it (http-node-transport D3). running, when
// set, receives the claimed run and the cancel func of its execution before the runtime starts. retry reports a
// claim Core refused for now (the node does not offer it again for RetryDelay); a run Core drops, a lost claim race
// or a claim that failed are neither: the offer is simply gone. Run failures go to Core as events.
func (w *Worker) ProcessRun(ctx context.Context, run string, running func(run string, cancel context.CancelFunc)) (retry bool) {
	epoch, c, err := w.ClaimV2(ctx, run, node.SlotOf(ctx))
	var r *Refusal
	switch {
	case errors.As(err, &r) && r.Drop:
		slog.Info("offer dropped", "run", run, "reason", r.Reason)
		return false
	case errors.As(err, &r):
		slog.Info("claim refused for now", "run", run, "reason", r.Reason, "detail", r.Detail)
		return true
	case err != nil:
		slog.Error("claim failed", "run", run, "error", err)
		return true
	}
	// Core's heartbeat and poll stops cancel with a node.StopCause; execute reports timed_out and idle_timeout.
	runCtx, cancelCause := context.WithCancelCause(ctx)
	defer cancelCause(nil)
	cancel := func() { cancelCause(nil) }
	if !w.Inventory.AddCause(run, epoch, cancelCause) {
		// Claimed under an epoch the node replaced meanwhile: Core ends it with that epoch.
		slog.Warn("claim of a replaced epoch ignored", "run", run, "epoch", epoch)
		return false
	}
	defer w.Inventory.End(run)
	slog.Info("run claimed", "run", run, "start_by", c.StartBy)
	if running != nil {
		running(run, cancel)
	}
	// A secret that does not open is reported by the run's setup as not delivered (setup_failed).
	if err := w.openSecrets(c.RunSpec.Company, run, w.Inventory.NodeID, c.Secrets); err != nil {
		slog.Error("run secrets", "run", run, "error", err)
	}
	defer w.forgetSecrets(run)
	if err := w.execute(runCtx, c.RunSpec, c.Lease, ""); err != nil {
		slog.Error("runtime run", "run", run, "error", err)
	}
	return false
}
