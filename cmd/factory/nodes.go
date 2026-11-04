package main

import (
	"context"
	"fmt"
	"net/url"

	"appfactory/internal/worker"
)

const nodesUsage = "nodes list | nodes remove NODE_ID | nodes enroll --name NAME | nodes keys | nodes approve NODE_ID FINGERPRINT | nodes reject NODE_ID | nodes revoke NODE_ID"

// nodesCommand: registered nodes and their keys (node-identity).
func nodesCommand(ctx context.Context, api worker.API, args []string) error {
	var result any
	var err error
	switch {
	case len(args) == 2 && args[1] == "list":
		err = api.Do(ctx, "GET", "/nodes", nil, &result)
	case len(args) == 3 && args[1] == "remove":
		err = api.Do(ctx, "DELETE", "/nodes/"+url.PathEscape(args[2]), nil, &result)
	case len(args) == 4 && args[1] == "enroll" && args[2] == "--name":
		err = api.Do(ctx, "POST", "/node-enrollments", map[string]string{"name": args[3]}, &result)
	case len(args) == 2 && args[1] == "keys":
		err = api.Do(ctx, "GET", "/node-keys", nil, &result)
	case len(args) == 4 && args[1] == "approve":
		err = api.Do(ctx, "POST", "/node-keys/"+url.PathEscape(args[2])+"/approve", map[string]string{"fingerprint": args[3]}, &result)
	case len(args) == 3 && args[1] == "reject":
		err = api.Do(ctx, "POST", "/node-keys/"+url.PathEscape(args[2])+"/reject", nil, &result)
	case len(args) == 3 && args[1] == "revoke":
		err = api.Do(ctx, "POST", "/nodes/"+url.PathEscape(args[2])+"/revoke", nil, &result)
	default:
		return fmt.Errorf("usage: %s", nodesUsage)
	}
	if err != nil {
		return err
	}
	return printJSON(result)
}
