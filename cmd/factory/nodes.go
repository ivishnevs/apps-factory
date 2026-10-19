package main

import (
	"context"
	"fmt"
	"net/url"

	"appfactory/internal/worker"
)

const nodesUsage = "nodes list | nodes remove NODE_ID | nodes token create NAME [--node-id NODE_ID] | nodes token list | nodes token revoke TOKEN_ID"

// nodesCommand: registered nodes and per-machine node tokens (node-agent 1.1).
func nodesCommand(ctx context.Context, api worker.API, args []string) error {
	var result any
	var err error
	switch {
	case len(args) == 2 && args[1] == "list":
		err = api.Do(ctx, "GET", "/nodes", nil, &result)
	case len(args) == 3 && args[1] == "remove":
		err = api.Do(ctx, "DELETE", "/nodes/"+url.PathEscape(args[2]), nil, &result)
	case len(args) == 3 && args[1] == "token" && args[2] == "list":
		err = api.Do(ctx, "GET", "/node-tokens", nil, &result)
	case len(args) == 4 && args[1] == "token" && args[2] == "revoke":
		err = api.Do(ctx, "POST", "/node-tokens/"+url.PathEscape(args[3])+"/revoke", nil, &result)
	case (len(args) == 4 || len(args) == 6) && args[1] == "token" && args[2] == "create":
		body := map[string]string{"name": args[3]}
		if len(args) == 6 {
			if args[4] != "--node-id" {
				return fmt.Errorf("usage: %s", nodesUsage)
			}
			body["node_id"] = args[5]
		}
		err = api.Do(ctx, "POST", "/node-tokens", body, &result)
	default:
		return fmt.Errorf("usage: %s", nodesUsage)
	}
	if err != nil {
		return err
	}
	return printJSON(result)
}
