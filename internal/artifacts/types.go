// Package artifacts defines immutable version references and owner approval requests.
package artifacts

import (
	"appfactory/internal/messaging"
)
import "appfactory/internal/tasks"

type Record struct {
	tasks.Artifact
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id,omitempty"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	// ArchiveImages are the images inside a zip or tar version (artifact-duplicates).
	ArchiveImages []ArchiveEntry `json:"archive_images,omitempty"`
	// ArchivedAt (Unix seconds) and ArchivedReason are set when retention archived this version
	// (artifact-retention): its metadata stays, its content may be gone (reads answer 410 Gone).
	ArchivedAt     int64  `json:"archived_at,omitempty"`
	ArchivedReason string `json:"archived_reason,omitempty"`
	// Includes are the versions this composite shows (people-topics D11).
	Includes []Part `json:"includes,omitempty"`
	// Unchanged: a register of the same content as the latest version answered with that version, nothing new.
	Unchanged bool `json:"unchanged,omitempty"`
}

// ArchiveEntry is one file inside an archive version.
type ArchiveEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
type Register struct {
	// Dedup: content equal to the artifact's latest version answers with that version (artifact-duplicates).
	Dedup     bool           `json:"-"`
	Artifact  tasks.Artifact `json:"artifact"`
	TaskID    string         `json:"task_id"`
	RunID     string         `json:"run_id,omitempty"`
	Actor     string         `json:"-"`
	RequestID string         `json:"request_id"`
	// Includes are the versions a composite (a contact sheet, a collage) shows: rejecting or replacing any of them
	// withdraws the composite's approval request (people-topics D11).
	Includes []Part `json:"includes,omitempty"`
}

// Part is one artifact version a composite includes.
type Part struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type ApprovalRequest struct {
	ArtifactID      string `json:"artifact_id"`
	ArtifactVersion string `json:"artifact_version"`
	Agent           string `json:"agent,omitempty"`
	TaskID          string `json:"-"`
	RunID           string `json:"-"`
	Reason          string `json:"reason"`
	Blocking        bool   `json:"blocking,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	RequestID       string `json:"request_id"`
	// Audience is internal (the team's own material) or external (what leaves the company; the default).
	Audience string `json:"audience,omitempty"`
	// Delegate and Setting are set by Core from the approval modes (approval-delegation).
	Delegate string `json:"-"`
	Setting  string `json:"-"`
	// What people read when it reaches them (people-topics D3).
	Brief  *messaging.Brief    `json:"brief,omitempty"`
	Note   *messaging.ItemNote `json:"note,omitempty"`
	Topic  string              `json:"topic,omitempty"`
	Strict bool                `json:"-"`
	// Options make a choice approval (artifact-choice-approvals): 2–6 registered versions people choose one of,
	// instead of ArtifactID/ArtifactVersion; one option is the yes/no approval of that version. Recommended names the
	// requester's recommendation: the option's number (from 1), label, id@version or artifact id.
	Options     []ChoiceOption `json:"options,omitempty"`
	Recommended any            `json:"recommended,omitempty"`
}

// ChoiceOption is one option of a choice approval request.
type ChoiceOption struct {
	ArtifactID      string `json:"artifact_id"`
	ArtifactVersion string `json:"artifact_version"`
	Label           string `json:"label"`
	Note            string `json:"note,omitempty"`
}
