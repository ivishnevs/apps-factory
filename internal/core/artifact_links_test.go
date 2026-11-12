package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// TestArtifactLinks (artifact-links): a message to the owner, a question forwarded to the owner and a decision
// proposal that name an artifact of the project carry its version as a material (the inbox links it to its Library
// card): a bare id is its latest version, a URI or id@version that version; the sender's own materials come first
// and are not repeated; a plain-word id, an unregistered version and messages between agents link nothing.
func TestArtifactLinks(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "design/pouch.md"), "# Pouch\n")
	writeFile(t, filepath.Join(work, "design/pouch-2.md"), "# Pouch, rounder\n")
	writeFile(t, filepath.Join(work, "design/logo.md"), "# Logo\n")
	h.create("design", "code", "demo")
	r := h.start("design")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}
	call := func(name string, args map[string]any) any {
		t.Helper()
		out, err := a.Call(ctx, factorytools.Call{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(name, err)
		}
		return out
	}
	for i, up := range [][3]string{{"pouch-design", "v1", "design/pouch.md"}, {"pouch-design", "v2", "design/pouch-2.md"}, {"logo", "1", "design/logo.md"}} {
		call("factory_artifact_upload", map[string]any{"artifact_id": up[0], "artifact_version": up[1], "path": up[2], "request_id": "up-" + string(rune('a'+i))})
	}
	refs := func(ms []messaging.MaterialRef) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.ArtifactID+"@"+m.ArtifactVersion)
		}
		return out
	}
	same := func(what string, got []messaging.MaterialRef, want ...string) {
		t.Helper()
		if g := refs(got); len(g) != len(want) || len(want) > 0 && !equal(g, want) {
			t.Fatalf("%s: %v, want %v", what, g, want)
		}
	}

	// The real case: a question to the owner names the artifact in plain text.
	q := asAnswer(t, call("factory_owner_ask", map[string]any{"body": "The artifact pouch-design is ready: may it go to print?", "options": []any{"Print", "Rework"}, "request_id": "ask",
		"brief": map[string]any{"title": "Print the pouch design?", "after": "Print: it goes to the printer; Rework: the designer revises it", "recommendation": "Print: it matches the brief"}}))
	same("owner question", q.Materials, "pouch-design@v2")
	if q.Materials[0].Name != "pouch-2.md" || q.Materials[0].URI != "factory://artifacts/pouch-design/v2" || len(q.Materials[0].SHA256) != 64 {
		t.Fatalf("%+v", q.Materials[0])
	}
	if got, _ := h.s.Message(ctx, storage.DefaultCompany, q.ID); len(got.Materials) != 1 {
		t.Fatalf("stored %+v", got.Materials)
	}

	// Versions named in the text; the sender's own material first and once; a plain-word id and an unknown version
	// link nothing.
	m := asAnswer(t, call("factory_message_send", map[string]any{"recipient": "owner", "message_type": "review_request", "request_id": "cmp",
		"body":      "Compare pouch-design@v1 with factory://artifacts/pouch-design/v2 (not pouch-design@v7); the logo stays.",
		"materials": []any{map[string]any{"artifact_id": "pouch-design", "artifact_version": "v1", "caption": "before"}}}))
	same("named versions", m.Materials, "pouch-design@v1", "pouch-design@v2")
	if m.Materials[0].Caption != "before" {
		t.Fatalf("%+v", m.Materials)
	}

	// Between agents nothing is added.
	n := asAnswer(t, call("factory_message_send", map[string]any{"recipient": "manager", "message_type": "notification", "body": "pouch-design v1 is superseded", "request_id": "agent"}))
	same("to an agent", n.Materials)

	// A question to the manager that reaches the owner by a forward links what it names on the way.
	ask := asAnswer(t, call("factory_message_ask", map[string]any{"recipient": "manager", "body": "Can pouch-design v1 go to print?", "options": []any{"Yes", "No"}, "request_id": "ask-manager"}))
	same("question to the manager", ask.Materials)
	fwd, err := h.s.ForwardQuestion(ctx, storage.DefaultCompany, ask.ID, "manager", ask.Version, "the owner decides print runs; logo@1 too", "fwd", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Recipient != "owner" {
		t.Fatal(fwd.Recipient)
	}
	same("forwarded", fwd.Materials, "pouch-design@v1", "logo@1")

	// A decision proposal: its text and its linked artifacts.
	out := call("factory_decision_propose", map[string]any{"topic": "print-run", "value": "print pouch-design", "rationale": "it tested well",
		"artifacts": []any{map[string]any{"id": "logo", "version": "1", "uri": "factory://artifacts/logo/1"}}, "request_id": "dec", "brief": map[string]any{"after": "the team prints it"}})
	var d decisions.Decision
	b, _ := json.Marshal(out)
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(string(b), err)
	}
	same("decision", d.Materials, "pouch-design@v2", "logo@1")
	if dm, err := h.s.Message(ctx, storage.DefaultCompany, d.MessageID); err != nil || len(dm.Materials) != 2 {
		t.Fatal(dm.Materials, err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
