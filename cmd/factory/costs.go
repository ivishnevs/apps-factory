package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// ownerTaskCommand: comment TASK TEXT [--urgent], run TASK and retry TASK (release the task now), and
// telegram bind. The task authority actions (pause, resume, reassign, cancel) are factory tasks commands.
func ownerTaskCommand(ctx context.Context, api worker.API, args []string) error {
	var result any
	switch args[0] {
	case "comment":
		// Owner comments are authority input; --urgent also stops the running run (an urgent directive).
		urgent := args[len(args)-1] == "--urgent"
		if urgent {
			args = args[:len(args)-1]
		}
		if len(args) < 3 {
			return fmt.Errorf("comment TASK TEXT [--urgent]")
		}
		text := strings.Join(args[2:], " ")
		path, body := "/tasks/"+url.PathEscape(args[1])+"/comments", map[string]any{"text": text, "request_id": protocol.ID()}
		if urgent {
			path, body = "/tasks/"+url.PathEscape(args[1])+"/directives", map[string]any{"text": text, "urgent": true, "request_id": protocol.ID()}
		}
		if err := api.Do(ctx, "POST", path, body, &result); err != nil {
			return err
		}
	case "run", "retry":
		// run and retry release the task at its current version: counters, backoff and a retry escalation
		// are reset (an owner ready).
		if len(args) != 2 {
			return fmt.Errorf("%s TASK", args[0])
		}
		var t struct {
			Version int `json:"version"`
		}
		if err := api.Do(ctx, "GET", "/tasks/"+url.PathEscape(args[1]), nil, &t); err != nil {
			return err
		}
		body := map[string]any{"version": t.Version, "action": "ready", "reason": args[0] + " by owner", "request_id": protocol.ID()}
		if err := api.Do(ctx, "POST", "/tasks/"+url.PathEscape(args[1])+"/actions", body, &result); err != nil {
			return err
		}
	case "telegram":
		if len(args) != 2 || args[1] != "bind" {
			return fmt.Errorf("telegram bind")
		}
		if err := api.Do(ctx, "POST", "/telegram/bind", nil, &result); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown command")
	}
	return printJSON(result)
}

// costsCommand: costs [--task T] [--agent A] [--model M] [--by task|agent|day|model | --day | --model] [--since 24h]
// [--json]. A task alone lists its runs with their sources and the total.
func costsCommand(ctx context.Context, api worker.API, args []string) error {
	q := url.Values{}
	asJSON := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch a {
		case "--json":
			asJSON = true
		case "--day":
			q.Set("by", "day")
		case "--model":
			// --model alone groups by model; --model NAME filters.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				v, err = value()
				q.Set("model", v)
			} else {
				q.Set("by", "model")
			}
		case "--task", "--agent", "--by", "--since":
			v, err = value()
			q.Set(strings.TrimPrefix(a, "--"), v)
		default:
			return fmt.Errorf("costs [--task T] [--agent A] [--model [M]] [--by task|agent|day|model] [--day] [--since 24h] [--json]")
		}
		if err != nil {
			return err
		}
	}
	var rep storage.CostReport
	if err := api.Do(ctx, "GET", "/costs?"+q.Encode(), nil, &rep); err != nil {
		return err
	}
	if asJSON {
		return printJSON(rep)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if len(rep.Groups) > 0 || q.Get("by") != "" {
		fmt.Fprintf(tw, "%s\tRUNS\tTOKENS IN\tTOKENS OUT\tCOST\tSOURCES\n", strings.ToUpper(q.Get("by")))
		for _, g := range rep.Groups {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t$%.2f\t%s\n", g.Key, g.Runs, g.InputTokens+g.CacheReadTokens, g.OutputTokens, g.CostUSD, sourcesText(g.Sources))
		}
	} else {
		fmt.Fprintln(tw, "RUN\tTASK\tAGENT\tMODEL\tOUTCOME\tTOKENS IN\tTOKENS OUT\tCOST\tSOURCE\tENDED")
		for _, r := range rep.Runs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t$%.2f\t%s\t%s\n", r.RunID, r.TaskID, r.Agent, r.Model, r.Outcome, r.InputTokens+r.CacheReadTokens, r.OutputTokens, r.CostUSD, r.CostSource, time.Unix(r.RecordedAt, 0).Local().Format("01-02 15:04"))
		}
	}
	t := rep.Total
	fmt.Fprintf(tw, "total\t%d runs\t%d\t%d\t$%.2f\t%s\n", t.Runs, t.InputTokens+t.CacheReadTokens, t.OutputTokens, t.CostUSD, sourcesText(t.Sources))
	return tw.Flush()
}

func sourcesText(m map[string]int) string {
	parts := []string{}
	for _, s := range []string{"reported", "price_table", "none"} {
		if m[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, m[s]))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
