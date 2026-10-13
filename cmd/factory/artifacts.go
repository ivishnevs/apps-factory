package main

import (
	"appfactory/internal/worker"
	"context"
	"fmt"
	"net/url"
)

func artifactCommand(ctx context.Context, api worker.API, args []string) error {
	method, path := "GET", ""
	var body any
	var err error
	if len(args) == 3 && args[1] == "list" {
		path = "/projects/" + url.PathEscape(args[2]) + "/artifacts"
	} else if len(args) == 5 && args[1] == "get" {
		path = "/projects/" + url.PathEscape(args[2]) + "/artifacts/" + url.PathEscape(args[3]) + "/" + url.PathEscape(args[4])
	} else if len(args) == 4 && (args[1] == "register" || args[1] == "request-approval") {
		method = "POST"
		path = "/projects/" + url.PathEscape(args[2]) + "/artifacts"
		if args[1] == "request-approval" {
			path += "/approval"
		}
		body, err = jsonBody(args[3])
	} else {
		return fmt.Errorf("artifacts list PROJECT | get PROJECT ID VERSION | register/request-approval PROJECT JSON_FILE")
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
