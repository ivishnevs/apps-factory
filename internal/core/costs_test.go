package core

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Cost ledger (waits-events-cost-cli 4.1, 4.2).

func (h *exitHarness) usage(r exitRun, u ...protocol.Usage) {
	h.t.Helper()
	if err := h.s.Apply(context.Background(), storage.DefaultCompany, protocol.Wrap("agent.progress", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Usage: u})); err != nil {
		h.t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestRunCostsEveryEndedRun: revoked, lost and failed runs get exactly one row with their last usage; the
// cost is the runtime's, the price table's or none; task, agent, day and model queries sum the rows.
func TestRunCostsEveryEndedRun(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.c.Prices = config.Prices{"gpt-5": {Input: 1.25, Output: 10}}
	h.create("t", "notes", "demo")

	// Revoked by an urgent directive after consuming tokens.
	r := h.start("t")
	h.usage(r, protocol.Usage{Model: "gpt-5", Input: 1_000_000, Output: 100_000})
	if _, err := h.s.Directive(ctx, storage.DefaultCompany, "t", "owner", "stop", true, "u1"); err != nil {
		t.Fatal(err)
	}
	h.end(r, "revoked", "none")
	revoked := r.id

	// Lost: a worker end without terminal evidence; the runtime reported its own cost.
	r = h.start("t")
	h.usage(r, protocol.Usage{Model: "claude-sonnet", Input: 10, Output: 5, CostUSD: 0.5, CostSource: "claude_estimate"})
	h.end(r, "succeeded", "pending")
	lost := r.id
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, lost); run.Outcome != "lost" {
		t.Fatal(run.Outcome)
	}
	h.fake.Advance(time.Hour)

	// Failed, with a model the table does not price.
	r = h.start("t")
	h.usage(r, protocol.Usage{Model: "mystery", Input: 7, Output: 3})
	h.end(r, "failed", "none")
	if _, err := h.s.PriceRunCosts(ctx, storage.DefaultCompany, h.c.Prices); err != nil {
		t.Fatal(err)
	}

	w := h.do(h.c.Token, "GET", "/tasks/t/costs", nil, nil)
	var rep storage.CostReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(rep.Runs) != 3 || rep.Total.Runs != 3 || !near(rep.Total.CostUSD, 2.75) || rep.Total.Sources["price_table"] != 1 || rep.Total.Sources["reported"] != 1 || rep.Total.Sources["none"] != 1 {
		t.Fatalf("%+v", rep)
	}
	byRun := map[string]storage.RunCost{}
	for _, c := range rep.Runs {
		byRun[c.RunID] = c
	}
	if c := byRun[revoked]; c.Outcome != "revoked" || c.InputTokens != 1_000_000 || c.OutputTokens != 100_000 || c.CostSource != "price_table" || !near(c.CostUSD, 2.25) || c.Model != "gpt-5" || c.Agent != "demo" {
		t.Fatalf("%+v", c)
	}
	if c := byRun[lost]; c.Outcome != "lost" || c.CostSource != "reported" || !near(c.CostUSD, 0.5) {
		t.Fatalf("%+v", c)
	}
	if c := byRun[r.id]; c.Outcome != "failed" || c.CostSource != "none" || c.CostUSD != 0 || c.InputTokens != 7 {
		t.Fatalf("%+v", c)
	}

	// Late usage on an ended run updates its row and prices it again.
	h.usage(r, protocol.Usage{Model: "gpt-5", Input: 2_000_000})
	if _, err := h.s.PriceRunCosts(ctx, storage.DefaultCompany, h.c.Prices); err != nil {
		t.Fatal(err)
	}

	for by, want := range map[string]int{"agent": 1, "model": 2, "day": 1, "task": 1} {
		w = h.do(h.c.Token, "GET", "/costs?by="+by, nil, nil)
		var q storage.CostReport
		if err := json.Unmarshal(w.Body.Bytes(), &q); err != nil || len(q.Groups) != want || q.Total.Runs != 3 {
			t.Fatalf("by %s: %s", by, w.Body.String())
		}
		if by == "model" && (q.Groups[0].Key != "claude-sonnet" || q.Groups[1].Key != "gpt-5" || q.Groups[1].Runs != 2 || !near(q.Groups[1].CostUSD, 4.75)) {
			t.Fatalf("%+v", q.Groups)
		}
	}
	if w = h.do(h.c.Token, "GET", "/costs?since=30m", nil, nil); !json.Valid(w.Body.Bytes()) || w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var recent storage.CostReport
	_ = json.Unmarshal(w.Body.Bytes(), &recent)
	if recent.Total.Runs != 1 {
		t.Fatalf("since 30m: %+v", recent.Total)
	}
	if w = h.do(h.c.Token, "GET", "/costs?by=week", nil, nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

// TestRunCostModelCountsCache: the ledger names the model that processed the most tokens with cache
// included: Claude's main model (mostly cache reads), not its auxiliary model with more uncached input.
func TestRunCostModelCountsCache(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("m", "notes", "demo")
	r := h.start("m")
	h.usage(r,
		protocol.Usage{Model: "claude-haiku-5-5", Input: 8, Output: 1152, CacheRead: 113861, CacheWrite: 15400, CostUSD: 0.169, CostSource: "claude_estimate"},
		protocol.Usage{Model: "claude-haiku-4-5-20251001", Input: 1264, Output: 14, CostUSD: 0.001, CostSource: "claude_estimate"})
	h.end(r, "failed", "none")
	rep, err := h.s.Costs(ctx, storage.DefaultCompany, storage.CostFilter{Task: "m"})
	if err != nil || len(rep.Runs) != 1 || rep.Runs[0].Model != "claude-haiku-5-5" || rep.Runs[0].CacheReadTokens != 113861 {
		t.Fatalf("%+v %v", rep, err)
	}
}

// TestPricesYAML: the shipped table loads and prices per million tokens.
func TestPricesYAML(t *testing.T) {
	p, err := config.LoadPrices("../..")
	if err != nil || len(p) == 0 {
		t.Fatal(p, err)
	}
	if c, ok := p.Cost("gpt-5", 2_000_000, 1_000_000, 0, 0); !ok || !near(c, 12.5) {
		t.Fatal(c, ok)
	}
	if _, ok := p.Cost("unknown", 1, 1, 0, 0); ok {
		t.Fatal("unknown model priced")
	}
}
