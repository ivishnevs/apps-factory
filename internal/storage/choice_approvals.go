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

// Artifact approvals (artifact-choice-approvals, one approval model). An agent asks one approval over 1–6 registered
// artifact versions of its project, each with a short label (optional for a single version) and an optional note,
// and may recommend one. A request for one version is an approval with one option. The decider chooses one (the
// chosen version is approved, the others are not chosen: not approved, not rejected, usable later; approving a
// one-option approval is choosing it), asks for changes (text, optionally naming options) or rejects all (reason).
// The approval's artifact columns are its only option's version, or with several options empty until one is chosen,
// then the chosen version; approval_options finds an approval by any of its versions.

const (
	maxOptionLabel = 60
	maxOptionNote  = 300
	maxDropReason  = 500
)

// oneLine collapses s to one line of single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// requestOptions is a request's options: Options, or ArtifactID/ArtifactVersion as one option without a label.
func requestOptions(in artifacts.ApprovalRequest) ([]artifacts.ChoiceOption, error) {
	if in.ArtifactID == "" && in.ArtifactVersion == "" {
		return in.Options, nil
	}
	if len(in.Options) > 0 {
		return nil, inputError("give either options or artifact_id and artifact_version (the same as one option), not both")
	}
	return []artifacts.ChoiceOption{{ArtifactID: in.ArtifactID, ArtifactVersion: in.ArtifactVersion}}, nil
}

// choiceOptions checks a request's options against the project's registered versions: each is a registered, not
// archived version that no other approval holds (pending, or approved), with a label when there are several; none
// repeats a version or a label. It returns the options and the recommended option (0: none).
func choiceOptions(ctx context.Context, tx *Tx, project string, in artifacts.ApprovalRequest) ([]approvals.Option, int, error) {
	opts, err := requestOptions(in)
	if err != nil {
		return nil, 0, err
	}
	if n := len(opts); n < approvals.MinOptions || n > approvals.MaxOptions {
		return nil, 0, inputError(fmt.Sprintf("an artifact approval has %d to %d options (artifact_id and artifact_version for one version), this one has %d", approvals.MinOptions, approvals.MaxOptions, n))
	}
	versions, labels := map[string]int{}, map[string]int{}
	out := make([]approvals.Option, 0, len(opts))
	for i, o := range opts {
		name := fmt.Sprintf("option %d", i+1)
		if len(opts) == 1 {
			name = "the version"
		}
		label, note := oneLine(o.Label), oneLine(o.Note)
		key := o.ArtifactID + "@" + o.ArtifactVersion
		shown := label
		if shown == "" {
			shown = key
		}
		switch {
		case o.ArtifactID == "" || o.ArtifactVersion == "":
			return nil, 0, inputError(name + ": artifact_id and artifact_version required")
		case label == "" && len(opts) > 1:
			return nil, 0, inputError(name + " (" + key + "): a short label required, such as \"V1 Mascot\"")
		case utf8.RuneCountInString(label) > maxOptionLabel:
			return nil, 0, inputError(fmt.Sprintf("%s: the label is up to %d characters", name, maxOptionLabel))
		case utf8.RuneCountInString(note) > maxOptionNote:
			return nil, 0, inputError(fmt.Sprintf("%s: the note is one line up to %d characters", name, maxOptionNote))
		}
		if j, ok := versions[key]; ok {
			return nil, 0, inputError(fmt.Sprintf("%s repeats option %d (%s): each option is a different version", name, j, key))
		}
		if j, ok := labels[strings.ToLower(label)]; ok && label != "" {
			return nil, 0, inputError(fmt.Sprintf("%s repeats the label %q of option %d: each option has its own label", name, label, j))
		}
		versions[key], labels[strings.ToLower(label)] = i+1, i+1
		rec, err := artifactVersionTx(ctx, tx, project, o.ArtifactID, o.ArtifactVersion)
		if errors.Is(err, ErrNotFound) {
			return nil, 0, inputError(fmt.Sprintf("%s (%s): %s is not a registered version of this project: upload or register it first (factory_artifact_list lists them)", name, shown, key))
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
			// Pending (alone or with other options) or approved; a not chosen option may be asked for again.
			switch old.StatusOf(o.ArtifactID, o.ArtifactVersion) {
			case "PENDING":
				return nil, 0, conflictError(fmt.Sprintf("%s (%s): %s already waits for approval %s", name, shown, key, old.ID))
			case "APPROVED":
				return nil, 0, conflictError(fmt.Sprintf("%s (%s): %s is already approved (approval %s): leave it out", name, shown, key, old.ID))
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

// optionsText lists an approval's options for the request's message and the agent deciding it: number, label,
// version, note, the recommendation and what a delegate dropped.
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

// decideHint tells the agent holding an approval how to decide it, the same for every number of options: approve
// (choose) only with cites of what people said, which Core checks; changes or reject with a reason; or escalate.
func decideHint(a approvals.Approval) string {
	choose, escalate := "choose (choice: the option's number or label)", "escalate to hand it to people (escalating needs a brief), optionally dropping options not good enough with drop: [{option, reason}] (keep at least one).\nApproving without choosing is not possible."
	if a.Single() {
		choose, escalate = "approve", "escalate to hand it to people (escalating needs a brief)."
	}
	return "Use factory_approval_decide with approval_id " + a.ID + " and one of these actions:\n" +
		"- " + choose + " only when what people said covers it: cite in cites their earlier answers (message ids), recorded decisions (decision ids) or the company's or project's rules (\"project rules: <quote>\", \"company rules: <quote>\"); Core checks every cite;\n" +
		"- changes (reason: what to change) or reject (reason) when the work is not good enough; they need no cite;\n" +
		"- " + escalate
}

// approvalAsk says what an artifact approval asks for its deciding agent: "to approve artifact lens version 2" or
// "to choose one of 3 versions".
func approvalAsk(a approvals.Approval) string {
	if a.Single() {
		o := a.Options[0].Artifact
		return "to approve artifact " + o.ID + " version " + o.Version
	}
	return fmt.Sprintf("to choose one of %d versions", len(a.Options))
}

// RequestArtifactApproval asks to approve an artifact version or choose one of several: one approval of 1–6
// options, the one way an artifact approval is made.
func (s *Store) RequestArtifactApproval(ctx context.Context, company, project string, in artifacts.ApprovalRequest) (approvals.Approval, error) {
	// The question shows every option (inbox-visual-previews), captioned with its label.
	opts, _ := requestOptions(in)
	refs := make([]messaging.MaterialRef, len(opts))
	for i, o := range opts {
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
		if a.Single() {
			// The only option is the approval's version from the start.
			o := a.Options[0].Artifact
			a.Artifact = &o
		}
		// Older versions' open requests of the options' artifacts are superseded by this one (artifact-duplicates 1.3):
		// they leave the Inbox and their agent is told; the agent waits for the newest only.
		keep := map[string][]string{}
		order := []string{}
		for _, o := range a.Options {
			if _, ok := keep[o.Artifact.ID]; !ok {
				order = append(order, o.Artifact.ID)
			}
			keep[o.Artifact.ID] = append(keep[o.Artifact.ID], o.Artifact.Version)
		}
		by := "the choice approval " + a.ID
		if a.Single() {
			by = "version " + a.Artifact.Version
		}
		for _, id := range order {
			if err = supersedeOlderVersions(ctx, tx, project, id, keep[id], by); err != nil {
				return a, err
			}
		}
		if a.Single() {
			// A composite showing an older version of this artifact goes (people-topics D11).
			if err = supersedeComposites(ctx, tx, project, a.Artifact.ID, "", a.Artifact.Version, "its part "+a.Artifact.ID+" was replaced by "+a.Artifact.Version); err != nil {
				return a, err
			}
		}
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		a.ExpiresAt = a.CreatedAt + int64(timeout)
		// A delegated request is not for people until its delegate escalates it (people-topics D1). One version is an
		// artifact version line in people topics; a choice of several joins its task's topic (people-topics D4).
		people := messaging.PeopleRef{Path: messaging.PathArtifact, Kind: "approval", Ref: a.ID, HasOptions: true, Skip: a.Delegate != ""}
		body := "Artifact choice approval " + a.ID + ": " + in.Reason + "\nOptions:" + optionsText(a)
		if a.Single() {
			people.Lineage, people.Version, people.HasOptions = a.Artifact.ID, a.Artifact.Version, false
			ref, _ := json.Marshal(a.Artifact)
			body = "Artifact approval " + a.ID + ": " + in.Reason + "\nExact artifact version: " + string(ref)
		}
		m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, Sender: in.Agent, Recipient: "owner", MessageType: "question",
			Body: body, Blocking: in.Blocking, TimeoutSeconds: timeout, RequestID: in.RequestID, Materials: shown,
			Brief: in.Brief, Note: in.Note, Topic: in.Topic, Strict: in.Strict, People: people})
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
		var artifactID, artifactVersion any
		if a.Artifact != nil {
			artifactID, artifactVersion = a.Artifact.ID, a.Artifact.Version
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO approvals(id,project_id,agent,task_id,fingerprint,message_id,status,body,kind,artifact_id,artifact_version,company_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", a.ID, project, a.Agent, a.TaskID, "artifact:"+a.ID, m.ID, a.Status, b, "artifact", artifactID, artifactVersion, tx.company); err != nil {
			return a, err
		}
		for i, o := range a.Options {
			if _, err = tx.ExecContext(ctx, "INSERT INTO approval_options(company_id,approval_id,project_id,position,artifact_id,artifact_version) VALUES(?,?,?,?,?,?)", tx.company, a.ID, project, i+1, o.Artifact.ID, o.Artifact.Version); err != nil {
				return a, err
			}
		}
		if a.Delegate != "" {
			// The delegate decides it (approval-delegation): it gets the request; people see it once it escalates.
			body := fmt.Sprintf("Approval %s for you to decide: %s asks %s: %s\nOptions:%s\nYou decide it under %s. %s", a.ID, in.Agent, approvalAsk(a), in.Reason, optionsText(a), a.Setting, decideHint(a))
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

// decideChoice applies a decision to a pending artifact approval, whatever its number of options: choose (an
// option; approve chooses the only option of a one-option approval), changes (text, optionally on options) or
// reject (all, with a reason). Approving one of several options without choosing is refused.
func decideChoice(a *approvals.Approval, in approvals.Decision) error {
	n := len(a.Options)
	if in.Action == approvals.ActionApprove {
		if n != 1 {
			return inputError(fmt.Sprintf("this approval is a choice among %d options: choose one, ask for changes or reject all", n))
		}
		in.Action, in.Option = approvals.ActionChoose, 1
	}
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
	default:
		if n == 1 {
			return inputError("action must be approve (choose), changes or reject")
		}
		return inputError("action must be choose, changes or reject")
	}
	return nil
}

// choiceDecision says what was decided on an artifact approval and by whom, for its requester and the record: "V2
// (lens@2) chosen by the owner: darker paywall; not chosen (still usable later): V1 (lens@1)". One option is named
// by its approval, so it reads "approved by the owner: ship it", "changes requested by …", "rejected by …".
func choiceDecision(a approvals.Approval) string {
	by := approvalDecidedBy(a)
	reason := ""
	if r := strings.TrimSpace(a.DecisionReason); r != "" {
		reason = ": " + r
	}
	if a.Single() {
		return strings.ToLower(strings.ReplaceAll(a.Status, "_", " ")) + " " + by + reason
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
	if len(a.Options) < 2 {
		return inputError("drop leaves out some of several options: this approval has one, reject it or escalate it whole")
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
