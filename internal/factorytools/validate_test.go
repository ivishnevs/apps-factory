package factorytools

import (
	"slices"
	"strings"
	"testing"

	"appfactory/internal/closedset"
)

func tool(t *testing.T, name string) Tool {
	t.Helper()
	for _, d := range append(Definitions(), ExitDefinitions()...) {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no tool %s", name)
	return Tool{}
}

// A guessed call learns every missing and unknown argument from one 400, not one per retry.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	approval := tool(t, "factory_artifact_request_approval")
	err := Validate(approval, map[string]any{"artifact_id": "p", "artifact_version": "1", "blocking": false, "summary": "x"})
	if err == nil {
		t.Fatal("guessed arguments accepted")
	}
	want := "missing required arguments: reason, request_id; unknown arguments: summary (accepted arguments: artifact_id, artifact_version, audience, blocking, brief, note, options, reason, recommended, request_id, timeout_seconds, topic)"
	if err.Error() != want {
		t.Fatalf("got  %q\nwant %q", err, want)
	}

	get := tool(t, "factory_artifact_get")
	err = Validate(get, map[string]any{"id": "p", "version": 1.5})
	want = "missing required arguments: artifact_id, artifact_version; unknown arguments: id, version (accepted arguments: artifact_id, artifact_version)"
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %q", err, want)
	}

	if err := Validate(get, map[string]any{"artifact_id": "p", "artifact_version": "1"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNestedPaths(t *testing.T) {
	create := tool(t, "factory_task_create")
	err := Validate(create, map[string]any{"title": 3.0, "request_id": "r", "required_artifacts": []any{map[string]any{"uri": 1.0, "x": true}}})
	if err == nil {
		t.Fatal("bad nested arguments accepted")
	}
	for _, part := range []string{"required_artifacts[0].x", "required_artifacts[0].uri: string required", "title: string required"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("%q lacks %q", err, part)
		}
	}
}

// throughput-signals 2.2: task resources are a map of class to an integer need.
func TestValidateTaskResources(t *testing.T) {
	create := tool(t, "factory_task_create")
	if err := Validate(create, map[string]any{"title": "gate", "request_id": "r", "resources": map[string]any{"mac-build": 1.0}}); err != nil {
		t.Fatal(err)
	}
	err := Validate(create, map[string]any{"title": "gate", "request_id": "r", "resources": map[string]any{"mac-build": "one"}})
	if err == nil || !strings.Contains(err.Error(), "resources.mac-build: integer required") {
		t.Fatalf("bad need accepted: %v", err)
	}
}

// TestClosedSetEnums (contracts-at-the-boundary §3): every closed-set argument's enum is the set Core validates against,
// and a value outside it is refused with every allowed value.
func TestClosedSetEnums(t *testing.T) {
	for _, c := range []struct {
		tool, arg string
		set       closedset.Set
	}{
		{"factory_report_create", "kind", closedset.ReportKinds},
		{"factory_report_list", "kind", closedset.ReportListKinds},
		{"factory_message_send", "message_type", closedset.MessageTypes},
		{"factory_message_ask", "questions[].type", closedset.QuestionTypes},
		{"factory_owner_ask", "questions[].type", closedset.QuestionTypes},
		{"factory_decision_list", "status", closedset.DecisionListStatuses},
		{"factory_decision_record", "status", closedset.DecisionRecordStatuses},
		{"factory_artifact_request_approval", "audience", closedset.ArtifactAudiences},
		{"factory_task_list", "status", closedset.TaskStatuses},
	} {
		props := tool(t, c.tool).InputSchema["properties"].(map[string]any)
		arg := c.arg
		if name, ok := strings.CutSuffix(arg, "[].type"); ok {
			props = props[name].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
			arg = "type"
		}
		enum, _ := props[arg].(map[string]any)["enum"].([]string)
		if !slices.Equal(enum, c.set) {
			t.Errorf("%s %s: enum %v, validator set %v", c.tool, c.arg, enum, c.set)
		}
	}
	err := Validate(tool(t, "factory_report_create"), map[string]any{"kind": "audit", "summary": "s", "purpose": "p", "changes": []any{}, "result": "r", "request_id": "x"})
	if err == nil || !strings.Contains(err.Error(), `kind: unsupported value "audit"; one of: dependency_installation, environment_change, plan_deviation, problem, stage_completed`) {
		t.Fatal(err)
	}
}
