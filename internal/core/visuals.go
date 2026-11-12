package core

import (
	"encoding/json"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// Visual attachments on messages and decision proposals (inbox-visual-previews): the tool calls carry attachments
// (files of the run's worktree the node uploaded) and materials (references); Core stores them with the message.

// toolWarning is a non-blocking warning in a tool answer: the call was applied.
type toolWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// visualArgs reads a tool call's attachments and materials.
func visualArgs(args map[string]any) ([]messaging.Attachment, []messaging.MaterialRef, error) {
	var in struct {
		Attachments []messaging.Attachment  `json:"attachments"`
		Materials   []messaging.MaterialRef `json:"materials"`
	}
	err := decodeTool(map[string]any{"attachments": args["attachments"], "materials": args["materials"]}, &in)
	for i := range in.Attachments {
		in.Attachments[i].Preview = nil // Core renders previews itself
	}
	for i := range in.Materials {
		m := &in.Materials[i]
		*m = messaging.MaterialRef{ArtifactID: m.ArtifactID, ArtifactVersion: m.ArtifactVersion, ReportID: m.ReportID, Path: m.Path, Caption: m.Caption}
	}
	return in.Attachments, in.Materials, err
}

// visualWarnings warns a message to people that names image or video files without attaching anything: the
// person would have to go and find them.
func visualWarnings(toPeople bool, atts []messaging.Attachment, mats []messaging.MaterialRef, texts ...string) []toolWarning {
	if !toPeople || len(atts) > 0 || len(mats) > 0 || !storage.NamesVisuals(strings.Join(texts, "\n")) {
		return nil
	}
	return []toolWarning{{Code: storage.VisualWarning, Message: "The message names image or video files but attaches none: people see only the path. " +
		"Send it again with attachments ({path, caption} of files in your worktree) or materials (an artifact version or a report attachment) so they see the images where they answer."}}
}

// withWarnings is v (a JSON object) with warnings added; v as it is without warnings.
func withWarnings(v any, warnings []toolWarning) any {
	if len(warnings) == 0 {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	out["warnings"] = warnings
	return out
}

// questionTexts is the text of a message's form.
func questionTexts(qs []messaging.Question) string {
	var b strings.Builder
	for _, q := range qs {
		b.WriteString(q.Question + "\n" + strings.Join(q.Options, "\n") + "\n")
	}
	return b.String()
}
