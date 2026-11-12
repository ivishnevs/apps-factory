package factorytools

import (
	"strings"
	"testing"
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
	want := "missing required arguments: reason, request_id; unknown arguments: summary (accepted arguments: artifact_id, artifact_version, audience, blocking, brief, note, reason, request_id, timeout_seconds, topic)"
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
