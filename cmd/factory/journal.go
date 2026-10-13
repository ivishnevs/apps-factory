package main

import (
	"appfactory/internal/worker"
	"context"
	"fmt"
	"net/url"
)

func journalCommand(ctx context.Context, api worker.API, args []string) error {
	usage := fmt.Errorf("usage: reports create PROJECT JSON_FILE | reports list PROJECT [QUERY] | reports get ID | decisions propose PROJECT JSON_FILE | decisions list PROJECT [QUERY] | decisions get ID | decisions record ID JSON_FILE; QUERY e.g. status=approved&after=0&limit=20")
	if len(args) < 3 {
		return usage
	}
	method, path := "GET", "/"+args[0]+"/"+url.PathEscape(args[2])
	var body any
	var err error
	switch args[1] {
	case "get":
		if len(args) != 3 {
			return usage
		}
	case "list":
		if len(args) > 4 {
			return usage
		}
		path = "/projects/" + url.PathEscape(args[2]) + "/" + args[0]
		if len(args) == 4 {
			q, e := url.ParseQuery(args[3])
			if e != nil {
				return e
			}
			path += "?" + q.Encode()
		}
	case "create", "propose":
		if len(args) != 4 || (args[0] == "reports" && args[1] != "create") || (args[0] == "decisions" && args[1] != "propose") {
			return usage
		}
		method = "POST"
		path = "/projects/" + url.PathEscape(args[2]) + "/" + args[0]
		body, err = jsonBody(args[3])
	case "record":
		if len(args) != 4 || args[0] != "decisions" {
			return usage
		}
		method = "POST"
		path += "/record"
		body, err = jsonBody(args[3])
	default:
		return usage
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
