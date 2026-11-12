package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"appfactory/internal/artifacts"
	"appfactory/internal/blobstore"
	"appfactory/internal/messaging"
	"appfactory/internal/preview"
	"appfactory/internal/reports"
)

// Visual attachments (inbox-visual-previews): a message or a decision proposal carries files of the sender's
// worktree (uploaded by the node, like report attachments) and references to existing materials (an artifact
// version, a report attachment). Core checks every blob and resolves every reference inside the transaction that
// stores the message, and renders previews of large images before it.

// maxCaption bounds a caption.
const maxCaption = 512

// withPreviews renders a preview of every large image among the attachments and the referenced materials, before
// the transaction that stores them (rendering reads and writes blobs). A material's digest is looked up here and
// again, authoritatively, in the transaction; a preview whose digest no longer matches is dropped there. A failed
// rendering leaves the image without a preview: the original is shown. It answers copies; the caller's input (the
// mutation's fingerprint) stays as given.
func (s *Store) withPreviews(ctx context.Context, company, project string, atts []messaging.Attachment, mats []messaging.MaterialRef) ([]messaging.Attachment, []messaging.MaterialRef) {
	if len(atts) > messaging.MaxVisuals || len(mats) > messaging.MaxVisuals {
		return atts, mats // the transaction refuses them
	}
	atts, mats = slices.Clone(atts), slices.Clone(mats)
	for i := range atts {
		atts[i].Preview = nil
		if blobstore.ValidDigest(atts[i].SHA256) {
			atts[i].Preview = s.previewOf(ctx, company, atts[i].SHA256, atts[i].Path)
		}
	}
	for i := range mats {
		mats[i].Preview = nil
		name, sha := s.materialBlob(ctx, company, project, mats[i])
		if sha != "" {
			mats[i].SHA256 = sha
			mats[i].Preview = s.previewOf(ctx, company, sha, name)
		}
	}
	return atts, mats
}

// materialBlob is the name and digest of what a reference names (read outside a transaction).
func (s *Store) materialBlob(ctx context.Context, company, project string, m messaging.MaterialRef) (string, string) {
	switch {
	case m.ArtifactID != "":
		a, err := s.Artifact(ctx, company, project, m.ArtifactID, m.ArtifactVersion)
		if err == nil {
			return cmpOrStr(a.Name, path.Base(a.URI)), a.SHA256
		}
	case m.ReportID != "":
		r, err := s.Report(ctx, company, m.ReportID)
		if err == nil && r.ProjectID == project {
			for _, a := range r.Attachments {
				if a.Path == m.Path {
					return a.Path, a.SHA256
				}
			}
		}
	}
	return "", ""
}

// previewOf stores a preview of the blob when it is an image larger than the display size.
func (s *Store) previewOf(ctx context.Context, company, sha, name string) *messaging.Preview {
	if !preview.Renderable(name) {
		return nil
	}
	b, err := s.blobBytes(ctx, company, sha, MaxBlob)
	if err != nil {
		return nil // validation in the transaction answers a missing blob
	}
	img, ok := preview.Render(name, b)
	if !ok {
		return nil
	}
	blob, err := s.PutBlob(ctx, company, bytes.NewReader(img.Data), MaxBlob)
	if err != nil {
		slog.Warn("preview not stored", "sha256", sha, "error", err)
		return nil
	}
	return &messaging.Preview{SHA256: blob.SHA256, Size: blob.Size, Width: img.Width, Height: img.Height}
}

// visualsTx checks a message's attachments (each an uploaded blob of the company) and resolves its material
// references in project; previews set by withPreviews are kept when their blob exists.
func visualsTx(ctx context.Context, tx *Tx, project string, atts []messaging.Attachment, mats []messaging.MaterialRef) ([]messaging.Attachment, []messaging.MaterialRef, error) {
	if len(atts) > messaging.MaxVisuals || len(mats) > messaging.MaxVisuals {
		return nil, nil, inputError(fmt.Sprintf("at most %d attachments and %d materials", messaging.MaxVisuals, messaging.MaxVisuals))
	}
	keepPreview := func(p *messaging.Preview) *messaging.Preview {
		if p == nil {
			return nil
		}
		if b, err := blobRow(ctx, tx, tx.company, p.SHA256); err != nil || b.Size != p.Size {
			return nil
		}
		return p
	}
	var outA []messaging.Attachment
	seen := map[string]bool{}
	for _, a := range atts {
		if a.Path == "" || len(a.Path) > 512 || seen[a.Path] {
			return nil, nil, inputError("attachments need distinct paths")
		}
		if len(a.Caption) > maxCaption {
			return nil, nil, inputError(fmt.Sprintf("attachment %s: caption up to %d bytes", a.Path, maxCaption))
		}
		seen[a.Path] = true
		blob, err := blobRow(ctx, tx, tx.company, a.SHA256)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInput) {
			return nil, nil, inputError("attachment " + a.Path + " was not uploaded (attach files through the node's Factory MCP adapter)")
		}
		if err != nil {
			return nil, nil, err
		}
		a.Size = blob.Size
		a.Preview = keepPreview(a.Preview)
		outA = append(outA, a)
	}
	var outM []messaging.MaterialRef
	for _, m := range mats {
		if len(m.Caption) > maxCaption {
			return nil, nil, inputError(fmt.Sprintf("material caption up to %d bytes", maxCaption))
		}
		was := m.SHA256
		r := messaging.MaterialRef{ArtifactID: m.ArtifactID, ArtifactVersion: m.ArtifactVersion, ReportID: m.ReportID, Path: m.Path, Caption: m.Caption}
		switch {
		case m.ArtifactID != "" && m.ReportID == "":
			if m.ArtifactVersion == "" {
				return nil, nil, inputError("material " + m.ArtifactID + ": artifact_version required")
			}
			a, err := getDoc[artifacts.Record](ctx, tx, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? AND version=?", tx.company, project, m.ArtifactID, m.ArtifactVersion)
			if errors.Is(err, ErrNotFound) {
				return nil, nil, inputError("material: artifact " + m.ArtifactID + " " + m.ArtifactVersion + " is not registered in this project")
			}
			if err != nil {
				return nil, nil, err
			}
			r.Path = ""
			r.Name, r.SHA256, r.Size, r.URI = cmpOrStr(a.Name, path.Base(a.URI), a.ID), a.SHA256, a.Size, a.URI
		case m.ReportID != "" && m.ArtifactID == "" && m.Path != "":
			rep, err := getDoc[reports.Report](ctx, tx, "SELECT body FROM reports WHERE company_id=? AND id=?", tx.company, m.ReportID)
			if errors.Is(err, ErrNotFound) || err == nil && rep.ProjectID != project {
				return nil, nil, inputError("material: report " + m.ReportID + " is not in this project")
			}
			if err != nil {
				return nil, nil, err
			}
			found := false
			for _, a := range rep.Attachments {
				if a.Path == m.Path {
					r.Name, r.SHA256, r.Size, r.Redacted, found = path.Base(a.Path), a.SHA256, a.Size, a.Redacted, true
				}
			}
			if !found {
				return nil, nil, inputError("material: report " + m.ReportID + " has no attachment " + m.Path)
			}
		default:
			return nil, nil, inputError("a material names an artifact (artifact_id, artifact_version) or a report attachment (report_id, path)")
		}
		if was == r.SHA256 && r.SHA256 != "" {
			r.Preview = keepPreview(m.Preview)
		}
		outM = append(outM, r)
	}
	return outA, outM, nil
}

// visualExts are the image and video files a message to people should attach rather than name.
var visualExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4"}

// NamesVisuals tells whether text names an image or video file (a path or a file name with one of visualExts).
func NamesVisuals(text string) bool {
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '`' || r == '"' || r == '\'' || r == '(' || r == ')' || r == '[' || r == ']' || r == ',' || r == '|' || r == '<' || r == '>'
	}) {
		f = strings.TrimRight(f, ".:;!?")
		for _, ext := range visualExts {
			if strings.HasSuffix(f, ext) && len(f) > len(ext) {
				return true
			}
		}
	}
	return false
}

// VisualWarning is the non-blocking warning of a message to people that names image or video files without
// attaching any (inbox-visual-previews).
const VisualWarning = "visual_without_attachment"
