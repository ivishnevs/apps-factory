package web

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/reports"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/tui"
)

// Material is one thing a task produced, as the task's Materials section and the Library list it: a file a
// report attached (a blob), an artifact version (uploaded content or an external link), or a pull request.
type Material struct {
	// ID is stable across reads: "file:<report>:<path>", "artifact:<project>:<id>:<version>" or
	// "pr:<project>:<number>".
	ID string `json:"id"`
	// Kind is what the viewer shows: document, pdf, web, image, data, code, file, link or pull_request.
	Kind string `json:"kind"`
	// Source is where it was recorded: report, artifact, submission or exit.
	Source  string `json:"source"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Size    int64  `json:"size,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	// ContentURL is this API's raw content (/api/blobs/... or /api/artifacts/...); URL an external link.
	ContentURL string            `json:"content_url,omitempty"`
	URL        string            `json:"url,omitempty"`
	Redacted   bool              `json:"redacted,omitempty"`
	ProjectID  string            `json:"project_id"`
	TaskID     string            `json:"task_id,omitempty"`
	TaskTitle  string            `json:"task_title,omitempty"`
	RunID      string            `json:"run_id,omitempty"`
	Author     string            `json:"author,omitempty"`
	CreatedAt  int64             `json:"created_at"`
	Report     *MaterialReport   `json:"report,omitempty"`
	Approval   *MaterialApproval `json:"approval,omitempty"`
	PR         *storage.PRFacts  `json:"pr,omitempty"`
	// Archive names the archive version an image comes from (artifact-duplicates: images inside archives).
	Archive string `json:"archive,omitempty"`
	// Versions are the artifact's older versions in the Library (one card per artifact), newest first.
	Versions []MaterialVersion `json:"versions,omitempty"`
	// ArchivedAt and ArchivedReason: retention archived this artifact version (artifact-retention); it has no
	// ContentURL then, its metadata stays.
	ArchivedAt     int64  `json:"archived_at,omitempty"`
	ArchivedReason string `json:"archived_reason,omitempty"`
	// ArtifactID is the artifact an artifact version (or an image inside its archive) belongs to; Version is its
	// version. Result marks the task's delivered result (task-result-first): the artifacts the task's last submit
	// or done exit named, and the images inside them.
	ArtifactID string `json:"artifact_id,omitempty"`
	Result     bool   `json:"result,omitempty"`
}

// MaterialVersion is an older version of a Library artifact.
type MaterialVersion struct {
	Version    string `json:"version"`
	CreatedAt  int64  `json:"created_at"`
	ContentURL string `json:"content_url,omitempty"`
	URL        string `json:"url,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ArchivedAt int64  `json:"archived_at,omitempty"`
}

// MaterialReport is the report a file came with.
type MaterialReport struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Result  string `json:"result,omitempty"`
}

// MaterialApproval is the owner approval of an artifact version.
type MaterialApproval struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	DecidedAt int64  `json:"decided_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// MaterialPage is a page of the Library, newest first; NextBefore continues it.
type MaterialPage struct {
	Items      []Material `json:"items"`
	NextBefore int64      `json:"next_before,omitempty"`
	HasMore    bool       `json:"has_more"`
}

var kindByExt = map[string]string{
	"md": "document", "markdown": "document", "txt": "document", "rtf": "document", "doc": "document", "docx": "document",
	"pdf": "pdf", "html": "web", "htm": "web",
	"png": "image", "jpg": "image", "jpeg": "image", "gif": "image", "webp": "image", "svg": "image", "avif": "image",
	"json": "data", "csv": "data", "tsv": "data", "yaml": "data", "yml": "data", "xml": "data", "xlsx": "data",
	"go": "code", "ts": "code", "tsx": "code", "js": "code", "jsx": "code", "py": "code", "swift": "code", "kt": "code",
	"sh": "code", "sql": "code", "css": "code", "rs": "code", "java": "code", "rb": "code", "log": "code",
}

// kindOf is a file's kind from its name.
func kindOf(name string) string {
	if k, ok := kindByExt[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]; ok {
		return k
	}
	return "file"
}

func blobURL(sha, name string) string {
	return "/api/blobs/" + sha + "?" + url.Values{"name": {name}}.Encode()
}

func artifactURL(project, name string, a tasks.Artifact) string {
	return "/api/artifacts/" + url.PathEscape(project) + "/" + url.PathEscape(a.ID) + "/" + url.PathEscape(a.Version) + "?" + url.Values{"name": {name}}.Encode()
}

// fromReport lists a report's attachments.
func fromReport(r reports.Report) []Material {
	out := []Material{}
	for _, a := range r.Attachments {
		name := path.Base(a.Path)
		m := Material{ID: "file:" + r.ID + ":" + a.Path, Kind: kindOf(name), Source: "report", Name: name, Path: a.Path, Size: a.Size, SHA256: a.SHA256,
			Redacted: a.Redacted, ProjectID: r.ProjectID, TaskID: r.TaskID, RunID: r.RunID, Author: r.CreatedBy, CreatedAt: r.CreatedAt,
			Report: &MaterialReport{ID: r.ID, Kind: r.Kind, Summary: r.Summary, Result: r.Result}}
		if tui.ValidSHA256(a.SHA256) {
			m.ContentURL = blobURL(a.SHA256, name)
		}
		out = append(out, m)
	}
	return out
}

// fromArtifact is an artifact version: uploaded content, or an external link.
func fromArtifact(project string, a tasks.Artifact, source, task, run, author string, at int64) Material {
	name := cmpOr(a.Name, path.Base(a.URI), a.ID)
	m := Material{ID: "artifact:" + project + ":" + a.ID + ":" + a.Version, ArtifactID: a.ID, Source: source, Name: name, Version: a.Version, Size: a.Size, SHA256: a.SHA256,
		ProjectID: project, TaskID: task, RunID: run, Author: author, CreatedAt: at}
	switch {
	case a.SHA256 != "":
		m.Kind, m.ContentURL = kindOf(name), artifactURL(project, name, a)
	case strings.HasPrefix(a.URI, "https://") || strings.HasPrefix(a.URI, "http://"):
		m.Kind, m.URL = "link", a.URI
	default:
		m.Kind = "link"
	}
	return m
}

func cmpOr(v ...string) string {
	for _, s := range v {
		if s != "" && s != "." && s != "/" {
			return s
		}
	}
	return ""
}

// collect merges a task's (or a project's) materials: report files, artifact versions, the submission's
// artifacts the versions do not already list, and the pull requests of the exits (the latest state of each).
//
// archived are the project's archived versions by "<id>\x00<version>" (artifact-retention): their materials, also
// those a report or a submission names, carry the archive date and no content link.
func collect(project string, reps []reports.Report, records []artifacts.Record, ts []tui.Task, exits map[string][]storage.TaskExit, archived map[string]artifacts.Record) []Material {
	out := []Material{}
	seen := map[string]bool{}
	add := func(m Material) {
		if seen[m.ID] {
			return
		}
		seen[m.ID] = true
		if m.ArtifactID != "" || m.Source == "archive" {
			if a, ok := archived[m.ArtifactID+"\x00"+m.Version]; ok {
				m.ArchivedAt, m.ArchivedReason, m.ContentURL = a.ArchivedAt, a.ArchivedReason, ""
			}
		}
		out = append(out, m)
	}
	for _, r := range records {
		m := fromArtifact(project, r.Artifact, "artifact", r.TaskID, r.RunID, r.CreatedBy, r.CreatedAt)
		add(m)
		for _, e := range r.ArchiveImages {
			name := path.Base(e.Path)
			add(Material{ID: m.ID + "#" + e.Path, ArtifactID: r.ID, Kind: "image", Source: "archive", Name: name, Path: e.Path, Version: r.Version, Size: e.Size,
				ContentURL: "/api/artifacts/" + url.PathEscape(project) + "/" + url.PathEscape(r.ID) + "/" + url.PathEscape(r.Version) + "/entry?" + url.Values{"path": {e.Path}, "name": {name}}.Encode(),
				ProjectID:  project, TaskID: r.TaskID, RunID: r.RunID, Author: r.CreatedBy, CreatedAt: r.CreatedAt, Archive: m.Name + " " + r.Version})
		}
	}
	for _, r := range reps {
		for _, m := range fromReport(r) {
			add(m)
		}
		for _, a := range r.Artifacts {
			add(fromArtifact(project, a, "report", r.TaskID, r.RunID, r.CreatedBy, r.CreatedAt))
		}
	}
	for _, t := range ts {
		if t.Submission != nil {
			for _, a := range t.Submission.Artifacts {
				add(fromArtifact(project, a, "submission", t.ID, "", t.Assignee, t.UpdatedAt))
			}
		}
		prs := map[int]Material{}
		for _, e := range exits[t.ID] {
			if e.Facts == nil || e.Facts.PR == nil || e.Facts.PR.Number == 0 {
				continue
			}
			pr := *e.Facts.PR
			prs[pr.Number] = Material{ID: fmt.Sprintf("pr:%s:%d", project, pr.Number), Kind: "pull_request", Source: "exit", Name: fmt.Sprintf("Pull request #%d", pr.Number),
				URL: pr.URL, ProjectID: project, TaskID: t.ID, RunID: e.RunID, Author: t.Assignee, CreatedAt: e.CreatedAt, PR: &pr}
		}
		for _, m := range prs {
			add(m)
		}
	}
	return out
}

// archivedOf indexes the archived versions among records (artifact-retention).
func archivedOf(records []artifacts.Record) map[string]artifacts.Record {
	out := map[string]artifacts.Record{}
	for _, r := range records {
		if r.ArchivedAt > 0 {
			out[r.ID+"\x00"+r.Version] = r
		}
	}
	return out
}

// newestFirst sorts materials newest first (ties by ID, so pages are stable).
func newestFirst(ms []Material) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].CreatedAt != ms[j].CreatedAt {
			return ms[i].CreatedAt > ms[j].CreatedAt
		}
		return ms[i].ID < ms[j].ID
	})
}

// titles fills each material's task title from the tasks.
func titles(ms []Material, ts []tui.Task) {
	byID := map[string]string{}
	for _, t := range ts {
		byID[t.ID] = t.Title
	}
	for i := range ms {
		ms[i].TaskTitle = byID[ms[i].TaskID]
	}
}

// taskMaterials is everything one task produced, with the approval state of its artifact versions.
func taskMaterials(ctx context.Context, c tui.Client, d tui.TaskDetail) ([]Material, error) {
	t := d.Task
	reps, err := c.Reports(ctx, t.ProjectID, t.ID)
	if err != nil {
		return nil, err
	}
	all, err := c.Artifacts(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	records := []artifacts.Record{}
	for _, r := range all {
		if r.TaskID == t.ID {
			records = append(records, r)
		}
	}
	ms := collect(t.ProjectID, reps, records, []tui.Task{t}, map[string][]storage.TaskExit{t.ID: d.Exits}, archivedOf(all))
	for i, m := range ms {
		if m.ArtifactID == "" {
			continue
		}
		if a, err := c.ArtifactApproval(ctx, t.ProjectID, m.ArtifactID, m.Version); err == nil && a != nil {
			ms[i].Approval = &MaterialApproval{ID: a.ID, Status: a.Status, DecidedAt: a.DecidedAt, Reason: a.DecisionReason}
		}
	}
	titles(ms, []tui.Task{t})
	newestFirst(ms)
	resultFirst(ms, resultOf(t, d.Exits))
	return ms, nil
}

// resultOf is a task's delivered result (task-result-first): the artifacts its last submit or done exit named,
// in the order named, by "<id>\x00<version>". A task submitted outside explicit exits has its submission's.
func resultOf(t tui.Task, exits []storage.TaskExit) map[string]int {
	named := []tasks.Artifact{}
	for i := len(exits) - 1; i >= 0; i-- {
		if e := exits[i]; (e.Kind == "submit" || e.Kind == "done") && len(e.Artifacts) > 0 {
			named = e.Artifacts
			break
		}
	}
	if len(named) == 0 && t.Submission != nil {
		named = t.Submission.Artifacts
	}
	out := map[string]int{}
	for i, a := range named {
		if _, ok := out[a.ID+"\x00"+a.Version]; !ok {
			out[a.ID+"\x00"+a.Version] = i
		}
	}
	return out
}

// resultFirst marks the result's materials and moves them to the front in the order the exit named them (an
// archive's images right after it); the rest stay newest first. ms is newest first.
func resultFirst(ms []Material, result map[string]int) {
	rank := func(m Material) (int, bool) {
		if m.ArtifactID == "" {
			return 0, false
		}
		r, ok := result[m.ArtifactID+"\x00"+m.Version]
		return r, ok
	}
	for i := range ms {
		_, ms[i].Result = rank(ms[i])
	}
	sort.SliceStable(ms, func(i, j int) bool {
		ri, oi := rank(ms[i])
		rj, oj := rank(ms[j])
		if oi != oj {
			return oi
		}
		if oi && ri != rj {
			return ri < rj
		}
		if oi { // the archive itself before the images inside it
			return ms[i].Source != "archive" && ms[j].Source == "archive"
		}
		return false
	})
}

// library reads every project's materials. The exits (pull requests) are read for the tasks that ended or
// submitted work, a few at a time; the whole list is kept for libraryTTL so paging and filters stay cheap.
type library struct {
	mu   sync.Mutex
	at   time.Time
	list []Material
}

const libraryTTL = 20 * time.Second

func (l *library) read(ctx context.Context, c tui.Client) ([]Material, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.list != nil && time.Since(l.at) < libraryTTL {
		return l.list, nil
	}
	o, err := c.Overview(ctx)
	if err != nil {
		return nil, err
	}
	out := []Material{}
	for _, p := range o.Status {
		id := p.Project.ID
		reps, err := c.Reports(ctx, id, "")
		if err != nil {
			return nil, err
		}
		records, err := c.Artifacts(ctx, id)
		if err != nil {
			return nil, err
		}
		ts := []tui.Task{}
		for _, t := range o.Tasks {
			if t.ProjectID == id && t.Kind != tasks.KindConversation {
				ts = append(ts, t)
			}
		}
		out = append(out, collect(id, reps, records, ts, taskExits(ctx, c, ts), archivedOf(records))...)
	}
	titles(out, o.Tasks)
	newestFirst(out)
	out = oneCardPerArtifact(out)
	l.list, l.at = out, time.Now()
	return out, nil
}

// taskExits reads the exits of the tasks that are done or in review (the ones that can carry a pull
// request), eight at a time. A failed read leaves that task without a pull request.
func taskExits(ctx context.Context, c tui.Client, ts []tui.Task) map[string][]storage.TaskExit {
	out := map[string][]storage.TaskExit{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, t := range ts {
		if t.Status != "DONE" && t.Status != "IN_REVIEW" && t.Submission == nil {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			exits, err := c.TaskExits(ctx, id)
			if err == nil && len(exits) > 0 {
				mu.Lock()
				out[id] = exits
				mu.Unlock()
			}
		}(t.ID)
	}
	wg.Wait()
	return out
}

// page filters the list (project, kinds as a comma list, q in the name, path, summary or task title) and
// cuts the page below before (Unix seconds; 0 is the newest). A page ends on a whole second, so items that
// share the last one's time are never split across pages.
func page(ms []Material, project, kinds, q string, before int64, limit int) MaterialPage {
	want := map[string]bool{}
	for _, k := range strings.Split(kinds, ",") {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	q = strings.ToLower(strings.TrimSpace(q))
	p := MaterialPage{Items: []Material{}}
	for _, m := range ms {
		if (project != "" && m.ProjectID != project) || (len(want) > 0 && !want[m.Kind]) || (before > 0 && m.CreatedAt >= before) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(m.Name + " " + m.Path + " " + m.TaskTitle)
			if m.Report != nil {
				hay += " " + strings.ToLower(m.Report.Summary)
			}
			if !strings.Contains(hay, q) {
				continue
			}
		}
		if len(p.Items) >= limit && m.CreatedAt != p.Items[len(p.Items)-1].CreatedAt {
			p.HasMore = true
			break
		}
		p.Items = append(p.Items, m)
	}
	if p.HasMore {
		p.NextBefore = p.Items[len(p.Items)-1].CreatedAt
	}
	return p
}

// contentType is a file's type from its name (markdown is text/markdown), else sniffed; text is UTF-8.
func contentType(name string, b []byte) string {
	ext := strings.ToLower(path.Ext(name))
	t := map[string]string{".md": "text/markdown", ".markdown": "text/markdown", ".yaml": "text/yaml", ".yml": "text/yaml", ".log": "text/plain", ".csv": "text/csv", ".go": "text/plain", ".ts": "text/plain", ".tsx": "text/plain", ".swift": "text/plain", ".kt": "text/plain", ".py": "text/plain"}[ext]
	if t == "" && ext != "" {
		t, _, _ = strings.Cut(mime.TypeByExtension(ext), ";")
	}
	if t == "" {
		t, _, _ = strings.Cut(http.DetectContentType(b), ";")
	}
	if strings.HasPrefix(t, "text/") || t == "application/json" || t == "image/svg+xml" {
		t += "; charset=utf-8"
	}
	return t
}

// contentTransferTime bounds streaming one artifact or blob to the browser (up to 64 MiB over a slow link);
// the server's write timeout is sized for JSON calls.
const contentTransferTime = 10 * time.Minute

// contentWriter streams raw content to the browser: the headers (contentHeaders) are set on the first write,
// typed by name or sniffed from the first bytes, so a failure before any content is still answered as an
// error, and a file of any stored size passes without being held in memory.
type contentWriter struct {
	w       http.ResponseWriter
	r       *http.Request
	name    string
	started bool
}

func newContentWriter(w http.ResponseWriter, r *http.Request, name string) *contentWriter {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(contentTransferTime))
	return &contentWriter{w: w, r: r, name: name}
}

func (c *contentWriter) Write(p []byte) (int, error) {
	if !c.started {
		c.started = true
		contentHeaders(c.w, c.r, c.name, p)
	}
	return c.w.Write(p)
}

// done answers err when nothing was sent yet, and an empty file when the content is empty; an error after
// the first bytes cuts the response short (the browser sees a failed download).
func (c *contentWriter) done(err error) {
	switch {
	case err != nil && !c.started:
		reply(c.w, nil, err)
	case err == nil && !c.started:
		contentHeaders(c.w, c.r, c.name, nil)
	}
}

// contentHeaders types raw content by name (or its first bytes) and sandboxes it (no scripts, no same
// origin); ?download=1 makes it a download named after the file.
func contentHeaders(w http.ResponseWriter, r *http.Request, name string, b []byte) {
	w.Header().Set("Content-Type", contentType(name, b))
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": cmpOr(path.Base(name), "download")}))
	}
}

// oneCardPerArtifact keeps an artifact's newest version in the Library and lists the older ones on it
// (artifact-duplicates 2.1); the list is newest first, so the first version seen is the newest.
func oneCardPerArtifact(ms []Material) []Material {
	out := make([]Material, 0, len(ms))
	at := map[string]int{}
	for _, m := range ms {
		if m.ArtifactID == "" || m.Source != "artifact" {
			out = append(out, m)
			continue
		}
		key := m.ProjectID + "\x00" + m.ArtifactID
		if i, ok := at[key]; ok {
			out[i].Versions = append(out[i].Versions, MaterialVersion{Version: m.Version, CreatedAt: m.CreatedAt, ContentURL: m.ContentURL, URL: m.URL, Size: m.Size, ArchivedAt: m.ArchivedAt})
			continue
		}
		at[key] = len(out)
		out = append(out, m)
	}
	return out
}
