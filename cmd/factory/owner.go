package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"appfactory/internal/messaging"
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
		case len(args) >= 2 && (args[0] == "project" || args[0] == "agent" || args[0] == "quota_key"):
			return args[0], args[1], args[2:], nil
		}
		return "", "", nil, fmt.Errorf("scope: --all | project ID | agent NAME | quota_key KEY")
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
	case "escalations":
		path := "/escalations"
		if len(args) > 1 && args[1] == "--all" {
			path += "?all=1"
		}
		if err := api.Do(ctx, "GET", path, nil, &result); err != nil {
			return err
		}
	case "pending":
		if err := api.Do(ctx, "GET", "/pending/count", nil, &result); err != nil {
			return err
		}
	case "direct":
		// factory direct TASK TEXT... [--urgent]: an owner directive (authority input) to a task.
		urgent := len(args) > 0 && args[len(args)-1] == "--urgent"
		if urgent {
			args = args[:len(args)-1]
		}
		if len(args) < 3 {
			return fmt.Errorf("direct TASK TEXT [--urgent]")
		}
		body := map[string]any{"text": strings.Join(args[2:], " "), "urgent": urgent, "request_id": protocol.ID()}
		if err := api.Do(ctx, "POST", "/tasks/"+url.PathEscape(args[1])+"/directives", body, &result); err != nil {
			return err
		}
	case "answer":
		if len(args) < 3 {
			return fmt.Errorf("answer MESSAGE_ID TEXT")
		}
		var m struct {
			Version   int                  `json:"version"`
			Questions []messaging.Question `json:"questions"`
		}
		if err := api.Do(ctx, "GET", "/messages/"+url.PathEscape(args[1]), nil, &m); err != nil {
			return err
		}
		text := strings.Join(args[2:], " ")
		body := map[string]any{"version": m.Version, "body": text, "request_id": protocol.ID()}
		switch {
		case len(m.Questions) == 1 && m.Questions[0].Type == "single_choice":
			// One choice (escalations): the answer is the option, e.g. factory answer MSG unblock.
			if !slices.Contains(m.Questions[0].Options, args[2]) {
				return fmt.Errorf("answer one of: %s", strings.Join(m.Questions[0].Options, ", "))
			}
			body["answers"] = map[string]any{m.Questions[0].ID: args[2]}
		case len(m.Questions) > 0:
			return fmt.Errorf("message %s has a structured form; use: inbox answer %s JSON_FILE", args[1], args[1])
		}
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
