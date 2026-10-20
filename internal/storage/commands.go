package storage

import (
	"context"
	"encoding/json"
	"slices"

	"appfactory/internal/approvals"
	"appfactory/internal/protocol"
)

// UseCommandGrant consumes the oldest APPROVED, unexpired command grant of the run's project, agent and
// task whose argv equals argv exactly (command guards, node-agent 4.2). The grant becomes USED; without
// one it returns ErrNotFound.
func (s *Store) UseCommandGrant(ctx context.Context, company, runID string, argv []string) (approvals.Approval, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return approvals.Approval{}, err
	}
	defer tx.Rollback()
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	grants, err := listDocs[approvals.Approval](ctx, tx, `SELECT body FROM approvals WHERE company_id=? AND kind='command' AND status='APPROVED'
 AND project_id=? AND agent=? AND task_id=? AND (body->>'expires_at')::bigint>? ORDER BY seq`, company, spec.Project, spec.Agent, spec.TaskID, tx.now().Unix())
	if err != nil {
		return approvals.Approval{}, err
	}
	for _, a := range grants {
		if slices.Equal(CommandArgv(a.Call.Arguments), argv) {
			a.Status = "USED"
			if err = saveApproval(ctx, tx, &a, "approval.used"); err != nil {
				return a, err
			}
			return a, tx.Commit()
		}
	}
	return approvals.Approval{}, ErrNotFound
}

// CommandArgv is the argv of a command grant's call arguments ({"argv": [...]}); nil when malformed.
func CommandArgv(args map[string]any) []string {
	raw, ok := args["argv"].([]any)
	if !ok {
		if s, ok := args["argv"].([]string); ok {
			return s
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// RecordCommandRefusal adds a refusal event to the run (spec node-agent 4.1) and a feed row.
func (s *Store) RecordCommandRefusal(ctx context.Context, company, runID string, argv []string, effect, rule string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, runID)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"argv": argv, "effect": effect, "rule": rule})
	e := protocol.Wrap("agent.progress", runID, "", protocol.Event{RunID: runID, Method: "factory/command_refused", Data: data})
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(id,run_id,body,company_id) VALUES(?,?,?,?)", e.ID, runID, body, company); err != nil {
		return err
	}
	summary := argv[0] + " refused (" + effect + ", rule " + rule + ")"
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "run.command_refused", EntityID: runID, ProjectID: spec.Project, CreatedAt: tx.now(), TaskID: spec.TaskID, RunID: runID, Agent: spec.Agent, Status: effect, Summary: summary, Ref: rule}); err != nil {
		return err
	}
	return tx.Commit()
}
