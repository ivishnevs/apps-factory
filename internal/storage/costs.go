package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
)

// Cost ledger (waits-events-cost-cli D5): run_costs holds exactly one row per ended run, written by the
// run_cost_ended trigger (migration 0031) from the usage stored on the run, so revoked, failed, timed-out
// and lost runs are included and late usage updates the row. PriceRunCosts then sets the cost: what the
// runtime reported (reported), else the price table (price_table), else none (tokens only).

// RunCost is one run's row.
type RunCost struct {
	RunID            string  `json:"run_id"`
	TaskID           string  `json:"task_id,omitempty"`
	ProjectID        string  `json:"project_id,omitempty"`
	Agent            string  `json:"agent"`
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	Outcome          string  `json:"outcome"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	CostSource       string  `json:"cost_source"`
	RecordedAt       int64   `json:"recorded_at"`
}

// PriceRunCosts prices the rows written or updated since the last call.
func (s *Store) PriceRunCosts(ctx context.Context, company string, prices config.Prices) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT c.run_id,r.usage FROM run_costs c JOIN runs r ON r.id=c.run_id WHERE c.company_id=? AND c.priced=0 LIMIT 500", company)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id    string
		usage UsageList
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.usage); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range todo {
		cost, source := priceUsage(p.usage, prices)
		if _, err = tx.ExecContext(ctx, "UPDATE run_costs SET cost_usd=?,cost_source=?,priced=1 WHERE run_id=?", cost, source, p.id); err != nil {
			return 0, err
		}
	}
	return len(todo), tx.Commit()
}

// priceUsage: reported when the runtime priced every model; price_table when every model is priced by the
// runtime or the table and the table priced one; else none (no cost).
func priceUsage(usage []protocol.Usage, prices config.Prices) (float64, string) {
	if len(usage) == 0 {
		return 0, "none"
	}
	var total float64
	table := false
	for _, u := range usage {
		if u.CostUSD > 0 {
			total += u.CostUSD
			continue
		}
		c, ok := prices.Cost(u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
		if !ok {
			return 0, "none"
		}
		total, table = total+c, true
	}
	if table {
		return total, "price_table"
	}
	return total, "reported"
}

// CostFilter narrows cost queries; By groups them (task, agent, day, model; empty lists runs).
type CostFilter struct {
	Task, Agent, Model, By string
	Since                  int64
}

// CostGroup is one group of a cost query: its rows summed, and how many figures each source gave.
type CostGroup struct {
	Key              string         `json:"key"`
	Runs             int            `json:"runs"`
	InputTokens      int64          `json:"input_tokens"`
	OutputTokens     int64          `json:"output_tokens"`
	CacheReadTokens  int64          `json:"cache_read_tokens"`
	CacheWriteTokens int64          `json:"cache_write_tokens"`
	CostUSD          float64        `json:"cost_usd"`
	Sources          map[string]int `json:"sources"`
}

type CostReport struct {
	Runs   []RunCost   `json:"runs,omitempty"`
	Groups []CostGroup `json:"groups,omitempty"`
	Total  CostGroup   `json:"total"`
}

// Costs reads the cost ledger; days are the company's local days.
func (s *Store) Costs(ctx context.Context, company string, f CostFilter) (CostReport, error) {
	out := CostReport{Total: CostGroup{Key: "total", Sources: map[string]int{}}}
	switch f.By {
	case "", "task", "agent", "day", "model":
	default:
		return out, inputError("by must be task, agent, day or model")
	}
	zone, err := s.CompanyTimeZone(ctx, company)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		loc = time.UTC
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT run_id,task_id,project_id,agent,provider,model,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd,cost_source,recorded_at
 FROM run_costs WHERE company_id=? AND (?='' OR task_id=?) AND (?='' OR agent=?) AND (?='' OR model=?) AND recorded_at>=? ORDER BY recorded_at,run_id`,
		company, f.Task, f.Task, f.Agent, f.Agent, f.Model, f.Model, f.Since)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	groups := map[string]*CostGroup{}
	add := func(g *CostGroup, r RunCost) {
		g.Runs++
		g.InputTokens += r.InputTokens
		g.OutputTokens += r.OutputTokens
		g.CacheReadTokens += r.CacheReadTokens
		g.CacheWriteTokens += r.CacheWriteTokens
		g.CostUSD += r.CostUSD
		g.Sources[r.CostSource]++
	}
	for rows.Next() {
		var r RunCost
		if err = rows.Scan(&r.RunID, &r.TaskID, &r.ProjectID, &r.Agent, &r.Provider, &r.Model, &r.Outcome, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.CostUSD, &r.CostSource, &r.RecordedAt); err != nil {
			return out, err
		}
		add(&out.Total, r)
		key := ""
		switch f.By {
		case "":
			out.Runs = append(out.Runs, r)
			continue
		case "task":
			key = r.TaskID
		case "agent":
			key = r.Agent
		case "model":
			key = r.Model
		case "day":
			key = time.Unix(r.RecordedAt, 0).In(loc).Format("2006-01-02")
		}
		if groups[key] == nil {
			groups[key] = &CostGroup{Key: key, Sources: map[string]int{}}
		}
		add(groups[key], r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Key < out.Groups[j].Key })
	return out, nil
}

// CostText is a short form of a cost figure with its source.
func CostText(cost float64, source string) string {
	if source == "none" || source == "" {
		return "cost unknown"
	}
	return fmt.Sprintf("$%.2f (%s)", cost, source)
}
