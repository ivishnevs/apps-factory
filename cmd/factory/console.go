package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// consoleCommand is the owner console v0: status, watch, log and show; --json prints JSON.
func consoleCommand(ctx context.Context, api worker.API, args []string) error {
	asJSON := false
	rest := []string{}
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	switch {
	case args[0] == "status" && len(args) <= 2:
		path := "/status"
		if len(args) == 2 {
			path += "?project=" + url.QueryEscape(args[1])
		}
		var projects []core.ProjectStatus
		if err := api.Do(ctx, "GET", path, nil, &projects); err != nil {
			return err
		}
		if asJSON {
			return printJSON(projects)
		}
		if err := renderStatus(projects); err != nil {
			return err
		}
		if len(args) == 2 { // one project: no node section
			return nil
		}
		var nodes []nodeStatus
		if err := api.Do(ctx, "GET", "/nodes", nil, &nodes); err != nil {
			return err
		}
		return renderNodes(nodes, time.Now())
	case args[0] == "watch":
		q := url.Values{}
		for _, a := range args[1:] {
			if a == "--attention" {
				q.Set("attention", "1")
			} else if q.Get("project") == "" && !strings.HasPrefix(a, "-") {
				q.Set("project", a)
			} else {
				return fmt.Errorf("watch [PROJECT] [--attention] [--json]")
			}
		}
		return watch(ctx, api, q, asJSON)
	case args[0] == "log" && len(args) == 2:
		items, _, err := feedAll(ctx, api, url.Values{"task": {args[1]}}, 0, 0)
		if err != nil {
			return err
		}
		if asJSON {
			return printJSON(items)
		}
		for _, item := range items {
			printFeed(item, false)
		}
		return nil
	case args[0] == "show" && len(args) == 2:
		return show(ctx, api, args[1], asJSON)
	}
	return fmt.Errorf("usage: status [PROJECT] | watch [PROJECT] [--attention] | log TASK | show TASK; add --json for JSON")
}

// taskDetail is everything factory show prints about one task.
type taskDetail struct {
	Task      map[string]any     `json:"task"`
	Why       tasks.Verdict      `json:"why"`
	Attempts  []storage.Run      `json:"attempts"`
	Comments  []storage.Comment  `json:"comments"`
	Events    []storage.FeedItem `json:"recent_events"`
	Workspace *tasks.Workspace   `json:"workspace,omitempty"`
}

func show(ctx context.Context, api worker.API, id string, asJSON bool) error {
	var d taskDetail
	base := "/tasks/" + url.PathEscape(id)
	for path, v := range map[string]any{base: &d.Task, base + "/why": &d.Why, base + "/attempts": &d.Attempts, base + "/comments": &d.Comments} {
		if err := api.Do(ctx, "GET", path, nil, v); err != nil {
			return err
		}
	}
	events, _, err := feedAll(ctx, api, url.Values{"task": {id}}, 0, 10)
	if err != nil {
		return err
	}
	d.Events = events
	var ws tasks.Workspace
	if api.Do(ctx, "GET", base+"/workspace", nil, &ws) == nil {
		d.Workspace = &ws
	}
	if asJSON {
		return printJSON(d)
	}
	str := func(k string) string { return fmt.Sprint(d.Task[k]) }
	fmt.Printf("%s — %s  [%s]\n", str("id"), str("title"), str("status"))
	fmt.Printf("project %s  assignee %s  priority %v  version %v\n", str("project_id"), str("assignee"), d.Task["priority"], d.Task["version"])
	fmt.Printf("why: %s", d.Why.Reason)
	if d.Why.Detail != "" {
		fmt.Printf(" — %s", d.Why.Detail)
	}
	fmt.Printf("\n  %s: %s\n", d.Why.Clearer.Who, d.Why.Clearer.Action)
	if len(d.Why.Pending) > 0 {
		pending := []string{}
		for _, g := range d.Why.Pending {
			pending = append(pending, g.Kind+" by "+g.Actor)
		}
		fmt.Println("pending triggers:", strings.Join(pending, ", "))
	}
	if deps, _ := d.Task["dependencies"].([]any); len(deps) > 0 {
		ids := make([]string, len(deps))
		for i, dep := range deps {
			ids[i] = fmt.Sprint(dep)
		}
		fmt.Println("dependencies:", strings.Join(ids, ", "))
	}
	if len(d.Attempts) > 0 {
		fmt.Println("attempts:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for i, a := range d.Attempts {
			result := strings.ReplaceAll(a.Result, "\n", " ")
			if len(result) > 80 {
				result = result[:80] + "…"
			}
			status := a.Status
			if a.Outcome != "" {
				status += "/" + a.Outcome
			}
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n", i+1, a.Spec.ID, status, usageText(a.Usage), result)
		}
		if err = tw.Flush(); err != nil {
			return err
		}
	}
	if w := d.Workspace; w != nil {
		fmt.Printf("workspace: %s", w.Mode)
		if w.Branch != "" {
			fmt.Printf(" branch %s", w.Branch)
		}
		if w.SealedHead != "" {
			fmt.Printf(" sealed %.12s", w.SealedHead)
		}
		if w.ExitSHA != "" {
			fmt.Printf(" exit %.12s", w.ExitSHA)
		}
		fmt.Println()
	}
	if len(d.Comments) > 0 {
		fmt.Println("comments:")
		for _, c := range d.Comments {
			fmt.Printf("  %s %s: %s\n", time.Unix(c.CreatedAt, 0).Local().Format("01-02 15:04"), c.Author, strings.ReplaceAll(c.Text, "\n", " "))
		}
	}
	if len(d.Events) > 0 {
		fmt.Println("recent events:")
		for _, item := range d.Events {
			fmt.Print("  ")
			printFeed(item, false)
		}
	}
	return nil
}

func renderStatus(projects []core.ProjectStatus) error {
	for i, p := range projects {
		if i > 0 {
			fmt.Println()
		}
		statuses := make([]string, 0, len(p.Tasks))
		for s, n := range p.Tasks {
			statuses = append(statuses, fmt.Sprintf("%s %d", s, n))
		}
		sort.Strings(statuses)
		fmt.Printf("%s (%s)  tasks: %s  runnable: %d  inbox: %d\n", p.Project.ID, p.Project.Name, strings.Join(statuses, ", "), p.Runnable, p.Inbox)
		if len(p.Runs) > 0 {
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  RUN\tAGENT\tTASK\tSTATUS\tSTARTED")
			for _, r := range p.Runs {
				started := "-"
				if r.StartedAt > 0 {
					started = time.Unix(r.StartedAt, 0).Format("15:04:05")
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", r.ID, r.Agent, r.TaskID, r.Status, started)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if len(p.Waiting) > 0 {
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  WAITING\tSTATUS\tREASON\tWHO\tACTION")
			for _, v := range p.Waiting {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", v.TaskID, v.Status, v.Reason, v.Clearer.Who, v.Clearer.Action)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// nodeStatus is one entry of GET /nodes.
type nodeStatus struct {
	storage.Node
	InUse storage.NodeUse `json:"in_use"`
}

// renderNodes is the status's node section (node-agent D5): state, epoch, held/total slots and resources (Core's
// claims), drivers, and disk, load and failing self-checks from the last inventory heartbeat.
func renderNodes(nodes []nodeStatus, now time.Time) error {
	if len(nodes) == 0 {
		return nil
	}
	fmt.Println()
	fmt.Println("nodes:")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NODE\tSTATE\tEPOCH\tSLOTS\tRESOURCES\tDRIVERS\tDISK\tLOAD\tFAILING CHECKS")
	for _, n := range nodes {
		classes := make([]string, 0, len(n.Resources))
		for k := range n.Resources {
			classes = append(classes, k)
		}
		sort.Strings(classes)
		resources := make([]string, 0, len(classes))
		for _, k := range classes {
			resources = append(resources, fmt.Sprintf("%s %d/%d", k, n.InUse.Resources[k], n.Resources[k]))
		}
		if len(resources) == 0 {
			resources = append(resources, "-")
		}
		var h struct {
			Load1         float64 `json:"load1"`
			FreeDiskBytes uint64  `json:"free_disk_bytes"`
			Disk          string  `json:"disk"`
			Checks        []struct {
				Check  string `json:"check"`
				Status string `json:"status"`
			} `json:"checks"`
		}
		_ = json.Unmarshal(n.Health, &h)
		state := "online"
		if !n.Online {
			state = "offline " + now.Sub(time.Unix(n.LastSeen, 0)).Round(time.Minute).String()
		}
		drivers := make([]string, 0, len(n.Drivers))
		for _, d := range n.Drivers {
			ok := "ok"
			if !d.OK {
				ok = "off"
			}
			drivers = append(drivers, d.Name+" "+ok)
		}
		disk := "-"
		if h.Disk != "" {
			disk = fmt.Sprintf("%s %.1f GiB", h.Disk, float64(h.FreeDiskBytes)/(1<<30))
		}
		failing := []string{}
		for _, c := range h.Checks {
			if c.Status == "fail" {
				failing = append(failing, c.Check)
			}
		}
		if len(failing) == 0 {
			failing = append(failing, "-")
		}
		name := n.Name
		if name == "" {
			name = n.NodeID
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%d/%d\t%s\t%s\t%s\t%.2f\t%s\n", name, state, n.Epoch, n.InUse.Runs, n.Slots, strings.Join(resources, ", "),
			strings.Join(drivers, ", "), disk, h.Load1, strings.Join(failing, ", "))
	}
	return tw.Flush()
}

// feedAll reads the feed after a cursor; keep > 0 returns only the last keep items.
func feedAll(ctx context.Context, api worker.API, q url.Values, after int64, keep int) ([]storage.FeedItem, int64, error) {
	items := []storage.FeedItem{}
	for {
		q.Set("after", fmt.Sprint(after))
		q.Set("limit", "100")
		var page storage.Page[storage.FeedItem]
		if err := api.Do(ctx, "GET", "/feed?"+q.Encode(), nil, &page); err != nil {
			return nil, after, err
		}
		items = append(items, page.Items...)
		if keep > 0 && len(items) > keep {
			items = items[len(items)-keep:]
		}
		after = page.NextAfter
		if !page.HasMore {
			return items, after, nil
		}
	}
}

func watch(ctx context.Context, api worker.API, q url.Values, asJSON bool) error {
	items, cursor, err := feedAll(ctx, api, q, 0, 20)
	if err != nil {
		return err
	}
	for {
		for _, item := range items {
			if asJSON {
				b, err := json.Marshal(item)
				if err != nil {
					return err
				}
				fmt.Println(string(b))
				continue
			}
			printFeed(item, true)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
		if items, cursor, err = feedAll(ctx, api, q, cursor, 0); err != nil {
			return err
		}
	}
}

func printFeed(item storage.FeedItem, withProject bool) {
	mark := " "
	if item.Attention {
		mark = "!"
	}
	who := item.Agent
	if item.Actor != "" && item.Actor != item.Agent {
		who = strings.Trim(who+" by "+item.Actor, " ")
	}
	where := item.TaskID
	if withProject {
		where = item.ProjectID + "/" + where
	}
	line := fmt.Sprintf("%s %s %-28s %-24s %s", item.CreatedAt.Local().Format("15:04:05"), mark, item.Type, where, who)
	if item.Summary != "" {
		line += " — " + strings.ReplaceAll(item.Summary, "\n", " ")
	}
	fmt.Println(line)
}

// usageText summarizes a run's usage: tokens in/out over all models and the runtime's cost estimate.
func usageText(u storage.UsageList) string {
	if len(u) == 0 {
		return "-"
	}
	var in, out int64
	var cost float64
	for _, m := range u {
		in, out, cost = in+m.Input+m.CacheRead, out+m.Output, cost+m.CostUSD
	}
	s := fmt.Sprintf("%dk in / %dk out", in/1000, out/1000)
	if cost > 0 {
		s += fmt.Sprintf(" ~$%.2f", cost)
	}
	return s
}
