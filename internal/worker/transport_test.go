package worker

import (
	"context"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/core"
	"appfactory/internal/node"
)

// runOffers is a one-slot node for tests (http-node-transport D2, D3): it long-polls Core for offers of the
// worker's provider and processes them one at a time until ctx ends.
func runOffers(ctx context.Context, w *Worker) error {
	drv, _ := w.driver()
	for ctx.Err() == nil {
		var a struct {
			Offers []struct {
				RunID string `json:"run_id"`
			} `json:"offers"`
		}
		req := map[string]any{"epoch": w.Inventory.Epoch(), "free": map[string]int{string(drv.Name()): 1}, "wait_seconds": 1}
		if err := w.API.Do(ctx, "POST", "/nodes/"+w.Inventory.NodeID+"/poll", req, &a); err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		for _, o := range a.Offers {
			w.ProcessRun(node.WithSlot(ctx, 0), o.RunID, nil)
		}
	}
	return nil
}

// startCore runs c's tick loop. The lease continuity is set first, as Core's loop would, so the HTTP handlers the
// worker calls meanwhile do not race the loop's start.
func startCore(ctx context.Context, c *core.Core) <-chan error {
	if c.Store.Continuity == nil {
		c.Store.Continuity = clock.NewContinuity(3 * time.Second)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return done
}
