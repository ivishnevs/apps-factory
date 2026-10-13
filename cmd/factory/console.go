package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// consoleCommand is the owner console v0: status, watch and log.
func consoleCommand(ctx context.Context, api worker.API, args []string) error {
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
		return renderStatus(projects)
	case args[0] == "watch":
		q := url.Values{}
		for _, a := range args[1:] {
			if a == "--attention" {
				q.Set("attention", "1")
			} else if q.Get("project") == "" && !strings.HasPrefix(a, "-") {
				q.Set("project", a)
			} else {
				return fmt.Errorf("watch [PROJECT] [--attention]")
			}
		}
		return watch(ctx, api, q)
	case args[0] == "log" && len(args) == 2:
		items, _, err := feedAll(ctx, api, url.Values{"task": {args[1]}}, 0, 0)
		if err != nil {
			return err
		}
		for _, item := range items {
			printFeed(item, false)
		}
		return nil
	}
	return fmt.Errorf("usage: status [PROJECT] | watch [PROJECT] [--attention] | log TASK")
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

func watch(ctx context.Context, api worker.API, q url.Values) error {
	items, cursor, err := feedAll(ctx, api, q, 0, 20)
	if err != nil {
		return err
	}
	for {
		for _, item := range items {
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
