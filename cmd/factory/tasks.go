package main

import (
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"text/tabwriter"
)

func engineCommand(ctx context.Context, api worker.API, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: projects create ID ABS_WORKSPACE [DESCRIPTION] | projects list/get/events/update | tasks create JSON_FILE | tasks list [PROJECT] [STATUS] | tasks get/attempts/events/comments ID | tasks update/submit ID JSON_FILE | tasks model ID VERSION MODEL|- | tasks ready/block/cancel ID VERSION [REASON] [--cascade] | tasks pause/resume ID VERSION [REASON] | tasks reassign ID VERSION AGENT [REASON] | tasks reprioritize ID VERSION PRIORITY |tasks review ID VERSION accept|reject [FEEDBACK] | tasks why ID|--project PROJECT [--json] | tasks comment ID TEXT")
	}
	if args[0] == "tasks" && args[1] == "why" {
		return whyCommand(ctx, api, args[2:])
	}
	method, path := "GET", "/"+args[0]
	var body any
	var err error
	idPath := func(id string) string { return path + "/" + url.PathEscape(id) }
	if args[0] == "projects" {
		switch args[1] {
		case "list":
			if len(args) != 2 {
				return fmt.Errorf("projects list")
			}
		case "get", "events", "context", "feed":
			if len(args) != 3 && !(args[1] == "feed" && len(args) == 4) {
				return fmt.Errorf("project ID required")
			}
			path = idPath(args[2])
			if args[1] != "get" {
				path += "/" + args[1]
			}
			if args[1] == "feed" && len(args) == 4 {
				q, e := url.ParseQuery(args[3])
				if e != nil {
					return e
				}
				path += "?" + q.Encode()
			}
		case "create":
			p := map[string]any{"request_id": protocol.ID()}
			positional := []string{}
			for i := 2; i < len(args); i++ {
				switch args[i] {
				case "--repo", "--branch":
					if i+1 == len(args) {
						return fmt.Errorf("%s needs a value", args[i])
					}
					p[map[string]string{"--repo": "repo_url", "--branch": "default_branch"}[args[i]]] = args[i+1]
					i++
				default:
					positional = append(positional, args[i])
				}
			}
			if len(positional) < 2 || len(positional) > 3 {
				return fmt.Errorf("projects create ID ABS_WORKSPACE [DESCRIPTION] [--repo URL] [--branch BRANCH]")
			}
			method = "POST"
			p["id"], p["name"], p["workspace"] = positional[0], positional[0], positional[1]
			if len(positional) == 3 {
				p["description"] = positional[2]
			}
			body = p
		case "update":
			if len(args) != 4 {
				return fmt.Errorf("projects update ID JSON_FILE")
			}
			method = "PATCH"
			path = idPath(args[2])
			body, err = jsonBody(args[3])
		default:
			return fmt.Errorf("unknown projects command")
		}
	} else {
		switch args[1] {
		case "list":
			if len(args) > 4 {
				return fmt.Errorf("tasks list [PROJECT] [STATUS]")
			}
			q := url.Values{}
			if len(args) >= 3 {
				q.Set("project", args[2])
			}
			if len(args) == 4 {
				q.Set("status", args[3])
			}
			path += "?" + q.Encode()
		case "get", "attempts", "events", "comments":
			if len(args) != 3 {
				return fmt.Errorf("task ID required")
			}
			path = idPath(args[2])
			if args[1] != "get" {
				path += "/" + args[1]
			}
		case "create":
			if len(args) != 3 {
				return fmt.Errorf("tasks create JSON_FILE (or - for stdin)")
			}
			method = "POST"
			body, err = jsonBody(args[2])
		case "update", "submit":
			if len(args) != 4 {
				return fmt.Errorf("tasks update|submit ID JSON_FILE")
			}
			method = "PATCH"
			path = idPath(args[2])
			if args[1] == "submit" {
				method = "POST"
				path += "/submit"
			}
			body, err = jsonBody(args[3])
		case "model":
			// Per-task model override (runtime-drivers 2.7); "-" clears it.
			if len(args) != 5 {
				return fmt.Errorf("tasks model ID VERSION MODEL|-")
			}
			version, e := strconv.Atoi(args[3])
			if e != nil {
				return e
			}
			model := args[4]
			if model == "-" {
				model = ""
			}
			method = "PATCH"
			path = idPath(args[2])
			body = map[string]any{"version": version, "model": model, "request_id": protocol.ID()}
		case "comment":
			if len(args) != 4 {
				return fmt.Errorf("tasks comment ID TEXT")
			}
			method = "POST"
			path = idPath(args[2]) + "/comments"
			body = map[string]any{"text": args[3], "request_id": protocol.ID()}
		case "ready", "block", "cancel":
			cascade := args[1] == "cancel" && args[len(args)-1] == "--cascade"
			if cascade {
				args = args[:len(args)-1]
			}
			if len(args) < 4 || len(args) > 5 {
				return fmt.Errorf("tasks ready|block|cancel ID VERSION [REASON] (cancel: [--cascade])")
			}
			version, e := strconv.Atoi(args[3])
			if e != nil {
				return e
			}
			method = "POST"
			path = idPath(args[2]) + "/actions"
			p := map[string]any{"version": version, "action": args[1], "request_id": protocol.ID()}
			if len(args) == 5 {
				p["reason"] = args[4]
			}
			if cascade {
				p["cascade"] = true
			}
			body = p
		case "pause", "resume", "reassign", "reprioritize":
			// Authority actions (hierarchy D2): pause|resume ID VERSION [REASON], reassign ID VERSION AGENT [REASON],
			// reprioritize ID VERSION PRIORITY.
			lo, hi := 4, 5
			if args[1] == "reassign" {
				lo, hi = 5, 6
			}
			if args[1] == "reprioritize" {
				lo, hi = 5, 5
			}
			if len(args) < lo || len(args) > hi {
				return fmt.Errorf("tasks pause|resume ID VERSION [REASON] | tasks reassign ID VERSION AGENT [REASON] | tasks reprioritize ID VERSION PRIORITY")
			}
			version, e := strconv.Atoi(args[3])
			if e != nil {
				return e
			}
			method = "POST"
			path = idPath(args[2]) + "/" + args[1]
			p := map[string]any{"version": version, "request_id": protocol.ID()}
			rest := args[4:]
			switch args[1] {
			case "reassign":
				p["assignee"], rest = rest[0], rest[1:]
			case "reprioritize":
				priority, e := strconv.Atoi(rest[0])
				if e != nil {
					return e
				}
				p["priority"], rest = priority, nil
			}
			if len(rest) == 1 {
				p["reason"] = rest[0]
			}
			body = p
		case "review":
			if len(args) < 5 || len(args) > 6 || (args[4] != "accept" && args[4] != "reject") {
				return fmt.Errorf("tasks review ID VERSION accept|reject [FEEDBACK]")
			}
			version, e := strconv.Atoi(args[3])
			if e != nil {
				return e
			}
			method = "POST"
			path = idPath(args[2]) + "/review"
			p := map[string]any{"version": version, "accept": args[4] == "accept", "request_id": protocol.ID()}
			if len(args) == 6 {
				p["feedback"] = args[5]
			}
			body = p
		default:
			return fmt.Errorf("unknown tasks command")
		}
	}
	if err != nil {
		return err
	}
	var result any
	if err = api.Do(ctx, method, path, body, &result); err != nil {
		return err
	}
	return printJSON(result)
}

// whyCommand explains why tasks do not run now and who clears each reason.
func whyCommand(ctx context.Context, api worker.API, args []string) error {
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
	var verdicts []tasks.Verdict
	switch {
	case len(args) == 1 && args[0] != "--project":
		var v tasks.Verdict
		if err := api.Do(ctx, "GET", "/tasks/"+url.PathEscape(args[0])+"/why", nil, &v); err != nil {
			return err
		}
		verdicts = []tasks.Verdict{v}
	case len(args) == 2 && args[0] == "--project":
		if err := api.Do(ctx, "GET", "/projects/"+url.PathEscape(args[1])+"/why", nil, &verdicts); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tasks why ID|--project PROJECT [--json]")
	}
	if asJSON {
		return printJSON(verdicts)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tSTATUS\tREASON\tDETAIL\tWHO\tACTION")
	for _, v := range verdicts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.TaskID, v.Status, v.Reason, v.Detail, v.Clearer.Who, v.Clearer.Action)
	}
	return tw.Flush()
}
func jsonBody(path string) (map[string]any, error) {
	var r io.Reader = os.Stdin
	var file *os.File
	var err error
	if path != "-" {
		file, err = os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		r = file
	}
	decoder := json.NewDecoder(io.LimitReader(r, 1<<20))
	decoder.UseNumber()
	var body map[string]any
	if err = decoder.Decode(&body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("JSON object required")
	}
	if _, ok := body["request_id"]; !ok {
		body["request_id"] = protocol.ID()
	}
	return body, nil
}
