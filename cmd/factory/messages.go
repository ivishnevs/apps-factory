package main

import (
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"context"
	"fmt"
	"net/url"
)

func communicationCommand(ctx context.Context, api worker.API, args []string) error {
	method, path := "GET", ""
	var body any
	var err error
	switch args[0] {
	case "conversations":
		if len(args) < 2 {
			return fmt.Errorf("conversations create JSON_FILE | list PROJECT | get/messages ID | send ID JSON_FILE | say PROJECT TEXT")
		}
		switch args[1] {
		case "create":
			if len(args) != 3 {
				return fmt.Errorf("conversations create JSON_FILE")
			}
			method = "POST"
			path = "/conversations"
			body, err = jsonBody(args[2])
		case "list":
			if len(args) != 3 {
				return fmt.Errorf("conversations list PROJECT")
			}
			path = "/conversations?project=" + url.QueryEscape(args[2])
		case "get", "messages":
			if len(args) != 3 {
				return fmt.Errorf("conversation ID required")
			}
			path = "/conversations/" + url.PathEscape(args[2])
			if args[1] == "messages" {
				path += "/messages"
			}
		case "send":
			if len(args) != 4 {
				return fmt.Errorf("conversations send ID JSON_FILE")
			}
			method = "POST"
			path = "/conversations/" + url.PathEscape(args[2]) + "/messages"
			body, err = jsonBody(args[3])
		case "say":
			// To the owner's default direct report, or "@name ..." to another (hierarchy D5).
			if len(args) != 4 {
				return fmt.Errorf("conversations say PROJECT TEXT")
			}
			method = "POST"
			path = "/messages"
			body = map[string]any{"project_id": args[2], "body": args[3], "request_id": protocol.ID()}
		default:
			return fmt.Errorf("unknown conversations command")
		}
	case "approvals":
		if len(args) == 2 && args[1] == "list" || len(args) == 3 && args[1] == "list" {
			path = "/approvals"
			if len(args) == 3 {
				path += "?project=" + url.QueryEscape(args[2])
			}
		} else if len(args) == 3 && args[1] == "get" {
			path = "/approvals/" + url.PathEscape(args[2])
		} else if len(args) == 4 && args[1] == "decide" {
			method = "POST"
			path = "/approvals/" + url.PathEscape(args[2]) + "/decision"
			body, err = jsonBody(args[3])
		} else {
			return fmt.Errorf("approvals list [PROJECT] | get ID | decide ID JSON_FILE")
		}
	case "messages":
		if len(args) != 3 || args[1] != "get" {
			return fmt.Errorf("messages get ID")
		}
		path = "/messages/" + url.PathEscape(args[2])
	case "inbox":
		if len(args) == 1 || len(args) >= 2 && args[1] == "list" {
			path = "/inbox"
			if len(args) == 3 {
				path += "?project=" + url.QueryEscape(args[2])
			} else if len(args) > 3 {
				return fmt.Errorf("inbox list [PROJECT]")
			}
		} else if len(args) == 4 && (args[1] == "answer" || args[1] == "read") {
			method = "POST"
			path = "/inbox/" + url.PathEscape(args[2]) + "/" + args[1]
			body, err = jsonBody(args[3])
		} else {
			return fmt.Errorf("inbox list [PROJECT] | answer/read ID JSON_FILE")
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
