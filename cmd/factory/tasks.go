package main

import (
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
)

func engineCommand(ctx context.Context, api worker.API, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: projects create ID ABS_WORKSPACE [DESCRIPTION] | projects list/get/events/update | tasks create JSON_FILE | tasks list [PROJECT] [STATUS] | tasks get/attempts/events/comments ID | tasks update/submit ID JSON_FILE | tasks ready/block/cancel ID VERSION [REASON] | tasks review ID VERSION accept|reject [FEEDBACK]")
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
			if len(args) < 4 || len(args) > 5 {
				return fmt.Errorf("projects create ID ABS_WORKSPACE [DESCRIPTION]")
			}
			method = "POST"
			p := map[string]any{"id": args[2], "name": args[2], "workspace": args[3], "request_id": protocol.ID()}
			if len(args) == 5 {
				p["description"] = args[4]
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
		case "ready", "block", "cancel":
			if len(args) < 4 || len(args) > 5 {
				return fmt.Errorf("tasks ready|block|cancel ID VERSION [REASON]")
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
