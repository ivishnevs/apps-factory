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
		return renderStatus(projects)
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
	Task     map[string]any     `json:"task"`
	Why      tasks.Verdict      `json:"why"`
	Attempts []storage.Run      `json:"attempts"`
	Comments []storage.Comment  `json:"comments"`
	Events   []storage.FeedItem `json:"recent_events"`
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
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", i+1, a.Spec.ID, a.Status, result)
		}
		if err = tw.Flush(); err != nil {
			return err
		}
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
