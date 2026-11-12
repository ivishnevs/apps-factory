package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Artifact retention (artifact-retention): a project keeps superseded artifact versions for
// artifact_retention_days (0: forever), capped by Core's ArtifactRetentionMaxDays. A daily run archives the
// versions older than that which are neither the latest version of their artifact, nor its latest approved one,
// nor under an open approval request, nor a required input of an open task. An archived version keeps its row
// (who, when, digest, size, task links, approvals, archive listing) with archived_at and archived_reason; its
// content is deleted once nothing live uses the digest. Reads of its content answer ArchivedError (410 Gone).

// maxRetentionDays bounds a project's setting (about 100 years).
const maxRetentionDays = 36500

// validRetention checks a project's artifact_retention_days against the bounds and Core's ceiling.
func (s *Store) validRetention(days int) error {
	if days < 0 || days > maxRetentionDays {
		return inputError(fmt.Sprintf("artifact_retention_days must be 0 (keep forever) to %d", maxRetentionDays))
	}
	if limit := s.ArtifactRetentionMaxDays; limit > 0 && days > limit {
		return inputError(fmt.Sprintf("artifact_retention_days: at most %d days on this Factory (its retention ceiling); 0 uses the ceiling", limit))
	}
	return nil
}

// EffectiveRetention is the number of days a project keeps superseded artifact versions: its setting, capped by
// Core's ceiling; a project at 0 gets the ceiling. 0 keeps them forever.
func (s *Store) EffectiveRetention(p tasks.Project) int {
	days, limit := p.ArtifactRetentionDays, s.ArtifactRetentionMaxDays
	if limit > 0 && (days == 0 || days > limit) {
		return limit
	}
	return days
}

// RetentionReason is the archived_reason of a version archived after days.
func RetentionReason(days int) string { return fmt.Sprintf("retention: %d days", days) }

// ArchivedError answers a read of an archived version's content (410 Gone): when and why it was archived and
// the artifact's latest version.
type ArchivedError struct {
	Project    string `json:"project_id"`
	ID         string `json:"artifact_id"`
	Version    string `json:"version"`
	ArchivedAt int64  `json:"archived_at"`
	Reason     string `json:"reason"`
	Latest     string `json:"latest_version,omitempty"`
}

func (e *ArchivedError) Error() string {
	msg := fmt.Sprintf("artifact %s version %s was archived on %s (%s); its content is no longer stored", e.ID, e.Version, time.Unix(e.ArchivedAt, 0).UTC().Format("2006-01-02"), e.Reason)
	if e.Latest != "" && e.Latest != e.Version {
		msg += "; the latest version is " + e.Latest
	}
	return msg
}

// archivedError describes an archived version with its artifact's latest version.
func (s *Store) archivedError(ctx context.Context, company, project string, rec artifacts.Record) error {
	e := &ArchivedError{Project: project, ID: rec.ID, Version: rec.Version, ArchivedAt: rec.ArchivedAt, Reason: rec.ArchivedReason}
	err := s.DB.QueryRowContext(ctx, "SELECT version FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? ORDER BY seq DESC LIMIT 1", company, project, rec.ID).Scan(&e.Latest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return e
}

// ArchivedNote says an archived version's content is gone, when and why, and names the latest version.
func (s *Store) ArchivedNote(ctx context.Context, company, project string, rec artifacts.Record) string {
	return s.archivedError(ctx, company, project, rec).Error()
}

// ArtifactContent is the record of a version whose content may be read: ArchivedError for an archived one.
func (s *Store) ArtifactContent(ctx context.Context, company, project, id, version string) (artifacts.Record, error) {
	rec, err := s.Artifact(ctx, company, project, id, version)
	if err == nil && rec.ArchivedAt > 0 {
		err = s.archivedError(ctx, company, project, rec)
	}
	return rec, err
}

// RetentionRun is what one retention run of a company did.
type RetentionRun struct {
	// Versions archived per project.
	Archived map[string]int `json:"archived"`
	// Contents deleted from the blob store and the bytes they held.
	Deleted    int   `json:"deleted"`
	BytesFreed int64 `json:"bytes_freed"`
}

// Total is the number of versions archived.
func (r RetentionRun) Total() int {
	n := 0
	for _, v := range r.Archived {
		n += v
	}
	return n
}

// dueVersions are a project's versions to archive at cutoff (see the package comment above).
const dueVersions = `SELECT v.id, v.version FROM artifact_versions v
 WHERE v.company_id=? AND v.project_id=? AND v.archived_at=0 AND COALESCE((v.body->>'created_at')::bigint,0) < ?
 AND v.seq <> (SELECT max(l.seq) FROM artifact_versions l WHERE l.company_id=v.company_id AND l.project_id=v.project_id AND l.id=v.id)
 AND v.seq IS DISTINCT FROM (SELECT max(a.seq) FROM artifact_versions a JOIN approvals p ON p.company_id=a.company_id AND p.project_id=a.project_id
   AND p.kind='artifact' AND p.artifact_id=a.id AND p.artifact_version=a.version AND p.status='APPROVED'
   WHERE a.company_id=v.company_id AND a.project_id=v.project_id AND a.id=v.id)
 AND NOT EXISTS (SELECT 1 FROM approvals p WHERE p.company_id=v.company_id AND p.project_id=v.project_id AND p.kind='artifact'
   AND p.artifact_id=v.id AND p.artifact_version=v.version AND p.status='PENDING')
 AND NOT EXISTS (SELECT 1 FROM approval_options o JOIN approvals p ON p.company_id=o.company_id AND p.id=o.approval_id
   WHERE o.company_id=v.company_id AND o.project_id=v.project_id AND o.artifact_id=v.id AND o.artifact_version=v.version AND p.status='PENDING')
 AND NOT EXISTS (SELECT 1 FROM tasks t, json_array_elements(CASE WHEN json_typeof(t.body->'required_artifacts')='array' THEN t.body->'required_artifacts' ELSE '[]'::json END) r
   WHERE t.company_id=v.company_id AND t.project_id=v.project_id AND t.status NOT IN ('DONE','CANCELLED') AND r->>'id'=v.id AND r->>'version'=v.version)
 ORDER BY v.seq`

// blobInUse counts what still uses a company's digest besides archived artifact versions. A message's attachments
// and the previews of its images count (inbox-visual-previews); a message's reference to an artifact version does
// not keep that version's content.
const blobInUse = `SELECT (SELECT count(*) FROM artifact_versions WHERE company_id=? AND sha256=? AND archived_at=0)
 + (SELECT count(*) FROM run_logs WHERE company_id=? AND sha256=?)
 + (SELECT count(*) FROM report_attachments WHERE company_id=? AND sha256=?)
 + (SELECT count(*) FROM skill_index WHERE scope=? AND blob=?)
 + (SELECT count(*) FROM messages m, json_array_elements(CASE WHEN json_typeof(m.body->'attachments')='array' THEN m.body->'attachments' ELSE '[]'::json END) a
    WHERE m.company_id=? AND (a->>'sha256'=? OR a->'preview'->>'sha256'=?))
 + (SELECT count(*) FROM messages m, json_array_elements(CASE WHEN json_typeof(m.body->'materials')='array' THEN m.body->'materials' ELSE '[]'::json END) a
    WHERE m.company_id=? AND a->'preview'->>'sha256'=?)`

// ArchiveExpiredArtifacts runs artifact retention for one company: it archives every project's due versions and
// deletes the contents nothing live uses any more, then records one feed event when it changed anything.
func (s *Store) ArchiveExpiredArtifacts(ctx context.Context, company string) (RetentionRun, error) {
	run := RetentionRun{Archived: map[string]int{}}
	projects, err := s.Projects(ctx, company)
	if err != nil {
		return run, err
	}
	for _, p := range projects {
		days := s.EffectiveRetention(p)
		if days <= 0 {
			continue
		}
		n, err := s.archiveProject(ctx, company, p.ID, days)
		if err != nil {
			return run, fmt.Errorf("project %s: %w", p.ID, err)
		}
		if n > 0 {
			run.Archived[p.ID] = n
		}
	}
	if run.Deleted, run.BytesFreed, err = s.deleteArchivedContent(ctx, company); err != nil {
		return run, err
	}
	if run.Total() == 0 && run.Deleted == 0 {
		return run, nil
	}
	return run, s.AddFeed(ctx, company, FeedItem{EventID: protocol.ID(), Type: "artifact.retention", EntityID: "artifact-retention", Actor: "system", Summary: retentionSummary(run)})
}

// archiveProject archives a project's due versions in one transaction.
func (s *Store) archiveProject(ctx context.Context, company, project string, days int) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := tx.now().Unix()
	rows, err := tx.QueryContext(ctx, dueVersions, company, project, now-int64(days)*86400)
	if err != nil {
		return 0, err
	}
	type key struct{ id, version string }
	var due []key
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.id, &k.version); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, k)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	reason := RetentionReason(days)
	for _, k := range due {
		if _, err = tx.ExecContext(ctx, `UPDATE artifact_versions SET archived_at=?, archived_reason=?,
 body=(body::jsonb || jsonb_build_object('archived_at', ?::bigint, 'archived_reason', ?::text))::json
 WHERE company_id=? AND project_id=? AND id=? AND version=? AND archived_at=0`, now, reason, now, reason, company, project, k.id, k.version); err != nil {
			return 0, err
		}
	}
	return len(due), tx.Commit()
}

// deleteArchivedContent deletes the contents only archived versions use: each in its own transaction that locks
// the blob's row, checks again that nothing live uses it, removes it from the blob store and forgets it.
func (s *Store) deleteArchivedContent(ctx context.Context, company string) (int, int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT v.sha256 FROM artifact_versions v JOIN blobs b ON b.company_id=v.company_id AND b.sha256=v.sha256
 WHERE v.company_id=? AND v.archived_at>0 AND v.sha256<>''`, company)
	if err != nil {
		return 0, 0, err
	}
	var shas []string
	for rows.Next() {
		var sha string
		if err = rows.Scan(&sha); err != nil {
			rows.Close()
			return 0, 0, err
		}
		shas = append(shas, sha)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, 0, err
	}
	deleted, freed := 0, int64(0)
	for _, sha := range shas {
		size, ok, err := s.deleteBlobIfUnused(ctx, company, sha)
		if err != nil {
			return deleted, freed, err
		}
		if ok {
			deleted++
			freed += size
		}
	}
	return deleted, freed, nil
}

func (s *Store) deleteBlobIfUnused(ctx context.Context, company, sha string) (int64, bool, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var size int64
	err = tx.QueryRowContext(ctx, "SELECT size FROM blobs WHERE company_id=? AND sha256=? FOR UPDATE", company, sha).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var used int
	if err = tx.QueryRowContext(ctx, blobInUse, company, sha, company, sha, company, sha, company, sha, company, sha, sha, company, sha).Scan(&used); err != nil {
		return 0, false, err
	}
	if used > 0 {
		return 0, false, nil
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM blobs WHERE company_id=? AND sha256=?", company, sha); err != nil {
		return 0, false, err
	}
	// The content goes while the row is locked: an upload of the same content waits for this commit and then
	// sees it gone (PutBlob).
	if err = s.Blobs.Delete(ctx, company, sha); err != nil {
		return 0, false, err
	}
	return size, true, tx.Commit()
}

// retentionSummary is the feed line of a retention run.
func retentionSummary(r RetentionRun) string {
	projects := make([]string, 0, len(r.Archived))
	for p := range r.Archived {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	parts := make([]string, len(projects))
	for i, p := range projects {
		parts[i] = fmt.Sprintf("%s %d", p, r.Archived[p])
	}
	msg := fmt.Sprintf("artifact retention archived %d old version(s)", r.Total())
	if len(parts) > 0 {
		msg += " (" + strings.Join(parts, ", ") + ")"
	}
	return msg + fmt.Sprintf("; deleted %d stored content(s), freed %s", r.Deleted, byteSize(r.BytesFreed))
}

// byteSize is a size in B, KB, MB or GB.
func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
