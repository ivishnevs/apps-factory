package factorytools

import (
	"slices"

	"appfactory/internal/messaging"
)

// People topics (people-topics): the tools that can bring an item to people take an optional brief (and note, for an
// item joining an open topic); Core's gate requires them only when the item goes to people.

// briefTools take brief and note; topicTools take topic (an open people topic to join).
var (
	briefTools = []string{"factory_owner_ask", "factory_message_ask", "factory_question_forward", "factory_owner_request_approval",
		"factory_artifact_request_approval", "factory_approval_decide", "factory_decision_propose", ExitPrefix + "ask", ExitPrefix + "submit"}
	topicTools = []string{"factory_owner_ask", "factory_message_ask", "factory_question_forward", "factory_artifact_request_approval",
		"factory_approval_decide", "factory_message_send", "factory_message_reply", ExitPrefix + "ask"}
)

func briefSchema() map[string]any {
	s := map[string]any{"type": "string"}
	return map[string]any{"type": "object", "additionalProperties": false, "description": messaging.BriefHelp, "properties": map[string]any{
		"title": s, "ask": s, "after": s, "recommendation": s, "changes": s, "scope": s, "context": s}}
}

func noteSchema() map[string]any {
	s := map[string]any{"type": "string"}
	return map[string]any{"type": "object", "additionalProperties": false, "description": "Your item's note when it joins a topic people already have open: what it is, and what changed since the version they saw.",
		"properties": map[string]any{"what": s, "changes": s}}
}

// withPeopleFields adds brief, note and topic to the people-facing tools.
func withPeopleFields(out []Tool) {
	for i := range out {
		props, _ := out[i].InputSchema["properties"].(map[string]any)
		if props == nil {
			continue
		}
		if slices.Contains(briefTools, out[i].Name) {
			props["brief"] = briefSchema()
			if _, taken := props["note"]; !taken { // question_forward's note is its own text for the manager
				props["note"] = noteSchema()
			}
			out[i].Description += " " + messaging.BriefHelp
		}
		if out[i].Name == "factory_decision_propose" { // its topic is the decision's subject
			props["people_topic"] = map[string]any{"type": "string", "description": "An open people topic to put this proposal in."}
		}
		if slices.Contains(topicTools, out[i].Name) {
			props["topic"] = map[string]any{"type": "string", "description": "An open people topic to put this in (its id from your brief or a people topic answer); without it Core picks the topic by the artifact's version line and the task."}
		}
	}
}
