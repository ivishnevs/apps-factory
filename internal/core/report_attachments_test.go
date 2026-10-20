package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/factorytools"
	"appfactory/internal/redact"
	"appfactory/internal/reports"
	"appfactory/internal/storage"
)

// TestReportAttachmentsUploadedRedacted (core-owned-project-data 3.5): a report attachment names a worktree
// file; the adapter uploads it with the node's known credential values replaced and the report keeps its
// digest. An attachment that was never uploaded is refused.
func TestReportAttachmentsUploadedRedacted(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()
	const token = "forge-token-0123456789abcdef"
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "out/build.log"), "build ok\npushed with "+token+"\n")
	h.create("rep", "notes", "demo")
	r := h.start("rep")
	a := &factorytools.Adapter{URL: server.URL, Token: r.token, Workdir: work, Redact: []string{token}}
	report := func(attachment map[string]any, key string) (any, error) {
		return a.Call(ctx, factorytools.Call{Name: "factory_report_create", Arguments: map[string]any{"kind": "problem", "summary": "build log", "purpose": "evidence", "changes": []any{}, "result": "see attachment", "attachments": []any{attachment}, "request_id": key}})
	}
	out, err := report(map[string]any{"path": "out/build.log"}, "report-1")
	if err != nil {
		t.Fatal(err)
	}
	var rep reports.Report
	b, _ := json.Marshal(out)
	if err = json.Unmarshal(b, &rep); err != nil || len(rep.Attachments) != 1 || !rep.Attachments[0].Redacted || rep.Attachments[0].Path != "out/build.log" {
		t.Fatal(string(b), err)
	}
	rc, _, err := h.s.OpenBlob(ctx, storage.DefaultCompany, rep.Attachments[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(rc)
	rc.Close()
	if strings.Contains(string(content), token) || !strings.Contains(string(content), "pushed with "+redact.Marker) {
		t.Fatal(string(content))
	}
	// Directly to Core, a digest nobody uploaded is refused.
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_report_create", Arguments: map[string]any{"kind": "problem", "summary": "s", "purpose": "p", "changes": []any{}, "result": "r", "attachments": []any{map[string]any{"path": "x", "sha256": strings.Repeat("0", 64)}}, "request_id": "report-2"}}, nil)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
