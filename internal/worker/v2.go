package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"github.com/nats-io/nats.go"
)

// Runtime protocol v2, executor side (docs/runtime-protocol-v2.md §4–§6).

// RetryDelay is the nak delay of a claim refused with retry (capacity, busy agent or project).
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
}

// Refusal is a 409 answer: Retry keeps the run QUEUED (nak), Drop ends the command (ack).
type Refusal struct {
	Retry  bool   `json:"retry"`
	Drop   bool   `json:"drop"`
	Reason string `json:"reason"`
	// Detail says what the node lacks for a capacity refusal (driver, labels, slots, a resource class).
	Detail string `json:"detail,omitempty"`
}

// pointer is the run id of a run command: the v2 pointer's run_id, else the id of a full RunSpec
// (Core sends RunSpecs during the transition). Nothing else is taken from the command.
func pointer(data []byte) (protocol.Envelope, string) {
	var e protocol.Envelope
	var p struct {
		RunID string `json:"run_id"`
		ID    string `json:"id"`
	}
	if json.Unmarshal(data, &e) != nil || e.Version != 1 || e.Type != "agent.run.requested" || json.Unmarshal(e.Payload, &p) != nil {
		return e, ""
	}
	run := p.RunID
	if run == "" {
		run = p.ID
	}
	if e.CorrelationID != "" && run != e.CorrelationID {
		return e, ""
	}
	return e, run
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

func (w *Worker) processV2(ctx context.Context, m *nats.Msg, running func(run string, cancel context.CancelFunc)) error {
	e, run := pointer(m.Data)
	if run == "" {
		_ = m.Term()
		slog.Error("invalid run command")
		return nil
	}
	epoch, c, err := w.ClaimV2(ctx, run, node.SlotOf(ctx))
	var r *Refusal
	switch {
	case errors.As(err, &r) && r.Drop:
		slog.Info("run command dropped", "run", run, "reason", r.Reason)
		return m.AckSync()
	case errors.As(err, &r):
		_ = m.NakWithDelay(RetryDelay)
		return nil
	case err != nil:
		slog.Error("claim failed", "run", run, "error", err)
		_ = m.NakWithDelay(RetryDelay)
		return nil
	}
	if err = m.AckSync(); err != nil {
		return err
	} // Core's lease and the start window now own recovery, not redelivery.
	// Core's heartbeat stops cancel with a node.StopCause; execute reports timed_out and idle_timeout.
	runCtx, cancelCause := context.WithCancelCause(ctx)
	defer cancelCause(nil)
	cancel := func() { cancelCause(nil) }
	if !w.Inventory.AddCause(run, epoch, cancelCause) {
		// Claimed under an epoch the node replaced meanwhile: Core ends it with that epoch.
		slog.Warn("claim of a replaced epoch ignored", "run", run, "epoch", epoch)
		return nil
	}
	defer w.Inventory.End(run)
	slog.Info("run claimed", "run", run, "start_by", c.StartBy)
	if running != nil {
		running(run, cancel)
	}
	if err := w.execute(runCtx, c.RunSpec, c.Lease, e.ID); err != nil {
		slog.Error("runtime run", "run", run, "error", err)
	}
	return nil
}
