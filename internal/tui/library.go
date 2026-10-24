package tui

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/reports"
	"appfactory/internal/storage"
)

// What tasks produced (owner-web task materials): reports with their attachments, uploaded artifact versions
// and the blobs behind them.

// Reports are a project's reports, oldest first (every page of GET /projects/{project}/reports); task narrows
// them to one task.
func (c *HTTPClient) Reports(ctx context.Context, project, task string) ([]reports.Report, error) {
	out := []reports.Report{}
	after := int64(0)
	for {
		v := url.Values{"after": {fmt.Sprint(after)}, "limit": {"100"}}
		if task != "" {
			v.Set("task_id", task)
		}
		var page storage.Page[reports.Report]
		if err := c.get(ctx, "/projects/"+url.PathEscape(project)+"/reports?"+v.Encode(), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if !page.HasMore || page.NextAfter <= after {
			return out, nil
		}
		after = page.NextAfter
	}
}

// Artifacts are a project's artifact versions, oldest first.
func (c *HTTPClient) Artifacts(ctx context.Context, project string) ([]artifacts.Record, error) {
	out := []artifacts.Record{}
	err := c.get(ctx, "/projects/"+url.PathEscape(project)+"/artifacts", &out)
	return out, err
}

// ArtifactApproval is the owner approval of an artifact version, nil when it was never asked for.
func (c *HTTPClient) ArtifactApproval(ctx context.Context, project, artifact, version string) (*approvals.Approval, error) {
	var v struct {
		Approval *approvals.Approval `json:"approval"`
	}
	err := c.get(ctx, "/projects/"+url.PathEscape(project)+"/artifacts/"+url.PathEscape(artifact)+"/"+url.PathEscape(version), &v)
	return v.Approval, err
}

// ValidSHA256 is a blob digest as Core names blobs: 64 lowercase hex characters.
var ValidSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString

// Blob is a stored blob's content (a report attachment), at most maxArtifactPreview bytes.
func (c *HTTPClient) Blob(ctx context.Context, sha256 string) ([]byte, error) {
	if !ValidSHA256(sha256) {
		return nil, fmt.Errorf("invalid blob digest")
	}
	b := &limitedBuffer{max: maxArtifactPreview}
	err := c.API.Fetch(ctx, "/blobs/"+sha256, "", b)
	return b.buf, err
}

// TaskExits are a task's accepted exits with their facts (the pull request).
func (c *HTTPClient) TaskExits(ctx context.Context, task string) ([]storage.TaskExit, error) {
	var v []storage.TaskExit
	err := c.get(ctx, "/tasks/"+url.PathEscape(task)+"/exits", &v)
	return v, err
}

func (c *FixtureClient) TaskExits(_ context.Context, task string) ([]storage.TaskExit, error) {
	return c.F.Details[task].Exits, nil
}

func (c *FixtureClient) Reports(_ context.Context, project, task string) ([]reports.Report, error) {
	out := []reports.Report{}
	for _, r := range c.F.Reports[project] {
		if task == "" || r.TaskID == task {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *FixtureClient) Artifacts(_ context.Context, project string) ([]artifacts.Record, error) {
	return append([]artifacts.Record{}, c.F.ArtifactRecords[project]...), nil
}

// ArtifactApproval finds the version's approval among the overview's approvals.
func (c *FixtureClient) ArtifactApproval(_ context.Context, project, artifact, version string) (*approvals.Approval, error) {
	for _, a := range c.F.Overview.Approvals {
		if a.ProjectID == project && a.Artifact != nil && a.Artifact.ID == artifact && a.Artifact.Version == version {
			return &a, nil
		}
	}
	return nil, nil
}

func (c *FixtureClient) Blob(_ context.Context, sha256 string) ([]byte, error) {
	if b, ok := c.F.Blobs[sha256]; ok {
		return []byte(b), nil
	}
	return nil, fmt.Errorf("blob %s not in the fixture", sha256)
}
