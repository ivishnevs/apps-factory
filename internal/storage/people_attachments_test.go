package storage

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// pngBytes is enough of a PNG for the type check (the signature) with distinct content per call.
func pngBytes(tag string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), []byte(tag)...)
}

// TestPeopleAttachments (people-attachments 1.1–1.5): a person uploads images, names them in a message and a
// comment; every run of the task gets them in its manifest with brief lines, the run's lease reads them, and an
// upload nobody named goes after a day.
func TestPeopleAttachments(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "fix", ProjectID: "p", Title: "fix the button", Assignee: "demo", RequestID: "fix"}); err != nil {
		t.Fatal(err)
	}

	// 1.1 Only images; the name is made safe and gets the type's extension.
	if _, err := s.PutUpload(ctx, co, "", "notes.zip", strings.NewReader("PK\x03\x04 not an image")); !errors.Is(err, ErrInput) {
		t.Fatalf("zip accepted: %v", err)
	}
	shot, err := s.PutUpload(ctx, co, "", "../My Screen shot.PNG", bytes.NewReader(pngBytes("shot")))
	if err != nil || shot.Path != "My-Screen-shot.png" || shot.Size == 0 {
		t.Fatal(shot, err)
	}
	unused, err := s.PutUpload(ctx, co, "", "", bytes.NewReader(pngBytes("unused")))
	if err != nil || unused.Path != "image.png" {
		t.Fatal(unused, err)
	}

	// 1.2 A person's message names uploads only; a blob that is not an upload is refused.
	other, err := s.PutBlob(ctx, co, strings.NewReader("raw blob"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "fix", Sender: "owner", Recipient: "demo", MessageType: "notification", Body: "look", RequestID: "bad",
		Attachments: []messaging.Attachment{{SHA256: other.SHA256}}}); !errors.Is(err, ErrInput) {
		t.Fatalf("non-upload accepted: %v", err)
	}
	m, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "fix", Sender: "owner", Recipient: "demo", MessageType: "notification", RequestID: "msg",
		Attachments: []messaging.Attachment{{SHA256: shot.SHA256, Caption: "cut off"}}})
	if err != nil || len(m.Attachments) != 1 || m.Attachments[0].Path != "My-Screen-shot.png" || m.Body != "(an image attached)" {
		t.Fatal(m, err)
	}

	// 1.3 A comment carries images; an agent cannot attach.
	if _, err = s.CommentTask(ctx, co, "fix", tasks.Comment{Actor: "demo", Text: "mine", RequestID: "agent-c", Attachments: []messaging.Attachment{{SHA256: shot.SHA256}}}); !errors.Is(err, ErrInput) {
		t.Fatalf("agent attached: %v", err)
	}
	if _, err = s.CommentTask(ctx, co, "fix", tasks.Comment{Text: "on iPhone SE", RequestID: "c1", Attachments: []messaging.Attachment{{SHA256: shot.SHA256}, {SHA256: shot.SHA256, Caption: "again"}}}); err != nil {
		t.Fatal(err)
	}
	cs, err := s.Comments(ctx, co, "fix")
	if err != nil || len(cs) != 1 || len(cs[0].Attachments) != 2 || cs[0].Attachments[1].Path != "My-Screen-shot-2.png" {
		t.Fatal(cs, err)
	}

	// 1.4, 1.5 Every run of the task gets the images (comment and message) and the brief names their paths.
	tx, err := s.begin(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	man, err := runManifest(ctx, tx, protocol.RunSpec{Project: "p", TaskID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range man.Files {
		paths[f.Path] = f.SHA256 == shot.SHA256
	}
	want := []string{AttachmentPath("cmt", cs[0].ID, 1, "My-Screen-shot.png"), AttachmentPath("cmt", cs[0].ID, 2, "My-Screen-shot-2.png"), AttachmentPath("msg", m.ID, 1, "My-Screen-shot.png")}
	for _, p := range want {
		if !paths[p] {
			t.Fatalf("manifest lacks %s: %+v", p, man.Files)
		}
	}
	task, err := taskTx(ctx, tx, "fix")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := briefTx(ctx, tx, task, protocol.RunSpec{ID: "r1", Project: "p", TaskID: "fix", Agent: "demo"}, 0, 0)
	_ = tx.Rollback()
	if err != nil || !strings.Contains(brief, want[0]) || !strings.Contains(brief, want[1]+" — again") {
		t.Fatalf("brief: %v\n%s", err, brief)
	}
	if rc, _, err := s.ProjectBlob(ctx, co, "p", shot.SHA256); err != nil {
		t.Fatal(err)
	} else {
		rc.Close()
	}
	if _, _, err := s.ProjectBlob(ctx, co, "elsewhere", shot.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project read it: %v", err)
	}

	// D5: after a day the unnamed upload goes; the named one stays (its message and comment use it).
	fake.Advance(UploadTTL + time.Hour)
	if _, err = s.SweepUploads(ctx, co); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HasBlob(ctx, co, unused.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unused upload kept: %v", err)
	}
	if _, err = s.HasBlob(ctx, co, shot.SHA256); err != nil {
		t.Fatalf("named upload deleted: %v", err)
	}
	// An expired upload cannot be named any more.
	if _, err = s.CommentTask(ctx, co, "fix", tasks.Comment{Text: "late", RequestID: "c2", Attachments: []messaging.Attachment{{SHA256: shot.SHA256}}}); !errors.Is(err, ErrInput) {
		t.Fatalf("expired upload named: %v", err)
	}
}

// TestConversationRunGetsPeopleImages (D4): a conversation run gets the images of the people's latest messages to
// its agent, whichever message woke it.
func TestConversationRunGetsPeopleImages(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	img, err := s.PutUpload(ctx, co, "", "sketch.jpg", bytes.NewReader(append([]byte("\xff\xd8\xff\xe0"), []byte("jpeg")...)))
	if err != nil || img.Path != "sketch.jpg" {
		t.Fatal(img, err)
	}
	conv, err := s.CreateConversation(ctx, co, messaging.ConversationCreate{ID: "owner-demo-p", ProjectID: "p", Agent: "demo", Title: "Owner and demo", RequestID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", Sender: "owner", Recipient: "demo", ConversationID: conv.ID, MessageType: "question", Body: "like this?", RequestID: "q",
		Attachments: []messaging.Attachment{{SHA256: img.SHA256}}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.begin(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ct, err := conversationTaskTx(ctx, tx, "p", "demo")
	if err != nil {
		t.Fatal(err)
	}
	atts, err := runAttachmentsTx(ctx, tx, ct)
	if err != nil || len(atts) != 1 || atts[0].Path != AttachmentPath("msg", m.ID, 1, "sketch.jpg") {
		t.Fatal(atts, err)
	}
}

// TestAnswerWithImage (people-attachments 1.2, 3.3): a person answers an agent's question with an image; the
// answer carries it and the asking task's next run gets it.
func TestAnswerWithImage(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "ask", ProjectID: "p", Title: "pick a color", Assignee: "demo", RequestID: "ask"}); err != nil {
		t.Fatal(err)
	}
	q, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "ask", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "which color?", RequestID: "q"})
	if err != nil {
		t.Fatal(err)
	}
	img, err := s.PutUpload(ctx, co, "", "palette.png", bytes.NewReader(pngBytes("palette")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "demo", Body: "x", RequestID: "agent", Attachments: []messaging.Attachment{{SHA256: img.SHA256}}}); err == nil {
		t.Fatal("an agent attached to an answer")
	}
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", RequestID: "a", Attachments: []messaging.Attachment{{SHA256: img.SHA256}}}); err != nil {
		t.Fatal(err)
	}
	ans, err := getDoc[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND message_type='answer' AND (body->>'in_reply_to')=?", co, q.ID)
	if err != nil || len(ans.Attachments) != 1 || ans.Body != "(an image attached)" {
		t.Fatal(ans, err)
	}
	tx, err := s.begin(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	man, err := runManifest(ctx, tx, protocol.RunSpec{Project: "p", TaskID: "ask"})
	if err != nil || len(man.Files) != 1 || man.Files[0].Path != AttachmentPath("msg", ans.ID, 1, "palette.png") {
		t.Fatal(man, err)
	}
}
