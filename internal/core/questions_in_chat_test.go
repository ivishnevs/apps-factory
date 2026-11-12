package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// TestAgentQuestionsInChat replays 2026-11-11 (agent-questions-in-chat 4.2) with fakes: App Release (demo, which
// reports to the lead) asks the owner which asc profile to use on its TestFlight task. Listed in the text only, the
// question is refused with a hint; with four options it is sent as a choice. It is open for the project (the web
// shows it in the lead's chat and on the task from this list), the lead's conversation brief carries it, the
// owner's answer "Stashy" closes it, wakes App Release with the answer, and the lead's next brief shows it answered.
func TestAgentQuestionsInChat(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	call := func(r exitRun, name string, args map[string]any) (int, string) {
		t.Helper()
		w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
		return w.Code, w.Body.String()
	}
	const body = "Which App Store Connect account (asc profile) should I use for the Tracely TestFlight upload? Profiles: ASC Key, Cheflet, Cheflet-H7N3F3AN5P (default), Stashy."
	profiles := []any{"ASC Key", "Cheflet", "Cheflet-H7N3F3AN5P", "Stashy"}

	h.create("tf", "notes", "demo")
	r := h.start("tf")
	// Listed choices without options: refused with the hint, nothing sent.
	if code, out := call(r, "factory_owner_ask", map[string]any{"body": body, "blocking": true, "request_id": "asc"}); code != 400 || !strings.Contains(out, "choices_without_options") || !strings.Contains(out, "options") {
		t.Fatal(code, out)
	}
	if code, out := call(r, "factory_exit_ask", map[string]any{"question": body}); code != 409 || !strings.Contains(out, "choices_without_options") {
		t.Fatal(code, out)
	}
	if open, _ := h.s.Inbox(ctx, storage.DefaultCompany, "notes"); len(open) != 0 {
		t.Fatalf("a refused question was sent: %+v", open)
	}
	// options and questions together are ambiguous.
	if code, out := call(r, "factory_owner_ask", map[string]any{"body": body, "options": profiles, "questions": []any{map[string]any{"id": "x", "type": "text", "question": "x"}}, "request_id": "both"}); code != 400 {
		t.Fatal(code, out)
	}
	code, out := call(r, "factory_owner_ask", map[string]any{"body": body, "options": profiles, "blocking": true, "request_id": "asc-options", "brief": testBrief()})
	if code != 200 {
		t.Fatal(code, out)
	}
	var q messaging.Message
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Questions) != 1 || q.Questions[0].Type != "single_choice" || len(q.Questions[0].Options) != 4 || !strings.HasSuffix(q.Questions[0].Question, "upload?") {
		t.Fatalf("%+v", q.Questions)
	}
	h.end(r, "succeeded", "none")

	// Open for the project with the asking agent and its task: the web's lead chat and task page read this list.
	open, err := h.s.Inbox(ctx, storage.DefaultCompany, "notes")
	if err != nil || len(open) != 1 || open[0].ID != q.ID || open[0].Sender != "demo" || open[0].TaskID != "tf" || strings.HasPrefix(open[0].ConversationID, "owner-") {
		t.Fatalf("%+v %v", open, err)
	}

	// The owner talks to the lead; the lead's conversation brief carries the team's open question.
	say := func(text, key string) {
		t.Helper()
		if w := h.do(h.c.Token, "POST", "/messages", map[string]any{"project_id": "notes", "body": "@manager " + text, "request_id": key}, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	say("how is the TestFlight upload going?", "say-1")
	conv := storage.ConversationID("notes", "manager")
	lead := h.start(conv)
	run, _ := h.s.Get(ctx, storage.DefaultCompany, lead.id)
	if !strings.Contains(run.Spec.Prompt, "Your team's questions to people") || !strings.Contains(run.Spec.Prompt, "open: demo asks on task tf (message "+q.ID+")") || !strings.Contains(run.Spec.Prompt, "[options: ASC Key, Cheflet, Cheflet-H7N3F3AN5P, Stashy]") {
		t.Fatal(run.Spec.Prompt)
	}
	if code, out := call(lead, "factory_exit_done", map[string]any{"summary": "answered"}); code != 200 {
		t.Fatal(code, out)
	}
	h.end(lead, "succeeded", "accepted")

	// The owner answers "Stashy" in the chat (the web's answer action): the question closes everywhere.
	h.fake.Advance(time.Minute)
	if w := h.do(h.c.Token, "POST", "/inbox/"+q.ID+"/answer", map[string]any{"version": q.Version, "body": "Stashy", "answers": map[string]any{"choice": "Stashy"}, "request_id": "answer-asc"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if open, _ = h.s.Inbox(ctx, storage.DefaultCompany, "notes"); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	// App Release wakes with the answer.
	if got := h.task("tf"); got.Status != "READY" {
		t.Fatalf("%+v", got)
	}
	next := h.start("tf")
	if run, _ = h.s.Get(ctx, storage.DefaultCompany, next.id); !strings.Contains(run.Spec.Prompt, "Answer to your blocking question "+q.ID) || !strings.Contains(run.Spec.Prompt, "Stashy") {
		t.Fatal(run.Spec.Prompt)
	}

	// The lead's next brief shows what was decided.
	say("thanks", "say-2")
	lead = h.start(conv)
	if run, _ = h.s.Get(ctx, storage.DefaultCompany, lead.id); !strings.Contains(run.Spec.Prompt, "answered: demo asked on task tf") || !strings.Contains(run.Spec.Prompt, "answered: Stashy") || strings.Contains(run.Spec.Prompt, "open: demo") {
		t.Fatal(run.Spec.Prompt)
	}

	// options: [] sends a question that only looks like a choice as free text; the owner may answer a choice in
	// their own words ("Something else…": the web sends the text without form answers).
	code, out = call(next, "factory_owner_ask", map[string]any{"body": "Should the build notes mention Android or not?", "options": []any{}, "request_id": "free", "brief": testBrief()})
	if code != 200 {
		t.Fatal(code, out)
	}
	code, out = call(next, "factory_owner_ask", map[string]any{"body": "Which tester group?", "options": []any{"Internal", "External"}, "request_id": "group"})
	if code != 200 {
		t.Fatal(code, out)
	}
	var g messaging.Message
	_ = json.Unmarshal([]byte(out), &g)
	if w := h.do(h.c.Token, "POST", "/inbox/"+g.ID+"/answer", map[string]any{"version": g.Version, "body": "Both, internal first", "request_id": "answer-group"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if a, err := h.s.AnswerFor(ctx, storage.DefaultCompany, g.ID); err != nil || a.Body != "Both, internal first" || len(a.Answers) != 0 {
		t.Fatalf("%+v %v", a, err)
	}
}
