package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/clock"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestArtifactRetentionAPI (artifact-retention 1.5): the ceiling is readable, a project setting above it is
// refused, the daily check archives due versions, and an archived version's content answers 410 Gone with when
// and why, while its metadata still reads.
func TestArtifactRetentionAPI(t *testing.T) {
	ctx := context.Background()
	s := storagetest.Open(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	s.ArtifactRetentionMaxDays = 30
	c := &Core{Store: s, Token: "owner", Company: "default"}
	h := c.Handler()
	do := func(method, path string, body any) (int, map[string]any) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var v map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return w.Code, v
	}
	if code, v := do("GET", "/settings/artifact_retention", nil); code != 200 || v["max_days"] != float64(30) {
		t.Fatal(code, v)
	}
	co := storage.DefaultCompany
	p, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-retention"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := do("PATCH", "/projects/p", map[string]any{"version": p.Version, "name": p.Name, "artifact_retention_days": 90}); code != 400 {
		t.Fatal("retention above the ceiling accepted", code)
	}
	if code, v := do("PATCH", "/projects/p", map[string]any{"version": p.Version, "name": p.Name, "artifact_retention_days": 14}); code != 200 || v["artifact_retention_days"] != float64(14) {
		t.Fatal(code, v)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "art", ProjectID: "p", Title: "art"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v1", "v2"} {
		b, err := s.PutBlob(ctx, co, strings.NewReader("doc "+v), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RegisterArtifact(ctx, co, "p", artifacts.Register{Artifact: tasks.Artifact{ID: "doc", Version: v, URI: storage.UploadURI("doc", v), SHA256: b.SHA256, Name: "doc.md"}, TaskID: "art", Actor: "owner", RequestID: v}); err != nil {
			t.Fatal(err)
		}
	}
	fake.Advance(15 * 24 * time.Hour)
	if err = c.checkUnmerged(ctx); err != nil {
		t.Fatal(err)
	}
	code, v := do("GET", "/projects/p/artifacts/doc/v1/content", nil)
	if code != 410 || v["reason"] != "retention: 14 days" || v["latest_version"] != "v2" || v["archived_at"] == nil {
		t.Fatal(code, v)
	}
	if code, _ = do("GET", "/projects/p/artifacts/doc/v2/content", nil); code != 200 {
		t.Fatal("latest version", code)
	}
	code, v = do("GET", "/projects/p/artifacts/doc/v1", nil)
	if code != 200 || !strings.Contains(v["archived"].(string), "latest version is v2") {
		t.Fatal(code, v)
	}
}
