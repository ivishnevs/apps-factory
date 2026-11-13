package web

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// grants are the sandbox tokens a previewed page must never get: with any of them it could reach the person's
// session, cookies or the Factory API, submit forms, open windows or navigate the app.
var grants = []string{"allow-same-origin", "allow-forms", "allow-popups", "allow-top-navigation", "allow-modals"}

// TestContentPolicy (approval-gates-merge 2.2): raw content is sandboxed in an opaque origin; only HTML may run
// scripts, and nothing is ever granted same-origin, forms, popups or top navigation.
func TestContentPolicy(t *testing.T) {
	for typ, want := range map[string]string{
		"text/html; charset=utf-8":        "sandbox allow-scripts",
		"text/html":                       "sandbox allow-scripts",
		"image/svg+xml; charset=utf-8":    "sandbox",
		"text/markdown; charset=utf-8":    "sandbox",
		"application/json; charset=utf-8": "sandbox",
		"application/pdf":                 "sandbox",
		"application/octet-stream":        "sandbox",
		"text/plain; charset=utf-8":       "sandbox",
		"application/xhtml+xml":           "sandbox",
	} {
		if got := contentPolicy(typ); got != want {
			t.Errorf("%s: %q, want %q", typ, got, want)
		}
	}
}

// TestHTMLArtifactRunsScriptsWithoutSameOrigin (approval-gates-merge 2.2): an HTML artifact is served with
// "sandbox allow-scripts" whether it is typed by its name (viewer, new tab, download) or sniffed from its bytes
// (the inbox preview URL), and the policy never grants same-origin state.
func TestHTMLArtifactRunsScriptsWithoutSameOrigin(t *testing.T) {
	srv, _ := demo(t)
	var ms []Material
	getJSON(t, srv.URL+"/api/tasks/P1-2/materials", &ms)
	var html Material
	for _, m := range ms {
		if m.Kind == "web" {
			html = m
		}
	}
	if html.ContentURL == "" {
		t.Fatal("no HTML material in the fixture")
	}
	bare, _, _ := strings.Cut(html.ContentURL, "?")
	for _, u := range []string{html.ContentURL, bare, html.ContentURL + "&download=1"} {
		res, err := http.Get(srv.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || csp != "sandbox allow-scripts" {
			t.Fatal(u, res.StatusCode, res.Header)
		}
		for _, g := range grants {
			if strings.Contains(csp, g) {
				t.Fatalf("%s grants %s: %q", u, g, csp)
			}
		}
	}
}

// TestPreviewFramesAreSandboxed (approval-gates-merge 2.2): every iframe in the web UI carries a sandbox
// attribute and none grants same-origin, forms, popups, modals or top navigation; the viewer and inbox preview
// frames allow scripts so a prototype renders.
func TestPreviewFramesAreSandboxed(t *testing.T) {
	root := "../../web/src"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no web sources")
	}
	frame := regexp.MustCompile(`<iframe\b[^>]*>`)
	scripted := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tsx") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, tag := range frame.FindAllString(string(b), -1) {
			if !strings.Contains(tag, "sandbox=") {
				t.Errorf("%s: iframe without sandbox: %s", p, tag)
			}
			if strings.Contains(tag, `"allow-scripts"`) {
				scripted[filepath.ToSlash(p)] = true
			}
			for _, g := range grants {
				if strings.Contains(tag, g) {
					t.Errorf("%s: iframe grants %s: %s", p, g, tag)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root + "/components/app/materials/viewer.tsx", root + "/components/app/inbox/artifact-preview.tsx"} {
		if !scripted[p] {
			t.Errorf("%s: preview frame does not allow scripts (%v)", p, scripted)
		}
	}
}
