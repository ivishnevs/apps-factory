package web

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func names(ms []Material) string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return strings.Join(out, ",")
}

// TestTaskMaterials (owner-web 2.6): a task's materials are its report files (with the report), its
// artifact versions (with the approval) and its pull requests; reports without files add nothing.
func TestTaskMaterials(t *testing.T) {
	srv, _ := demo(t)
	var ms []Material
	if code := getJSON(t, srv.URL+"/api/tasks/P1-2/materials", &ms); code != 200 {
		t.Fatal(code)
	}
	if len(ms) != 4 {
		t.Fatal(names(ms))
	}
	byName := map[string]Material{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	doc := byName["module-1-mockups.md"]
	if doc.Kind != "document" || doc.Source != "report" || doc.Report == nil || doc.Report.Kind != "stage_completed" || doc.Author != "designer" ||
		!strings.HasPrefix(doc.ContentURL, "/api/blobs/"+doc.SHA256+"?name=module-1-mockups.md") || doc.TaskTitle != "Task 2" || doc.Size == 0 {
		t.Fatalf("%+v", doc)
	}
	if byName["module-1-list.svg"].Kind != "image" || byName["tokens.json"].Kind != "data" {
		t.Fatalf("%+v", ms)
	}
	proto := byName["module-1-prototype.html"]
	if proto.Kind != "web" || proto.Source != "artifact" || proto.ContentURL != "/api/artifacts/project-1/module-1-prototype/v3?name=module-1-prototype.html" || proto.Version != "v3" {
		t.Fatalf("%+v", proto)
	}
	if ms[3].Name != "module-1-prototype.html" { // newest first: uploaded before the report
		t.Fatal(names(ms))
	}

	if getJSON(t, srv.URL+"/api/tasks/P2-4/materials", &ms); len(ms) != 1 || ms[0].Approval == nil || ms[0].Approval.Status != "PENDING" {
		t.Fatalf("%+v", ms)
	}
	if getJSON(t, srv.URL+"/api/tasks/P2-6/materials", &ms); len(ms) != 1 || ms[0].Kind != "pull_request" || ms[0].PR.State != "merged" || ms[0].URL == "" {
		t.Fatalf("%+v", ms)
	}
	if getJSON(t, srv.URL+"/api/tasks/P1-6/materials", &ms); len(ms) != 0 {
		t.Fatalf("%+v", ms)
	}
}

// TestLibrary: the company's materials newest first, filtered by project, kind and text, in pages.
func TestLibrary(t *testing.T) {
	srv, _ := demo(t)
	var p MaterialPage
	if code := getJSON(t, srv.URL+"/api/materials", &p); code != 200 || len(p.Items) < 8 || p.HasMore {
		t.Fatal(code, names(p.Items))
	}
	for i := 1; i < len(p.Items); i++ {
		if p.Items[i].CreatedAt > p.Items[i-1].CreatedAt {
			t.Fatal("not newest first", names(p.Items))
		}
	}
	all := len(p.Items)
	if getJSON(t, srv.URL+"/api/materials?project=project-3", &p); names(p.Items) != "module-2-spec.md" {
		t.Fatal(names(p.Items))
	}
	if getJSON(t, srv.URL+"/api/materials?kind=image,pull_request", &p); len(p.Items) != 3 {
		t.Fatal(names(p.Items))
	}
	if getJSON(t, srv.URL+"/api/materials?q=public+api", &p); names(p.Items) != "module-2-spec.md" {
		t.Fatal(names(p.Items))
	}
	seen := 0
	before := int64(0)
	for range 20 {
		url := srv.URL + "/api/materials?limit=2"
		if before > 0 {
			url += "&before=" + strconv.FormatInt(before, 10)
		}
		getJSON(t, url, &p)
		seen += len(p.Items)
		if !p.HasMore {
			break
		}
		before = p.NextBefore
	}
	if seen != all {
		t.Fatal(seen, all)
	}
	var bad map[string]string
	if code := getJSON(t, srv.URL+"/api/materials?limit=0", &bad); code != 400 {
		t.Fatal(code)
	}
}

// TestPageKeepsTies: a page never splits the items of one second.
func TestPageKeepsTies(t *testing.T) {
	ms := []Material{{ID: "a", CreatedAt: 5}, {ID: "b", CreatedAt: 4}, {ID: "c", CreatedAt: 4}, {ID: "d", CreatedAt: 3}}
	p := page(ms, "", "", "", 0, 2)
	if len(p.Items) != 3 || !p.HasMore || p.NextBefore != 4 {
		t.Fatalf("%+v", p)
	}
	if p = page(ms, "", "", "", 4, 2); len(p.Items) != 1 || p.Items[0].ID != "d" || p.HasMore {
		t.Fatalf("%+v", p)
	}
}

// TestBlob: a report attachment is served raw, typed by its file name, sandboxed; download names the file.
func TestBlob(t *testing.T) {
	srv, _ := demo(t)
	var ms []Material
	getJSON(t, srv.URL+"/api/tasks/P1-2/materials", &ms)
	var doc Material
	for _, m := range ms {
		if m.Name == "module-1-mockups.md" {
			doc = m
		}
	}
	res, err := http.Get(srv.URL + doc.ContentURL + "&download=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/markdown; charset=utf-8" || res.Header.Get("Content-Security-Policy") != "sandbox" ||
		!strings.Contains(res.Header.Get("Content-Disposition"), `filename=module-1-mockups.md`) || !strings.HasPrefix(string(b), "# Module 1") {
		t.Fatal(res.StatusCode, res.Header, string(b[:min(len(b), 40)]))
	}
	for _, m := range ms {
		if m.Name == "module-1-list.svg" {
			res, _ = http.Get(srv.URL + m.ContentURL)
			res.Body.Close()
			if res.Header.Get("Content-Type") != "image/svg+xml; charset=utf-8" || res.Header.Get("Content-Disposition") != "" {
				t.Fatal(res.Header)
			}
		}
	}
	var bad map[string]string
	if code := getJSON(t, srv.URL+"/api/blobs/not-a-digest", &bad); code != 400 {
		t.Fatal(code, bad)
	}
}
