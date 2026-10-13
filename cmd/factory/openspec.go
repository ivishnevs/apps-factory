package main

import (
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"context"
	"fmt"
	"net/url"
)

func openSpecCommand(ctx context.Context, api worker.API, args []string) error {
	usage := fmt.Errorf("usage: openspec init PROJECT | openspec list PROJECT [DIR] [QUERY] | openspec read PROJECT PATH [QUERY] | openspec check PROJECT CHANGE; QUERY e.g. offset=8192&sha256=DIGEST or after=NAME&limit=20")
	if len(args) < 3 {
		return usage
	}
	method, path := "GET", "/projects/"+url.PathEscape(args[2])+"/openspec/"+args[1]
	var body any
	q := url.Values{}
	switch args[1] {
	case "init":
		if len(args) != 3 {
			return usage
		}
		method = "POST"
		body = map[string]string{"request_id": protocol.ID()}
	case "check":
		if len(args) != 4 {
			return usage
		}
		path += "/" + url.PathEscape(args[3])
	case "list":
		if len(args) > 5 {
			return usage
		}
		if len(args) == 5 {
			var e error
			q, e = url.ParseQuery(args[4])
			if e != nil {
				return e
			}
		}
		if len(args) >= 4 {
			q.Set("dir", args[3])
		}
	case "read":
		if len(args) < 4 || len(args) > 5 {
			return usage
		}
		if len(args) == 5 {
			var e error
			q, e = url.ParseQuery(args[4])
			if e != nil {
				return e
			}
		}
		q.Set("path", args[3])
	default:
		return usage
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var v any
	if e := api.Do(ctx, method, path, body, &v); e != nil {
		return e
	}
	return printJSON(v)
}
