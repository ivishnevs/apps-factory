package core

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/artifacts"
	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/preview"
	"appfactory/internal/storage"
)

// writePNG writes a w x h PNG of one colour into the worktree.
func writePNG(t *testing.T, name string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// toolAnswer is a message tool's answer: the message and the non-blocking warnings.
type toolAnswer struct {
	messaging.Message
	Warnings []toolWarning `json:"warnings"`
}

func asAnswer(t *testing.T, out any) toolAnswer {
	t.Helper()
	var a toolAnswer
	b, _ := json.Marshal(out)
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(string(b), err)
	}
	return a
}

// TestVisualVerdictInInbox replays VP-2b (inbox-visual-previews 4.1) with a fake runtime: the producer asks the
// owner for a verdict on Storm Towers v2 and attaches the three screenshots captioned classic, v1 and v2. The node
// uploads them, Core keeps them in the order given with their captions, renders a preview of the large one and the
// question reaches the Inbox with them; the original stays downloadable. A path in the text without attachments is
// sent with the visual_without_attachment warning; an attachment nobody uploaded is refused.
func TestVisualVerdictInInbox(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	work := t.TempDir()
	writePNG(t, filepath.Join(work, "docs/qa/towers/classic.png"), 320, 200, color.RGBA{200, 0, 0, 255})
	writePNG(t, filepath.Join(work, "docs/qa/towers/v1.png"), 320, 200, color.RGBA{0, 200, 0, 255})
	writePNG(t, filepath.Join(work, "docs/qa/towers/v2.png"), 2400, 1500, color.RGBA{0, 0, 200, 255})
	h.create("vp-2b", "code", "demo")
	r := h.start("vp-2b")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}

	out, err := a.Call(ctx, factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{
		"body":      "Storm Towers v2 (VP-2b): which towers ship?",
		"questions": []any{map[string]any{"id": "verdict", "type": "single_choice", "question": "Verdict", "options": []any{"v2", "v1", "classic"}}},
		"attachments": []any{
			map[string]any{"path": "docs/qa/towers/classic.png", "caption": "classic"},
			map[string]any{"path": "docs/qa/towers/v1.png", "caption": "v1"},
			map[string]any{"path": "docs/qa/towers/v2.png", "caption": "v2"},
		},
		"request_id": "verdict-v2",
		"brief":      testBrief(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := asAnswer(t, out)
	if len(m.Warnings) != 0 || len(m.Attachments) != 3 {
		t.Fatalf("%+v", m)
	}
	for i, want := range []string{"classic", "v1", "v2"} {
		at := m.Attachments[i]
		if at.Caption != want || at.Path != "docs/qa/towers/"+want+".png" || at.Size == 0 || len(at.SHA256) != 64 {
			t.Fatalf("attachment %d: %+v", i, at)
		}
	}
	if m.Attachments[0].Preview != nil || m.Attachments[1].Preview != nil {
		t.Fatal("small images got a preview")
	}
	p := m.Attachments[2].Preview
	if p == nil || p.Width != preview.MaxSide || p.Height != 1000 || p.SHA256 == m.Attachments[2].SHA256 {
		t.Fatalf("preview %+v", p)
	}
	// The preview and the original are both stored: the inbox shows one, the original is downloadable.
	for _, sha := range []string{p.SHA256, m.Attachments[2].SHA256} {
		if w := h.do(h.c.Token, "GET", "/blobs/"+sha, nil, nil); w.Code != 200 {
			t.Fatal(sha, w.Code)
		}
	}
	inbox, err := h.s.Inbox(ctx, storage.DefaultCompany, "code")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range inbox {
		if x.ID == m.ID {
			found = len(x.Attachments) == 3 && x.Attachments[2].Caption == "v2" && x.Attachments[2].Preview != nil
		}
	}
	if !found {
		t.Fatalf("inbox %+v", inbox)
	}
	// The same question is in its conversation (the chat), images included; the owner answers it.
	chat, err := h.s.Messages(ctx, storage.DefaultCompany, m.ConversationID)
	if err != nil || len(chat) == 0 || chat[len(chat)-1].ID != m.ID || len(chat[len(chat)-1].Attachments) != 3 {
		t.Fatalf("chat %s %+v %v", m.ConversationID, chat, err)
	}
	if _, err = h.s.ReplyMessage(ctx, storage.DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "v2", Answers: map[string]any{"verdict": "v2"}, RequestID: "answer-v2"}); err != nil {
		t.Fatal(err)
	}

	// A path in the text without attachments: sent, with the warning.
	out, err = a.Call(ctx, factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{
		"body": "The image is docs/qa/towers/side_by_side_v2.jpg on that branch, with columns classic | v1 | v2. Ship v2?", "request_id": "verdict-path", "brief": testBrief()}})
	if err != nil {
		t.Fatal(err)
	}
	if w := asAnswer(t, out); w.ID == "" || len(w.Warnings) != 1 || w.Warnings[0].Code != "visual_without_attachment" {
		t.Fatalf("%+v", w)
	}
	// Sent again with the image, the same words are a new question (not a refused repeat) and carry no warning.
	out, err = a.Call(ctx, factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{
		"body": "The image is docs/qa/towers/side_by_side_v2.jpg on that branch, with columns classic | v1 | v2. Ship v2?", "request_id": "verdict-path-again",
		"attachments": []any{map[string]any{"path": "docs/qa/towers/v2.png", "caption": "side by side"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if w := asAnswer(t, out); len(w.Attachments) != 1 || len(w.Warnings) != 0 {
		t.Fatalf("%+v", w)
	}
	// To another agent the path is fine; a message without visual files has no warning either.
	for i, call := range []factorytools.Call{
		{Name: "factory_message_send", Arguments: map[string]any{"recipient": "manager", "body": "see shot.png", "message_type": "notification", "request_id": "to-manager"}},
		{Name: "factory_owner_ask", Arguments: map[string]any{"body": "Ship it on Friday?", "request_id": "plain"}},
	} {
		out, err := a.Call(ctx, call)
		if err != nil {
			t.Fatal(i, err)
		}
		if w := asAnswer(t, out); len(w.Warnings) != 0 {
			t.Fatalf("%d: %+v", i, w)
		}
	}
	// Directly to Core, a digest nobody uploaded is refused.
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{"body": "b", "attachments": []any{map[string]any{"path": "x.png", "sha256": strings.Repeat("0", 64)}}, "request_id": "fake"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A preview is Core's: an agent cannot name one.
	w = h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{"body": "b", "attachments": []any{map[string]any{"path": "x.png", "preview": map[string]any{"sha256": p.SHA256}}}, "request_id": "fake-preview"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestVisualMaterialsForwardAndDecisions (inbox-visual-previews 1.1, 1.2): a message references an uploaded
// artifact version and a report attachment (Core resolves both in the project, an unknown one is refused); a
// forwarded question keeps its attachments and adds the forwarder's; a decision proposal carries attachments to its
// message; an artifact approval's question shows the version.
func TestVisualMaterialsForwardAndDecisions(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	work := t.TempDir()
	writePNG(t, filepath.Join(work, "art/towers.png"), 64, 64, color.RGBA{1, 2, 3, 255})
	writePNG(t, filepath.Join(work, "art/towers-night.png"), 64, 64, color.RGBA{3, 2, 1, 255})
	writeFile(t, filepath.Join(work, "qa/notes.md"), "# notes\n")
	h.create("art", "code", "demo")
	r := h.start("art")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work}
	call := func(name string, args map[string]any) toolAnswer {
		t.Helper()
		out, err := a.Call(ctx, factorytools.Call{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(name, err)
		}
		return asAnswer(t, out)
	}
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_artifact_upload", Arguments: map[string]any{"artifact_id": "towers", "artifact_version": "v2", "path": "art/towers.png", "request_id": "up"}}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Call(ctx, factorytools.Call{Name: "factory_report_create", Arguments: map[string]any{"kind": "problem", "summary": "qa", "purpose": "evidence", "changes": []any{}, "result": "notes", "attachments": []any{map[string]any{"path": "qa/notes.md"}}, "request_id": "rep"}})
	if err != nil {
		t.Fatal(err)
	}
	var rep struct{ ID string }
	b, _ := json.Marshal(out)
	_ = json.Unmarshal(b, &rep)

	m := call("factory_message_send", map[string]any{"recipient": "owner", "body": "Towers v2 is in.", "message_type": "review_request", "request_id": "mat",
		"materials": []any{map[string]any{"artifact_id": "towers", "artifact_version": "v2", "caption": "v2"}, map[string]any{"report_id": rep.ID, "path": "qa/notes.md", "caption": "QA notes"}}})
	if len(m.Materials) != 2 || m.Materials[0].Name != "towers.png" || len(m.Materials[0].SHA256) != 64 || m.Materials[0].URI != "factory://artifacts/towers/v2" || m.Materials[1].Name != "notes.md" || m.Materials[1].Caption != "QA notes" {
		t.Fatalf("%+v", m.Materials)
	}
	for i, bad := range []map[string]any{{"artifact_id": "towers", "artifact_version": "v9"}, {"report_id": rep.ID, "path": "other.md"}, {"caption": "nothing"}} {
		w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_message_send", Arguments: map[string]any{"recipient": "owner", "body": "b", "message_type": "notification", "materials": []any{bad}, "request_id": "bad-" + string(rune('a'+i))}}, nil)
		if w.Code != 400 {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}

	// A question to the manager with an attachment, forwarded with one more: both reach the owner.
	q := call("factory_message_ask", map[string]any{"recipient": "manager", "body": "Night or day towers?", "options": []any{"Night", "Day"}, "attachments": []any{map[string]any{"path": "art/towers.png", "caption": "day"}}, "request_id": "ask-manager"})
	night, err := a.Call(ctx, factorytools.Call{Name: "factory_report_create", Arguments: map[string]any{"kind": "problem", "summary": "night", "purpose": "p", "changes": []any{}, "result": "r", "attachments": []any{map[string]any{"path": "art/towers-night.png"}}, "request_id": "rep-night"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(night)
	var nightRep struct {
		Attachments []struct{ SHA256 string }
	}
	_ = json.Unmarshal(b, &nightRep)
	fwd, err := h.s.ForwardQuestion(ctx, storage.DefaultCompany, q.ID, "manager", q.Version, "the owner decides art", "fwd", []messaging.Attachment{{Path: "art/towers-night.png", Caption: "night", SHA256: nightRep.Attachments[0].SHA256}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Recipient != "owner" || len(fwd.Attachments) != 2 || fwd.Attachments[0].Caption != "day" || fwd.Attachments[1].Caption != "night" {
		t.Fatalf("%+v", fwd)
	}
	if got, _ := h.s.Message(ctx, storage.DefaultCompany, q.ID); len(got.Attachments) != 2 {
		t.Fatalf("stored %+v", got.Attachments)
	}

	// A decision proposal: the decision and its message carry the attachments; a proposal naming an image without
	// attachments is warned.
	out, err = a.Call(ctx, factorytools.Call{Name: "factory_decision_propose", Arguments: map[string]any{"topic": "tower-style", "value": "v2", "alternatives": []any{"v1"}, "rationale": "reads better at a distance",
		"attachments": []any{map[string]any{"path": "art/towers.png", "caption": "v2"}}, "request_id": "dec"}})
	if err != nil {
		t.Fatal(err)
	}
	var d decisions.Decision
	b, _ = json.Marshal(out)
	if err = json.Unmarshal(b, &d); err != nil || len(d.Attachments) != 1 || d.Attachments[0].Caption != "v2" {
		t.Fatal(string(b), err)
	}
	if dm, err := h.s.Message(ctx, storage.DefaultCompany, d.MessageID); err != nil || len(dm.Attachments) != 1 || dm.DecisionID != d.ID {
		t.Fatal(dm, err)
	}
	out, err = a.Call(ctx, factorytools.Call{Name: "factory_decision_propose", Arguments: map[string]any{"topic": "tower-style-2", "value": "art/towers-night.png", "rationale": "see it", "request_id": "dec-path"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := asAnswer(t, out); len(w.Warnings) != 1 || w.Warnings[0].Code != storage.VisualWarning {
		t.Fatalf("%+v", w)
	}

	// An artifact approval's question shows the version it asks about.
	ap, err := h.s.RequestArtifactApproval(ctx, storage.DefaultCompany, "code", artifacts.ApprovalRequest{ArtifactID: "towers", ArtifactVersion: "v2", Agent: "demo", TaskID: "art", RunID: r.id, Reason: "ship v2?", RequestID: "approve-towers"})
	if err != nil {
		t.Fatal(err)
	}
	if am, err := h.s.Message(ctx, storage.DefaultCompany, ap.MessageID); err != nil || len(am.Materials) != 1 || am.Materials[0].ArtifactID != "towers" || am.Materials[0].Name != "towers.png" {
		t.Fatal(am.Materials, err)
	}
}
