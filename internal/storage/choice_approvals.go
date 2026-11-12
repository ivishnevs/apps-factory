package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
)

// Choice approvals (artifact-choice-approvals). An agent asks one approval over 2–6 registered artifact versions of
// its project, each with a short label and an optional note, and may recommend one. The decider chooses one (the
// chosen version is approved, the others are not chosen: not approved, not rejected, usable later), asks for changes
// (text, optionally naming options) or rejects all (reason). The approval's artifact columns are empty until an
// option is chosen, then they are the chosen version; approval_options finds a choice by any of its versions.

const (
	maxOptionLabel = 60
	maxOptionNote  = 300
	maxDropReason  = 500
)

// oneLine collapses s to one line of single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// choiceOptions checks a choice request's options against the project's registered versions: each is a registered,
// not archived version that no other approval holds (pending, or approved), with a label; none repeats a version or
// a label. It returns the options and the recommended option (0: none).
func choiceOptions(ctx context.Context, tx *Tx, project string, in artifacts.ApprovalRequest) ([]approvals.Option, int, error) {
	if in.ArtifactID != "" || in.ArtifactVersion != "" {
		return nil, 0, inputError("give either options (a choice) or artifact_id and artifact_version (one version), not both")
	}
	if n := len(in.Options); n < approvals.MinOptions || n > approvals.MaxOptions {
		return nil, 0, inputError(fmt.Sprintf("a choice approval has %d to %d options, this one has %d: for one version use artifact_id and artifact_version", approvals.MinOptions, approvals.MaxOptions, n))
	}
	versions, labels := map[string]int{}, map[string]int{}
	out := make([]approvals.Option, 0, len(in.Options))
	for i, o := range in.Options {
		name := fmt.Sprintf("option %d", i+1)
		label, note := oneLine(o.Label), oneLine(o.Note)
		key := o.ArtifactID + "@" + o.ArtifactVersion
		switch {
		case o.ArtifactID == "" || o.ArtifactVersion == "":
			return nil, 0, inputError(name + ": artifact_id and artifact_version required")
		case label == "":
			return nil, 0, inputError(name + " (" + key + "): a short label required, such as \"V1 Mascot\"")
		case utf8.RuneCountInString(label) > maxOptionLabel:
			return nil, 0, inputError(fmt.Sprintf("%s: the label is up to %d characters", name, maxOptionLabel))
		case utf8.RuneCountInString(note) > maxOptionNote:
			return nil, 0, inputError(fmt.Sprintf("%s: the note is one line up to %d characters", name, maxOptionNote))
		}
		if j, ok := versions[key]; ok {
			return nil, 0, inputError(fmt.Sprintf("%s repeats option %d (%s): each option is a different version", name, j, key))
		}
		if j, ok := labels[strings.ToLower(label)]; ok {
			return nil, 0, inputError(fmt.Sprintf("%s repeats the label %q of option %d: each option has its own label", name, label, j))
		}
		versions[key], labels[strings.ToLower(label)] = i+1, i+1
		rec, err := artifactVersionTx(ctx, tx, project, o.ArtifactID, o.ArtifactVersion)
		if errors.Is(err, ErrNotFound) {
			return nil, 0, inputError(fmt.Sprintf("%s (%s): %s is not a registered version of this project: upload or register it first (factory_artifact_list lists them)", name, label, key))
		}
		if err != nil {
			return nil, 0, err
		}
		if rec.ArchivedAt > 0 {
			return nil, 0, inputError(name + ": " + tx.store.archivedError(ctx, tx.company, project, rec).Error())
		}
		old, err := artifactApprovalTx(ctx, tx, project, o.ArtifactID, o.ArtifactVersion)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, 0, err
		}
		if err == nil {
			switch old.StatusOf(o.ArtifactID, o.ArtifactVersion) {
			case "PENDING":
				return nil, 0, conflictError(fmt.Sprintf("%s (%s): %s already waits for approval %s", name, label, key, old.ID))
			case "APPROVED":
				return nil, 0, conflictError(fmt.Sprintf("%s (%s): %s is already approved (approval %s): leave it out", name, label, key, old.ID))
			}
		}
		out = append(out, approvals.Option{Artifact: rec.Artifact, Label: label, Note: note})
	}
	recommended := 0
	if r := in.Recommended; r != nil && r != "" {
		n, err := approvals.Approval{Options: out}.ResolveOption(r)
		if err != nil {
			return nil, 0, inputError("recommended: " + err.Error())
		}
		recommended = n
	}
	return out, recommended, nil
}

// optionsText lists a choice's options for the request's message and the delegate: number, label, version, note,
// the recommendation and what a delegate dropped.
func optionsText(a approvals.Approval) string {
	var b strings.Builder
	for i, o := range a.Options {
		fmt.Fprintf(&b, "\n%d. %s", i+1, a.OptionName(i+1))
		if o.Note != "" {
			b.WriteString(" — " + o.Note)
		}
		if a.Recommended == i+1 {
			b.WriteString(" (recommended)")
		}
		if o.Dropped != "" {
			b.WriteString(" (dropped by " + o.DroppedBy + ": " + o.Dropped + ")")
		}
	}
	return b.String()
}

func (s *Store) requestChoiceApproval(ctx context.Context, company, project string, in artifacts.ApprovalRequest) (approvals.Approval, error) {
	// The question shows every option (inbox-visual-previews), captioned with its label.
	refs := make([]messaging.MaterialRef, len(in.Options))
	for i, o := range in.Options {
		refs[i] = messaging.MaterialRef{ArtifactID: o.ArtifactID, ArtifactVersion: o.ArtifactVersion, Caption: oneLine(o.Label)}
	}
	_, shown := s.withPreviews(ctx, company, project, nil, refs)
	return mutate(ctx, s, company, "artifact.approval:"+project+":"+in.Agent+":"+in.TaskID, in.RequestID, in, func(tx *Tx) (approvals.Approval, error) {
		a := approvals.Approval{ID: protocol.ID(), Kind: "artifact", ProjectID: project, Agent: in.Agent, TaskID: in.TaskID, RunID: in.RunID, Reason: in.Reason, Status: "PENDING", Version: 1, CreatedAt: tx.now().Unix(),
			Audience: in.Audience, Delegate: in.Delegate, Setting: in.Setting}
		if in.RequestID == "" || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 || in.Agent == "" || in.Agent == "owner" || in.TimeoutSeconds < 0 || in.TimeoutSeconds > 7*86400 {
			return a, inputError("agent, reason, request_id and bounded timeout required")
		}
		var err error
		if a.Options, a.Recommended, err = choiceOptions(ctx, tx, project, in); err != nil {
			return a, err
		}
		// Older versions' open requests of the options' artifacts are superseded by this one (artifact-duplicates 1.3).
		keep := map[string][]string{}
		order := []string{}
		for _, o := range a.Options {
			if _, ok := keep[o.Artifact.ID]; !ok {
				order = append(order, o.Artifact.ID)
			}
			keep[o.Artifact.ID] = append(keep[o.Artifact.ID], o.Artifact.Version)
		}
		for _, id := range order {
			if err = supersedeOlderVersions(ctx, tx, project, id, keep[id], "the choice approval "+a.ID); err != nil {
				return a, err
			}
		}
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		a.ExpiresAt = a.CreatedAt + int64(timeout)
		m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, Sender: in.Agent, Recipient: "owner", MessageType: "question",
			Body: "Artifact choice approval " + a.ID + ": " + in.Reason + "\nOptions:" + optionsText(a), Blocking: in.Blocking, TimeoutSeconds: timeout, RequestID: in.RequestID, Materials: shown,
			// A choice is not a version line: it joins its task's topic (people-topics D4). A delegated request is not for
			// people until its delegate escalates it (people-topics D1).
			Brief: in.Brief, Note: in.Note, Topic: in.Topic, Strict: in.Strict, People: messaging.PeopleRef{Path: messaging.PathArtifact, Kind: "approval", Ref: a.ID, HasOptions: true, Skip: a.Delegate != ""}})
		if err != nil {
			return a, err
		}
		a.MessageID = m.ID
		m.ApprovalID = a.ID
		m.DelegatedTo = a.Delegate
		mb, _ := json.Marshal(m)
		if _, err = tx.ExecContext(ctx, "UPDATE messages SET body=? WHERE id=?", mb, m.ID); err != nil {
			return a, err
		}
		b, _ := json.Marshal(a)
		if _, err = tx.ExecContext(ctx, "INSERT INTO approvals(id,project_id,agent,task_id,fingerprint,message_id,status,body,kind,company_id) VALUES(?,?,?,?,?,?,?,?,?,?)", a.ID, project, a.Agent, a.TaskID, "artifact:"+a.ID, m.ID, a.Status, b, "artifact", tx.company); err != nil {
			return a, err
		}
		for i, o := range a.Options {
			if _, err = tx.ExecContext(ctx, "INSERT INTO approval_options(company_id,approval_id,project_id,position,artifact_id,artifact_version) VALUES(?,?,?,?,?,?)", tx.company, a.ID, project, i+1, o.Artifact.ID, o.Artifact.Version); err != nil {
				return a, err
			}
		}
		if a.Delegate != "" {
			// The delegate decides it (approval-delegation): it gets the request; people see it once it escalates.
			body := fmt.Sprintf("Approval %s for you to decide: %s asks to choose one of %d versions: %s\nOptions:%s\nYou decide it under %s. Use factory_approval_decide with approval_id %s and one of these actions:\n"+
				"- choose (choice: the option's number or label) only when what people said covers the choice: cite in cites their earlier answers (message ids), recorded decisions (decision ids) or the company's or project's rules (\"project rules: <quote>\", \"company rules: <quote>\"); Core checks every cite;\n"+
				"- changes (reason: what to change; options: which ones) or reject (reason) when the work is not good enough;\n"+
				"- escalate to hand it to people, optionally dropping options not good enough with drop: [{option, reason}] (keep at least one).\n"+
				"Approving without choosing is not possible.", a.ID, in.Agent, len(a.Options), in.Reason, optionsText(a), a.Setting, a.ID)
			if _, err = sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, Sender: "factory", Recipient: a.Delegate, MessageType: "review_request", Body: body, RequestID: "delegate-" + a.ID}); err != nil {
				return a, err
			}
		}
		return a, entityEvent(ctx, tx, "approval.requested", project, a.ID, a)
	})
}

// supersedeOlderVersions supersedes the pending requests of artifact id's versions other than keep (artifact-duplicates
// 1.3): they leave the Inbox and their agent is told; by names the request that replaces them.
func supersedeOlderVersions(ctx context.Context, tx *Tx, project, id string, keep []string, by string) error {
	older, err := listDocs[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND project_id=? AND kind='artifact' AND artifact_id=? AND status='PENDING'", tx.company, project, id)
	if err != nil {
		return err
	}
	for _, o := range older {
		if o.Artifact == nil || containsString(keep, o.Artifact.Version) {
			continue
		}
		if m, e := messageTx(ctx, tx, o.MessageID); e == nil && (m.Status == "PENDING" || m.Status == "ESCALATED") {
			if _, e = replyMessageTx(ctx, tx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Approval " + o.ID + " SUPERSEDED by " + by + ": the newer version is under review.", RequestID: "superseded-" + o.ID}); e != nil {
				return e
			}
		}
		o.Status, o.DecisionReason, o.DecidedAt = "SUPERSEDED", "superseded by "+by, tx.now().Unix()
		if err = saveApproval(ctx, tx, &o, "approval.superseded"); err != nil {
			return err
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// decideChoice applies a decision to a pending choice approval: choose (an option), changes (text, optionally on
// options) or reject (all, with a reason). Approving without choosing is refused.
func decideChoice(a *approvals.Approval, in approvals.Decision) error {
	n := len(a.Options)
	switch in.Action {
	case approvals.ActionChoose:
		if in.Option < 1 || in.Option > n {
			return inputError(fmt.Sprintf("choose an option from 1 to %d", n))
		}
		o := a.Options[in.Option-1].Artifact
		a.Status, a.Chosen, a.Artifact, a.ExpiresAt = "APPROVED", in.Option, &o, 0
	case approvals.ActionChanges:
		seen := map[int]bool{}
		for _, x := range in.Options {
			if x < 1 || x > n || seen[x] {
				return inputError(fmt.Sprintf("changes: options are distinct numbers from 1 to %d", n))
			}
			seen[x] = true
		}
		a.Status, a.ChangesOn = approvals.StatusChanges, in.Options
	case approvals.ActionReject:
		a.Status = "REJECTED"
	case "approve":
		return inputError(fmt.Sprintf("this approval is a choice among %d options: choose one, ask for changes or reject all", n))
	default:
		return inputError("action must be choose, changes or reject")
	}
	return nil
}

// choiceDecision says what was decided on a choice approval and by whom, for its requester and the record: "V2
// (lens@2) chosen by the owner: darker paywall; not chosen (still usable later): V1 (lens@1)".
func choiceDecision(a approvals.Approval) string {
	by := approvalDecidedBy(a)
	reason := ""
	if r := strings.TrimSpace(a.DecisionReason); r != "" {
		reason = ": " + r
	}
	switch a.Status {
	case "APPROVED":
		s := a.OptionName(a.Chosen) + " chosen " + by + reason
		var others []string
		for i := range a.Options {
			if i+1 != a.Chosen {
				others = append(others, a.OptionName(i+1))
			}
		}
		return s + "; not chosen (still usable later): " + strings.Join(others, ", ")
	case approvals.StatusChanges:
		on := ""
		if len(a.ChangesOn) > 0 {
			names := make([]string, len(a.ChangesOn))
			for i, x := range a.ChangesOn {
				names[i] = a.OptionName(x)
			}
			on = " on " + strings.Join(names, ", ")
		}
		return "changes requested " + by + on + reason
	case "REJECTED":
		return "all options rejected " + by + reason
	}
	return strings.ToLower(a.Status) + " " + by + reason
}

// applyDrops records the options a delegate drops before escalating a choice approval (each with its reason); at
// least one option stays. People still see the dropped ones and may choose them.
func applyDrops(a *approvals.Approval, agent string, drops []approvals.Drop) error {
	if len(drops) == 0 {
		return nil
	}
	if !a.Choice() {
		return inputError("drop is for choice approvals only")
	}
	seen := map[int]bool{}
	for _, d := range drops {
		reason := oneLine(d.Reason)
		switch {
		case d.Option < 1 || d.Option > len(a.Options) || seen[d.Option]:
			return inputError(fmt.Sprintf("drop: options are distinct numbers from 1 to %d", len(a.Options)))
		case reason == "":
			return inputError(fmt.Sprintf("drop: option %d needs the reason it is dropped", d.Option))
		case utf8.RuneCountInString(reason) > maxDropReason:
			return inputError(fmt.Sprintf("drop: the reason is up to %d characters", maxDropReason))
		}
		seen[d.Option] = true
	}
	kept := 0
	for i, o := range a.Options {
		if o.Dropped == "" && !seen[i+1] {
			kept++
		}
	}
	if kept == 0 {
		return inputError("dropping every option is rejecting all: use action reject with the reason")
	}
	for _, d := range drops {
		a.Options[d.Option-1].Dropped, a.Options[d.Option-1].DroppedBy = oneLine(d.Reason), agent
	}
	return nil
}
