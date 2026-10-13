package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// ownerCommand: pause/resume/pauses, stop/start (kill switch), pending, and short answer/approve/reject.
func ownerCommand(ctx context.Context, api worker.API, args []string) error {
	scope := func(args []string) (string, string, []string, error) {
		switch {
		case len(args) >= 1 && args[0] == "--all":
			return "company", "", args[1:], nil
		case len(args) >= 2 && (args[0] == "project" || args[0] == "agent"):
			return args[0], args[1], args[2:], nil
		}
		return "", "", nil, fmt.Errorf("scope: --all | project ID | agent NAME")
	}
	var result any
	switch args[0] {
	case "pause":
		s, t, rest, err := scope(args[1:])
		if err != nil {
			return err
		}
		if err = api.Do(ctx, "POST", "/pauses", map[string]string{"scope": s, "target": t, "reason": strings.Join(rest, " ")}, &result); err != nil {
			return err
		}
	case "resume", "start":
		s, t := "company", ""
		if args[0] == "resume" {
			var err error
			if s, t, _, err = scope(args[1:]); err != nil {
				return err
			}
		}
		if err := api.Do(ctx, "POST", "/pauses/resume", map[string]string{"scope": s, "target": t}, &result); err != nil {
			return err
		}
	case "pauses":
		var pauses []storage.Pause
		if err := api.Do(ctx, "GET", "/pauses", nil, &pauses); err != nil {
			return err
		}
		result = pauses
	case "stop":
		if err := api.Do(ctx, "POST", "/stop", map[string]string{"reason": strings.Join(args[1:], " ")}, &result); err != nil {
			return err
		}
	case "pending":
		if err := api.Do(ctx, "GET", "/pending/count", nil, &result); err != nil {
			return err
		}
	case "answer":
		if len(args) < 3 {
			return fmt.Errorf("answer MESSAGE_ID TEXT")
		}
		var m struct {
			Version   int   `json:"version"`
			Questions []any `json:"questions"`
		}
		if err := api.Do(ctx, "GET", "/messages/"+url.PathEscape(args[1]), nil, &m); err != nil {
			return err
		}
		if len(m.Questions) > 0 {
			return fmt.Errorf("message %s has a structured form; use: inbox answer %s JSON_FILE", args[1], args[1])
		}
		body := map[string]any{"version": m.Version, "body": strings.Join(args[2:], " "), "request_id": protocol.ID()}
		if err := api.Do(ctx, "POST", "/inbox/"+url.PathEscape(args[1])+"/answer", body, &result); err != nil {
			return err
		}
	case "approve", "reject":
		if len(args) < 3 {
			return fmt.Errorf("%s APPROVAL_ID REASON", args[0])
		}
		var a struct {
			Version int `json:"version"`
		}
		if err := api.Do(ctx, "GET", "/approvals/"+url.PathEscape(args[1]), nil, &a); err != nil {
			return err
		}
		body := map[string]any{"version": a.Version, "action": args[0], "reason": strings.Join(args[2:], " "), "request_id": protocol.ID()}
		if err := api.Do(ctx, "POST", "/approvals/"+url.PathEscape(args[1])+"/decision", body, &result); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown owner command")
	}
	return printJSON(result)
}
