package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// People's attachments (people-attachments): a person uploads images first (POST /uploads), then names them in a
// message or a comment by digest. Core keeps the original; the run of the task or conversation gets a copy in its
// working directory (runManifest) and the brief names it.

const (
	// MaxPersonAttachments bounds the images of one person's message or comment (D2).
	MaxPersonAttachments = 10
	// MaxPersonUpload bounds one uploaded image (D2).
	MaxPersonUpload = 20 << 20
	// UploadTTL is how long an upload no message or comment names is kept (D5).
	UploadTTL = 24 * time.Hour
)

// uploadTypes are the image types a person may attach (D1) with the extension their file gets.
var uploadTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// PutUpload stores an image a person uploads and records it for company; the answer is the attachment to name in
// a message or comment (path is the file name, preview rendered for a large image).
func (s *Store) PutUpload(ctx context.Context, company, actor, name string, r io.Reader) (messaging.Attachment, error) {
	br := bufio.NewReaderSize(r, 512)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return messaging.Attachment{}, err
	}
	if len(head) == 0 {
		return messaging.Attachment{}, inputError("empty file")
	}
	kind := http.DetectContentType(head)
	ext, ok := uploadTypes[kind]
	if !ok {
		return messaging.Attachment{}, inputError("only images can be attached: PNG, JPEG, GIF or WebP")
	}
	blob, err := s.PutBlob(ctx, company, br, MaxPersonUpload)
	if errors.Is(err, ErrInput) {
		return messaging.Attachment{}, inputError(fmt.Sprintf("an image up to %d MB", MaxPersonUpload>>20))
	}
	if err != nil {
		return messaging.Attachment{}, err
	}
	if actor == "" {
		actor = personName(ctx)
	}
	name = uploadName(name, ext)
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO uploads(company_id,sha256,name,size,actor,created_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT (company_id,sha256) DO UPDATE SET created_at=excluded.created_at, actor=excluded.actor`, company, blob.SHA256, name, blob.Size, actor, s.Now().Unix()); err != nil {
		return messaging.Attachment{}, err
	}
	return messaging.Attachment{Path: name, SHA256: blob.SHA256, Size: blob.Size, Preview: s.previewOf(ctx, company, blob.SHA256, name)}, nil
}

// uploadName is a safe file name for an upload: the base name of what the person picked, with the extension of
// its detected type ("image.png" when nothing usable was given).
func uploadName(name, ext string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	base := strings.Trim(b.String(), ".")
	base = strings.TrimSuffix(base, path.Ext(base))
	if base == "" {
		base = "image"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return base + ext
}

// imagesOnlyBody is the text of a person's message or comment that has images and no words.
func imagesOnlyBody(n int) string {
	if n == 1 {
		return "(an image attached)"
	}
	return fmt.Sprintf("(%d images attached)", n)
}

// withPersonPreviews renders previews for a person's attachments before the transaction that stores them.
func (s *Store) withPersonPreviews(ctx context.Context, company string, atts []messaging.Attachment) []messaging.Attachment {
	if len(atts) == 0 || len(atts) > MaxPersonAttachments {
		return atts
	}
	out, _ := s.withPreviews(ctx, company, "", atts, nil)
	return out
}

// personAttachmentsTx checks the images a person names in a message or comment: each an upload of this company,
// at most MaxPersonAttachments, distinct file names (a repeated name gets a number). The size comes from the upload.
func personAttachmentsTx(ctx context.Context, tx *Tx, atts []messaging.Attachment) ([]messaging.Attachment, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	if len(atts) > MaxPersonAttachments {
		return nil, inputError(fmt.Sprintf("at most %d images", MaxPersonAttachments))
	}
	out := make([]messaging.Attachment, 0, len(atts))
	seen := map[string]bool{}
	for i, a := range atts {
		if len(a.Caption) > maxCaption {
			return nil, inputError(fmt.Sprintf("image %d: caption up to %d bytes", i+1, maxCaption))
		}
		var name string
		var size int64
		err := tx.QueryRowContext(ctx, "SELECT name,size FROM uploads WHERE company_id=? AND sha256=?", tx.company, a.SHA256).Scan(&name, &size)
		if err != nil {
			if isNoRows(err) {
				return nil, inputError(fmt.Sprintf("image %d was not uploaded, or its upload expired: upload it again", i+1))
			}
			return nil, err
		}
		if a.Path != "" {
			name = uploadName(a.Path, path.Ext(name))
		}
		for n := 2; seen[name]; n++ {
			ext := path.Ext(name)
			name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n, ext)
		}
		seen[name] = true
		p := a.Preview
		if p != nil {
			if b, err := blobRow(ctx, tx, tx.company, p.SHA256); err != nil || b.Size != p.Size {
				p = nil
			}
		}
		out = append(out, messaging.Attachment{Path: name, Caption: a.Caption, SHA256: a.SHA256, Size: size, Preview: p})
	}
	return out, nil
}

// SweepUploads deletes the uploads no message or comment named within UploadTTL, and their content when nothing
// else uses it.
func (s *Store) SweepUploads(ctx context.Context, company string) (int, error) {
	cut := s.Now().Add(-UploadTTL).Unix()
	rows, err := s.DB.QueryContext(ctx, "DELETE FROM uploads WHERE company_id=? AND created_at<? RETURNING sha256", company, cut)
	if err != nil {
		return 0, err
	}
	var shas []string
	for rows.Next() {
		var sha string
		if err = rows.Scan(&sha); err != nil {
			rows.Close()
			return 0, err
		}
		shas = append(shas, sha)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, sha := range shas {
		_, ok, err := s.deleteBlobIfUnused(ctx, company, sha)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// runAttachment is a person's image a run gets in its working directory.
type runAttachment struct {
	Path, Caption, SHA256 string
	Size                  int64
}

// maxConversationImages bounds the people's messages with images a conversation run gets (the latest ones).
const maxConversationImages = 20

// AttachmentPath is where a run finds the n-th (1-based) image of a message ("msg") or comment ("cmt"), relative
// to its working directory.
func AttachmentPath(kind, id string, n int, name string) string {
	return fmt.Sprintf("%s/%s-%s/%d/%s", protocol.InputsDir, kind, id, n, name)
}

// runAttachmentsTx are the people's images a run of task t gets (people-attachments D4): for a work task, those
// of its comments and of the people's messages filed under it; for a conversation task, those of the latest people's
// messages to its agent in the project. Each run gets them again, on whichever node it runs.
func runAttachmentsTx(ctx context.Context, tx *Tx, t tasks.Task) ([]runAttachment, error) {
	var out []runAttachment
	add := func(kind, id string, raw []byte) {
		var atts []messaging.Attachment
		if json.Unmarshal(raw, &atts) != nil {
			return
		}
		for i, a := range atts {
			if a.SHA256 == "" || a.Path == "" {
				continue
			}
			out = append(out, runAttachment{Path: AttachmentPath(kind, id, i+1, path.Base(a.Path)), Caption: a.Caption, SHA256: a.SHA256, Size: a.Size})
		}
	}
	scan := func(kind, query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err = rows.Scan(&id, &raw); err != nil {
				return err
			}
			add(kind, id, raw)
		}
		return rows.Err()
	}
	// Board comments of other tasks this run owes an outcome for (forwarded, the lead's copy) bring their images.
	open, err := openComments(ctx, tx, t.ID)
	if err != nil {
		return nil, err
	}
	for _, o := range open {
		if o.commentTask == t.ID {
			continue
		}
		if err = scan("cmt", "SELECT id, attachments FROM task_comments WHERE company_id=? AND id=? AND jsonb_array_length(attachments)>0", tx.company, o.comment); err != nil {
			return nil, err
		}
	}
	if t.Kind == tasks.KindConversation {
		return out, scan("msg", `SELECT id, body->'attachments' FROM (SELECT id, body, seq FROM messages
 WHERE company_id=? AND project_id=? AND recipient=? AND sender='owner' AND json_typeof(body->'attachments')='array'
 ORDER BY seq DESC LIMIT ?) m ORDER BY seq`, tx.company, t.ProjectID, t.Assignee, maxConversationImages)
	}
	if err := scan("cmt", "SELECT id, attachments FROM task_comments WHERE company_id=? AND task_id=? AND jsonb_array_length(attachments)>0 ORDER BY created_at,seq", tx.company, t.ID); err != nil {
		return nil, err
	}
	return out, scan("msg", "SELECT id, body->'attachments' FROM messages WHERE company_id=? AND task_id=? AND sender='owner' AND json_typeof(body->'attachments')='array' ORDER BY seq", tx.company, t.ID)
}

// attachmentLines are the brief's lines for the images of a message or comment: where the run finds each one.
func attachmentLines(kind, id string, atts []messaging.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Images attached (files in your working directory; open them to see them):\n")
	for i, a := range atts {
		line := "- " + AttachmentPath(kind, id, i+1, path.Base(a.Path))
		if a.Caption != "" {
			line += " — " + a.Caption
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// commentAttachmentsTx are the images of one comment.
func commentAttachmentsTx(ctx context.Context, tx *Tx, id string) ([]messaging.Attachment, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT attachments FROM task_comments WHERE company_id=? AND id=?", tx.company, id).Scan(&raw)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var atts []messaging.Attachment
	_ = json.Unmarshal(raw, &atts)
	return atts, nil
}
